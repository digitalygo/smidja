package envai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixture(t *testing.T, fn http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/jwks.json" && r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing bearer")
		}
		if strings.Contains(r.URL.Path, "/atp/") {
			if len(r.Header.Values("X-Envai-Extensions")) != 1 || r.Header.Get("X-Envai-Extensions") != "native-tasks-v1" {
				t.Error("missing native task extension opt-in")
			}
			if r.Header.Get("X-Envai-Contract-Version") != "3" {
				t.Error("missing contract")
			}
			w.Header().Set("X-Envai-Contract-Version", "3")
		}
		if !strings.Contains(r.URL.Path, "/atp/") && r.Header.Get("X-Envai-Extensions") != "" {
			t.Error("extension opt-in sent outside ATP")
		}
		fn(w, r)
	}))
	t.Cleanup(server.Close)
	c, err := New(Config{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestConfig(t *testing.T) {
	for _, url := range []string{"", ":bad", "ftp://host", "http://u:p@host", "http://host?q=x", "http://host#x"} {
		if _, err := New(Config{BaseURL: url, APIKey: "key"}); err == nil {
			t.Fatal(url)
		}
	}
	for _, key := range []string{"", " ", "x\ny"} {
		if _, err := New(Config{BaseURL: "http://localhost", APIKey: key}); err == nil {
			t.Fatal(key)
		}
	}
	for _, id := range []string{"", "..", ".", "x/y", "x?y", "x#y", "x\\y", "x\ny"} {
		if _, err := identifier(id); err == nil {
			t.Fatal(id)
		}
	}
}
func TestResources(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/atp/v3/capabilities":
			fmt.Fprint(w, `{"protocol_version":3,"contract_version":3,"features":["sse","strict_model"]}`)
		case "/api/v1/teams":
			if r.Method == "GET" {
				fmt.Fprint(w, `{"data":[{"id":"team"}]}`)
			} else {
				var b TeamInput
				json.NewDecoder(r.Body).Decode(&b)
				if b.OrchestratorID != "agent" {
					t.Error(b)
				}
				fmt.Fprint(w, `{"data":{"id":"team"}}`)
			}
		case "/api/v1/agents":
			var b AgentInput
			json.NewDecoder(r.Body).Decode(&b)
			if b.Model != "model" {
				t.Error(b)
			}
			fmt.Fprint(w, `{"data":{"id":"agent"}}`)
		case "/api/v1/workspaces/ws/chats":
			if r.Method == "GET" {
				fmt.Fprint(w, `{"data":[{"id":"chat"}]}`)
			} else {
				var b map[string]string
				json.NewDecoder(r.Body).Decode(&b)
				if b["team_id"] != "team" {
					t.Error(b)
				}
				fmt.Fprint(w, `{"data":{"id":"chat"}}`)
			}
		case "/api/v1/atp/v3/chats/chat/messages":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			if b["message"] != "hello" || b["strict_model"] != true {
				t.Error(b)
			}
			w.WriteHeader(202)
			fmt.Fprint(w, `{"status":"accepted","turn_id":"turn"}`)
		case "/api/v1/atp/v3/chats/chat/tool-output":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			if b["call_id"] != "call" || b["output"] != "result" || b["is_error"] != true {
				t.Error(b)
			}
			w.WriteHeader(204)
		case "/api/v1/atp/v3/chats/chat/cancel":
			w.WriteHeader(202)
		default:
			t.Error(r.URL.Path)
		}
	})
	ctx := context.Background()
	if list, err := c.ListTeams(ctx); err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	if team, err := c.CreateTeam(ctx, TeamInput{Name: "n", OrchestratorID: "agent"}); err != nil || team.ID != "team" {
		t.Fatal(team, err)
	}
	if agent, err := c.CreateAgent(ctx, AgentInput{Name: "n", SystemPrompt: "p", Mode: "primary", Model: "model"}); err != nil || agent.ID != "agent" {
		t.Fatal(agent, err)
	}
	if chat, err := c.CreateChat(ctx, "ws", "team"); err != nil || chat.ID != "chat" {
		t.Fatal(chat, err)
	}
	if chats, err := c.ListChats(ctx, "ws"); err != nil || len(chats) != 1 {
		t.Fatal(chats, err)
	}
	if turn, err := c.StartTurn(ctx, "chat", "hello"); err != nil || turn.TurnID != "turn" {
		t.Fatal(turn, err)
	}
	if err := c.SubmitToolOutput(ctx, "chat", "call", "result", true); err != nil {
		t.Fatal(err)
	}
	if err := c.CancelTurn(ctx, "chat"); err != nil {
		t.Fatal(err)
	}
	if err := c.RequireFeatures(ctx, "sse"); err != nil {
		t.Fatal(err)
	}
	if err := c.RequireFeatures(ctx, "detached"); !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatal(err)
	}
}
func TestErrors(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
		ack        string
		want       error
	}{
		{"nested", `{"error":{"code":"nope","message":"denied"}}`, 403, "3", nil},
		{"plain", `{"code":"nope","message":"denied"}`, 403, "3", nil},
		{"missing", `{}`, 500, "3", nil}, {"badJSON", "bad", 200, "3", nil},
		{"ack", "{}", 200, "", ErrContractMismatch},
		{"version", `{"protocol_version":2,"contract_version":3}`, 200, "3", ErrContractMismatch},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Envai-Contract-Version", tt.ack)
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			_, err := c.Capabilities(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatal(err)
			}
			if tt.status >= 400 {
				var api *APIError
				if !errors.As(err, &api) || api.Status != tt.status || api.Error() == "" {
					t.Fatal(err)
				}
			}
		})
	}
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://never.invalid", 302) })
	if _, err := c.ListTeams(context.Background()); err == nil {
		t.Fatal("redirect followed")
	}
	c = fixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", maximumResponseBytes+1))
	})
	if _, err := c.ListTeams(context.Background()); err == nil {
		t.Fatal("oversized response")
	}
}
func TestValidation(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected HTTP") })
	ctx := context.Background()
	checks := []func() error{
		func() error { _, e := c.CreateTeam(ctx, TeamInput{}); return e }, func() error { _, e := c.CreateAgent(ctx, AgentInput{}); return e },
		func() error { _, e := c.CreateChat(ctx, "/", "team"); return e }, func() error { _, e := c.CreateChat(ctx, "ws", "/"); return e },
		func() error { _, e := c.ListChats(ctx, "/"); return e }, func() error { _, e := c.StartTurn(ctx, "/", "hello"); return e }, func() error { _, e := c.StartTurn(ctx, "chat", ""); return e }, func() error { return c.CancelTurn(ctx, "/") }, func() error { return c.SubmitToolOutput(ctx, "/", "call", "out", false) }, func() error { return c.SubmitToolOutput(ctx, "chat", "", "out", false) }, func() error { return c.SubmitToolOutput(ctx, "chat", "call", strings.Repeat("x", 131073), false) },
	}
	for i, check := range checks {
		if check() == nil {
			t.Fatal(i)
		}
	}
}
func TestEmptyIDsAndNoRetry(t *testing.T) {
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "capabilities") {
			fmt.Fprint(w, `{"protocol_version":3,"contract_version":3,"features":["strict_model"]}`)
			return
		}
		calls++
		fmt.Fprint(w, `{"data":{}}`)
	})
	ctx := context.Background()
	if _, err := c.CreateAgent(ctx, AgentInput{Name: "a", SystemPrompt: "p", Mode: "primary"}); err == nil {
		t.Fatal("empty agent")
	}
	if _, err := c.CreateTeam(ctx, TeamInput{Name: "n", OrchestratorID: "a"}); err == nil {
		t.Fatal("empty team")
	}
	if _, err := c.CreateChat(ctx, "w", "t"); err == nil {
		t.Fatal("empty chat")
	}
	if _, err := c.StartTurn(ctx, "c", "x"); err == nil {
		t.Fatal("empty turn")
	}
	if calls != 4 {
		t.Fatal(calls)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.ListTeams(ctx); err == nil {
		t.Fatal("cancelled request")
	}
	if _, err := c.request(context.Background(), "POST", "/", make(chan int), false); err == nil {
		t.Fatal("marshal")
	}
}
func TestToolRequest(t *testing.T) {
	e := Event{Type: "agent.tool.request", ChatID: "chat", TurnID: "turn", Agent: &Agent{InstanceID: "agent"}, Data: json.RawMessage(`{"call_id":"call","tool_name":"tool","kind":"client","args":{},"signature":{"alg":"EdDSA","kid":"key","sig":"sig"}}`)}
	if req, err := e.ToolRequest(); err != nil || req.CallID != "call" {
		t.Fatal(req, err)
	}
	e.Agent = nil
	if _, err := e.ToolRequest(); err == nil {
		t.Fatal("no binding")
	}
	e.Data = []byte("bad")
	if _, err := e.ToolRequest(); err == nil {
		t.Fatal("bad json")
	}
	e.Type = "other"
	if _, err := e.ToolRequest(); err == nil {
		t.Fatal("wrong type")
	}
}

func TestCredentialsRequireSecureOrigin(t *testing.T) {
	for _, origin := range []string{"http://example.com", "http://192.0.2.1"} {
		if _, err := New(Config{BaseURL: origin, APIKey: "fixture"}); err == nil {
			t.Fatal("cleartext external credentials accepted")
		}
	}
	for _, origin := range []string{"http://localhost", "http://127.0.0.1", "http://[::1]", "https://example.com"} {
		if _, err := New(Config{BaseURL: origin, APIKey: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
}
