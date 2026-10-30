package manager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
)

func cancellationReceipt(t *testing.T, s *Server, output string, complete bool) managerstate.Task {
	t.Helper()
	task := workflowTask(t, s)
	var err error
	task, err = s.cfg.Store.StartPhase(task.ID, "cancel-request")
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.cfg.Store.AttachRemote(task.ID, "cancel-request", "target-chat", "target-turn")
	if err != nil {
		t.Fatal(err)
	}
	owned, err := s.cfg.Store.CreateWorkflowSession("Workflow planning", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachSessionRemote(owned.ID, "target-chat"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.cfg.Store.BeginTool("cancel-request", "origin-chat", "origin-turn", "workflow.delegate", json.RawMessage(`{}`), "envai"); err != nil {
		t.Fatal(err)
	}
	if complete {
		if err = s.cfg.Store.FinishTool("cancel-request", output, false); err != nil {
			t.Fatal(err)
		}
	}
	return task
}
func cancellationHandle() string {
	raw, _ := json.Marshal(PhaseExecution{ChatID: "target-chat", TurnID: "target-turn", NativeTaskID: "native-task", OriginChatID: "origin-chat"})
	return string(raw)
}

func TestWorkflowCancellationAfterManagerRestart(t *testing.T) {
	directory := t.TempDir()
	store, err := managerstate.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Token: testToken, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	task := cancellationReceipt(t, s, cancellationHandle(), true)
	s.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := managerstate.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := New(Config{Token: testToken, Store: reopened})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	defer resumed.Close()
	remote := &nativeFake{}
	resumed.cfg.WorkflowRuntime = &nativeWorkflow{server: resumed, remote: remote}
	got, err := resumed.cancelTask(context.Background(), task.ID)
	if err != nil || got.Status != managerstate.Cancelled || remote.cancels != 1 {
		t.Fatal(got, err, remote.cancels)
	}
	if !resumed.workflowToolCancelled("target-chat") || resumed.workflowToolCancelled("unrelated-chat") {
		t.Fatal("incorrect cancellation scope")
	}
	if err = resumed.handleTool(context.Background(), envai.Event{ChatID: "target-chat"}); err == nil || !strings.Contains(err.Error(), "forbids further tools") {
		t.Fatal(err)
	}
	if err = resumed.handleDetachedTool(context.Background(), envai.Event{ChatID: "target-chat"}); err == nil || !strings.Contains(err.Error(), "forbids further tools") {
		t.Fatal(err)
	}
}

func TestWorkflowCancellationUnconfirmedAndExplicitRetry(t *testing.T) {
	for _, mode := range []string{"pending-admission", "bad-receipt", "missing-handle", "remote-error"} {
		t.Run(mode, func(t *testing.T) {
			s := testServer(t, nil)
			f := &nativeFake{}
			s.cfg.WorkflowRuntime = &nativeWorkflow{server: s, remote: f}
			output := cancellationHandle()
			complete := true
			switch mode {
			case "pending-admission":
				complete = false
			case "bad-receipt":
				output = "{"
			case "missing-handle":
				output = "{}"
			case "remote-error":
				f.cancelErr = errors.New("HTTP502")
			}
			task := cancellationReceipt(t, s, output, complete)
			w := request(s, "POST", "/api/tasks/"+task.ID+"/cancel", "", ownerHeaders())
			if w.Code != 502 || !strings.Contains(w.Body.String(), "remote cancellation unconfirmed") {
				t.Fatal(w.Code, w.Body)
			}
			got := getWorkflowTask(t, s, task.ID)
			if got.Status != managerstate.Paused || !got.CancelRequested || got.CancelError == "" || !s.workflowToolCancelled("target-chat") {
				t.Fatal(got)
			}
			if _, err := s.cfg.Store.Resume(task.ID, "resume anyway"); err == nil {
				t.Fatal("unconfirmed cancellation resumed")
			}
			if mode == "remote-error" {
				if !strings.Contains(got.CancelError, "HTTP502") {
					t.Fatal(got)
				}
				f.cancelErr = nil
				w = request(s, "POST", "/api/tasks/"+task.ID+"/cancel", "", ownerHeaders())
				if w.Code != 200 || getWorkflowTask(t, s, task.ID).Status != managerstate.Cancelled || f.cancels != 2 {
					t.Fatal(w.Code, w.Body)
				}
			} else if f.cancels != 0 {
				t.Fatal("cancel called without known handle")
			}
		})
	}
}

func TestWorkflowCancellationBeforeAdmissionAndInvalidTask(t *testing.T) {
	s := testServer(t, nil)
	f := &nativeFake{}
	n := &nativeWorkflow{server: s, remote: f}
	s.cfg.WorkflowRuntime = n
	if _, err := s.cancelTask(context.Background(), "missing"); err == nil {
		t.Fatal("missing task cancellation succeeded")
	}
	task := workflowTask(t, s)
	if got, err := s.cancelTask(context.Background(), task.ID); err != nil || got.Status != managerstate.Cancelled || f.cancels != 0 {
		t.Fatal(got, err)
	}
	task = workflowTask(t, s)
	task, err := s.cfg.Store.StartPhase(task.ID, "no-admission")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.cancelTask(context.Background(), task.ID); err != nil || got.Status != managerstate.Cancelled || f.cancels != 0 {
		t.Fatal(got, err)
	}
	task.PhaseIndex = len(task.Phases)
	if err = n.CancelPhase(context.Background(), task); err != nil {
		t.Fatal(err)
	}
}

type blockedNotificationRuntime struct {
	workflowFake
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *blockedNotificationRuntime) Notify(ctx context.Context, task managerstate.Task) error {
	if task.Status == managerstate.Blocked {
		f.once.Do(func() { close(f.entered) })
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func TestWorkflowResumeQueuedDuringPreviousNotification(t *testing.T) {
	f := &blockedNotificationRuntime{workflowFake: workflowFake{decisions: []managerstate.Decision{managerstate.DecisionBlocked}}, entered: make(chan struct{}), release: make(chan struct{})}
	s := testServer(t, func(c *Config) { c.WorkflowRuntime = f })
	task := workflowTask(t, s)
	var release sync.Once
	defer release.Do(func() { close(f.release) })
	s.scheduleTask(task.ID)
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("notification was not entered")
	}
	w := request(s, "POST", "/api/tasks/"+task.ID+"/resume", `{"clarification":"resolved while notification pending"}`, ownerHeaders())
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	release.Do(func() { close(f.release) })
	deadline := time.Now().Add(time.Second)
	for getWorkflowTask(t, s, task.ID).Status != managerstate.Completed {
		if time.Now().After(deadline) {
			t.Fatal("resume was dropped while prior worker exited")
		}
		time.Sleep(time.Millisecond)
	}
	s.Close()
	if len(f.starts) != 4 {
		t.Fatal("wrong number of phase attempts", len(f.starts))
	}
}

func TestWorkflowCancellationDoesNotBlockSharedDirectTeamChat(t *testing.T) {
	s := testServer(t, nil)
	session, err := s.cfg.Store.CreateSession("Direct planning team conversation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachSessionRemote(session.ID, "direct-team-chat"); err != nil {
		t.Fatal(err)
	}
	task := workflowTask(t, s)
	if _, err = s.cfg.Store.StartPhase(task.ID, "legacy-shared-phase"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachRemote(task.ID, "legacy-shared-phase", "direct-team-chat", "legacy-turn"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	if s.workflowToolCancelled("direct-team-chat") {
		t.Fatal("cancelling workflow blocked unrelated direct-team chat")
	}
	if err = s.handleTool(context.Background(), envai.Event{ChatID: "direct-team-chat"}); err != nil && strings.Contains(err.Error(), "forbids further tools") {
		t.Fatal(err)
	}
	ownedTask := cancellationReceipt(t, s, cancellationHandle(), true)
	if _, err = s.cfg.Store.Cancel(ownedTask.ID); err != nil {
		t.Fatal(err)
	}
	if !s.workflowToolCancelled("target-chat") || s.workflowToolCancelled("direct-team-chat") {
		t.Fatal("exclusive ownership cancellation scope failed")
	}
}
