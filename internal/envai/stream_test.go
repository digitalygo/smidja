package envai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func sse(seq int64, kind, role, text string) string {
	e := Event{Version: 3, ID: fmt.Sprint(seq), Sequence: seq, ChatID: "chat", TurnID: "turn", Type: kind, Agent: &Agent{InstanceID: role, Role: role}, Data: json.RawMessage(`{}`)}
	if text != "" {
		e.Data, _ = json.Marshal(map[string]string{"text": text})
	}
	b, _ := json.Marshal(e)
	return "data: " + string(b) + "\n\n"
}
func TestDecodeEvents(t *testing.T) {
	seen := 0
	err := decodeEvents(strings.NewReader(":comment\nevent: ignored\n\ndata: {\"v\":3,\"type\":\"ping\",\"seq\":0}\n\n"+sse(1, "new.event", "member", "")), "chat", 0, func(Event) error { seen++; return nil })
	if !errors.Is(err, io.EOF) || seen != 1 {
		t.Fatal(err, seen)
	}
	for _, tt := range []struct {
		input string
		after int64
		want  error
	}{
		{sse(2, "event", "member", ""), 0, ErrReplayGap}, {sse(1, "event", "member", ""), 1, ErrReplayGap},
		{strings.TrimSuffix(sse(1, "event", "member", ""), "\n"), 0, ErrStreamInterrupted},
		{strings.Replace(sse(1, "event", "member", ""), `"v":3`, `"v":2`, 1), 0, ErrContractMismatch},
		{strings.Replace(sse(1, "event", "member", ""), `"chat_id":"chat"`, `"chat_id":"wrong"`, 1), 0, nil},
		{"data: bad\n\n", 0, nil}, {"data: " + strings.Repeat("x", maximumResponseBytes) + "\n\n", 0, nil},
	} {
		err := decodeEvents(strings.NewReader(tt.input), "chat", tt.after, func(Event) error { return nil })
		if err == nil {
			t.Fatal("missing failure")
		}
		if tt.want != nil && !errors.Is(err, tt.want) {
			t.Fatal(err)
		}
	}
	marker := errors.New("persist failed")
	if err := decodeEvents(strings.NewReader(sse(1, "event", "member", "")), "chat", 0, func(Event) error { return marker }); !errors.Is(err, marker) {
		t.Fatal(err)
	}
}
func TestFollowTurnReconnect(t *testing.T) {
	connections := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		connections++
		if connections == 1 {
			if r.URL.Query().Get("after") != "0" {
				t.Error(r.URL)
			}
			fmt.Fprint(w, sse(1, "agent.started", "orchestrator", ""), sse(2, "agent.text.done", "member", "not root"))
			return
		}
		if r.URL.Query().Get("after") != "2" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, sse(3, "agent.text.done", "orchestrator", "  full\noutput  "), sse(4, "turn.completed", "", ""))
	})
	seen := 0
	result, err := c.FollowTurn(context.Background(), "chat", "turn", 0, func(Event) error { seen++; return nil })
	if err != nil || result.Output != "  full\noutput  " || result.LastSequence != 4 || seen != 4 || connections != 2 {
		t.Fatal(result, err, seen, connections)
	}
}
func TestTurnFailures(t *testing.T) {
	for _, kind := range []string{"turn.completed", "turn.failed", "turn.cancelled"} {
		t.Run(kind, func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, sse(1, kind, "", ""))
			})
			result, err := c.FollowTurn(context.Background(), "chat", "turn", 0, nil)
			if err == nil || result.Terminal.Type != kind {
				t.Fatal(result, err)
			}
			if kind == "turn.cancelled" && !errors.Is(err, ErrTurnCancelled) {
				t.Fatal(err)
			}
		})
	}
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Content-Type", "text/event-stream") })
	if _, err := c.FollowTurn(context.Background(), "chat", "turn", 0, nil); !errors.Is(err, ErrStreamInterrupted) {
		t.Fatal(err)
	}
	if _, err := c.FollowTurn(context.Background(), "chat", "/", 0, nil); err == nil {
		t.Fatal("bad turn")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.FollowTurn(ctx, "chat", "turn", 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestStreamFailures(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "not SSE") })
	if err := c.StreamEvents(context.Background(), "chat", 0, func(Event) error { return nil }); err == nil {
		t.Fatal("bad contenttype")
	}
	if err := c.StreamEvents(context.Background(), "/", 0, func(Event) error { return nil }); err == nil {
		t.Fatal("bad chat")
	}
	if err := c.StreamEvents(context.Background(), "chat", -1, nil); err == nil {
		t.Fatal("bad cursor")
	}
	c = fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse(1, "event", "", ""))
	})
	marker := errors.New("persist failed")
	if _, err := c.FollowTurn(context.Background(), "chat", "turn", 0, func(Event) error { return marker }); !errors.Is(err, marker) {
		t.Fatal(err)
	}
}

func TestCallbackEOFDoesNotReplay(t *testing.T) {
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse(1, "event", "", ""))
	})
	if _, err := c.FollowTurn(context.Background(), "chat", "turn", 0, func(Event) error { return io.EOF }); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("callback failure retried", calls)
	}
}
func TestSSEMultilineBound(t *testing.T) {
	input := strings.Repeat("data: "+strings.Repeat("x", 1024)+"\n", 8193) + "\n"
	if err := decodeEvents(strings.NewReader(input), "chat", 0, func(Event) error { return nil }); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatal(err)
	}
}

func TestNestedOrchestratorCannotReplaceRootOutput(t *testing.T) {
	parent := "orchestrator"
	event := Event{Version: 3, ID: "3", Sequence: 3, ChatID: "chat", TurnID: "turn", Type: "agent.text.done", Agent: &Agent{InstanceID: "child", Role: "orchestrator", ParentID: &parent}, Data: json.RawMessage(`{"text":"wrong"}`)}
	raw, _ := json.Marshal(event)
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse(1, "agent.started", "orchestrator", ""), sse(2, "agent.text.done", "orchestrator", "right"), "data: "+string(raw)+"\n\n", sse(4, "turn.completed", "", ""))
	})
	result, err := c.FollowTurn(context.Background(), "chat", "turn", 0, nil)
	if err != nil || result.Output != "right" {
		t.Fatal(result, err)
	}
}
