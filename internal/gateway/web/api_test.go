package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/gateway"
	"github.com/digitalygo/smidja/internal/session"
)

func TestSendWithCookieAndCSRF(t *testing.T) {
	_, fw, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")

	resp := doJSON(t, client, "POST", ts.URL+"/api/send", `{"workspace":"demo","text":"hello"}`, map[string]string{"X-CSRF": csrf})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	var receipt struct {
		ID            string `json:"id"`
		QueuePosition int    `json:"queuePosition"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&receipt); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if receipt.ID == "" {
		t.Error("receipt missing id")
	}
	submitted := fw.delivered()
	if len(submitted) != 1 {
		t.Fatalf("submitted = %d, want 1", len(submitted))
	}
	if submitted[0].Text != "hello" {
		t.Errorf("text = %q", submitted[0].Text)
	}
	if submitted[0].Transport != transportWeb {
		t.Errorf("transport = %q", submitted[0].Transport)
	}
	if !strings.HasPrefix(submitted[0].ExternalChatKey, "") {
		t.Error("missing external chat key")
	}
}

func TestSendValidation(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")
	headers := map[string]string{"X-CSRF": csrf}
	cases := []struct {
		name string
		body string
		want int
	}{
		{name: "missing workspace", body: `{"text":"hi"}`, want: http.StatusBadRequest},
		{name: "missing text", body: `{"workspace":"demo"}`, want: http.StatusBadRequest},
		{name: "unknown workspace", body: `{"workspace":"nope","text":"hi"}`, want: http.StatusBadRequest},
		{name: "invalid json", body: `{`, want: http.StatusBadRequest},
		{name: "text too long", body: `{"workspace":"demo","text":"` + strings.Repeat("a", maxSendText+1) + `"}`, want: http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, client, "POST", ts.URL+"/api/send", tc.body, headers)
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestSendBodyTooLarge(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")
	big := strings.Repeat("a", maxSendBytes)
	resp := doJSON(t, client, "POST", ts.URL+"/api/send", `{"workspace":"demo","text":"`+big+`"}`, map[string]string{"X-CSRF": csrf})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestSendCSRFEnforcement(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{name: "missing csrf", headers: nil},
		{name: "wrong csrf", headers: map[string]string{"X-CSRF": "deadbeef"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, client, "POST", ts.URL+"/api/send", `{"workspace":"demo","text":"hi"}`, tc.headers)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403", resp.StatusCode)
			}
		})
	}
}

func TestSendCrossOriginRejected(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")
	req, err := http.NewRequest("POST", ts.URL+"/api/send", strings.NewReader(`{"workspace":"demo","text":"hi"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF", csrf)
	req.Header.Set("Origin", "http://evil.example")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestSendSameOriginRefererAllowed(t *testing.T) {
	_, fw, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")
	req, err := http.NewRequest("POST", ts.URL+"/api/send", strings.NewReader(`{"workspace":"demo","text":"hi"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF", csrf)
	req.Header.Set("Referer", ts.URL+"/index.html")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
	if len(fw.delivered()) != 1 {
		t.Error("message was not submitted")
	}
}

func TestSendGatewayErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "rate limited", err: gateway.ErrRateLimited, want: http.StatusTooManyRequests},
		{name: "too many active", err: gateway.ErrTooManyActive, want: http.StatusTooManyRequests},
		{name: "inbound too large", err: gateway.ErrInboundTooLarge, want: http.StatusRequestEntityTooLarge},
		{name: "duplicate", err: gateway.ErrDuplicate, want: http.StatusConflict},
		{name: "closed", err: gateway.ErrClosed, want: http.StatusServiceUnavailable},
		{name: "generic", err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, fw := newTestServer(t, nil)
			fw.submitErr = tc.err
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()
			client := jarClient(t, ts)
			csrf := loginAs(t, client, ts, "secret-token")
			resp := doJSON(t, client, "POST", ts.URL+"/api/send", `{"workspace":"demo","text":"hi"}`, map[string]string{"X-CSRF": csrf})
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestSessionsList(t *testing.T) {
	workspaces := map[string]string{"demo": t.TempDir(), "play": t.TempDir()}
	_, _, ts := startTestServer(t, workspaces)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")
	headers := map[string]string{"X-CSRF": csrf}

	doJSON(t, client, "POST", ts.URL+"/api/send", `{"workspace":"demo","text":"one"}`, headers).Body.Close()
	doJSON(t, client, "POST", ts.URL+"/api/send", `{"workspace":"play","text":"two"}`, headers).Body.Close()

	resp := doJSON(t, client, "GET", ts.URL+"/api/sessions", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Sessions   []sessionInfo `json:"sessions"`
		Workspaces []string      `json:"workspaces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(out.Sessions))
	}
	if out.Sessions[0].Workspace == out.Sessions[1].Workspace {
		t.Error("expected two distinct workspaces")
	}
	if len(out.Workspaces) != 2 || out.Workspaces[0] != "demo" || out.Workspaces[1] != "play" {
		t.Errorf("workspaces = %v", out.Workspaces)
	}
	if !out.Sessions[0].CreatedAt.After(out.Sessions[1].CreatedAt) {
		t.Error("sessions not sorted newest first")
	}
}

func TestSessionsScopedToUser(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	clientA := jarClient(t, ts)
	csrfA := loginAs(t, clientA, ts, "secret-token")
	doJSON(t, clientA, "POST", ts.URL+"/api/send", `{"workspace":"demo","text":"a"}`, map[string]string{"X-CSRF": csrfA}).Body.Close()

	clientB := jarClient(t, ts)
	loginAs(t, clientB, ts, "secret-token")
	resp := doJSON(t, clientB, "GET", ts.URL+"/api/sessions", "", nil)
	defer resp.Body.Close()
	var out struct {
		Sessions []sessionInfo `json:"sessions"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Sessions) != 0 {
		t.Errorf("other session sees %d sessions, want 0", len(out.Sessions))
	}
}

func TestCancelFlow(t *testing.T) {
	_, fw, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")

	resp := doJSON(t, client, "POST", ts.URL+"/api/cancel", `{"id":"msg-1"}`, map[string]string{"X-CSRF": csrf})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want 202", resp.StatusCode)
	}
	fw.mu.Lock()
	keys := append([]string(nil), fw.cancelKeys...)
	fw.mu.Unlock()
	userID := cookieValue(t, client, ts)
	if len(keys) != 1 || keys[0] != transportWeb+":"+userID {
		t.Errorf("cancel keys = %v, want [%s:%s]", keys, transportWeb, userID)
	}
	if userID == "" {
		t.Fatal("no session cookie found")
	}
}

func TestCancelGatewayError(t *testing.T) {
	s, fw := newTestServer(t, nil)
	fw.cancelOK = false
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")
	resp := doJSON(t, client, "POST", ts.URL+"/api/cancel", `{"id":"msg-1"}`, map[string]string{"X-CSRF": csrf})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestCancelValidation(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")
	resp := doJSON(t, client, "POST", ts.URL+"/api/cancel", `{}`, map[string]string{"X-CSRF": csrf})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestTranscriptFromSessionFile(t *testing.T) {
	root := t.TempDir()
	_, _, ts := startTestServer(t, map[string]string{"demo": root})
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")

	store, err := session.NewStore(root + "/.smidja/sessions")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.Create(root)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hello there"`), Timestamp: 1}); err != nil {
		t.Fatalf("AppendUser: %v", err)
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{Role: string(agent.RoleAssistant), Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "hi back"}, {Type: agent.BlockTypeThinking, Thinking: "hidden"}}, Timestamp: 2}); err != nil {
		t.Fatalf("AppendAssistant: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	paths, err := store.List(root)
	if err != nil || len(paths) != 1 {
		t.Fatalf("List: %v, %d paths", err, len(paths))
	}
	loader, err := session.Load(paths[0])
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	id := loader.Header().ID

	resp := doJSON(t, client, "GET", ts.URL+"/api/transcript?id="+id, "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var view transcriptView
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.ID != id {
		t.Errorf("id = %q, want %q", view.ID, id)
	}
	if len(view.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(view.Messages))
	}
	if view.Messages[0].Role != "user" || view.Messages[0].Text != "hello there" {
		t.Errorf("first message = %+v", view.Messages[0])
	}
	if view.Messages[1].Role != "assistant" || view.Messages[1].Text != "hi back" {
		t.Errorf("second message = %+v", view.Messages[1])
	}
}

func TestTranscriptBlockContentUser(t *testing.T) {
	root := t.TempDir()
	_, _, ts := startTestServer(t, map[string]string{"demo": root})
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")

	store, err := session.NewStore(root + "/.smidja/sessions")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.Create(root)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`[{"type":"text","text":"blocked user"}]`), Timestamp: 1}); err != nil {
		t.Fatalf("AppendUser: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	paths, _ := store.List(root)
	loader, _ := session.Load(paths[0])
	id := loader.Header().ID

	resp := doJSON(t, client, "GET", ts.URL+"/api/transcript?id="+id, "", nil)
	defer resp.Body.Close()
	var view transcriptView
	_ = json.NewDecoder(resp.Body).Decode(&view)
	if len(view.Messages) != 1 || view.Messages[0].Text != "blocked user" {
		t.Errorf("messages = %+v", view.Messages)
	}
}

func TestTranscriptErrors(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")
	cases := []struct {
		name string
		id   string
		want int
	}{
		{name: "invalid chars", id: "../etc", want: http.StatusBadRequest},
		{name: "too long", id: strings.Repeat("a", 65), want: http.StatusBadRequest},
		{name: "missing", id: "00000000-0000-7000-8000-000000000000", want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, client, "GET", ts.URL+"/api/transcript?id="+tc.id, "", nil)
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestTranscriptSkipsNonMessageEntries(t *testing.T) {
	root := t.TempDir()
	_, _, ts := startTestServer(t, map[string]string{"demo": root})
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")

	store, err := session.NewStore(root + "/.smidja/sessions")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.Create(root)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hello"`), Timestamp: 1}); err != nil {
		t.Fatalf("AppendUser: %v", err)
	}
	if err := sess.AppendToolResult(&agent.ToolResultMessage{Role: string(agent.RoleToolResult), ToolCallID: "tc", ToolName: "read", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "file contents"}}, Timestamp: 2}); err != nil {
		t.Fatalf("AppendToolResult: %v", err)
	}
	if err := sess.AppendEntry(&session.CompactionEntry{Summary: "sum"}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	if err := sess.AppendAssistant(&agent.AssistantMessage{Role: string(agent.RoleAssistant), Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "done"}}, Timestamp: 3}); err != nil {
		t.Fatalf("AppendAssistant: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	paths, _ := store.List(root)
	loader, err := session.Load(paths[0])
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	msgs := transcriptMessages(loader)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (tool results and compactions skipped)", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Errorf("roles = %q, %q", msgs[0].Role, msgs[1].Role)
	}
}

func TestUserTextShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "plain string", raw: `"hi"`, want: "hi"},
		{name: "block array", raw: `[{"type":"text","text":"a"},{"type":"thinking","thinking":"x"},{"type":"text","text":"b"}]`, want: "ab"},
		{name: "single block", raw: `{"type":"text","text":"single"}`, want: "single"},
		{name: "single non-text block", raw: `{"type":"thinking","thinking":"x"}`, want: ""},
		{name: "unknown shape", raw: `{"nested":[1,2]}`, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := userText([]byte(tc.raw)); got != tc.want {
				t.Errorf("userText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFindSessionFileFailureBranches(t *testing.T) {
	good := t.TempDir()
	bad := t.TempDir()
	if err := os.MkdirAll(bad+"/.smidja/sessions", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	munged := strings.TrimPrefix(bad, "/")
	munged = strings.ReplaceAll(munged, "/", "-")
	munged = strings.ReplaceAll(munged, ":", "-")
	if err := os.WriteFile(bad+"/.smidja/sessions/--"+munged+"--", []byte("file in the way"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	store, err := session.NewStore(good + "/.smidja/sessions")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sess, err := store.Create(good)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sess.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hi"`), Timestamp: 1}); err != nil {
		t.Fatalf("AppendUser: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	paths, _ := store.List(good)
	loader, err := session.Load(paths[0])
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	id := loader.Header().ID

	s, _ := newTestServer(t, map[string]string{"broken": bad, "good": good})
	path, ok := s.findSessionFile(id)
	if !ok || path == "" {
		t.Error("session not found despite broken workspace in the map")
	}
	if _, ok := s.findSessionFile("00000000-0000-7000-8000-000000000000"); ok {
		t.Error("unknown session found")
	}
}

func TestCancelBodyTooLarge(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")
	big := strings.Repeat("a", maxCancelBytes)
	resp := doJSON(t, client, "POST", ts.URL+"/api/cancel", `{"id":"`+big+`"}`, map[string]string{"X-CSRF": csrf})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

func TestRememberDeduplicatesWorkspace(t *testing.T) {
	_, _, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")
	headers := map[string]string{"X-CSRF": csrf}
	doJSON(t, client, "POST", ts.URL+"/api/send", `{"workspace":"demo","text":"one"}`, headers).Body.Close()
	doJSON(t, client, "POST", ts.URL+"/api/send", `{"workspace":"demo","text":"two"}`, headers).Body.Close()
	resp := doJSON(t, client, "GET", ts.URL+"/api/sessions", "", nil)
	defer resp.Body.Close()
	var out struct {
		Sessions []sessionInfo `json:"sessions"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Sessions) != 1 {
		t.Errorf("sessions = %d, want 1", len(out.Sessions))
	}
}

func TestTranscriptCorruptFile(t *testing.T) {
	root := t.TempDir()
	_, _, ts := startTestServer(t, map[string]string{"demo": root})
	client := jarClient(t, ts)
	loginAs(t, client, ts, "secret-token")

	store, err := session.NewStore(root + "/.smidja/sessions")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	dir, err := store.DirForCwd(root)
	if err != nil {
		t.Fatalf("DirForCwd: %v", err)
	}
	path := dir + "/2026-01-01T00-00-00.000Z_someid.jsonl"
	if err := os.WriteFile(path, []byte("not json\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp := doJSON(t, client, "GET", ts.URL+"/api/transcript?id=someid", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestDeliverUpdatesSessionID(t *testing.T) {
	_, fw, ts := startTestServer(t, nil)
	client := jarClient(t, ts)
	csrf := loginAs(t, client, ts, "secret-token")
	doJSON(t, client, "POST", ts.URL+"/api/send", `{"workspace":"demo","text":"hi"}`, map[string]string{"X-CSRF": csrf}).Body.Close()

	userID := cookieValue(t, client, ts)
	if err := fw.sink.Deliver(context.Background(), gateway.Delivery{
		ID:              "m1",
		Transport:       transportWeb,
		ExternalChatKey: userID,
		Text:            "hi",
		Result:          gateway.RunResult{Text: "ok", SessionID: "sess-9"},
	}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	resp := doJSON(t, client, "GET", ts.URL+"/api/sessions", "", nil)
	defer resp.Body.Close()
	var out struct {
		Sessions []sessionInfo `json:"sessions"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Sessions) != 1 || out.Sessions[0].SessionID != "sess-9" {
		t.Errorf("sessions = %+v", out.Sessions)
	}
}

func TestDeliverUnknownUserNoPanic(t *testing.T) {
	s, fw, _ := startTestServer(t, nil)
	if fw.sink == nil {
		t.Fatal("sink not registered")
	}
	if err := s.Deliver(context.Background(), gateway.Delivery{
		ID:              "m1",
		Transport:       transportWeb,
		ExternalChatKey: "unknown-user",
		Result:          gateway.RunResult{SessionID: "sess-9"},
	}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
}
