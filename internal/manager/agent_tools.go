package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

func ManagerToolDefinitions() []machinetools.Definition {
	return []machinetools.Definition{
		{Name: "pr_review", Description: "Start the read-only four-phase PR review: freeze base/head SHA and diff, analyze, verify evidence, synthesize. Runs in background and notifies this CEO chat. No PR changes or code execution.", Parameters: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},"number":{"type":"integer","minimum":1},"focus":{"type":"string"}},"required":["repository","number"],"additionalProperties":false}`)},
		{Name: "clients_execute", Description: "Execute an advertised tool on a selected online client. Inspect clients_get first for available tool names and schemas; never invent a route.", Parameters: json.RawMessage(`{"type":"object","properties":{"client_id":{"type":"string"},"tool":{"type":"string"},"arguments":{"type":"object"}},"required":["client_id","tool","arguments"],"additionalProperties":false}`)},
		{Name: "config_schema", Description: "Read the required payload shape before proposing a private team, workflow or extension.", Parameters: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":["team","workflow","extension"]}},"required":["kind"],"additionalProperties":false}`)},
		{Name: "skills_list", Description: "List available installed package skill IDs without loading their contents.", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "skills_read", Description: "Read one installed package skill, on an online client or the manager.", Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`)},
		{Name: "teams_list", Description: "Discover remote and approved private/package teams.", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "teams_delegate", Description: "Delegate work to a team natively in the background. Return immediately; results wake this conversation later.", Parameters: json.RawMessage(`{"type":"object","properties":{"team_ref":{"type":"string"},"prompt":{"type":"string"}},"required":["team_ref","prompt"],"additionalProperties":false}`)},
		{Name: "teams_task", Description: "Read a native team job; unknown after retention is not evidence of nonexecution.", Parameters: json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"string"}},"required":["task_id"],"additionalProperties":false}`)},
		{Name: "teams_cancel", Description: "Request native cancellation of a delegated job and its children; past external effects remain.", Parameters: json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"string"}},"required":["task_id"],"additionalProperties":false}`)},
		{Name: "config_propose", Description: "Propose a private team, workflow or extension change. Requires owner approval before activation; never approve your own change.", Parameters: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["create","update","archive","restore"]},"id":{"type":"string"},"kind":{"type":"string","enum":["team","workflow","extension"]},"base_revision":{"type":"integer"},"payload":{"type":"object"}},"required":["action","id","kind"],"additionalProperties":false}`)},
		{Name: "config_list", Description: "Read approved private teams, workflows and extensions.", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "workflows_list", Description: "List installed immutable workflows available to start.", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "tasks_list", Description: "List workflow tasks and their progress.", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "tasks_get", Description: "Read a task and all exact phase outputs.", Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`)},
		{Name: "tasks_start", Description: "Start a sequential workflow in the background. Returns its task ID immediately; completion is delivered natively to this conversation.", Parameters: json.RawMessage(`{"type":"object","properties":{"workflow":{"type":"object"},"inputs":{"type":"object"},"focus":{"type":"string"}},"required":["workflow","inputs"],"additionalProperties":false}`)},
		{Name: "tasks_resume", Description: "Resume a blocked task at its blocked phase with the user's clarification; completed phases are retained.", Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"clarification":{"type":"string"}},"required":["id","clarification"],"additionalProperties":false}`)},
		{Name: "clients_list", Description: "List machines and online status; offline capabilities are only returned by clients_get.", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "clients_get", Description: "Read the last announced tools, teams and packages for one client, including when offline.", Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`)},
	}
}
func toolResult(value any, err error) machinetools.Result {
	if err != nil {
		return machinetools.Result{Error: err.Error(), Code: "manager_error"}
	}
	return machinetools.Result{OK: true, Data: value}
}
func (s *Server) executeManagerTool(ctx context.Context, event envai.Event, name string, args json.RawMessage) machinetools.Result {
	var identity struct {
		ID            string `json:"id"`
		Clarification string `json:"clarification"`
		Kind          string `json:"kind"`
	}
	if err := json.Unmarshal(args, &identity); err != nil {
		return toolResult(nil, err)
	}
	switch strings.TrimPrefix(name, "manager.") {
	case "pr_review":
		return s.prReviewTool(ctx, event, args)
	case "clients_execute":
		var input struct {
			ClientID  string          `json:"client_id"`
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return toolResult(nil, err)
		}
		return s.executeClientTool(ctx, input.ClientID, input.Tool, input.Arguments)
	case "config_schema":
		value, err := configurationSchema(identity.Kind)
		return toolResult(value, err)
	case "skills_list", "skills_read":
		skills, err := s.availableSkills(ctx)
		if err != nil {
			return toolResult(nil, err)
		}
		if strings.TrimPrefix(name, "manager.") == "skills_read" {
			for _, skill := range skills {
				if skill.ID == identity.ID {
					return toolResult(skill, nil)
				}
			}
			return toolResult(nil, errors.New("skill unavailable or its client is offline"))
		}
		result := []map[string]string{}
		for _, skill := range skills {
			result = append(result, map[string]string{"id": skill.ID, "package": skill.Package.ID, "version": skill.Package.Version})
		}
		return toolResult(result, nil)
	case "teams_list", "teams_delegate", "teams_task", "teams_cancel":
		return s.executeTeamTool(ctx, event, strings.TrimPrefix(name, "manager."), args)
	case "workflows_list":
		result, err := s.availableWorkflows(ctx)
		return toolResult(result, err)
	case "config_list":
		if s.cfg.Private == nil {
			return toolResult(nil, errors.New("private configuration unavailable"))
		}
		return toolResult(s.cfg.Private.List("", false), nil)
	case "config_propose":
		if s.cfg.Private == nil {
			return toolResult(nil, errors.New("private configuration unavailable"))
		}
		var change privateconfig.Change
		if err := json.Unmarshal(args, &change); err != nil {
			return toolResult(nil, err)
		}
		proposal, err := s.cfg.Private.Propose(change)
		return toolResult(proposal, err)
	case "clients_list":
		clients := s.cfg.Clients.List()
		result := make([]map[string]any, 0, len(clients))
		for _, client := range clients {
			result = append(result, map[string]any{"id": client.ID, "name": client.Name, "online": client.Online, "last_seen": client.LastSeen})
		}
		return toolResult(result, nil)
	case "clients_get":
		for _, client := range s.cfg.Clients.List() {
			if client.ID == identity.ID {
				return toolResult(client, nil)
			}
		}
		return toolResult(nil, managerstate.ErrNotFound)
	case "tasks_list":
		return toolResult(s.cfg.Store.ListTasks(), nil)
	case "tasks_get":
		task, err := s.cfg.Store.GetTask(identity.ID)
		return toolResult(task, err)
	case "tasks_start":
		if s.cfg.WorkflowRuntime == nil {
			return toolResult(nil, errors.New("native workflow runtime unavailable"))
		}
		if err := s.cfg.WorkflowRuntime.Ready(ctx); err != nil {
			return toolResult(nil, err)
		}
		var input managerstate.StartTask
		if err := json.Unmarshal(args, &input); err != nil {
			return toolResult(nil, err)
		}
		input.OriginSessionID = ""
		input.OriginChatID = event.ChatID
		input.OriginTurnID = event.TurnID
		for _, session := range s.cfg.Store.ListSessions() {
			if session.ChatID == event.ChatID {
				input.OriginSessionID = session.ID
				break
			}
		}
		if input.OriginSessionID == "" {
			return toolResult(nil, errors.New("workflow tool must be called from a tracked CEO conversation"))
		}
		request, err := event.ToolRequest()
		if err != nil {
			return toolResult(nil, err)
		}
		digest := sha256.Sum256([]byte(event.ChatID + "\x00" + request.CallID))
		input.InvocationKey = "workflow:" + hex.EncodeToString(digest[:])
		workflow, err := s.resolveAgentWorkflow(event, input.Workflow)
		if err != nil {
			return toolResult(nil, err)
		}
		input.Workflow = workflow
		task, err := s.cfg.Store.CreateTask(input)
		if err == nil {
			s.scheduleTask(task.ID)
		}
		return toolResult(task, err)
	case "tasks_resume":
		pinned, err := s.cfg.Store.GetTask(identity.ID)
		if err != nil {
			return toolResult(nil, err)
		}
		if err = s.authorizeWorkflow(event, pinned.Workflow); err != nil {
			return toolResult(nil, err)
		}
		task, err := s.cfg.Store.Resume(identity.ID, identity.Clarification)
		if err == nil {
			s.scheduleTask(task.ID)
		}
		return toolResult(task, err)
	default:
		return toolResult(nil, errors.New("unknown manager tool"))
	}
}
