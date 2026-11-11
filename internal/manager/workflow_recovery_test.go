package manager

import (
	"context"
	"errors"
	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
	"strings"
	"testing"
)

func TestNativeResumeFollowsRetainedReceipt(t *testing.T) {
	s, n, f, task := singleNativeTask(t)
	s.cfg.WorkflowRuntime = n
	phase, err := s.cfg.Store.CurrentContext(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := n.StartPhase(context.Background(), task, phase)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachRemote(task.ID, "phase-request-single", execution.ChatID, execution.TurnID); err != nil {
		t.Fatal(err)
	}
	f.taskErr = errors.New("503 temporarily unavailable")
	s.runTask(context.Background(), task.ID)
	paused := getWorkflowTask(t, s, task.ID)
	if paused.Status != managerstate.Paused {
		t.Fatal(paused)
	}
	if _, err = s.cfg.Store.Resume(task.ID, "retry observation"); err != nil {
		t.Fatal(err)
	}
	f.taskErr = nil
	s.runTask(context.Background(), task.ID)
	got := getWorkflowTask(t, s, task.ID)
	if got.Status != managerstate.Completed || len(got.Phases[0].Attempts) != 1 || got.Phases[0].Attempts[0].RequestID != "phase-request-single" || len(f.delegates) != 1 || len(got.Clarifications) != 0 {
		t.Fatal(got, len(f.delegates))
	}
}

func TestNativeNotificationRedeliveryKeepsSnapshot(t *testing.T) {
	s, n, f, task := singleNativeTask(t)
	phase, _ := s.cfg.Store.CurrentContext(task.ID)
	if _, err := n.StartPhase(context.Background(), task, phase); err != nil {
		t.Fatal(err)
	}
	s.cfg.WorkflowRuntime = n
	f.deliverErr = errors.New("lost acknowledgement")
	s.runTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Completed || len(f.deliveries) != 1 {
		t.Fatal(task, f.deliveries)
	}
	first := f.deliveries[0]
	task.Reason = "must not alter saved notification"
	f.deliverErr = nil
	if err := n.Notify(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if len(f.deliveries) != 2 || f.deliveries[1] != first {
		t.Fatal(f.deliveries)
	}
	if err := n.Notify(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if len(f.deliveries) != 2 || len(f.delegates) != 1 {
		t.Fatal(f.deliveries, f.delegates)
	}
}

func singleNativeTask(t *testing.T) (*Server, *nativeWorkflow, *nativeFake, managerstate.Task) {
	t.Helper()
	s, n, f, original := nativeTest(t)
	input := workflowInput()
	input.Workflow.Phases = input.Workflow.Phases[:1]
	input.OriginSessionID = original.OriginSessionID
	input.OriginChatID = original.OriginChatID
	input.OriginTurnID = original.OriginTurnID
	task, err := s.cfg.Store.CreateTask(input)
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.cfg.Store.StartPhase(task.ID, "phase-request-single")
	if err != nil {
		t.Fatal(err)
	}
	return s, n, f, task
}

func TestNotificationRetryAPIRequiresCurrentReceipt(t *testing.T) {
	s, n, f, task := singleNativeTask(t)
	phase, _ := s.cfg.Store.CurrentContext(task.ID)
	if _, err := n.StartPhase(context.Background(), task, phase); err != nil {
		t.Fatal(err)
	}
	s.cfg.WorkflowRuntime = n
	f.deliverErr = errors.New("lost acknowledgement")
	s.runTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	path := "/api/tasks/" + task.ID + "/retry-notification"
	body := `{"event_id":"` + notificationEventID(task) + `"}`
	if w := request(s, "POST", path, body, nil); w.Code == 200 {
		t.Fatal("unauthorized retry")
	}
	if w := request(s, "POST", path, `{"event_id":"stale"}`, ownerHeaders()); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	f.deliverErr = nil
	for range 2 {
		if w := request(s, "POST", path, body, ownerHeaders()); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if len(f.deliveries) != 2 || len(f.delegates) != 1 {
		t.Fatal(f.deliveries, f.delegates)
	}
}

type cancelDuringAdmission struct {
	*nativeFake
	server *Server
	taskID string
}

func (f *cancelDuringAdmission) Delegate(ctx context.Context, chat string, in envai.DelegationInput) (envai.Delegation, error) {
	result, err := f.nativeFake.Delegate(ctx, chat, in)
	if _, cancelErr := f.server.cfg.Store.RequestCancellation(f.taskID); cancelErr != nil {
		return result, cancelErr
	}
	return result, err
}
func TestLateAdmissionHandleIsCancelled(t *testing.T) {
	s, n, f, task := singleNativeTask(t)
	n.remote = &cancelDuringAdmission{nativeFake: f, server: s, taskID: task.ID}
	phase, _ := s.cfg.Store.CurrentContext(task.ID)
	if _, err := n.StartPhase(context.Background(), task, phase); err == nil {
		t.Fatal("cancelled admission reported success")
	}
	if f.cancels != 1 {
		t.Fatal(f.cancels)
	}
	receipt, err := s.cfg.Store.GetTool("phase-request-single")
	if err != nil || receipt.Status != managerstate.Completed {
		t.Fatal(receipt, err)
	}
}

func TestCancellationVisitsEveryUnfinishedReceipt(t *testing.T) {
	s, n, f, task := singleNativeTask(t)
	for _, id := range []string{"older", "newer", "finished"} {
		if _, _, err := s.cfg.Store.BeginTool(id, "origin-chat", "origin-turn", "workflow.delegate", nil, "envai"); err != nil {
			t.Fatal(err)
		}
		if err := s.cfg.Store.FinishTool(id, cancellationHandle(), false); err != nil {
			t.Fatal(err)
		}
	}
	task.Phases[0].Attempts = []managerstate.Attempt{{RequestID: "older"}, {RequestID: "newer"}, {RequestID: "finished", OutputRecorded: true}}
	if err := n.CancelPhase(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if f.cancels != 2 {
		t.Fatal(f.cancels)
	}
}

func TestNotificationConcurrentRedeliveryUsesOneReceipt(t *testing.T) {
	s, n, f, task := singleNativeTask(t)
	phase, _ := s.cfg.Store.CurrentContext(task.ID)
	if _, err := n.StartPhase(context.Background(), task, phase); err != nil {
		t.Fatal(err)
	}
	s.cfg.WorkflowRuntime = n
	f.deliverErr = errors.New("lost ack")
	s.runTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	f.deliverErr = nil
	outcomes := make(chan error, 2)
	for range 2 {
		go func() { outcomes <- n.Notify(context.Background(), task) }()
	}
	for range 2 {
		if err := <-outcomes; err != nil {
			t.Fatal(err)
		}
	}
	if len(f.deliveries) != 2 {
		t.Fatal(f.deliveries)
	}
}

func TestFailedLateCancellationRetainsRecoverableHandle(t *testing.T) {
	s, n, f, task := singleNativeTask(t)
	f.cancelErr = errors.New("remote unavailable")
	n.remote = &cancelDuringAdmission{nativeFake: f, server: s, taskID: task.ID}
	phase, _ := s.cfg.Store.CurrentContext(task.ID)
	if _, err := n.StartPhase(context.Background(), task, phase); err == nil || !strings.Contains(err.Error(), "remote unavailable") {
		t.Fatal(err)
	}
	task = getWorkflowTask(t, s, task.ID)
	if !task.CancelRequested || task.Status == managerstate.Cancelled {
		t.Fatal(task)
	}
	f.cancelErr = nil
	if err := n.CancelPhase(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if f.cancels != 2 {
		t.Fatal(f.cancels)
	}
}

type resumeDuringNotification struct {
	*nativeFake
	server *Server
	taskID string
}

func (f *resumeDuringNotification) Deliver(ctx context.Context, chat string, input envai.DeliveryInput) (envai.DeliveryReceipt, error) {
	if _, err := f.server.cfg.Store.Resume(f.taskID, "continue with new instructions"); err != nil {
		return envai.DeliveryReceipt{}, err
	}
	return f.nativeFake.Deliver(ctx, chat, input)
}
func TestAdmittedNotificationPinsOldOriginAcrossResume(t *testing.T) {
	s, n, f, task := singleNativeTask(t)
	if _, err := s.cfg.Store.RecordOutput(task.ID, "phase-request-single", "blocked output"); err != nil {
		t.Fatal(err)
	}
	task, err := s.cfg.Store.Classify(task.ID, "phase-request-single", managerstate.DecisionBlocked, "classifier")
	if err != nil {
		t.Fatal(err)
	}
	f.deliverErr = errors.New("lost ack")
	if err = n.Notify(context.Background(), task); err == nil {
		t.Fatal("expected failure")
	}
	first := f.deliveries[0]
	f.deliverErr = nil
	n.remote = &resumeDuringNotification{nativeFake: f, server: s, taskID: task.ID}
	if err = n.Notify(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if f.deliveries[1] != first || getWorkflowTask(t, s, task.ID).Status != managerstate.Queued {
		t.Fatal(f.deliveries)
	}
	s.cfg.WorkflowRuntime = n
	body := `{"event_id":"` + first.EventID + `"}`
	if w := request(s, "POST", "/api/tasks/"+task.ID+"/retry-notification", body, ownerHeaders()); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestRetainedReceiptRestartsObserverOnNewManager(t *testing.T) {
	s, n, f, task := singleNativeTask(t)
	phase, _ := s.cfg.Store.CurrentContext(task.ID)
	execution, err := n.StartPhase(context.Background(), task, phase)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachRemote(task.ID, "phase-request-single", execution.ChatID, execution.TurnID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.Pause(task.ID, "manager stopped"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	next, err := New(Config{Token: testToken, Store: s.cfg.Store, Remote: f, WorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	next.cfg.WorkflowRuntime = &nativeWorkflow{server: next, remote: f}
	if _, err = next.cfg.Store.Resume(task.ID, "recover observer"); err != nil {
		t.Fatal(err)
	}
	next.runTask(context.Background(), task.ID)
	got := getWorkflowTask(t, next, task.ID)
	if got.Status != managerstate.Completed || len(f.delegates) != 1 || len(got.Phases[0].Attempts) != 1 {
		t.Fatal(got, f.delegates)
	}
}

func TestBlockedNotificationFailuresKeepEachAttemptIdentity(t *testing.T) {
	s, _, _, task := singleNativeTask(t)
	task.Status = managerstate.Blocked
	first := notificationEventID(task)
	s.recordNotificationFailure(task, errors.New("lost ack one"))
	task.Phases[0].Attempts = append(task.Phases[0].Attempts, managerstate.Attempt{RequestID: "second-blocked-attempt"})
	second := notificationEventID(task)
	s.recordNotificationFailure(task, errors.New("lost ack two"))
	session, err := s.cfg.Store.GetSession(task.OriginSessionID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, message := range session.Messages {
		if strings.HasPrefix(message.ID, "notification-failure:") {
			seen[message.ID] = true
		}
	}
	if !seen["notification-failure:"+first] || !seen["notification-failure:"+second] {
		t.Fatal(session.Messages)
	}
}
