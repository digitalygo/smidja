package managerstate

import (
	"fmt"
	"github.com/digitalygo/smidja/internal/verk"
	"strings"
)

func (s *Store) ApplyGraphEvent(id string, transition verk.Event) (Task, error) {
	return s.change(event{Type: "graph.transition", TaskID: id, GraphEvent: &transition})
}
func (s *Store) reduceGraph(t Task, e event) (func(), error) {
	transition := verk.Event{ExpectedRevision: t.GraphState.Revision, At: e.At, Text: e.Text}
	switch e.Type {
	case "graph.transition":
		if e.GraphEvent == nil {
			return nil, ErrConflict
		}
		transition = *e.GraphEvent
		transition.At = e.At
	case "task.paused":
		transition.Type = "pause"
	case "task.resumed":
		transition.Type = "resume"
	case "task.cancelled":
		transition.Type = "cancel"
	case "task.cancellation_requested":
		t.CancelRequested = true
		t.CancelError = ""
		t.Status = Paused
		t.Reason = "cancellation requested; remote outcome not yet confirmed"
	case "task.cancellation_failed":
		if !t.CancelRequested || strings.TrimSpace(e.Text) == "" {
			return nil, ErrConflict
		}
		t.CancelError = e.Text
		t.Reason = "remote cancellation unconfirmed: " + e.Text
		t.Status = Paused
	case "task.cancellation_confirmed":
		if !t.CancelRequested {
			return nil, ErrConflict
		}
		transition.Type = "cancel"
		t.CancelError = ""
	default:
		return nil, fmt.Errorf("unsupported graph task event %s", e.Type)
	}
	if transition.Type != "" {
		state, err := verk.Apply(*t.Workflow.Graph, *t.GraphState, transition, ValidateWorkflowInputs)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrConflict, err)
		}
		t.GraphState = &state
		t.Status = state.Status
		t.Reason = state.Reason
		t.Clarifications = append([]string{}, state.Clarifications...)
	}
	t.UpdatedAt = e.At
	return func() { s.tasks[t.ID] = t }, nil
}
