package managerstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/digitalygo/smidja/internal/verk"
	"github.com/digitalygo/smidja/internal/verktriggers"
	"strings"
)

func (s *Store) CreateTask(start StartTask) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(start.Inputs) == 0 {
		start.Inputs = json.RawMessage(`{}`)
	}
	start = clone(start)
	normalized, err := graphTaskInputs(start.Workflow, start.Inputs)
	if err != nil {
		return Task{}, err
	}
	start.Inputs = normalized
	if start.InvocationKey != "" {
		if id, ok := s.invocations[start.InvocationKey]; ok {
			existing := s.tasks[id]
			old := StartTask{ParentTaskID: existing.ParentTaskID, Depth: existing.Depth, Workflow: existing.Workflow, Inputs: existing.Inputs, Focus: existing.Focus, OriginSessionID: existing.OriginSessionID, OriginChatID: existing.OriginChatID, OriginTurnID: existing.OriginTurnID, InvocationKey: existing.InvocationKey}
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(start)
			if string(a) != string(b) {
				return Task{}, fmt.Errorf("%w: invocation key reused with different input", ErrConflict)
			}
			return clone(existing), nil
		}
	}
	id := newID()
	if err := s.commit(event{Type: "task.created", TaskID: id, Start: &start}); err != nil {
		return Task{}, err
	}
	return clone(s.tasks[id]), nil
}

func graphTaskInputs(workflow Workflow, raw json.RawMessage) (json.RawMessage, error) {
	if workflow.Graph == nil {
		return raw, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fmt.Errorf("graph inputs must be an object")
	}
	for _, input := range workflow.Graph.InputValues {
		if _, exists := values[input.Name]; exists {
			continue
		}
		encoded, err := json.Marshal(input.Value)
		if err != nil {
			return nil, err
		}
		values[input.Name] = encoded
	}
	return json.Marshal(values)
}

func (s *Store) change(e event) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.commit(e); err != nil {
		return Task{}, err
	}
	return clone(s.tasks[e.TaskID]), nil
}

func (s *Store) StartPhase(id, requestID string) (Task, error) {
	return s.change(event{Type: "phase.started", TaskID: id, RequestID: requestID})
}
func (s *Store) AttachRemote(id, requestID, chatID, turnID string) (Task, error) {
	return s.change(event{Type: "phase.remote_attached", TaskID: id, RequestID: requestID, ChatID: chatID, TurnID: turnID})
}
func (s *Store) RecordOutput(id, requestID, output string) (Task, error) {
	return s.change(event{Type: "phase.output_recorded", TaskID: id, RequestID: requestID, Text: output})
}
func (s *Store) Classify(id, requestID string, decision Decision, model string) (Task, error) {
	return s.change(event{Type: "phase.classified", TaskID: id, RequestID: requestID, Decision: decision, Model: model})
}
func (s *Store) Pause(id, reason string) (Task, error) {
	return s.change(event{Type: "task.paused", TaskID: id, Text: reason})
}
func (s *Store) RetryClassification(id string) (Task, error) {
	return s.change(event{Type: "phase.classification_retried", TaskID: id})
}
func (s *Store) Resume(id, clarification string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kind := "task.resumed"
	if task, ok := s.tasks[id]; ok && task.Status == Paused && task.PhaseIndex < len(task.Phases) {
		attempts := task.Phases[task.PhaseIndex].Attempts
		if len(attempts) > 0 && !attempts[len(attempts)-1].OutputRecorded {
			kind = "phase.observation_resumed"
		}
	}
	if err := s.commit(event{Type: kind, TaskID: id, Text: clarification}); err != nil {
		return Task{}, err
	}
	return clone(s.tasks[id]), nil
}
func (s *Store) RequestCancellation(id string) (Task, error) {
	return s.change(event{Type: "task.cancellation_requested", TaskID: id})
}
func (s *Store) ConfirmCancellation(id string) (Task, error) {
	return s.change(event{Type: "task.cancellation_confirmed", TaskID: id})
}
func (s *Store) CancellationFailed(id, reason string) (Task, error) {
	return s.change(event{Type: "task.cancellation_failed", TaskID: id, Text: reason})
}
func (s *Store) Cancel(id string) (Task, error) {
	return s.change(event{Type: "task.cancelled", TaskID: id})
}

