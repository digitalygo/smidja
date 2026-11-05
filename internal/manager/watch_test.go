package manager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
)

type observingRemote struct {
	chatRemote
	calls    chan int64
	events   chan envai.Event
	failures chan error
}

func (f *observingRemote) StreamEvents(ctx context.Context, _ string, after int64, handler envai.EventHandler) error {
	select {
	case f.calls <- after:
	case <-ctx.Done():
		return ctx.Err()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-f.failures:
			return err
		case event := <-f.events:
			if err := handler(event); err != nil {
				return err
			}
		}
	}
}
func observingServer(t *testing.T) (*Server, *observingRemote, managerstate.Session) {
	t.Helper()
	remote := &observingRemote{calls: make(chan int64, 8), events: make(chan envai.Event, 8), failures: make(chan error, 8)}
	s := testServer(t, func(c *Config) { c.Remote = remote; c.WorkspaceID = "workspace"; c.CEOTeamID = "ceo" })
	session := newChatSession(t, s)
	if _, err := s.cfg.Store.AttachSessionRemote(session.ID, "chat"); err != nil {
		t.Fatal(err)
	}
	return s, remote, session
}
func awaitWatch(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("observer did not reach expected state")
}
func watchEvent(sequence int64, turn, kind string) envai.Event {
	return envai.Event{ID: kind + ":" + turn, ChatID: "chat", TurnID: turn, Sequence: sequence, Type: kind}
}
func startObserved(t *testing.T, s *Server, id string) {
	t.Helper()
	if _, err := s.cfg.Store.StartSessionTurn(id, "request", "message"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.cfg.Store.AttachSessionTurn(id, "request", "chat", "turn"); err != nil {
		t.Fatal(err)
	}
}
func rootOutput(sequence int64, turn, text string) envai.Event {
	event := watchEvent(sequence, turn, "agent.text.done")
	event.Agent = &envai.Agent{Role: "orchestrator"}
	event.Data, _ = json.Marshal(map[string]string{"text": text})
	return event
}

func TestObserverRemainsLiveForNativeDelivery(t *testing.T) {
	s, remote, session := observingServer(t)
	startObserved(t, s, session.ID)
	if !s.observeSession(session.ID) || !s.observeSession(session.ID) {
		t.Fatal("observer unavailable")
	}
	if after := <-remote.calls; after != 0 {
		t.Fatal(after)
	}
	remote.events <- rootOutput(1, "turn", "initial output")
	remote.events <- watchEvent(2, "turn", "turn.completed")
	awaitWatch(t, func() bool { return getChat(t, s, session.ID).Turn.Status == managerstate.Completed })
	remote.events <- watchEvent(3, "", "delivery.accepted")
	remote.events <- rootOutput(4, "native-delivery-turn", "background result")
	awaitWatch(t, func() bool { return getChat(t, s, session.ID).LastSequence == 4 })
	saved := getChat(t, s, session.ID)
	if len(saved.Messages) != 3 || saved.Messages[2].Content != "background result" {
		t.Fatal(saved.Messages)
	}
	select {
	case <-remote.calls:
		t.Fatal("duplicate observer")
	default:
	}
	if err := s.observeEvent(context.Background(), session.ID, rootOutput(4, "native-delivery-turn", "background result")); err != nil {
		t.Fatal(err)
	}
	if len(getChat(t, s, session.ID).Messages) != 3 {
		t.Fatal("duplicate passive final")
	}
	remote.failures <- io.EOF
	awaitWatch(t, func() bool {
		select {
		case after := <-remote.calls:
			if after != 4 {
				t.Error(after)
			}
			return true
		default:
			return false
		}
	})
	remote.failures <- envai.ErrReplayGap
	awaitWatch(t, func() bool { return len(getChat(t, s, session.ID).Messages) == 4 })
	if !strings.Contains(getChat(t, s, session.ID).Messages[3].Content, "explicit recovery") {
		t.Fatal("missing recovery diagnostic")
	}
}
func TestObserverErrorsAndTerminalStates(t *testing.T) {
	s, _, session := observingServer(t)
	startObserved(t, s, session.ID)
	for _, event := range []envai.Event{{ID: "bad", ChatID: "other", Sequence: 1}, watchEvent(2, "turn", "ping"), rootOutput(1, "turn", "invalid")} {
		if event.Type == "agent.text.done" {
			event.Data = json.RawMessage(`{`)
		}
		if err := s.observeEvent(context.Background(), session.ID, event); err == nil {
			t.Fatal("accepted invalid event", event)
		}
	}
	if err := s.observeEvent(context.Background(), session.ID, watchEvent(1, "turn", "turn.completed")); err == nil {
		t.Fatal("accepted completion without output")
	}
	if err := s.observeEvent(context.Background(), session.ID, watchEvent(1, "turn", "turn.cancelled")); err != nil {
		t.Fatal(err)
	}
	if getChat(t, s, session.ID).Turn.Status != managerstate.Cancelled {
		t.Fatal("not cancelled")
	}
	event := watchEvent(2, "turn", "agent.tool.request")
	if err := s.observeEvent(context.Background(), session.ID, event); err != nil {
		t.Fatal(err)
	}
	event = watchEvent(3, "", "agent.tool.request")
	if err := s.observeEvent(context.Background(), session.ID, event); err == nil {
		t.Fatal("missing tool identity accepted")
	}
	event = rootOutput(3, "passive", "ignored child")
	parent := "parent"
	event.Agent.ParentID = &parent
	if err := s.observeEvent(context.Background(), session.ID, event); err != nil {
		t.Fatal(err)
	}
	if len(getChat(t, s, session.ID).Messages) != 1 {
		t.Fatal("child final exposed as CEO")
	}
	other := newChatSession(t, s)
	if s.observeSession(other.ID) || s.observeSession("missing") {
		t.Fatal("watched unbound chat")
	}
	if err := s.observeEvent(context.Background(), "missing", event); err == nil {
		t.Fatal("missing session accepted")
	}
}
func TestObserverTransportFailurePausesTurn(t *testing.T) {
	s, remote, session := observingServer(t)
	startObserved(t, s, session.ID)
	s.observeSession(session.ID)
	<-remote.calls
	remote.failures <- errors.New("connection lost")
	awaitWatch(t, func() bool { return getChat(t, s, session.ID).Turn.Status == managerstate.Paused })
	if err := s.observeEvent(context.Background(), session.ID, rootOutput(1, "turn", "late")); err == nil {
		t.Fatal("paused turn silently consumed")
	}
	if getChat(t, s, session.ID).LastSequence != 0 {
		t.Fatal("cursor advanced for paused turn")
	}
}
func TestObserverSubmittingRaceCancellation(t *testing.T) {
	s, _, session := observingServer(t)
	if _, err := s.cfg.Store.StartSessionTurn(session.ID, "request", "message"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.observeEvent(ctx, session.ID, rootOutput(1, "turn", "early")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestObservedScheduleUsesStreamInsteadOfFollow(t *testing.T) {
	s, remote, session := observingServer(t)
	w := request(s, "POST", "/api/sessions/"+session.ID+"/messages", `{"message":"hello"}`, ownerHeaders())
	if w.Code != 202 {
		t.Fatal(w.Body)
	}
	select {
	case <-remote.calls:
	case <-time.After(time.Second):
		t.Fatal("no observer")
	}
	remote.events <- rootOutput(1, "turn", "done")
	remote.events <- watchEvent(2, "turn", "turn.completed")
	awaitWatch(t, func() bool { return getChat(t, s, session.ID).Turn.Status == managerstate.Completed })
	if remote.follows != 0 {
		t.Fatal("used per-turn follower instead of native chat observer")
	}
}
func TestChatCursorPersists(t *testing.T) {
	dir := t.TempDir()
	store, err := managerstate.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession("persisted")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.RecordChatSequence(session.ID, 1); err == nil {
		t.Fatal("cursor without remote chat")
	}
	if _, err = store.AttachSessionRemote(session.ID, "chat"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RecordChatSequence(session.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RecordChatSequence(session.ID, 3); err == nil {
		t.Fatal("cursor gap accepted")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = managerstate.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saved, err := store.GetSession(session.ID)
	if err != nil || saved.LastSequence != 1 {
		t.Fatal(saved, err)
	}
	remote := &observingRemote{calls: make(chan int64, 1), events: make(chan envai.Event), failures: make(chan error)}
	server, err := New(Config{Token: testToken, Store: store, Remote: remote})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	select {
	case after := <-remote.calls:
		if after != 1 {
			t.Fatal(after)
		}
	case <-time.After(time.Second):
		t.Fatal("startup did not restore observer")
	}
}

func TestObserverNativeToolsFailuresAndReconnectRoute(t *testing.T) {
	s, remote, session := observingServer(t)
	remote.tool = envai.ToolRequest{CallID: "passive-call", ToolName: "disabled", Args: json.RawMessage(`{}`)}
	if err := s.observeEvent(context.Background(), session.ID, watchEvent(1, "native-turn", "agent.tool.request")); err != nil {
		t.Fatal(err)
	}
	if remote.submits != 1 || !remote.outputErrors[0] {
		t.Fatal("native passive tool not serviced")
	}
	event := watchEvent(2, "", "task.event")
	event.Data = json.RawMessage(`{"event_type":"agent.text.done","task_id":"task"}`)
	if err := s.observeEvent(context.Background(), session.ID, event); err != nil {
		t.Fatal(err)
	}
	event = watchEvent(3, "", "task.event")
	event.Data = json.RawMessage(`{"event_type":"agent.tool.request","task_id":"task"}`)
	if err := s.observeEvent(context.Background(), session.ID, event); err == nil {
		t.Fatal("unverified detached tool accepted")
	}
	startObserved(t, s, session.ID)
	event = watchEvent(3, "turn", "turn.failed")
	event.Data = json.RawMessage(`{"error":"provider unavailable"}`)
	if err := s.observeEvent(context.Background(), session.ID, event); err != nil {
		t.Fatal(err)
	}
	if getChat(t, s, session.ID).Turn.Status != managerstate.Paused {
		t.Fatal("failed turn not paused")
	}
	w := request(s, "POST", "/api/sessions/"+session.ID+"/observe", `{}`, ownerHeaders())
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	select {
	case after := <-remote.calls:
		if after != 3 {
			t.Fatal(after)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnect did not start stream")
	}
	blank := newChatSession(t, s)
	w = request(s, "POST", "/api/sessions/"+blank.ID+"/observe", `{}`, ownerHeaders())
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	plain := chatServer(t, &chatRemote{})
	plainSession := newChatSession(t, plain)
	_, _ = plain.cfg.Store.AttachSessionRemote(plainSession.ID, "chat")
	w = request(plain, "POST", "/api/sessions/"+plainSession.ID+"/observe", `{}`, ownerHeaders())
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
}
