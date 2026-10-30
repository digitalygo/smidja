package manager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
)

type chatRemote struct {
	createErr, startErr, followErr, cancelErr, verifyErr, submitErr error
	events                                                          []envai.Event
	result                                                          envai.TurnResult
	tool                                                            envai.ToolRequest
	creates, starts, follows, cancels, submits                      int
	after                                                           int64
	outputs                                                         []string
	outputErrors                                                    []bool
}

func (f *chatRemote) Capabilities(context.Context) (envai.Capabilities, error) {
	return envai.Capabilities{}, nil
}
func (f *chatRemote) ListTeams(context.Context) ([]envai.Team, error) { return []envai.Team{}, nil }
func (f *chatRemote) CreateChat(context.Context, string, string) (envai.Chat, error) {
	f.creates++
	return envai.Chat{ID: "chat"}, f.createErr
}
func (f *chatRemote) StartTurn(context.Context, string, string) (envai.Turn, error) {
	f.starts++
	return envai.Turn{TurnID: "turn"}, f.startErr
}
func (f *chatRemote) FollowTurn(_ context.Context, _, _ string, after int64, handler envai.EventHandler) (envai.TurnResult, error) {
	f.follows++
	f.after = after
	for _, e := range f.events {
		if e.Sequence > after {
			if err := handler(e); err != nil {
				return envai.TurnResult{}, err
			}
		}
	}
	return f.result, f.followErr
}
func (f *chatRemote) CancelTurn(context.Context, string) error { f.cancels++; return f.cancelErr }
func (f *chatRemote) VerifyToolRequest(context.Context, envai.Event) (envai.ToolRequest, error) {
	return f.tool, f.verifyErr
}
func (f *chatRemote) SubmitToolOutput(_ context.Context, _, _ string, output string, isError bool) error {
	f.submits++
	f.outputs = append(f.outputs, output)
	f.outputErrors = append(f.outputErrors, isError)
	return f.submitErr
}
func chatServer(t *testing.T, remote *chatRemote) *Server {
	return testServer(t, func(c *Config) { c.Remote = remote; c.WorkspaceID = "workspace"; c.CEOTeamID = "ceo" })
}
func newChatSession(t *testing.T, s *Server) managerstate.Session {
	t.Helper()
	session, err := s.cfg.Store.CreateSession("chat")
	if err != nil {
		t.Fatal(err)
	}
	return session
}
func sendChat(t *testing.T, s *Server, id string) {
	t.Helper()
	w := request(s, "POST", "/api/sessions/"+id+"/messages", `{"message":"Build it"}`, ownerHeaders())
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	s.wg.Wait()
}
func getChat(t *testing.T, s *Server, id string) managerstate.Session {
	t.Helper()
	session, err := s.cfg.Store.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestChatCompletionAndRecovery(t *testing.T) {
	remote := &chatRemote{events: []envai.Event{{ChatID: "chat", TurnID: "turn", Sequence: 1, Type: "agent.text.done", Agent: &envai.Agent{Role: "orchestrator"}, Data: json.RawMessage(`{"text":"Durable final output"}`)}}, followErr: envai.ErrStreamInterrupted}
	s := chatServer(t, remote)
	session := newChatSession(t, s)
	sendChat(t, s, session.ID)
	saved := getChat(t, s, session.ID)
	if saved.Turn.Status != managerstate.Paused || !saved.Turn.OutputRecorded || saved.Turn.LastSequence != 1 {
		t.Fatalf("%+v", saved.Turn)
	}
	remote.events = []envai.Event{{TurnID: "turn", Sequence: 2, Type: "turn.completed"}}
	remote.result = envai.TurnResult{Terminal: envai.Event{Type: "turn.completed"}}
	remote.followErr = errors.New("adapter has no text on terminal-only replay")
	w := request(s, "POST", "/api/sessions/"+session.ID+"/recover", `{}`, ownerHeaders())
	if w.Code != 202 {
		t.Fatal(w.Body)
	}
	s.wg.Wait()
	saved = getChat(t, s, session.ID)
	if saved.Turn.Status != managerstate.Completed || len(saved.Messages) != 2 || saved.Messages[1].Content != "Durable final output" || remote.starts != 1 || remote.creates != 1 || remote.after != 1 {
		t.Fatalf("%+v remote %+v", saved, remote)
	}
	if saved.Turn.LastSequence != 1 {
		t.Fatal("terminal cursor advanced before durable completion")
	}
}
func TestChatErrorsAndRoutes(t *testing.T) {
	for _, stage := range []string{"create", "start", "follow"} {
		t.Run(stage, func(t *testing.T) {
			remote := &chatRemote{}
			failure := errors.New(stage + " unavailable")
			switch stage {
			case "create":
				remote.createErr = failure
			case "start":
				remote.startErr = failure
			case "follow":
				remote.followErr = failure
			}
			s := chatServer(t, remote)
			session := newChatSession(t, s)
			sendChat(t, s, session.ID)
			saved := getChat(t, s, session.ID)
			if saved.Turn.Status != managerstate.Paused || !strings.Contains(saved.Turn.Reason, stage) {
				t.Fatal(saved.Turn)
			}
		})
	}
	remote := &chatRemote{result: envai.TurnResult{Output: "done"}}
	s := chatServer(t, remote)
	session := newChatSession(t, s)
	for _, tc := range []struct {
		path, body string
		code       int
	}{{session.ID + "/messages", `{`, 400}, {session.ID + "/messages", `{"message":" "}`, 400}, {session.ID + "/recover", `{}`, 409}, {session.ID + "/unknown", `{}`, 404}, {"missing/messages", `{"message":"x"}`, 404}} {
		if w := request(s, "POST", "/api/sessions/"+tc.path, tc.body, ownerHeaders()); w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body)
		}
	}
	if w := request(s, "GET", "/api/sessions/"+session.ID+"/messages", "", ownerHeaders()); w.Code != 404 {
		t.Fatal(w.Body)
	}
	for _, path := range []string{"/api/teams", "/api/capabilities"} {
		if w := request(s, "GET", path, "", ownerHeaders()); w.Code != 200 {
			t.Fatal(w.Body)
		}
	}
	sendChat(t, s, session.ID)
	if getChat(t, s, session.ID).Turn.Status != managerstate.Completed {
		t.Fatal("not completed")
	}
	s.scheduleSession("missing", false)
	s.wg.Wait()
	s.scheduleSession(newChatSession(t, s).ID, false)
	s.wg.Wait()
	noRemote := testServer(t, nil)
	if w := request(noRemote, "POST", "/api/sessions/nope/messages", `{}`, ownerHeaders()); w.Code != 503 {
		t.Fatal(w.Body)
	}
}
func TestChatCancellationAndLaunch(t *testing.T) {
	remote := &chatRemote{cancelErr: errors.New("offline")}
	s := chatServer(t, remote)
	session := newChatSession(t, s)
	_, err := s.cfg.Store.StartSessionTurn(session.ID, "req", "message")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.cfg.Store.AttachSessionTurn(session.ID, "req", "chat", "turn")
	if err != nil {
		t.Fatal(err)
	}
	w := request(s, "POST", "/api/sessions/"+session.ID+"/cancel", `{}`, ownerHeaders())
	if w.Code != 502 || getChat(t, s, session.ID).Turn.Status != managerstate.Cancelled {
		t.Fatal(w.Body)
	}
	release := make(chan struct{})
	started := make(chan struct{})
	if !s.launch("unique", func(ctx context.Context) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}) {
		t.Fatal("did not launch")
	}
	<-started
	if s.launch("unique", func(context.Context) {}) {
		t.Fatal("duplicate launch")
	}
	s.cancelJob("unique")
	s.wg.Wait()
	close(release)
	s.cancel()
	if s.launch("closed", func(context.Context) {}) {
		t.Fatal("launched closed server")
	}
}
func TestVerifiedToolDeduplicationAndFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	args, _ := json.Marshal(map[string]string{"path": path, "content": "one"})
	remote := &chatRemote{tool: envai.ToolRequest{CallID: "call", ToolName: "write-file", Args: args}}
	s := chatServer(t, remote)
	s.cfg.ToolBindings = map[string]ToolBinding{"write-file": {Name: "write"}}
	event := envai.Event{ChatID: "chat", TurnID: "turn"}
	if err := s.handleTool(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.handleTool(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "external edit" || remote.submits != 2 || remote.outputs[0] != remote.outputs[1] || remote.outputErrors[0] {
		t.Fatal("replayed execution instead of recorded output", remote.outputs)
	}
	remote.verifyErr = errors.New("invalid signature")
	if err := s.handleTool(context.Background(), event); err == nil {
		t.Fatal("accepted invalid signature")
	}
	remote.verifyErr = nil
	remote.tool = envai.ToolRequest{CallID: "disabled", ToolName: "disabled", Args: json.RawMessage(`{}`)}
	if err := s.handleTool(context.Background(), event); err != nil || !remote.outputErrors[len(remote.outputErrors)-1] {
		t.Fatal(err)
	}
	remote.tool = envai.ToolRequest{CallID: "uncertain", ToolName: "write-file", Args: args}
	if _, _, err := s.cfg.Store.BeginTool("uncertain", "chat", "turn", "write-file", args, "manager"); err != nil {
		t.Fatal(err)
	}
	if err := s.handleTool(context.Background(), event); err == nil || !strings.Contains(err.Error(), "unknown outcome") {
		t.Fatal(err)
	}
	remote.tool = envai.ToolRequest{CallID: "offline", ToolName: "remote", Args: json.RawMessage(`{}`)}
	s.cfg.ToolBindings["remote"] = ToolBinding{Name: "read", ClientID: "offline"}
	if err := s.handleTool(context.Background(), event); err != nil || !remote.outputErrors[len(remote.outputErrors)-1] {
		t.Fatal(err)
	}
	remote.tool = envai.ToolRequest{CallID: "large", ToolName: "read-file", Args: json.RawMessage(`{"path":"` + path + `"}`)}
	s.cfg.ToolBindings["read-file"] = ToolBinding{Name: "read"}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 150000)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.handleTool(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.outputs[len(remote.outputs)-1], "output_limit") {
		t.Fatal("large output not bounded")
	}
}

