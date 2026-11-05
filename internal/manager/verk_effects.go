package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
	"github.com/digitalygo/smidja/internal/verk"
)

type graphRemote interface {
	NativeRemote
	StartJob(context.Context, string, envai.JobInput) (envai.Job, error)
	ClassifyChoice(context.Context, string, envai.ChoiceInput) (envai.ChoiceResult, error)
}
type graphResult struct {
	Output string `json:"output"`
	Port   string `json:"port"`
}

func (s *Server) executeGraphNode(ctx context.Context, task managerstate.Task, node verk.Node, run verk.Run) (string, string, error) {
	if run.OutputRecorded {
		operation, err := s.cfg.Store.GetTool(run.RequestID + ":result")
		if err != nil {
			return "", "", errors.New("recorded output lacks routing receipt")
		}
		var result graphResult
		if operation.Status != managerstate.Completed || json.Unmarshal([]byte(operation.Output), &result) != nil {
			return "", "", errors.New("invalid routing receipt")
		}
		return result.Output, result.Port, nil
	}
	var output, port string
	var err error
	switch node.Type {
	case "lid":
		output, port, err = s.graphLid(ctx, task, node, run)
	case "tool":
		output, port, err = s.graphTool(ctx, task, node, run)
	case "ai":
		output, port, err = s.graphChoice(ctx, task, node, run)
	case "verk":
		output, port, err = s.graphChild(ctx, task, node, run)
	default:
		return "", "", errors.New("unsupported graph effect")
	}
	if err != nil {
		return "", "", err
	}
	result := graphResult{output, port}
	raw, _ := json.Marshal(result)
	operation, created, err := s.cfg.Store.BeginTool(run.RequestID+":result", "graph", task.ID, "verk.result", raw, "manager")
	if err != nil {
		return "", "", err
	}
	if created {
		err = s.cfg.Store.FinishTool(operation.CallID, string(raw), false)
	} else if operation.Status != managerstate.Completed {
		err = errors.New("routing receipt uncertain")
	}
	return output, port, err
}
func (s *Server) graphExecutionChat(ctx context.Context, task managerstate.Task, run verk.Run, teamID string) (PhaseExecution, error) {
	key := run.RequestID + ":chat"
	record, created, err := s.cfg.Store.BeginTool(key, "graph", task.ID, "verk.chat", json.RawMessage(fmt.Sprintf(`{"team_id":%q}`, teamID)), "envai")
	if err != nil {
		return PhaseExecution{}, err
	}
	if !created {
		var execution PhaseExecution
		if record.Status != managerstate.Completed {
			return execution, errors.New("chat creation outcome uncertain; inspect before retry")
		}
		err = json.Unmarshal([]byte(record.Output), &execution)
		return execution, err
	}
	chat, err := s.cfg.Remote.CreateChat(ctx, s.cfg.WorkspaceID, teamID)
	if err != nil {
		return PhaseExecution{}, err
	}
	session, err := s.cfg.Store.CreateWorkflowSession("Verk "+task.ID, task.ID)
	if err != nil {
		return PhaseExecution{}, err
	}
	if _, err = s.cfg.Store.AttachSessionIdentity(session.ID, chat.ID, teamID, teamID); err != nil {
		return PhaseExecution{}, err
	}
	execution := PhaseExecution{SessionID: session.ID, ChatID: chat.ID, OriginChatID: chat.ID}
	raw, _ := json.Marshal(execution)
	err = s.cfg.Store.FinishTool(key, string(raw), false)
	return execution, err
}
func (s *Server) graphLid(ctx context.Context, task managerstate.Task, node verk.Node, run verk.Run) (string, string, error) {
	remote, ok := s.cfg.Remote.(graphRemote)
	if !ok {
		return "", "", errors.New("native graph job transport unavailable")
	}
	if err := remote.RequireFeatures(ctx, "independent_jobs", "strict_model", "delegated_job_lifecycle"); err != nil {
		return "", "", err
	}
	team, err := s.ensureWorkflowTeam(ctx, task.Workflow, graphParam(node, "ref"))
	if err != nil {
		return "", "", err
	}
	execution, err := s.graphExecutionChat(ctx, task, run, team)
	if err != nil {
		return "", "", err
	}
	execution.Observed = s.observeSession(execution.SessionID)
	body, err := graphPayload(task, node)
	if err != nil {
		return "", "", err
	}
	record, created, err := s.cfg.Store.BeginTool(run.RequestID, execution.ChatID, task.ID, "workflow.delegate", body, "envai")
	if err != nil {
		return "", "", err
	}
	if !created {
		if record.Status != managerstate.Completed {
			return "", "", errors.New("native admission outcome uncertain; refusing blind restart")
		}
		if err = json.Unmarshal([]byte(record.Output), &execution); err != nil {
			return "", "", err
		}
		execution.Observed = s.observeSession(execution.SessionID)
	} else {
		strict := true
		job, e := remote.StartJob(ctx, execution.ChatID, envai.JobInput{WorkspaceID: s.cfg.WorkspaceID, RequestID: run.RequestID, Prompt: "Execute this Verk block. Preserve complete results. When output_schema is present return a JSON object conforming to it.\n" + string(body), StrictModel: &strict})
		if e != nil {
			return "", "", e
		}
		execution.NativeTaskID = job.TaskID
		execution.TurnID = job.TurnID
		raw, _ := json.Marshal(execution)
		if err = s.cfg.Store.FinishTool(run.RequestID, string(raw), false); err != nil {
			return "", "", err
		}
	}
	current, err := s.cfg.Store.GetTask(task.ID)
	if err != nil {
		return "", "", err
	}
	if current.CancelRequested || current.Status == managerstate.Cancelled {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, e := remote.CancelTask(cancelCtx, execution.ChatID, execution.NativeTaskID, true)
		return "", "", errors.Join(context.Canceled, e)
	}
	raw, _ := json.Marshal(execution)
	if _, err = s.graphEvent(current, verk.Event{Type: "remote", Remote: raw}); err != nil {
		return "", "", err
	}
	output, err := (&nativeWorkflow{server: s, remote: remote}).FollowPhase(ctx, execution)
	if err != nil {
		return "", "", err
	}
	return output, "done", nil
}
func (s *Server) graphTool(ctx context.Context, task managerstate.Task, node verk.Node, run verk.Run) (string, string, error) {
	args, err := graphBindings(task, node, "input")
	if err != nil {
		return "", "", err
	}
	ref, client, err := graphToolTarget(task.Workflow, node)
	if err != nil {
		return "", "", err
	}
	if raw := task.Workflow.ToolDefinitions["graph:"+node.ID]; len(raw) > 0 {
		var binding graphToolBinding
		if json.Unmarshal(raw, &binding) != nil {
			return "", "", errors.New("invalid pinned graph tool binding")
		}
		client = binding.ClientID
	}
	if strings.HasPrefix(ref, "private:") && len(task.Workflow.ToolDefinitions["graph:"+node.ID]) == 0 {
		var definition privateconfig.Extension
		if json.Unmarshal(task.Workflow.ToolDefinitions[ref], &definition) == nil && definition.ClientID != "" && definition.ClientID != "manager" {
			return "", "", errors.New("private client tool binding is not pinned")
		}
		client = "manager"
	}
	record, created, err := s.cfg.Store.BeginTool(run.RequestID, "graph", task.ID, "verk.tool:"+ref, args, client)
	if err != nil {
		return "", "", err
	}
	if !created {
		if record.Status != managerstate.Completed {
			return "", "", errors.New("tool effect outcome uncertain; inspect before any retry")
		}
		var cached machinetools.Result
		if json.Unmarshal([]byte(record.Output), &cached) != nil {
			return "", "", errors.New("invalid cached tool result")
		}
		if cached.Code == "outcome_unknown" {
			return "", "", errors.New("tool outcome uncertain; no automatic retry")
		}
		port := "done"
		if record.IsError {
			port = "error"
		}
		return record.Output, port, nil
	}
	result, registered, err := s.graphRegisteredTool(ctx, task, node, args)
	if err != nil {
		return "", "", err
	}
	if !registered && strings.Contains(ref, "/") {
		return "", "", errors.New("registered graph tool binding is not pinned")
	} else if !registered && client != "manager" {
		if !graphBuiltinTool(ref) {
			return "", "", errors.New("client tool binding is not pinned")
		}
		result = s.executeClientTool(ctx, client, ref, args)
	} else if !registered && strings.HasPrefix(ref, "private:") {
		if s.cfg.Private == nil || s.cfg.Extensions == nil {
			return "", "", errors.New("private extension unavailable")
		}
		payload := task.Workflow.ToolDefinitions[ref]
		if len(payload) == 0 {
			return "", "", errors.New("private graph tool is not pinned")
		}
		var definition privateconfig.Extension
		if e := json.Unmarshal(payload, &definition); e != nil {
			return "", "", e
		}
		environment := map[string]string{}
		for name, reference := range definition.Environment {
			for _, protected := range s.cfg.ProtectedEnvironment {
				if reference == protected {
					return "", "", errors.New("tool cannot request manager credentials")
				}
			}
			if s.cfg.LookupEnv == nil || s.cfg.LookupEnv(reference) == "" {
				return "", "", errors.New("tool environment missing: " + reference)
			}
			environment[name] = s.cfg.LookupEnv(reference)
		}
		result = s.cfg.Extensions.WithEnvironment(environment).ExecuteDefinition(ctx, definition, args)
	} else if !registered {
		result = s.cfg.Tools.Execute(ctx, ref, args)
	}
	if ctx.Err() != nil {
		result = machinetools.Result{Code: "outcome_unknown", Error: "Execution interrupted; effects may have occurred"}
	}
	raw, _ := json.Marshal(result)
	if err = s.cfg.Store.FinishTool(run.RequestID, string(raw), !result.OK); err != nil {
		return "", "", err
	}
	if result.Code == "outcome_unknown" || ctx.Err() != nil {
		return "", "", errors.New("tool outcome uncertain; no automatic retry")
	}
	port := "done"
	if !result.OK {
		port = "error"
	}
	return string(raw), port, nil
}
func (s *Server) graphChoice(ctx context.Context, task managerstate.Task, node verk.Node, run verk.Run) (string, string, error) {
	remote, ok := s.cfg.Remote.(graphRemote)
	if !ok {
		return "", "", errors.New("native choice transport unavailable")
	}
	execution, err := s.graphExecutionChat(ctx, task, run, s.cfg.CEOTeamID)
	if err != nil {
		return "", "", err
	}
	body, err := graphPayload(task, node)
	if err != nil {
		return "", "", err
	}
	input := envai.ChoiceInput{RequestID: run.RequestID, Context: body, Question: graphParam(node, "question")}
	for _, port := range node.Outputs {
		if port.ID != "invalid" {
			input.Choices = append(input.Choices, envai.Choice{Label: port.ID, Description: port.Label})
		}
	}
	raw, _ := json.Marshal(input)
	record, created, err := s.cfg.Store.BeginTool(run.RequestID, execution.ChatID, task.ID, "verk.choice", raw, "envai")
	if err != nil {
		return "", "", err
	}
	var result envai.ChoiceResult
	if !created {
		if record.Status != managerstate.Completed {
			return "", "", errors.New("AI decision outcome uncertain; no automatic paid retry")
		}
		err = json.Unmarshal([]byte(record.Output), &result)
	} else {
		result, err = remote.ClassifyChoice(ctx, execution.ChatID, input)
		if err == nil {
			raw, _ = json.Marshal(result)
			err = s.cfg.Store.FinishTool(run.RequestID, string(raw), false)
		}
	}
	if err != nil {
		return "", "", err
	}
	raw, _ = json.Marshal(result)
	return string(raw), result.Choice, nil
}
func (s *Server) graphChild(ctx context.Context, task managerstate.Task, node verk.Node, run verk.Run) (string, string, error) {
	var workflow managerstate.Workflow
	if err := json.Unmarshal(task.Workflow.WorkflowDefinitions[node.ID], &workflow); err != nil {
		return "", "", errors.New("nested Verk definition is not pinned")
	}
	inputs, err := graphBindings(task, node, "input")
	if err != nil {
		return "", "", err
	}
	child, err := s.cfg.Store.CreateTask(managerstate.StartTask{Workflow: workflow, Inputs: inputs, ParentTaskID: task.ID, Depth: task.Depth + 1, OriginSessionID: task.OriginSessionID, OriginChatID: task.OriginChatID, OriginTurnID: task.OriginTurnID, InvocationKey: "verk-child:" + task.ID + ":" + run.RequestID})
	if err != nil {
		return "", "", err
	}
	parent, parentErr := s.cfg.Store.GetTask(task.ID)
	if parentErr != nil || parent.CancelRequested || parent.Status == managerstate.Cancelled {
		if child.Status != managerstate.Completed && child.Status != managerstate.Cancelled {
			_, _ = s.cancelTask(context.Background(), child.ID)
		}
		return "", "", context.Canceled
	}
	s.scheduleTask(child.ID)
	for {
		child, err = s.cfg.Store.GetTask(child.ID)
		if err != nil {
			return "", "", err
		}
		switch child.Status {
		case managerstate.Completed:
			raw, _ := json.Marshal(child)
			return string(raw), "done", nil
		case managerstate.Paused, managerstate.Blocked:
			return "", "", fmt.Errorf("child Verk %s requires intervention: %s", child.ID, child.Reason)
		case managerstate.Cancelled:
			return "", "", errors.New("child Verk cancelled")
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