func ValidateWorkflowPolicy(workflow Workflow) error {
	if err := ValidateInputSchema(workflow.InputSchema); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, invoker := range workflow.AllowedInvokers {
		if seen[invoker] || (invoker != "ceo" && (!strings.HasPrefix(invoker, "team:") || strings.TrimSpace(strings.TrimPrefix(invoker, "team:")) == "")) {
			return fmt.Errorf("invalid or duplicate allowed invoker")
		}
		seen[invoker] = true
	}
	return nil
}
func ValidateWorkflow(workflow Workflow) error      { return validateWorkflow(workflow, true) }
func ValidateWorkflowDraft(workflow Workflow) error { return validateWorkflow(workflow, false) }
func validateWorkflow(workflow Workflow, runnable bool) error {
	if err := ValidateWorkflowPolicy(workflow); err != nil {
		return err
	}
	if workflow.Graph != nil {
		if workflow.SchemaVersion != 2 || len(workflow.Phases) > 0 || strings.TrimSpace(workflow.ID) == "" || strings.TrimSpace(workflow.Version) == "" {
			return fmt.Errorf("invalid graph workflow identity or version")
		}
		if err := verk.Validate(*workflow.Graph, runnable); err != nil {
			return err
		}
		for _, node := range workflow.Graph.Nodes {
			if runnable && node.Type == "entry" {
				for i, trigger := range node.Triggers {
					if err := verktriggers.Validate(trigger); err != nil {
						return fmt.Errorf("node %s trigger %d (%s): %w", node.ID, i+1, trigger.Type, err)
					}
				}
			}
			if err := ValidateInputSchema(node.OutputSchema); err != nil {
				return fmt.Errorf("node %s output schema: %w", node.ID, err)
			}
		}
		return nil
	}
	if workflow.SchemaVersion > 1 {
		return fmt.Errorf("unsupported workflow schema version")
	}
	if strings.TrimSpace(workflow.ID) == "" || strings.TrimSpace(workflow.Version) == "" || len(workflow.Phases) == 0 {
		return fmt.Errorf("invalid workflow identity or empty phases")
	}
	ids := map[string]bool{}
	for _, p := range workflow.Phases {
		if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.TeamRef) == "" || ids[p.ID] {
			return fmt.Errorf("invalid or duplicate workflow phase")
		}
		ids[p.ID] = true
	}
	return nil
}

