package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
)

type phaseLifecycleRemote struct {
	*nativeFake
	onDelegate        func()
	stream            chan envai.Event
	observed          chan envai.Event
	opened            chan struct{}
	verifies, submits int
	jobs              chan envai.DetachedTask
	queries           chan struct{}
}

func (f *phaseLifecycleRemote) Delegate(ctx context.Context, chatID string, input envai.DelegationInput) (envai.Delegation, error) {
	if f.onDelegate != nil {
		f.onDelegate()
	}
	return f.nativeFake.Delegate(ctx, chatID, input)
}

func (f *phaseLifecycleRemote) Task(ctx context.Context, chatID, taskID string) (envai.DetachedTask, error) {
	if f.jobs == nil {
		return f.nativeFake.Task(ctx, chatID, taskID)
	}
	if chatID != "origin-chat" || taskID != "native-task" {
		return envai.DetachedTask{}, errors.New("wrong authoritative job identity")
	}
	select {
	case snapshot := <-f.jobs:
		select {
		case f.queries <- struct{}{}:
		case <-ctx.Done():
			return envai.DetachedTask{}, ctx.Err()
		}
		return snapshot, nil
	case <-ctx.Done():
		return envai.DetachedTask{}, ctx.Err()
	}
}
func (f *phaseLifecycleRemote) StreamEvents(ctx context.Context, chatID string, after int64, handler envai.EventHandler) error {
	if chatID != "target-chat" || after != 0 {
		return errors.New("unexpected phase watcher binding")
	}
	close(f.opened)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-f.stream:
			if err := handler(event); err != nil {
				return err
			}
			select {
			case f.observed <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
func (f *phaseLifecycleRemote) VerifyToolRequest(_ context.Context, event envai.Event) (envai.ToolRequest, error) {
	f.verifies++
	return envai.ToolRequest{CallID: event.ID, ToolName: "phase_tasks", Kind: "client", Args: json.RawMessage(`{}`)}, nil
}
func (f *phaseLifecycleRemote) SubmitToolOutput(context.Context, string, string, string, bool) error {
	f.submits++
	return nil
}
func phaseEvent(sequence int64, kind, turn, text string) envai.Event {
	data := json.RawMessage(`{}`)
	if text != "" {
		data, _ = json.Marshal(map[string]string{"text": text})
	}
	return envai.Event{Version: 3, ID: fmt.Sprintf("event-%d", sequence), Sequence: sequence, ChatID: "target-chat", TurnID: turn, Type: kind, Agent: &envai.Agent{InstanceID: "root", Role: "orchestrator"}, Data: data}
}
func sendPhaseEvent(t *testing.T, remote *phaseLifecycleRemote, event envai.Event) {
	t.Helper()
	select {
	case remote.stream <- event:
	case <-time.After(time.Second):
		t.Fatal("watcher unavailable")
	}
	select {
	case got := <-remote.observed:
		if got.ID != event.ID {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("phase event not observed")
	}
}

func TestPhaseWatcherRetainedBeforeAdmissionAndAfterRootCompletion(t *testing.T) {
	s, n, f, task := nativeTest(t)
	remote := &phaseLifecycleRemote{nativeFake: f, stream: make(chan envai.Event, 8), observed: make(chan envai.Event, 8), opened: make(chan struct{})}
	s.cfg.Remote = remote
	s.cfg.ToolBindings = map[string]ToolBinding{"phase_tasks": {Name: "manager.tasks_list"}}
	n.remote = remote
	remote.onDelegate = func() {
		found := false
		for _, session := range s.cfg.Store.ListSessions() {
			if session.ChatID == "target-chat" {
				found = true
				s.mu.Lock()
				_, watching := s.active["watch:"+session.ID]
				s.mu.Unlock()
				if !watching {
					t.Error("delegation admitted before watcher registration")
				}
			}
		}
		if !found {
			t.Error("delegation admitted before durable phase session")
		}
	}
	phaseContext, err := s.cfg.Store.CurrentContext(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := n.StartPhase(context.Background(), task, phaseContext)
	if err != nil || !execution.Observed || execution.SessionID == "" {
		t.Fatal(execution, err)
	}
	select {
	case <-remote.opened:
	case <-time.After(time.Second):
		t.Fatal("phase watcher not connected")
	}
	tool := phaseEvent(1, "agent.tool.request", "target-turn", "")
	output := phaseEvent(2, "agent.text.done", "target-turn", "full result")
	terminal := phaseEvent(3, "turn.completed", "target-turn", "")
	for _, event := range []envai.Event{tool, output, terminal} {
		sendPhaseEvent(t, remote, event)
	}
	f.events = []envai.Event{tool, output, terminal}
	if _, err = n.FollowPhase(context.Background(), execution); err != nil {
		t.Fatal(err)
	}
	if remote.verifies != 1 || remote.submits != 1 {
		t.Fatal("FollowPhase duplicated observer tool handling", remote.verifies, remote.submits)
	}
	sendPhaseEvent(t, remote, phaseEvent(4, "agent.tool.request", "wake-turn", ""))
	sendPhaseEvent(t, remote, phaseEvent(5, "agent.text.done", "wake-turn", "late native result"))
	sendPhaseEvent(t, remote, phaseEvent(6, "turn.completed", "wake-turn", ""))
	if remote.verifies != 2 || remote.submits != 2 {
		t.Fatal("watcher stopped at original terminal", remote.verifies, remote.submits)
	}
	saved, err := s.cfg.Store.GetSession(execution.SessionID)
	if err != nil || saved.LastSequence != 6 || len(saved.Messages) != 2 || saved.Messages[1].Content != "late native result" {
		t.Fatal(saved, err)
	}
	prefix, payload, ok := strings.Cut(f.delegates[0].Prompt, "\n")
	if !ok || !strings.Contains(prefix, "Use the native delegation lifecycle for specialist work") {
		t.Fatal(f.delegates[0].Prompt)
	}
	var body struct {
		Phase   managerstate.Phase        `json:"phase"`
		Context managerstate.PhaseContext `json:"context"`
	}
	if err = json.Unmarshal([]byte(payload), &body); err != nil || body.Phase.ID != "plan" || !strings.Contains(payload, "ascii board") {
		t.Fatal(body, err)
	}
	expected, _ := json.Marshal(struct {
		Phase   managerstate.Phase        `json:"phase"`
		Context managerstate.PhaseContext `json:"context"`
	}{task.Phases[task.PhaseIndex].Phase, phaseContext})
	if payload != string(expected) {
		t.Fatal("phase payload changed behind execution guard", payload, string(expected))
	}
}

func TestPhaseNativeJobWaitsPastInitialAckForFinalSynthesis(t *testing.T) {
	s, n, f, task := nativeTest(t)
	remote := &phaseLifecycleRemote{nativeFake: f, stream: make(chan envai.Event, 8), observed: make(chan envai.Event, 8), opened: make(chan struct{}), jobs: make(chan envai.DetachedTask, 1), queries: make(chan struct{}, 1)}
	s.cfg.Remote = remote
	n.remote = remote
	phaseContext, _ := s.cfg.Store.CurrentContext(task.ID)
	execution, err := n.StartPhase(context.Background(), task, phaseContext)
	if err != nil {
		t.Fatal(err)
	}
	remote.jobs <- envai.DetachedTask{TaskID: "native-task", TargetChatID: "target-chat", OriginTurnID: "origin-turn", Status: "running", Output: "initial acknowledgement must be ignored"}
	type outcome struct {
		output string
		err    error
	}
	done := make(chan outcome, 1)
	go func() { out, err := n.FollowPhase(context.Background(), execution); done <- outcome{out, err} }()
	select {
	case <-remote.queries:
	case <-time.After(time.Second):
		t.Fatal("job status not queried")
	}
	for _, event := range []envai.Event{phaseEvent(1, "agent.text.done", "target-turn", "started background work"), phaseEvent(2, "turn.completed", "target-turn", "")} {
		sendPhaseEvent(t, remote, event)
	}
	select {
	case got := <-done:
		t.Fatal("initial turn acknowledgement completed phase", got)
	default:
	}
	if len(f.classifications) != 0 {
		t.Fatal("classified acknowledgement")
	}
	for _, event := range []envai.Event{phaseEvent(3, "agent.text.done", "synthesis-turn", " final synthesis\nverbatim "), phaseEvent(4, "turn.completed", "synthesis-turn", "")} {
		sendPhaseEvent(t, remote, event)
	}
	remote.jobs <- envai.DetachedTask{TaskID: "native-task", TargetChatID: "target-chat", OriginTurnID: "origin-turn", Status: "completed", Output: " final synthesis\nverbatim "}
	select {
	case got := <-done:
		if got.err != nil || got.output != " final synthesis\nverbatim " {
			t.Fatal(got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("final native job output not collected")
	}
	if _, err = n.Classify(context.Background(), task, phaseContext, " final synthesis\nverbatim "); err != nil {
		t.Fatal(err)
	}
	if len(f.classifications) != 1 || f.classifications[0].FinalOutput != " final synthesis\nverbatim " {
		t.Fatal(f.classifications)
	}
}

type jobSnapshotRemote struct {
	*nativeFake
	snapshot envai.DetachedTask
}

func (f *jobSnapshotRemote) Task(context.Context, string, string) (envai.DetachedTask, error) {
	return f.snapshot, nil
}
func TestPhaseNativeJobTerminalValidation(t *testing.T) {
	for _, tt := range []struct {
		name, status, output, taskID, target string
		want                                 error
	}{
		{"failed", "failed", "", "native-task", "target-chat", nil},
		{"cancelled", "cancelled", "", "native-task", "target-chat", envai.ErrTurnCancelled},
		{"empty-final", "completed", "", "native-task", "target-chat", nil},
		{"unknown", "queued", "", "native-task", "target-chat", nil},
		{"wrong-task", "completed", "out", "other", "target-chat", nil},
		{"wrong-target", "completed", "out", "native-task", "other", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, n, f, task := nativeTest(t)
			remote := &jobSnapshotRemote{nativeFake: f, snapshot: envai.DetachedTask{TaskID: tt.taskID, TargetChatID: tt.target, Status: tt.status, Output: tt.output, Error: json.RawMessage(`"failure"`)}}
			s.cfg.Remote = remote
			n.remote = remote
			phaseContext, _ := s.cfg.Store.CurrentContext(task.ID)
			execution, err := n.StartPhase(context.Background(), task, phaseContext)
			if err != nil {
				t.Fatal(err)
			}
			out, err := n.FollowPhase(context.Background(), execution)
			if err == nil || out != "" || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Fatal(out, err)
			}
		})
	}
	s, n, f, task := nativeTest(t)
	phaseContext, _ := s.cfg.Store.CurrentContext(task.ID)
	execution, err := n.StartPhase(context.Background(), task, phaseContext)
	if err != nil {
		t.Fatal(err)
	}
	f.taskErr = &envai.APIError{Status: 404, Code: "task_unknown"}
	if _, err = n.FollowPhase(context.Background(), execution); err == nil || f.taskCalls != 1 {
		t.Fatal("unknown task retried or succeeded", err, f.taskCalls)
	}
	f.taskErr = nil
	s.mu.Lock()
	cancel := s.active["watch:"+execution.SessionID]
	s.mu.Unlock()
	cancel()
	s.wg.Wait()
	if _, err = n.FollowPhase(context.Background(), execution); err == nil || !strings.Contains(err.Error(), "observer stopped") {
		t.Fatal(err)
	}
}
