package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/verk"
)

func (s *Server) graphEvent(task managerstate.Task, event verk.Event) (managerstate.Task, error) {
	event.ExpectedRevision = task.GraphState.Revision
	event.At = time.Now().UTC()
	if event.RunID == "" {
		if run, ok := verk.CurrentRun(*task.GraphState); ok {
			event.RunID = run.ID
		}
	}
	return s.cfg.Store.ApplyGraphEvent(task.ID, event)
}
func (s *Server) graphPause(task managerstate.Task, err error) {
	if current, e := s.cfg.Store.GetTask(task.ID); e == nil && current.GraphState != nil && (current.Status == managerstate.Running || current.Status == managerstate.Queued) {
		_, _ = s.graphEvent(current, verk.Event{Type: "pause", Text: err.Error()})
	}
}
func graphParam(node verk.Node, key string) string {
	v := node.Params[key]
	if text, ok := v.(string); ok {
		return text
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func graphFields(output string) map[string]any {
	fields := map[string]any{}
	_ = json.Unmarshal([]byte(output), &fields)
	if fields == nil {
		fields = map[string]any{}
	}
	fields["final_message"] = output
	return fields
}
func graphPayload(task managerstate.Task, node verk.Node) (json.RawMessage, error) {
	input, err := graphBindings(task, node, "input")
	if err != nil {
		return nil, err
	}
	resolvedContext, err := graphContext(*task.GraphState, node.Params["context"])
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"inputs": task.GraphState.Inputs, "arguments": input, "context": resolvedContext, "variables": task.GraphState.Variables, "previous_runs": task.GraphState.Runs, "clarifications": task.GraphState.Clarifications, "instruction": graphParam(node, "instruction"), "output_schema": node.OutputSchema})
}

var graphContextPath = regexp.MustCompile(`^(inputs|variables|nodes)\.[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*$`)

func graphContext(state verk.State, value any) (any, error) {
	if operand, ok := value.(map[string]any); ok {
		if _, typed := operand["mode"]; typed {
			return verk.Bind(state, operand)
		}
	}
	if path, ok := value.(string); ok && graphContextPath.MatchString(path) {
		resolved, err := verk.Resolve(state, path)
		if err != nil {
			return nil, err
		}
		if !resolved.Found {
			return nil, fmt.Errorf("context reference %s missing", path)
		}
		return resolved.Value, nil
	}
	return value, nil
}

func (s *Server) runGraphTask(ctx context.Context, id string) {
	for ctx.Err() == nil {
		task, err := s.cfg.Store.GetTask(id)
		if err != nil || task.GraphState == nil {
			return
		}
		if task.CancelRequested || task.Status == managerstate.Cancelled {
			return
		}
		if task.Status == managerstate.Completed || task.Status == managerstate.Blocked {
			_ = s.notifyGraph(ctx, task)
			return
		}
		if task.Status == managerstate.Paused {
			return
		}
		node, err := verk.CurrentNode(*task.Workflow.Graph, *task.GraphState)
		if err != nil {
			s.graphPause(task, err)
			return
		}
		if task.Status == managerstate.Queued {
			started, startErr := s.graphEvent(task, verk.Event{Type: "start", RunID: randomID(), RequestID: randomID()})
			if startErr != nil {
				s.graphPause(task, startErr)
				return
			}
			task = started
		}
		run, ok := verk.CurrentRun(*task.GraphState)
		if !ok {
			s.graphPause(task, errors.New("missing graph execution"))
			return
		}
		if node.Type == "clarify" || node.Type == "approve" || node.Type == "wait" {
			gate := &verk.Gate{ID: randomID(), RunID: run.ID, Kind: node.Type, Prompt: graphParam(node, "question")}
			if node.Type == "approve" {
				gate.Prompt = graphParam(node, "proposal")
				gate.ProposalDigest = verk.ProposalDigest(gate.Prompt)
			}
			if node.Type == "wait" {
				gate.Prompt = "Waiting for " + graphParam(node, "event")
				gate.SourceRef = graphParam(node, "event")
			}
			if raw := graphParam(node, "timeout"); raw != "" {
				seconds, e := strconv.Atoi(raw)
				if e != nil || seconds < 1 || seconds > 31536000 {
					s.graphPause(task, errors.New("invalid gate timeout"))
					return
				}
				deadline := time.Now().UTC().Add(time.Duration(seconds) * time.Second)
				gate.Deadline = &deadline
			}
			task, err = s.graphEvent(task, verk.Event{Type: "wait", Gate: gate})
			if err != nil {
				s.graphPause(task, err)
				return
			}
			_ = s.notifyGraph(ctx, task)
			return
		}
		evaluation, err := verk.Evaluate(*task.Workflow.Graph, *task.GraphState)
		if err != nil {
			s.graphPause(task, err)
			return
		}
		if evaluation.BlockReason != "" {
			task, err = s.graphEvent(task, verk.Event{Type: "block", Text: evaluation.BlockReason})
			if err == nil {
				_ = s.notifyGraph(ctx, task)
			}
			return
		}
		if evaluation.External {
			output, port, e := s.executeGraphNode(ctx, task, node, run)
			if e != nil {
				s.graphPause(task, e)
				return
			}
			task, err = s.cfg.Store.GetTask(id)
			if err != nil || task.CancelRequested {
				return
			}
			latest, _ := verk.CurrentRun(*task.GraphState)
			if !latest.OutputRecorded {
				task, err = s.graphEvent(task, verk.Event{Type: "output", Output: output, Fields: graphFields(output)})
				if err != nil {
					return
				}
			}
			evaluation.Port = port
		}
		_, err = s.graphEvent(task, verk.Event{Type: "complete", Port: evaluation.Port, Fields: evaluation.Fields, Variables: evaluation.Variables, Trace: evaluation.Trace})
		if err != nil {
			s.graphPause(task, err)
			return
		}
	}
}

func graphBindings(task managerstate.Task, node verk.Node, key string) (json.RawMessage, error) {
	raw := node.Params[key]
	if text, ok := raw.(string); ok {
		if text == "" {
			return json.RawMessage(`{}`), nil
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(text), &value); err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	values, err := verk.Bindings(*task.GraphState, raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(values)
}