func (s *Store) reduceTask(e event) (func(), error) {
	if e.Type == "task.created" {
		if e.TaskID == "" || e.Start == nil {
			return nil, fmt.Errorf("invalid task creation")
		}
		if _, exists := s.tasks[e.TaskID]; exists {
			return nil, ErrConflict
		}
		start := clone(*e.Start)
		if start.Depth < 0 || start.Depth > 8 || (start.Depth > 0 && start.ParentTaskID == "") || (start.Depth == 0 && start.ParentTaskID != "") {
			return nil, fmt.Errorf("invalid nested workflow depth")
		}
		if start.ParentTaskID != "" {
			parent, ok := s.tasks[start.ParentTaskID]
			if !ok || parent.Depth+1 != start.Depth || parent.CancelRequested || parent.Status == Cancelled || parent.Status == Completed {
				return nil, fmt.Errorf("invalid parent task")
			}
		}
		if err := ValidateWorkflow(start.Workflow); err != nil {
			return nil, err
		}
		normalized, err := graphTaskInputs(start.Workflow, start.Inputs)
		if err != nil {
			return nil, err
		}
		start.Inputs = normalized
		if err := ValidateWorkflowInputs(start.Workflow.InputSchema, start.Inputs); err != nil {
			return nil, err
		}
		if start.InvocationKey != "" && s.invocations[start.InvocationKey] != "" {
			return nil, ErrConflict
		}
		if start.OriginSessionID != "" {
			if _, ok := s.sessions[start.OriginSessionID]; !ok {
				return nil, fmt.Errorf("origin session: %w", ErrNotFound)
			}
		}
		t := Task{ParentTaskID: start.ParentTaskID, Depth: start.Depth, ID: e.TaskID, Workflow: start.Workflow, Inputs: start.Inputs, Focus: start.Focus, OriginSessionID: start.OriginSessionID, OriginChatID: start.OriginChatID, OriginTurnID: start.OriginTurnID, InvocationKey: start.InvocationKey, Status: Queued, Phases: []PhaseState{}, Clarifications: []string{}, CreatedAt: e.At, UpdatedAt: e.At}
		if start.Workflow.Graph != nil {
			var inputs map[string]any
			decoder := json.NewDecoder(bytes.NewReader(start.Inputs))
			decoder.UseNumber()
			if err := decoder.Decode(&inputs); err != nil {
				return nil, err
			}
			state, err := verk.NewState(*start.Workflow.Graph, inputs)
			if err != nil {
				return nil, err
			}
			t.GraphState = &state
		}
		for _, p := range start.Workflow.Phases {
			t.Phases = append(t.Phases, PhaseState{Phase: p, Status: Queued, Attempts: []Attempt{}})
		}
		return func() {
			s.tasks[t.ID] = t
			if t.InvocationKey != "" {
				s.invocations[t.InvocationKey] = t.ID
			}
		}, nil
	}
	current, ok := s.tasks[e.TaskID]
	if !ok {
		return nil, ErrNotFound
	}
	t := clone(current)
	if t.Status == Completed || t.Status == Cancelled {
		return nil, fmt.Errorf("%w: task is terminal", ErrConflict)
	}
	if t.CancelRequested && e.Type != "task.cancellation_requested" && e.Type != "task.cancellation_confirmed" && e.Type != "task.cancellation_failed" {
		return nil, fmt.Errorf("%w: remote cancellation remains unconfirmed", ErrConflict)
	}
	if t.GraphState != nil {
		return s.reduceGraph(t, e)
	}
	phase := &t.Phases[t.PhaseIndex]
	var attempt *Attempt
	if len(phase.Attempts) > 0 {
		attempt = &phase.Attempts[len(phase.Attempts)-1]
	}
	switch e.Type {
	case "phase.started":
		if t.Status != Queued || e.RequestID == "" {
			return nil, ErrConflict
		}
		for _, p := range t.Phases {
			for _, a := range p.Attempts {
				if a.RequestID == e.RequestID {
					return nil, fmt.Errorf("%w: request id reused", ErrConflict)
				}
			}
		}
		phase.Attempts = append(phase.Attempts, Attempt{Number: len(phase.Attempts) + 1, RequestID: e.RequestID, StartedAt: e.At})
		phase.Status = Running
		t.Status = Running
		t.Reason = ""
	case "phase.remote_attached":
		if t.Status != Running || attempt == nil || attempt.RequestID != e.RequestID || attempt.OutputRecorded || e.ChatID == "" || e.TurnID == "" {
			return nil, ErrConflict
		}
		if (attempt.ChatID != "" && attempt.ChatID != e.ChatID) || (attempt.TurnID != "" && attempt.TurnID != e.TurnID) {
			return nil, ErrConflict
		}
		attempt.ChatID = e.ChatID
		attempt.TurnID = e.TurnID
	case "phase.output_recorded":
		if t.Status != Running || attempt == nil || attempt.RequestID != e.RequestID || attempt.OutputRecorded {
			return nil, ErrConflict
		}
		attempt.Output = e.Text
		attempt.OutputRecorded = true
	case "phase.classified":
		if t.Status != Running || attempt == nil || attempt.RequestID != e.RequestID || !attempt.OutputRecorded || attempt.Decision != "" {
			return nil, ErrConflict
		}
		if e.Decision != DecisionBlocked && e.Decision != DecisionNotBlocked {
			return nil, fmt.Errorf("invalid classifier decision")
		}
		if strings.TrimSpace(e.Model) == "" {
			return nil, fmt.Errorf("classifier model is required")
		}
		attempt.Decision = e.Decision
		attempt.ClassifierModel = e.Model
		if e.Decision == DecisionBlocked {
			phase.Status = Blocked
			t.Status = Blocked
		} else {
			phase.Status = Completed
			t.PhaseIndex++
			if t.PhaseIndex == len(t.Phases) {
				t.Status = Completed
			} else {
				t.Status = Queued
			}
		}
	case "task.paused":
		if (t.Status != Running && t.Status != Queued) || strings.TrimSpace(e.Text) == "" {
			return nil, ErrConflict
		}
		t.Status = Paused
		t.Reason = e.Text
		phase.Status = Paused
	case "phase.classification_retried":
		if t.Status != Paused || attempt == nil || !attempt.OutputRecorded || attempt.Decision != "" {
			return nil, ErrConflict
		}
		t.Status = Running
		t.Reason = ""
		phase.Status = Running
	case "phase.observation_resumed":
		if t.Status != Paused || attempt == nil || attempt.OutputRecorded || strings.TrimSpace(e.Text) == "" {
			return nil, ErrConflict
		}
		t.Status = Running
		t.Reason = ""
		phase.Status = Running
	case "task.resumed":
		if t.Status != Blocked && t.Status != Paused {
			return nil, ErrConflict
		}
		if strings.TrimSpace(e.Text) == "" {
			return nil, fmt.Errorf("resume clarification is required")
		}
		if t.Status == Paused && attempt != nil && attempt.OutputRecorded && attempt.Decision == "" {
			return nil, fmt.Errorf("%w: retry classification instead", ErrConflict)
		}
		t.Clarifications = append(t.Clarifications, e.Text)
		t.Status = Queued
		t.Reason = ""
		phase.Status = Queued
	case "task.cancellation_requested":
		t.CancelRequested = true
		t.CancelError = ""
		t.Status = Paused
		t.Reason = "cancellation requested; remote outcome not yet confirmed"
		phase.Status = Paused
	case "task.cancellation_confirmed":
		if !t.CancelRequested {
			return nil, ErrConflict
		}
		t.CancelError = ""
		t.Reason = ""
		t.Status = Cancelled
		phase.Status = Cancelled
	case "task.cancellation_failed":
		if !t.CancelRequested || strings.TrimSpace(e.Text) == "" {
			return nil, ErrConflict
		}
		t.CancelError = e.Text
		t.Reason = "remote cancellation unconfirmed: " + e.Text
		t.Status = Paused
		phase.Status = Paused
	case "task.cancelled":
		t.Status = Cancelled
		phase.Status = Cancelled
	default:
		return nil, fmt.Errorf("unknown task event %q", e.Type)
	}
	t.UpdatedAt = e.At
	return func() { s.tasks[t.ID] = t }, nil
}

func (s *Store) reduce(e event) (func(), error) {
	switch e.Type {
	case "tool.started", "tool.finished":
		return s.reduceTool(e)
	case "session.identity_attached", "session.chat_sequence", "session.turn_output", "session.created", "session.remote_attached", "session.message_appended", "session.turn_started", "session.turn_attached", "session.turn_completed", "session.turn_paused", "session.turn_followed", "session.turn_sequence", "session.turn_cancelled":
		return s.reduceSession(e)
	default:
		return s.reduceTask(e)
	}
}
