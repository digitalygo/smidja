package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/managerstate"
)

type workflowFake struct {
	readyErr, startErr, followErr, classifyErr, notifyErr error
	starts                                                []managerstate.Task
	contexts                                              []managerstate.PhaseContext
	outputs                                               []string
	notifications                                         []managerstate.Task
	decisions                                             []managerstate.Decision
	classifications                                       int
	onFollow                                              func(context.Context)
	invalidExecution                                      bool
}

func (f *workflowFake) Ready(context.Context) error { return f.readyErr }
func (f *workflowFake) StartPhase(_ context.Context, t managerstate.Task, c managerstate.PhaseContext) (PhaseExecution, error) {
	f.starts = append(f.starts, t)
	f.contexts = append(f.contexts, c)
	if f.invalidExecution {
		return PhaseExecution{}, nil
	}
	return PhaseExecution{ChatID: "chat", TurnID: fmt.Sprintf("turn-%d", len(f.starts))}, f.startErr
}
func (f *workflowFake) FollowPhase(ctx context.Context, _ PhaseExecution) (string, error) {
	if f.onFollow != nil {
		f.onFollow(ctx)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return fmt.Sprintf("Complete phase output %d\nAll findings and precise details preserved.", len(f.starts)), f.followErr
}
func (f *workflowFake) Classify(_ context.Context, _ managerstate.Task, _ managerstate.PhaseContext, output string) (Classification, error) {
	f.outputs = append(f.outputs, output)
	index := f.classifications
	f.classifications++
	decision := managerstate.DecisionNotBlocked
	if index < len(f.decisions) {
		decision = f.decisions[index]
	}
	return Classification{Decision: decision, Model: "gpt-6-luna-decision"}, f.classifyErr
}
func (f *workflowFake) Notify(_ context.Context, t managerstate.Task) error {
	f.notifications = append(f.notifications, t)
	return f.notifyErr
}
func workflowInput() managerstate.StartTask {
	return managerstate.StartTask{Workflow: managerstate.Workflow{ID: "coding", Version: "1.0.0", Phases: []managerstate.Phase{{ID: "plan", TeamRef: "planning"}, {ID: "build", TeamRef: "coding"}, {ID: "review", TeamRef: "review"}}}, Inputs: json.RawMessage(`{"feature":"ascii board"}`), Focus: "preserve behaviour"}
}
func workflowTask(t *testing.T, s *Server) managerstate.Task {
	t.Helper()
	task, err := s.cfg.Store.CreateTask(workflowInput())
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func getWorkflowTask(t *testing.T, s *Server, id string) managerstate.Task {
	t.Helper()
	task, err := s.cfg.Store.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestWorkflowPreservesEveryOutputAndSequentialContext(t *testing.T) {
	f := &workflowFake{}
	s := testServer(t, func(c *Config) { c.WorkflowRuntime = f })
	task := workflowTask(t, s)
	s.runTask(context.Background(), task.ID)
	got := getWorkflowTask(t, s, task.ID)
	if got.Status != managerstate.Completed || got.PhaseIndex != 3 || len(f.starts) != 3 || f.classifications != 3 || len(f.notifications) != 1 {
		t.Fatalf("task=%+v runtime=%+v", got, f)
	}
	for i, p := range got.Phases {
		if len(p.Attempts) != 1 || !p.Attempts[0].OutputRecorded || p.Attempts[0].Output != f.outputs[i] || p.Attempts[0].ChatID != "chat" || p.Attempts[0].ClassifierModel != "gpt-6-luna-decision" {
			t.Fatal(p)
		}
		if len(f.contexts[i].Outputs) != i || string(f.contexts[i].Inputs) != string(task.Inputs) || f.contexts[i].Focus != task.Focus {
			t.Fatal(f.contexts[i])
		}
		for j, out := range f.contexts[i].Outputs {
			if out.Output != f.outputs[j] {
				t.Fatal("output truncated or changed")
			}
		}
	}
	s.runTask(context.Background(), task.ID)
	s.runTask(context.Background(), "missing")
	if len(f.starts) != 3 {
		t.Fatal("terminal workflow reran")
	}
}

func TestWorkflowBlockedResumeOnlyBlockedPhase(t *testing.T) {
	f := &workflowFake{decisions: []managerstate.Decision{managerstate.DecisionNotBlocked, managerstate.DecisionBlocked}}
	s := testServer(t, func(c *Config) { c.WorkflowRuntime = f })
	task := workflowTask(t, s)
	s.runTask(context.Background(), task.ID)
	got := getWorkflowTask(t, s, task.ID)
	if got.Status != managerstate.Blocked || got.PhaseIndex != 1 || len(f.notifications) != 1 {
		t.Fatal(got)
	}
	if _, err := s.cfg.Store.Resume(task.ID, "credentials are now available"); err != nil {
		t.Fatal(err)
	}
	s.runTask(context.Background(), task.ID)
	got = getWorkflowTask(t, s, task.ID)
	if got.Status != managerstate.Completed || len(got.Phases[0].Attempts) != 1 || len(got.Phases[1].Attempts) != 2 || len(f.starts) != 4 || len(f.notifications) != 2 {
		t.Fatal(got)
	}
	if f.starts[2].PhaseIndex != 1 || len(f.contexts[2].Clarifications) != 1 || len(f.contexts[2].Outputs) != 2 {
		t.Fatal(f.contexts[2])
	}
}

func TestWorkflowClassifierRetryNeverRepeatsTeam(t *testing.T) {
	f := &workflowFake{classifyErr: errors.New("decision service unavailable")}
	s := testServer(t, func(c *Config) { c.WorkflowRuntime = f })
	task := workflowTask(t, s)
	s.runTask(context.Background(), task.ID)
	got := getWorkflowTask(t, s, task.ID)
	if got.Status != managerstate.Paused || !strings.Contains(got.Reason, "classification failed") || !got.Phases[0].Attempts[0].OutputRecorded {
		t.Fatal(got)
	}
	if _, err := s.cfg.Store.Resume(task.ID, "try again"); err == nil {
		t.Fatal("ordinary resume allowed duplicate work")
	}
	if _, err := s.cfg.Store.RetryClassification(task.ID); err != nil {
		t.Fatal(err)
	}
	f.classifyErr = nil
	s.runTask(context.Background(), task.ID)
	got = getWorkflowTask(t, s, task.ID)
	if got.Status != managerstate.Completed || len(f.starts) != 3 || f.classifications != 4 || f.outputs[0] != f.outputs[1] || len(got.Phases[0].Attempts) != 1 {
		t.Fatal(got, f)
	}
}

func TestWorkflowFailuresPauseAndDoNotInventSuccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fake   workflowFake
		reason string
	}{
		{"ready", workflowFake{readyErr: errors.New("contract missing")}, "contract missing"},
		{"start", workflowFake{startErr: errors.New("cannot start")}, "cannot start"},
		{"follow", workflowFake{followErr: errors.New("connection lost")}, "connection lost"},
		{"binding", workflowFake{invalidExecution: true}, "state conflict"},
		{"decision", workflowFake{decisions: []managerstate.Decision{"INVALID"}}, "invalid classifier decision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.fake
			s := testServer(t, func(c *Config) { c.WorkflowRuntime = &f })
			task := workflowTask(t, s)
			s.runTask(context.Background(), task.ID)
			got := getWorkflowTask(t, s, task.ID)
			if got.Status != managerstate.Paused || !strings.Contains(got.Reason, tc.reason) || len(f.notifications) != 0 {
				t.Fatal(got)
			}
		})
	}
	f := &workflowFake{}
	s := testServer(t, func(c *Config) { c.WorkflowRuntime = f })
	task := workflowTask(t, s)
	if _, err := s.cfg.Store.StartPhase(task.ID, "previous-request"); err != nil {
		t.Fatal(err)
	}
	s.runTask(context.Background(), task.ID)
	got := getWorkflowTask(t, s, task.ID)
	if got.Status != managerstate.Paused || !strings.Contains(got.Reason, "uncertain") || len(f.starts) != 0 {
		t.Fatal(got)
	}
}

