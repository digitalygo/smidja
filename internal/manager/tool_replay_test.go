package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
)

type receiptHTTPRemote struct {
	chatRemote
	client *envai.Client
}

func (r *receiptHTTPRemote) SubmitToolOutput(ctx context.Context, chatID, callID, output string, isError bool) error {
	return r.client.SubmitToolOutput(ctx, chatID, callID, output, isError)
}

func TestCompletedToolReplayAdvancesCursorAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "effect.txt")
	args, _ := json.Marshal(map[string]string{"path": path, "content": "first execution"})
	calls := 0
	var submitted []string
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/atp/v3/chats/chat/tool-output" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			CallID  string `json:"call_id"`
			Output  string `json:"output"`
			IsError bool   `json:"is_error"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CallID != "call" || body.IsError {
			t.Errorf("unexpected receipt: %+v, %v", body, err)
		}
		submitted = append(submitted, body.Output)
		calls++
		w.Header().Set("X-Envai-Contract-Version", "3")
		w.Header().Set("Content-Type", "application/json")
		if calls > 1 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"tool_already_resolved","message":"Tool call is already resolved"}}`))
		} else {
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer httpServer.Close()
	client, err := envai.New(envai.Config{BaseURL: httpServer.URL, APIKey: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	remote := &receiptHTTPRemote{client: client, chatRemote: chatRemote{tool: envai.ToolRequest{CallID: "call", ToolName: "write-file", Args: args}}}
	directory := t.TempDir()
	store, err := managerstate.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Token: testToken, Store: store, Remote: remote, ToolBindings: map[string]ToolBinding{"write-file": {Name: "write"}}}
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	session := newChatSession(t, server)
	if _, err := store.AttachSessionRemote(session.ID, "chat"); err != nil {
		t.Fatal(err)
	}
	startObserved(t, server, session.ID)
	event := watchEvent(1, "turn", "agent.tool.request")
	if err := server.handleTool(ctx, event); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := managerstate.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	config.Store = recovered
	resumed, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if err := resumed.observeEvent(ctx, session.ID, event); err != nil {
		t.Fatal(err)
	}
	if err := resumed.observeEvent(ctx, session.ID, rootOutput(2, "turn", "continued")); err != nil {
		t.Fatal(err)
	}
	if err := resumed.observeEvent(ctx, session.ID, watchEvent(3, "turn", "turn.completed")); err != nil {
		t.Fatal(err)
	}
	saved, err := recovered.GetSession(session.ID)
	if err != nil || saved.LastSequence != 3 || saved.Turn.Status != managerstate.Completed || len(saved.Messages) != 2 || saved.Messages[1].Content != "continued" {
		t.Fatal(saved, err)
	}
	if err := resumed.observeEvent(ctx, session.ID, event); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "external edit" || calls != 2 || len(submitted) != 2 || submitted[0] != submitted[1] {
		t.Fatal(string(data), err, calls, submitted)
	}
}

func TestCompletedToolReplayOnlyAcceptsResolvedConflict(t *testing.T) {
	resolved := &envai.APIError{Status: http.StatusConflict, Code: "tool_already_resolved"}
	for _, tc := range []struct {
		name     string
		response error
		accepted bool
	}{
		{"accepted", nil, true},
		{"resolved", resolved, true},
		{"wrapped resolved", fmt.Errorf("transport: %w", resolved), true},
		{"expired", &envai.APIError{Status: http.StatusConflict, Code: "tool_expired"}, false},
		{"unrelated conflict", &envai.APIError{Status: http.StatusConflict, Code: "turn_not_running"}, false},
		{"wrong status", &envai.APIError{Status: http.StatusForbidden, Code: "tool_already_resolved"}, false},
		{"untyped text", errors.New("409 tool_already_resolved"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := &chatRemote{submitErr: tc.response, tool: envai.ToolRequest{CallID: "call", ToolName: "write-file", Args: json.RawMessage(`{}`)}}
			server := chatServer(t, remote)
			server.cfg.ToolBindings = map[string]ToolBinding{"write-file": {Name: "write"}}
			session := newChatSession(t, server)
			if _, err := server.cfg.Store.AttachSessionRemote(session.ID, "chat"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := server.cfg.Store.BeginTool("call", "chat", "turn", "write-file", remote.tool.Args, "manager"); err != nil {
				t.Fatal(err)
			}
			if err := server.cfg.Store.FinishTool("call", "durable output", true); err != nil {
				t.Fatal(err)
			}
			err := server.observeEvent(context.Background(), session.ID, watchEvent(1, "turn", "agent.tool.request"))
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v, error=%v", tc.accepted, err)
			}
			saved, _ := server.cfg.Store.GetSession(session.ID)
			if (saved.LastSequence == 1) != tc.accepted || remote.submits != 1 || remote.outputs[0] != "durable output" || !remote.outputErrors[0] {
				t.Fatal(saved, remote)
			}
		})
	}
}

func TestToolReplayDoesNotHideUnverifiedOrMismatchedReceipts(t *testing.T) {
	for _, mode := range []string{"first submission", "unfinished", "identity mismatch", "invalid signature", "disabled binding"} {
		t.Run(mode, func(t *testing.T) {
			remote := &chatRemote{submitErr: &envai.APIError{Status: http.StatusConflict, Code: "tool_already_resolved"}, tool: envai.ToolRequest{CallID: "call", ToolName: "write-file", Args: json.RawMessage(`{}`)}}
			server := chatServer(t, remote)
			server.cfg.ToolBindings = map[string]ToolBinding{"write-file": {Name: "write"}}
			if mode != "first submission" && mode != "disabled binding" {
				if _, _, err := server.cfg.Store.BeginTool("call", "chat", "turn", "write-file", remote.tool.Args, "manager"); err != nil {
					t.Fatal(err)
				}
				if mode != "unfinished" {
					if err := server.cfg.Store.FinishTool("call", "durable output", false); err != nil {
						t.Fatal(err)
					}
				}
			}
			switch mode {
			case "identity mismatch":
				remote.tool.Args = json.RawMessage(`{"different":true}`)
			case "invalid signature":
				remote.verifyErr = errors.New("signature invalid")
			case "disabled binding":
				delete(server.cfg.ToolBindings, "write-file")
			}
			if err := server.handleTool(context.Background(), watchEvent(1, "turn", "agent.tool.request")); err == nil {
				t.Fatal("accepted unsafe receipt")
			}
			expectedSubmits := 0
			if mode == "first submission" || mode == "disabled binding" {
				expectedSubmits = 1
			}
			if remote.submits != expectedSubmits {
				t.Fatal(remote.submits)
			}
		})
	}
}
