package managerstate

import "testing"

func TestWorkflowSessionOwnershipPersists(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateWorkflowSession("Invalid", ""); err == nil {
		t.Fatal("missing task owner accepted")
	}
	owner, err := store.CreateTask(StartTask{Workflow: Workflow{ID: "review", Version: "1", Phases: []Phase{{ID: "phase", TeamRef: "team"}}}})
	if err != nil {
		t.Fatal(err)
	}
	phase, err := store.CreateWorkflowSession("Review phase", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := store.CreateSession("Direct team")
	if err != nil {
		t.Fatal(err)
	}
	if phase.WorkflowTaskID != owner.ID || direct.WorkflowTaskID != "" {
		t.Fatal(phase, direct)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saved, err := store.GetSession(phase.ID)
	if err != nil || saved.WorkflowTaskID != owner.ID {
		t.Fatal(saved, err)
	}
	ordinary, err := store.GetSession(direct.ID)
	if err != nil || ordinary.WorkflowTaskID != "" {
		t.Fatal(ordinary, err)
	}
}