func TestWorkflowCancellationAndNotificationFailure(t *testing.T) {
	f := &workflowFake{}
	s := testServer(t, func(c *Config) { c.WorkflowRuntime = f })
	task := workflowTask(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.runTask(ctx, task.ID)
	if got := getWorkflowTask(t, s, task.ID); got.Status != managerstate.Paused || got.Reason != "context canceled" {
		t.Fatal(got)
	}
	task = workflowTask(t, s)
	ctx, cancel = context.WithCancel(context.Background())
	f.onFollow = func(context.Context) { cancel() }
	s.runTask(ctx, task.ID)
	if got := getWorkflowTask(t, s, task.ID); got.Status != managerstate.Paused || got.Phases[0].Attempts[0].OutputRecorded {
		t.Fatal(got)
	}
	f.onFollow = nil
	f.notifyErr = errors.New("CEO unavailable")
	session, err := s.cfg.Store.CreateSession("origin")
	if err != nil {
		t.Fatal(err)
	}
	input := workflowInput()
	input.OriginSessionID = session.ID
	task, err = s.cfg.Store.CreateTask(input)
	if err != nil {
		t.Fatal(err)
	}
	s.runTask(context.Background(), task.ID)
	if got := getWorkflowTask(t, s, task.ID); got.Status != managerstate.Completed {
		t.Fatal(got)
	}
	session, err = s.cfg.Store.GetSession(session.ID)
	if err != nil || len(session.Messages) != 1 || !strings.Contains(session.Messages[0].Content, "notification_failed") || !strings.Contains(session.Messages[0].Content, task.ID) {
		t.Fatal(session, err)
	}
	s.recordNotificationFailure(managerstate.Task{}, errors.New("no origin"))
}

func TestWorkflowStartAPIValidationAndScheduling(t *testing.T) {
	s := testServer(t, nil)
	s.scheduleTask("missing")
	if w := request(s, "POST", "/api/tasks", `{`, ownerHeaders()); w.Code != 400 {
		t.Fatal(w.Code)
	}
	unavailable := testServer(t, func(c *Config) { c.WorkflowRuntime = &workflowFake{readyErr: errors.New("missing native background")} })
	if w := request(unavailable, "POST", "/api/tasks", `{}`, ownerHeaders()); w.Code != 503 {
		t.Fatal(w.Code)
	}
	running := testServer(t, func(c *Config) { c.WorkflowRuntime = &workflowFake{} })
	if w := request(running, "POST", "/api/tasks", `{}`, ownerHeaders()); w.Code != 400 {
		t.Fatal(w.Code)
	}
	raw, _ := json.Marshal(workflowInput())
	w := request(running, "POST", "/api/tasks", string(raw), ownerHeaders())
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	var task managerstate.Task
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for getWorkflowTask(t, running, task.ID).Status != managerstate.Completed {
		if time.Now().After(deadline) {
			t.Fatal("scheduled workflow did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTaskControlAPI(t *testing.T) {
	s := testServer(t, nil)
	task := workflowTask(t, s)
	base := "/api/tasks/" + task.ID
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", base, "", 200}, {"GET", base + "/resume", "", 404}, {"POST", base, "", 404}, {"POST", base + "/unknown", "", 404},
		{"POST", base + "/resume", "{", 400}, {"POST", base + "/resume", `{"clarification":"new info"}`, 409},
		{"POST", base + "/retry-classification", "", 409},
	} {
		w := request(s, tc.method, tc.path, tc.body, ownerHeaders())
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body)
		}
	}
	if _, err := s.cfg.Store.Pause(task.ID, "waiting"); err != nil {
		t.Fatal(err)
	}
	if w := request(s, "POST", base+"/resume", `{"clarification":"done"}`, ownerHeaders()); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	cancelled := false
	s.mu.Lock()
	s.active["task:"+task.ID] = func() { cancelled = true }
	s.mu.Unlock()
	if w := request(s, "POST", base+"/cancel", "", ownerHeaders()); w.Code != 200 || !cancelled {
		t.Fatal(w.Code, w.Body)
	}
	task = workflowTask(t, s)
	task, _ = s.cfg.Store.StartPhase(task.ID, "request")
	task, _ = s.cfg.Store.RecordOutput(task.ID, "request", "full output")
	task, _ = s.cfg.Store.Pause(task.ID, "classify unavailable")
	if w := request(s, "POST", "/api/tasks/"+task.ID+"/retry-classification", "", ownerHeaders()); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestWorkflowAPIRequiresRealOriginAndPinsRemoteTurn(t *testing.T) {
	s := testServer(t, func(c *Config) { c.WorkflowRuntime = &workflowFake{} })
	input := workflowInput()
	input.OriginSessionID = "missing"
	raw, _ := json.Marshal(input)
	if w := request(s, "POST", "/api/tasks", string(raw), ownerHeaders()); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	session, err := s.cfg.Store.CreateSession("CEO")
	if err != nil {
		t.Fatal(err)
	}
	input.OriginSessionID = session.ID
	raw, _ = json.Marshal(input)
	if w := request(s, "POST", "/api/tasks", string(raw), ownerHeaders()); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	if _, err = s.cfg.Store.StartSessionTurn(session.ID, "request", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachSessionTurn(session.ID, "request", "verified-chat", "verified-turn"); err != nil {
		t.Fatal(err)
	}
	input.OriginChatID = "spoofed-chat"
	input.OriginTurnID = "spoofed-turn"
	raw, _ = json.Marshal(input)
	w := request(s, "POST", "/api/tasks", string(raw), ownerHeaders())
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	var task managerstate.Task
	if err = json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.OriginChatID != "verified-chat" || task.OriginTurnID != "verified-turn" {
		t.Fatal(task)
	}
}
