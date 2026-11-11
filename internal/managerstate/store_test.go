package managerstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func workflow() Workflow {
	return Workflow{ID: "review", Version: "1.0.0", Phases: []Phase{{ID: "analysis", Name: "Analysis", TeamRef: "reviewer-v1"}, {ID: "verification", Name: "Verification", TeamRef: "verifier-v1"}}}
}

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, dir
}

func startTestTask(t *testing.T, s *Store) Task {
	t.Helper()
	task, err := s.CreateTask(StartTask{Workflow: workflow(), Inputs: json.RawMessage(`{"pr":"https://example.test/pr/1"}`), Focus: "security"})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func mustTask(t *testing.T, task Task, err error) Task {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestWorkflowCompletesAndReplaysVerbatim(t *testing.T) {
	s, dir := openTestStore(t)
	original := StartTask{Workflow: workflow(), Inputs: json.RawMessage(`{"request":"review"}`), InvocationKey: "event-1"}
	task, err := s.CreateTask(original)
	if err != nil {
		t.Fatal(err)
	}
	original.Workflow.Phases[0].TeamRef = "mutated"
	got, _ := s.GetTask(task.ID)
	if got.Workflow.Phases[0].TeamRef != "reviewer-v1" {
		t.Fatal("workflow not pinned")
	}
	original.Workflow.Phases[0].TeamRef = "reviewer-v1"
	repeated, err := s.CreateTask(original)
	if err != nil || repeated.ID != task.ID {
		t.Fatalf("dedup: %v %#v", err, repeated)
	}
	original.Focus = "different"
	if _, err = s.CreateTask(original); !errors.Is(err, ErrConflict) {
		t.Fatal("different invocation accepted")
	}
	longOutput := "\n  quoted \"data\"\n" + strings.Repeat("content ✓\n", 10000)
	if _, err = s.StartPhase(task.ID, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachRemote(task.ID, "r1", "chat1", "turn1"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordOutput(task.ID, "r1", longOutput); err != nil {
		t.Fatal(err)
	}
	next, err := s.Classify(task.ID, "r1", DecisionNotBlocked, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if next.Status != Queued || next.PhaseIndex != 1 {
		t.Fatalf("not next phase: %#v", next)
	}
	ctx, err := s.CurrentContext(task.ID)
	if err != nil || len(ctx.Outputs) != 1 || ctx.Outputs[0].Output != longOutput {
		t.Fatal("lost verbatim context")
	}
	if _, err = s.StartPhase(task.ID, "r2"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordOutput(task.ID, "r2", ""); err != nil {
		t.Fatal("empty completed output must be preserved", err)
	}
	done, err := s.Classify(task.ID, "r2", DecisionNotBlocked, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != Completed {
		t.Fatal(done.Status)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayed, err := reopened.GetTask(task.ID)
	if err != nil || !reflect.DeepEqual(done, replayed) {
		t.Fatal("replay changed task", err)
	}
	replayed.Phases[0].Attempts[0].Output = "mutated"
	pristine, _ := reopened.GetTask(task.ID)
	if pristine.Phases[0].Attempts[0].Output != longOutput {
		t.Fatal("returned values alias state")
	}
	if len(reopened.ListTasks()) != 1 {
		t.Fatal("wrong task count")
	}
}

func TestBlockedResumePreservesHistory(t *testing.T) {
	s, _ := openTestStore(t)
	task := startTestTask(t, s)
	if _, err := s.StartPhase(task.ID, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordOutput(task.ID, "r1", "Need token\nkeep this exact"); err != nil {
		t.Fatal(err)
	}
	blocked, err := s.Classify(task.ID, "r1", DecisionBlocked, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != Blocked || blocked.PhaseIndex != 0 {
		t.Fatal("not blocked")
	}
	if _, err = s.Resume(task.ID, ""); err == nil {
		t.Fatal("empty clarification accepted")
	}
	if _, err = s.Resume(task.ID, "Configured token"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartPhase(task.ID, "r1"); err == nil {
		t.Fatal("reused request ID")
	}
	if _, err = s.StartPhase(task.ID, "r2"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordOutput(task.ID, "r2", "Now complete"); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.Classify(task.ID, "r2", DecisionNotBlocked, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Phases[0].Attempts) != 2 || resumed.Phases[0].Attempts[0].Decision != DecisionBlocked {
		t.Fatal("history lost")
	}
	ctx, err := s.CurrentContext(task.ID)
	if err != nil || len(ctx.Outputs) != 2 || ctx.Clarifications[0] != "Configured token" {
		t.Fatal("context lost")
	}
	if _, err = s.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resume(task.ID, "ignored"); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled task resumed")
	}
}

func TestClassifierRetryDoesNotRepeatTeam(t *testing.T) {
	s, _ := openTestStore(t)
	task := startTestTask(t, s)
	if _, err := s.StartPhase(task.ID, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordOutput(task.ID, "r1", "full result"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pause(task.ID, "classifier unavailable"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(task.ID, "try again"); err == nil {
		t.Fatal("classification failure reran team")
	}
	ready, err := s.RetryClassification(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != Running || len(ready.Phases[0].Attempts) != 1 || ready.Phases[0].Attempts[0].Output != "full result" {
		t.Fatal("retry lost output")
	}
	if _, err = s.Classify(task.ID, "r1", DecisionNotBlocked, "luna"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pause(task.ID, "client offline before dispatch"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resume(task.ID, "client online"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartPhase(task.ID, "r2"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pause(task.ID, "remote outcome unknown"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RetryClassification(task.ID); err == nil {
		t.Fatal("classification without output")
	}
	if _, err = s.Resume(task.ID, "User verified operation not performed"); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidTransitionsDoNotWrite(t *testing.T) {
	s, dir := openTestStore(t)
	task := startTestTask(t, s)
	checks := []func() error{
		func() error { _, e := s.AttachRemote(task.ID, "r1", "c", "t"); return e },
		func() error { _, e := s.RecordOutput(task.ID, "r1", "result"); return e },
		func() error { _, e := s.Classify(task.ID, "r1", DecisionNotBlocked, "luna"); return e },
		func() error { _, e := s.StartPhase(task.ID, ""); return e },
		func() error { _, e := s.Resume(task.ID, "no"); return e },
		func() error { _, e := s.RetryClassification(task.ID); return e },
		func() error { _, e := s.Pause(task.ID, ""); return e },
	}
	path := filepath.Join(dir, "manager.jsonl")
	before, _ := os.ReadFile(path)
	for _, check := range checks {
		if check() == nil {
			t.Fatal("invalid transition accepted")
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed transitions wrote journal")
	}
	if _, err := s.StartPhase(task.ID, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachRemote(task.ID, "wrong", "c", "t"); err == nil {
		t.Fatal("wrong request accepted")
	}
	if _, err := s.AttachRemote(task.ID, "r1", "c", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachRemote(task.ID, "r1", "changed", "t"); err == nil {
		t.Fatal("remote changed")
	}
	if _, err := s.RecordOutput(task.ID, "wrong", "result"); err == nil {
		t.Fatal("wrong output accepted")
	}
	if _, err := s.RecordOutput(task.ID, "r1", "result"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordOutput(task.ID, "r1", "again"); err == nil {
		t.Fatal("duplicate output accepted")
	}
	if _, err := s.Classify(task.ID, "r1", Decision("UNKNOWN"), "luna"); err == nil {
		t.Fatal("invalid enum")
	}
	if _, err := s.Classify(task.ID, "r1", DecisionNotBlocked, ""); err == nil {
		t.Fatal("empty model")
	}
	if _, err := s.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordOutput(task.ID, "r1", "late"); err == nil {
		t.Fatal("late event revived task")
	}
	if _, err := s.GetTask("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.CurrentContext("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Cancel("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestInputValidation(t *testing.T) {
	s, _ := openTestStore(t)
	cases := []StartTask{{}, {Workflow: Workflow{ID: "x", Version: "1"}}, {Workflow: Workflow{ID: "x", Version: "1", Phases: []Phase{{ID: "x", TeamRef: "y"}, {ID: "x", TeamRef: "y"}}}}, {Workflow: Workflow{ID: "x", Version: "1", Phases: []Phase{{ID: "x"}}}}, {Workflow: workflow(), Inputs: json.RawMessage(`invalid`)}, {Workflow: workflow(), OriginSessionID: "missing"}}
	for _, input := range cases {
		if _, err := s.CreateTask(input); err == nil {
			t.Fatalf("accepted %#v", input)
		}
	}
	if _, err := s.CreateTask(StartTask{Workflow: workflow()}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionPersistenceAndDuplicateMessages(t *testing.T) {
	s, dir := openTestStore(t)
	session, err := s.CreateSession("CEO")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachSessionRemote(session.ID, "remote"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachSessionRemote(session.ID, "other"); err == nil {
		t.Fatal("session rebound")
	}
	message := Message{ID: "msg1", Role: "assistant", Content: "\ncomplete ✓ output\n", ChatID: "remote", TurnID: "turn"}
	saved, err := s.AppendMessage(session.ID, message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AppendMessage(session.ID, message); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AppendMessage(session.ID, Message{ID: "msg1", Role: "assistant", Content: "changed"}); err == nil {
		t.Fatal("message changed")
	}
	if _, err = s.AppendMessage(session.ID, Message{Role: "bad"}); err == nil {
		t.Fatal("bad role")
	}
	if _, err = s.AppendMessage(session.ID, Message{Role: "user", ChatID: "different"}); err == nil {
		t.Fatal("wrong remote")
	}
	if _, err = s.AppendMessage("missing", message); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.GetSession("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.AttachSessionRemote("missing", "remote"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if len(s.ListSessions()) != 1 {
		t.Fatal("wrong session count")
	}
	if _, err = s.CreateTask(StartTask{Workflow: workflow(), OriginSessionID: session.ID}); err != nil {
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
	got, err := reopened.GetSession(session.ID)
	if err != nil || !reflect.DeepEqual(saved, got) {
		t.Fatal("session replay mismatch", err)
	}
	got.Messages[0].Content = "modified"
	pristine, _ := reopened.GetSession(session.ID)
	if pristine.Messages[0].Content != message.Content {
		t.Fatal("message alias")
	}
	if _, err = reopened.AppendMessage(session.ID, Message{Role: "user", Content: "next"}); err != nil {
		t.Fatal(err)
	}
}

func TestStoreLockAndClose(t *testing.T) {
	s, dir := openTestStore(t)
	if second, err := Open(dir); err == nil {
		second.Close()
		t.Fatal("second writer allowed")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(StartTask{Workflow: workflow()}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	next, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	next.Close()
}

func TestTrailingPartialIsQuarantined(t *testing.T) {
	s, dir := openTestStore(t)
	task := startTestTask(t, s)
	s.Close()
	path := filepath.Join(dir, "manager.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString(`{"partial":`)
	file.Close()
	recovered, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if !recovered.RecoveredIncompleteTail() {
		t.Fatal("not reported")
	}
	if _, err = recovered.GetTask(task.ID); err != nil {
		t.Fatal(err)
	}
	paths, _ := filepath.Glob(path + ".incomplete-*")
	if len(paths) != 1 {
		t.Fatal("tail not isolated")
	}
	content, _ := os.ReadFile(paths[0])
	if string(content) != `{"partial":` {
		t.Fatal("tail changed")
	}
	if _, err = recovered.CreateSession("after"); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptionFailsClosed(t *testing.T) {
	for _, data := range []string{"bad\n", "\n", `{"schema_version":1}` + "\n", `{"schema_version":1,"unknown":true}` + "\n", "{} {}\n"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "manager.jsonl"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if store, err := Open(dir); err == nil {
			store.Close()
			t.Fatalf("accepted %q", data)
		}
	}
	s, dir := openTestStore(t)
	startTestTask(t, s)
	s.Close()
	path := filepath.Join(dir, "manager.jsonl")
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(data, data...), 0600); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(dir); err == nil {
		store.Close()
		t.Fatal("duplicate sequence accepted")
	}
}

func TestJournalPathsAndWriteFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	os.WriteFile(target, nil, 0600)
	linkDir := t.TempDir()
	os.Symlink(target, filepath.Join(linkDir, "manager.jsonl"))
	if s, err := Open(linkDir); err == nil {
		s.Close()
		t.Fatal("symlink accepted")
	}
	if s, err := Open(target); err == nil {
		s.Close()
		t.Fatal("directory file accepted")
	}
	folderDir := t.TempDir()
	os.Mkdir(filepath.Join(folderDir, "manager.jsonl"), 0700)
	if s, err := Open(folderDir); err == nil {
		s.Close()
		t.Fatal("directory journal accepted")
	}
	s, _ := openTestStore(t)
	s.file.Close()
	if _, err := s.CreateSession("cannot persist"); err == nil {
		t.Fatal("closed journal accepted write")
	}
	if len(s.ListSessions()) != 0 {
		t.Fatal("mutated before durable append")
	}
	if _, err := s.CreateSession("still failed"); err == nil {
		t.Fatal("failure not sticky")
	}
}

func TestConcurrentInvocationDeduplication(t *testing.T) {
	s, _ := openTestStore(t)
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, err := s.CreateTask(StartTask{Workflow: workflow(), InvocationKey: "same"})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- task.ID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("duplicate task")
		}
	}
	if len(s.ListTasks()) != 1 {
		t.Fatal("wrong count")
	}
}
