package managerstate

import (
	"encoding/json"
	"github.com/digitalygo/smidja/internal/verk"
	"testing"
)

func graphWorkflow() Workflow {
	return Workflow{ID: "graph", Version: "1", SchemaVersion: 2, Graph: &verk.Graph{SchemaVersion: 1, Nodes: []verk.Node{{ID: "entry", Type: "entry", Outputs: []verk.Port{{ID: "next", Label: "Next"}}}, {ID: "end", Type: "end"}}, Edges: []verk.Edge{{ID: "edge", From: "entry", Port: "next", To: "end"}}}}
}
func TestGraphJournalReplayAndRejectedTransition(t *testing.T) {
	store, dir := openTestStore(t)
	task, err := store.CreateTask(StartTask{Workflow: graphWorkflow(), Inputs: json.RawMessage(`{"x":1}`), InvocationKey: "invocation"})
	if err != nil {
		t.Fatal(err)
	}
	if task.GraphState == nil || task.GraphState.Status != Queued {
		t.Fatal(task)
	}
	task, err = store.ApplyGraphEvent(task.ID, verk.Event{Type: "start", RunID: "r", RequestID: "request"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ApplyGraphEvent(task.ID, verk.Event{Type: "complete", RunID: "r", Port: "next", ExpectedRevision: 0}); err == nil {
		t.Fatal("stale revision accepted")
	}
	task, err = store.ApplyGraphEvent(task.ID, verk.Event{Type: "complete", RunID: "r", Port: "next", ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restored, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.GraphState.Revision != 2 || restored.GraphState.Cursor != "end" || len(restored.GraphState.Runs) != 1 {
		t.Fatal(restored)
	}
	same, err := store.CreateTask(StartTask{Workflow: graphWorkflow(), Inputs: json.RawMessage(`{"x":1}`), InvocationKey: "invocation"})
	if err != nil || same.ID != task.ID {
		t.Fatal(same, err)
	}
	if _, err = store.StartPhase(task.ID, "legacy"); err == nil {
		t.Fatal("accepted legacy event")
	}
	task, err = store.Pause(task.ID, "operator pause")
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.Resume(task.ID, "continue")
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.ApplyGraphEvent(task.ID, verk.Event{Type: "start", RunID: "end", RequestID: "end", ExpectedRevision: task.GraphState.Revision})
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.ApplyGraphEvent(task.ID, verk.Event{Type: "complete", RunID: "end", ExpectedRevision: task.GraphState.Revision})
	if err != nil || task.Status != Completed {
		t.Fatal(task, err)
	}
}
func TestGraphDraftAndNestedDepth(t *testing.T) {
	store, _ := openTestStore(t)
	draft := graphWorkflow()
	draft.Graph.Edges = nil
	if ValidateWorkflowDraft(draft) != nil || ValidateWorkflow(draft) == nil {
		t.Fatal("draft/runnable distinction")
	}
	if _, err := store.CreateTask(StartTask{Workflow: draft}); err == nil {
		t.Fatal("ran draft")
	}
	w := graphWorkflow()
	w.Phases = workflow().Phases
	if ValidateWorkflow(w) == nil {
		t.Fatal("ambiguous workflow")
	}
	w = graphWorkflow()
	w.SchemaVersion = 1
	if ValidateWorkflow(w) == nil {
		t.Fatal("wrong version")
	}
	w = workflow()
	w.SchemaVersion = 2
	if ValidateWorkflow(w) == nil {
		t.Fatal("missing graph")
	}
	parent, err := store.CreateTask(StartTask{Workflow: graphWorkflow()})
	if err != nil {
		t.Fatal(err)
	}
	for depth := 1; depth <= 8; depth++ {
		parent, err = store.CreateTask(StartTask{Workflow: graphWorkflow(), ParentTaskID: parent.ID, Depth: depth})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, start := range []StartTask{{Workflow: graphWorkflow(), ParentTaskID: parent.ID, Depth: 9}, {Workflow: graphWorkflow(), Depth: 1}, {Workflow: graphWorkflow(), ParentTaskID: "missing", Depth: 1}, {Workflow: graphWorkflow(), ParentTaskID: parent.ID, Depth: 1}, {Workflow: graphWorkflow(), Depth: -1}} {
		if _, err = store.CreateTask(start); err == nil {
			t.Fatal("invalid nested workflow accepted")
		}
	}
}
func TestGraphCancellationConfirmedSeparately(t *testing.T) {
	store, _ := openTestStore(t)
	for _, finish := range []string{"confirm", "cancel"} {
		task, err := store.CreateTask(StartTask{Workflow: graphWorkflow()})
		if err != nil {
			t.Fatal(err)
		}
		if finish == "cancel" {
			task, err = store.Cancel(task.ID)
		} else {
			if _, err = store.ConfirmCancellation(task.ID); err == nil {
				t.Fatal("unrequested confirm")
			}
			task, err = store.RequestCancellation(task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.ApplyGraphEvent(task.ID, verk.Event{Type: "cancel", TrustedOwner: true, ExpectedRevision: task.GraphState.Revision}); err == nil {
				t.Fatal("graph bypassed pending cancellation")
			}
			if _, err = store.Resume(task.ID, "bypass"); err == nil {
				t.Fatal("bypassed cancel")
			}
			task, err = store.CancellationFailed(task.ID, "remote uncertain")
			if err != nil || task.CancelError == "" {
				t.Fatal(task, err)
			}
			task, err = store.ConfirmCancellation(task.ID)
		}
		if err != nil || task.Status != Cancelled || task.GraphState.Status != Cancelled {
			t.Fatal(task, err)
		}
	}
}

func TestNestedAdmissionRejectsCancellingOrTerminalParent(t *testing.T) {
	store, _ := openTestStore(t)
	for _, kind := range []string{"requested", "cancelled", "completed"} {
		parent, err := store.CreateTask(StartTask{Workflow: graphWorkflow()})
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case "requested":
			_, err = store.RequestCancellation(parent.ID)
		case "cancelled":
			_, err = store.Cancel(parent.ID)
		case "completed":
			parent, err = store.ApplyGraphEvent(parent.ID, verk.Event{Type: "start", RunID: "e", RequestID: "e"})
			if err != nil {
				t.Fatal(err)
			}
			parent, err = store.ApplyGraphEvent(parent.ID, verk.Event{Type: "complete", RunID: "e", Port: "next", ExpectedRevision: 1})
			if err != nil {
				t.Fatal(err)
			}
			parent, err = store.ApplyGraphEvent(parent.ID, verk.Event{Type: "start", RunID: "z", RequestID: "z", ExpectedRevision: 2})
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.ApplyGraphEvent(parent.ID, verk.Event{Type: "complete", RunID: "z", ExpectedRevision: 3})
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.CreateTask(StartTask{Workflow: graphWorkflow(), ParentTaskID: parent.ID, Depth: 1}); err == nil {
			t.Fatal("nested admission accepted for", kind)
		}
	}
}
