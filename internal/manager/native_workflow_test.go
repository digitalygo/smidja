package manager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
)

type nativeFake struct {
	Remote
	readyErr, createErr, delegateErr, followErr, cancelErr, classifyErr, deliverErr error
	features                                                                        []string
	delegates                                                                       []envai.DelegationInput
	deliveries                                                                      []envai.DeliveryInput
	classifications                                                                 []envai.ClassificationInput
	creates, cancels                                                                int
	model                                                                           string
	events                                                                          []envai.Event
	taskStatus                                                                      string
	taskOutput                                                                      string
	taskErr                                                                         error
	taskCalls                                                                       int
}

func (f *nativeFake) RequireFeatures(_ context.Context, features ...string) error {
	f.features = features
	return f.readyErr
}
func (f *nativeFake) CreateChat(context.Context, string, string) (envai.Chat, error) {
	f.creates++
	return envai.Chat{ID: "target-chat"}, f.createErr
}
func (f *nativeFake) Delegate(_ context.Context, _ string, in envai.DelegationInput) (envai.Delegation, error) {
	f.delegates = append(f.delegates, in)
	return envai.Delegation{TaskID: "native-task", TargetChatID: in.TargetChatID, TurnID: "target-turn"}, f.delegateErr
}
func (f *nativeFake) Deliver(_ context.Context, _ string, in envai.DeliveryInput) (envai.DeliveryReceipt, error) {
	f.deliveries = append(f.deliveries, in)
	return envai.DeliveryReceipt{EventID: in.EventID, Status: "queued"}, f.deliverErr
}
func (f *nativeFake) Task(_ context.Context, chatID, taskID string) (envai.DetachedTask, error) {
	f.taskCalls++
	if chatID != "origin-chat" || taskID != "native-task" {
		return envai.DetachedTask{}, errors.New("wrong native job lookup")
	}
	status := f.taskStatus
	if status == "" {
		status = "completed"
	}
	output := f.taskOutput
	if output == "" {
		output = "full native phase output"
	}
	return envai.DetachedTask{TaskID: taskID, TargetChatID: "target-chat", OriginTurnID: "origin-turn", Status: status, Output: output}, f.taskErr
}
func (f *nativeFake) StreamEvents(ctx context.Context, _ string, _ int64, _ envai.EventHandler) error {
	<-ctx.Done()
	return ctx.Err()
}
func (f *nativeFake) CancelTask(ctx context.Context, chatID, taskID string, subtree bool) (envai.TaskCancellation, error) {
	f.cancels++
	if ctx.Err() != nil || chatID != "origin-chat" || taskID != "native-task" || !subtree {
		return envai.TaskCancellation{}, errors.New("incorrect cancellation binding")
	}
	return envai.TaskCancellation{TaskID: taskID, Status: "cancelled", Subtree: true}, f.cancelErr
}
func (f *nativeFake) ClassifyPhase(_ context.Context, _ string, in envai.ClassificationInput) (envai.Classification, error) {
	f.classifications = append(f.classifications, in)
	return envai.Classification{Decision: "NOT BLOCKED", Model: f.model, RequestID: in.RequestID}, f.classifyErr
}
func (f *nativeFake) FollowTurn(_ context.Context, _ string, _ string, _ int64, handler envai.EventHandler) (envai.TurnResult, error) {
	for _, event := range f.events {
		if err := handler(event); err != nil {
			return envai.TurnResult{}, err
		}
	}
	return envai.TurnResult{Output: "full native phase output"}, f.followErr
}
func nativeTest(t *testing.T) (*Server, *nativeWorkflow, *nativeFake, managerstate.Task) {
	t.Helper()
	f := &nativeFake{model: "classifier"}
	s := testServer(t, func(c *Config) { c.ClassifierModel = f.model; c.WorkspaceID = "workspace" })
	session, err := s.cfg.Store.CreateSession("CEO")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.StartSessionTurn(session.ID, "origin-request", "implement"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachSessionTurn(session.ID, "origin-request", "origin-chat", "origin-turn"); err != nil {
		t.Fatal(err)
	}
	input := workflowInput()
	input.OriginSessionID = session.ID
	input.OriginChatID = "origin-chat"
	input.OriginTurnID = "origin-turn"
	task, err := s.cfg.Store.CreateTask(input)
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.cfg.Store.StartPhase(task.ID, "phase-request")
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Remote = f
	return s, &nativeWorkflow{server: s, remote: f}, f, task
}

func TestNativeDelegationJournalPreventsDuplicateAdmission(t *testing.T) {
	s, n, f, task := nativeTest(t)
	ctx := context.Background()
	phaseContext, err := s.cfg.Store.CurrentContext(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = n.Ready(ctx); err != nil || len(f.features) != 7 {
		t.Fatal(err, f.features)
	}
	for _, required := range []string{"native_deliveries", "cross_team_delegation", "detached_background", "phase_classification", "delegated_job_lifecycle", "delegation_notify_origin", "strict_model"} {
		found := false
		for _, feature := range f.features {
			if feature == required {
				found = true
			}
		}
		if !found {
			t.Fatal("missing capability gate", required)
		}
	}
	execution, err := n.StartPhase(ctx, task, phaseContext)
	if err != nil {
		t.Fatal(err)
	}
	if execution.ChatID != "target-chat" || execution.NativeTaskID != "native-task" || execution.OriginChatID != "origin-chat" || f.creates != 1 || len(f.delegates) != 1 {
		t.Fatal(execution)
	}
	repeated, err := n.StartPhase(ctx, task, phaseContext)
	if err != nil || repeated != execution || f.creates != 1 {
		t.Fatal(repeated, err)
	}
	in := f.delegates[0]
	if in.StrictModel == nil || !*in.StrictModel || in.EventID != "phase-request" || in.WorkspaceID != "workspace" || in.OriginTurnID != "origin-turn" || in.NotifyOrigin == nil || *in.NotifyOrigin || !strings.Contains(in.Prompt, "ascii board") {
		t.Fatal(in)
	}
	out, err := n.FollowPhase(ctx, execution)
	if err != nil || out != "full native phase output" {
		t.Fatal(out, err)
	}
	classification, err := n.Classify(ctx, task, phaseContext, out)
	if err != nil || classification.Decision != managerstate.DecisionNotBlocked || classification.Model != "classifier" {
		t.Fatal(classification, err)
	}
	if len(f.classifications) != 1 || f.classifications[0].FinalOutput != out || f.classifications[0].Attempt != 1 || f.classifications[0].PhaseID != "plan" {
		t.Fatal(f.classifications)
	}
	task, _ = s.cfg.Store.RecordOutput(task.ID, "phase-request", out)
	task, err = s.cfg.Store.Classify(task.ID, "phase-request", managerstate.DecisionBlocked, "classifier")
	if err != nil {
		t.Fatal(err)
	}
	if err = n.Notify(ctx, task); err != nil {
		t.Fatal(err)
	}
	if len(f.deliveries) != 1 || f.deliveries[0].Status != managerstate.Blocked || !strings.Contains(f.deliveries[0].Output, out) || !strings.Contains(f.deliveries[0].EventID, "phase-request") {
		t.Fatal(f.deliveries)
	}
	task.Status = managerstate.Completed
	task.PhaseIndex = len(task.Phases)
	task.Phases[len(task.Phases)-1].Attempts = task.Phases[0].Attempts
	if err = n.Notify(ctx, task); err != nil {
		t.Fatal(err)
	}
}

func TestNativeWorkflowMissingOriginAndAdmissionErrors(t *testing.T) {
	for _, mode := range []string{"missing", "unattached", "create", "delegate", "uncertain", "bad-input"} {
		t.Run(mode, func(t *testing.T) {
			s, n, f, task := nativeTest(t)
			phaseContext, _ := s.cfg.Store.CurrentContext(task.ID)
			switch mode {
			case "missing":
				task.OriginSessionID = "missing"
			case "unattached":
				session, _ := s.cfg.Store.CreateSession("new")
				task.OriginSessionID = session.ID
			case "create":
				f.createErr = errors.New("cannot create chat")
			case "delegate":
				f.delegateErr = errors.New("unknown admission")
			case "uncertain":
				f.delegateErr = errors.New("connection lost")
				_, _ = n.StartPhase(context.Background(), task, phaseContext)
				f.delegateErr = nil
			case "bad-input":
				phaseContext.Inputs = json.RawMessage(`{`)
			}
			if _, err := n.StartPhase(context.Background(), task, phaseContext); err == nil {
				t.Fatal("expected failure")
			}
			if mode == "uncertain" && len(f.delegates) != 1 {
				t.Fatal("duplicate admission")
			}
			if mode == "missing" || mode == "unattached" {
				if _, err := n.Classify(context.Background(), task, phaseContext, "out"); err == nil {
					t.Fatal("classification missing origin")
				}
				if err := n.Notify(context.Background(), task); err == nil {
					t.Fatal("notification missing origin")
				}
			}
		})
	}
}

func TestNativeClassificationUncertaintyAndActualModel(t *testing.T) {
	s, n, f, task := nativeTest(t)
	phaseContext, _ := s.cfg.Store.CurrentContext(task.ID)
	f.classifyErr = errors.New("provider502")
	if _, err := n.Classify(context.Background(), task, phaseContext, "out"); err == nil || !strings.Contains(err.Error(), "charge/outcome may be unknown") {
		t.Fatal(err)
	}
	first := f.classifications[0].RequestID
	f.classifyErr = nil
	f.model = "classifier-20261014"
	if got, err := n.Classify(context.Background(), task, phaseContext, "out"); err != nil || got.Model != f.model {
		t.Fatal(got, err)
	}
	if f.classifications[1].RequestID == first {
		t.Fatal("explicit retry reused classifier billing identity")
	}
	phaseContext.Inputs = json.RawMessage(`{`)
	if _, err := n.Classify(context.Background(), task, phaseContext, "out"); err == nil {
		t.Fatal("invalid context accepted")
	}
	f.deliverErr = errors.New("delivery unavailable")
	if err := n.Notify(context.Background(), task); err == nil {
		t.Fatal("delivery failure hidden")
	}
	task.Inputs = json.RawMessage(`{`)
	if err := n.Notify(context.Background(), task); err == nil {
		t.Fatal("invalid task accepted")
	}
}

func TestNativeObservationCancellationDoesNotCancelRemoteJob(t *testing.T) {
	s, n, f, task := nativeTest(t)
	phaseContext, _ := s.cfg.Store.CurrentContext(task.ID)
	execution, err := n.StartPhase(context.Background(), task, phaseContext)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = n.FollowPhase(ctx, execution); !errors.Is(err, context.Canceled) || f.cancels != 0 {
		t.Fatal(err, f.cancels)
	}
	s.Close()
	if _, err = n.FollowPhase(s.ctx, execution); !errors.Is(err, context.Canceled) || f.cancels != 0 {
		t.Fatal("manager shutdown cancelled remote job", err, f.cancels)
	}
	execution.Observed = false
	if _, err = n.FollowPhase(context.Background(), execution); err == nil || !strings.Contains(err.Error(), "retained observer") {
		t.Fatal(err)
	}
}

func TestNativeOriginRemainsPinnedWhenCEOAcceptsAnotherRequest(t *testing.T) {
	s, n, f, task := nativeTest(t)
	if _, err := s.cfg.Store.CompleteSessionTurn(task.OriginSessionID, "origin-request", "delegated"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.cfg.Store.StartSessionTurn(task.OriginSessionID, "new-request", "unrelated question"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.cfg.Store.AttachSessionTurn(task.OriginSessionID, "new-request", "origin-chat", "new-turn"); err != nil {
		t.Fatal(err)
	}
	phaseContext, _ := s.cfg.Store.CurrentContext(task.ID)
	if _, err := n.StartPhase(context.Background(), task, phaseContext); err != nil {
		t.Fatal(err)
	}
	if f.delegates[0].OriginTurnID != "origin-turn" {
		t.Fatal("workflow rebound to new CEO request")
	}
	if err := n.Notify(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if f.deliveries[0].OriginTurnID != "origin-turn" {
		t.Fatal("notification rebound to new CEO request")
	}
}
