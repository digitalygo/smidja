package managerstate

import (
	"errors"
	"reflect"
	"testing"
)

func TestSessionTurnRecovery(t *testing.T) {
	s, dir := openTestStore(t)
	session, err := s.CreateSession("CEO")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.StartSessionTurn(session.ID, "request1", "  Original request\n")
	if err != nil {
		t.Fatal(err)
	}
	if pending.Turn.Status != "submitting" || pending.Messages[0].Content != "  Original request\n" {
		t.Fatal("input not recorded")
	}
	if _, err = s.AttachSessionTurn(session.ID, "request1", "chat1", "turn1"); err != nil {
		t.Fatal(err)
	}
	running, err := s.RecordSessionSequence(session.ID, "request1", 42)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.GetSession(session.ID)
	if err != nil || !reflect.DeepEqual(running, restored) {
		t.Fatal("turn replay mismatch", err)
	}
	if _, err = reopened.StartSessionTurn(session.ID, "request2", "new"); !errors.Is(err, ErrConflict) {
		t.Fatal("second pending turn accepted")
	}
	if _, err = reopened.PauseSessionTurn(session.ID, "request1", "connection lost"); err != nil {
		t.Fatal(err)
	}
	following, err := reopened.ResumeSessionFollow(session.ID, "request1")
	if err != nil {
		t.Fatal(err)
	}
	if following.Turn.LastSequence != 42 || following.Turn.TurnID != "turn1" || len(following.Messages) != 1 {
		t.Fatal("recovery resubmitted")
	}
	completed, err := reopened.CompleteSessionTurn(session.ID, "request1", "\nOriginal final text ✓\n")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Turn.Status != Completed || len(completed.Messages) != 2 || completed.Messages[1].Content != "\nOriginal final text ✓\n" {
		t.Fatal("bad final output")
	}
	if _, err = reopened.CompleteSessionTurn(session.ID, "request1", "late duplicate"); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate completion accepted")
	}
	if _, err = reopened.StartSessionTurn(session.ID, "request1", "duplicate request"); !errors.Is(err, ErrConflict) {
		t.Fatal("request replayed")
	}
	if _, err = reopened.StartSessionTurn(session.ID, "request2", "second"); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.CancelSessionTurn(session.ID, "request2"); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.AttachSessionTurn(session.ID, "request2", "chat1", "turn2"); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled turn revived")
	}
	if _, err = reopened.StartSessionTurn(session.ID, "request3", "third"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionTurnInvalidTransitions(t *testing.T) {
	s, _ := openTestStore(t)
	session, err := s.CreateSession("CEO")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartSessionTurn(session.ID, "", "text"); err == nil {
		t.Fatal("empty ID")
	}
	if _, err = s.StartSessionTurn(session.ID, "r1", ""); err == nil {
		t.Fatal("empty text")
	}
	if _, err = s.ResumeSessionFollow(session.ID, "r1"); err == nil {
		t.Fatal("missing turn")
	}
	if _, err = s.StartSessionTurn(session.ID, "r1", "text"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteSessionTurn(session.ID, "r1", "result"); err == nil {
		t.Fatal("unattached completion")
	}
	if _, err = s.RecordSessionSequence(session.ID, "r1", 1); err == nil {
		t.Fatal("unattached event")
	}
	if _, err = s.AttachSessionTurn(session.ID, "r1", "", "turn"); err == nil {
		t.Fatal("no chat")
	}
	if _, err = s.PauseSessionTurn(session.ID, "wrong", "reason"); err == nil {
		t.Fatal("wrong correlation")
	}
	if _, err = s.PauseSessionTurn(session.ID, "r1", ""); err == nil {
		t.Fatal("empty pause reason")
	}
	if _, err = s.PauseSessionTurn(session.ID, "r1", "unknown dispatch result"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResumeSessionFollow(session.ID, "r1"); err == nil {
		t.Fatal("unknown submission retried")
	}
	if _, err = s.CancelSessionTurn(session.ID, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartSessionTurn(session.ID, "r2", "text"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachSessionTurn(session.ID, "r2", "chat", "turn"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordSessionSequence(session.ID, "r2", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordSessionSequence(session.ID, "r2", 1); err == nil {
		t.Fatal("duplicate sequence")
	}
	if _, err = s.RecordSessionSequence(session.ID, "r2", 0); err == nil {
		t.Fatal("backwards sequence")
	}
	if _, err = s.ResumeSessionFollow(session.ID, "r2"); err != nil {
		t.Fatal("running recovery refused", err)
	}
	if _, err = s.CompleteSessionTurn(session.ID, "r2", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartSessionTurn(session.ID, "r3", "text"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachSessionTurn(session.ID, "r3", "different", "turn3"); err == nil {
		t.Fatal("chat changed")
	}
}

func TestSessionOutputBeforeCursorSurvivesRestart(t *testing.T) {
	s, dir := openTestStore(t)
	session, _ := s.CreateSession("CEO")
	s.StartSessionTurn(session.ID, "request", "input")
	if _, err := s.RecordSessionOutput(session.ID, "request", "premature"); err == nil {
		t.Fatal("unattached output accepted")
	}
	s.AttachSessionTurn(session.ID, "request", "chat", "turn")
	const output = "\nVerbatim result ✓\n"
	if _, err := s.RecordSessionOutput(session.ID, "request", output); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSessionOutput(session.ID, "request", output); err != nil {
		t.Fatal("replayed identical output rejected", err)
	}
	if _, err := s.RecordSessionOutput(session.ID, "request", "different"); err == nil {
		t.Fatal("output overwritten")
	}
	if _, err := s.CompleteSessionTurn(session.ID, "request", "different"); err == nil {
		t.Fatal("completion overwrote recorded output")
	}
	if _, err := s.RecordSessionSequence(session.ID, "request", 19); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := reopened.ResumeSessionFollow(session.ID, "request")
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Turn.OutputRecorded || restored.Turn.Output != output || restored.Turn.LastSequence != 19 || len(restored.Messages) != 1 {
		t.Fatal("recovery lost output or resubmitted", restored)
	}
	completed, err := reopened.CompleteSessionTurn(session.ID, "request", restored.Turn.Output)
	if err != nil || len(completed.Messages) != 2 || completed.Messages[1].Content != output {
		t.Fatal("completion lost output", err)
	}
	if _, err := reopened.RecordSessionOutput(session.ID, "request", output); err == nil {
		t.Fatal("terminal output accepted")
	}
}

func TestTaskOriginRemainsPinnedToOriginalTurn(t *testing.T) {
	s, dir := openTestStore(t)
	session, err := s.CreateSession("CEO")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartSessionTurn(session.ID, "r1", "input"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachSessionTurn(session.ID, "r1", "chat", "first"); err != nil {
		t.Fatal(err)
	}
	start := StartTask{Workflow: workflow(), OriginSessionID: session.ID, OriginChatID: "chat", OriginTurnID: "first", InvocationKey: "invocation"}
	task, err := s.CreateTask(start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteSessionTurn(session.ID, "r1", "working in background"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartSessionTurn(session.ID, "r2", "another request"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachSessionTurn(session.ID, "r2", "chat", "second"); err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.CreateTask(start)
	if err != nil || duplicate.ID != task.ID {
		t.Fatal("dedup failed", err)
	}
	start.OriginTurnID = "second"
	if _, err = s.CreateTask(start); err == nil {
		t.Fatal("origin reassigned")
	}
	s.Close()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetTask(task.ID)
	if err != nil || got.OriginTurnID != "first" || got.OriginChatID != "chat" || got.OriginSessionID != session.ID {
		t.Fatal("task origin changed", got, err)
	}
}