func TestQueuedSessionStartCapturesRequestAndDoesNotLoseWakeup(t *testing.T) {
	for _, queueLatest := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale-callback", true: "latest-request-runs"}[queueLatest], func(t *testing.T) {
			remote := &chatRemote{result: envai.TurnResult{Output: "done"}}
			s := chatServer(t, remote)
			session := newChatSession(t, s)
			release := make(chan struct{})
			started := make(chan struct{})
			if !s.launch("session:"+session.ID, func(context.Context) { close(started); <-release }) {
				t.Fatal("failed to reserve session worker")
			}
			<-started
			if _, err := s.cfg.Store.StartSessionTurn(session.ID, "old-request", "old"); err != nil {
				t.Fatal(err)
			}
			s.scheduleSession(session.ID, false)
			if _, err := s.cfg.Store.CancelSessionTurn(session.ID, "old-request"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.cfg.Store.StartSessionTurn(session.ID, "new-request", "new"); err != nil {
				t.Fatal(err)
			}
			if queueLatest {
				s.scheduleSession(session.ID, false)
			}
			close(release)
			s.wg.Wait()
			saved := getChat(t, s, session.ID)
			if queueLatest {
				if remote.starts != 1 || saved.Turn.Status != managerstate.Completed || saved.Turn.RequestID != "new-request" {
					t.Fatal(remote.starts, saved.Turn)
				}
			} else if remote.starts != 0 || saved.Turn.Status != "submitting" {
				t.Fatal("stale queued callback acted on replacement turn", remote.starts, saved.Turn)
			}
		})
	}
}

