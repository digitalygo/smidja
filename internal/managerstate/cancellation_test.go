package managerstate

import (
	"errors"
	"testing"
)

func TestCancellationPersistsUncertaintyAndRejectsLateResults(t *testing.T) {
	s, dir := openTestStore(t)
	task := startTestTask(t, s)
	if _, err := s.StartPhase(task.ID, "request"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachRemote(task.ID, "request", "chat", "turn"); err != nil {
		t.Fatal(err)
	}
	requested, err := s.RequestCancellation(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requested.Status != Paused || !requested.CancelRequested || requested.CancelError != "" {
		t.Fatal(requested)
	}
	for _, operation := range []func() (Task, error){
		func() (Task, error) { return s.RecordOutput(task.ID, "request", "late") },
		func() (Task, error) { return s.Classify(task.ID, "request", DecisionNotBlocked, "model") },
		func() (Task, error) { return s.StartPhase(task.ID, "retry") },
		func() (Task, error) { return s.AttachRemote(task.ID, "request", "chat", "turn") },
		func() (Task, error) { return s.RetryClassification(task.ID) },
		func() (Task, error) { return s.Resume(task.ID, "continue") },
		func() (Task, error) { return s.Cancel(task.ID) },
		func() (Task, error) { return s.Pause(task.ID, "other error") },
	} {
		if _, err = operation(); !errors.Is(err, ErrConflict) {
			t.Fatal("unconfirmed cancellation allowed transition", err)
		}
	}
	failed, err := s.CancellationFailed(task.ID, "remote disconnected")
	if err != nil || failed.Status != Paused || failed.CancelError != "remote disconnected" {
		t.Fatal(failed, err)
	}
	s.Close()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.GetTask(task.ID)
	if err != nil || !restored.CancelRequested || restored.CancelError != "remote disconnected" || restored.Status != Paused {
		t.Fatal(restored, err)
	}
	retry, err := reopened.RequestCancellation(task.ID)
	if err != nil || retry.CancelError != "" || !retry.CancelRequested {
		t.Fatal(retry, err)
	}
	cancelled, err := reopened.ConfirmCancellation(task.ID)
	if err != nil || cancelled.Status != Cancelled || cancelled.CancelError != "" || cancelled.Reason != "" {
		t.Fatal(cancelled, err)
	}
	if _, err = reopened.RequestCancellation(task.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("terminal task reactivated", err)
	}
}
func TestCancellationRequiresIntentAndKeepsSavedOutput(t *testing.T) {
	s, _ := openTestStore(t)
	task := startTestTask(t, s)
	if _, err := s.ConfirmCancellation(task.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("confirmed without intent", err)
	}
	if _, err := s.CancellationFailed(task.ID, "error"); !errors.Is(err, ErrConflict) {
		t.Fatal("failed without intent", err)
	}
	s.StartPhase(task.ID, "request")
	s.RecordOutput(task.ID, "request", "saved output")
	if _, err := s.RequestCancellation(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancellationFailed(task.ID, ""); !errors.Is(err, ErrConflict) {
		t.Fatal("empty failure", err)
	}
	cancelled, err := s.ConfirmCancellation(task.ID)
	if err != nil || cancelled.Phases[0].Attempts[0].Output != "saved output" {
		t.Fatal(cancelled, err)
	}
	queued := startTestTask(t, s)
	if _, err = s.RequestCancellation(queued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmCancellation(queued.ID); err != nil {
		t.Fatal(err)
	}
}