func TestWorkflowPhaseReadOnlyDoesNotRestrictDirectTeamConversation(t *testing.T) {
	remote := &chatRemote{result: envai.TurnResult{Output: "direct result"}}
	s := chatServer(t, remote)
	owner, err := s.cfg.Store.CreateTask(workflowInput())
	if err != nil {
		t.Fatal(err)
	}
	phase, err := s.cfg.Store.CreateWorkflowSession("Workflow phase", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachSessionRemote(phase.ID, "phase-chat"); err != nil {
		t.Fatal(err)
	}
	w := request(s, "POST", "/api/sessions/"+phase.ID+"/messages", `{"message":"interfere"}`, ownerHeaders())
	if w.Code != 409 || !strings.Contains(w.Body.String(), "read-only") {
		t.Fatal(w.Code, w.Body)
	}
	if remote.starts != 0 || getChat(t, s, phase.ID).Turn != nil {
		t.Fatal("phase conversation admitted external turn")
	}
	direct := newChatSession(t, s)
	if _, err = s.cfg.Store.AttachSessionRemote(direct.ID, "direct-chat"); err != nil {
		t.Fatal(err)
	}
	sendChat(t, s, direct.ID)
	if remote.starts != 1 || getChat(t, s, direct.ID).Turn.Status != managerstate.Completed {
		t.Fatal("ordinary direct team conversation incorrectly read-only")
	}
	if w = request(s, "GET", "/api/sessions/"+phase.ID, "", ownerHeaders()); w.Code != 200 || !strings.Contains(w.Body.String(), `"workflow_task_id":"`+owner.ID+`"`) {
		t.Fatal(w.Code, w.Body)
	}
}
