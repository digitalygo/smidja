package envai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func nativeFixture(t *testing.T, handler http.HandlerFunc) *Client {
	return fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/capabilities") {
			fmt.Fprint(w, `{"protocol_version":3,"contract_version":3,"features":["native_deliveries","cross_team_delegation","detached_background","phase_classification"]}`)
			return
		}
		handler(w, r)
	})
}
func TestNativeCalls(t *testing.T) {
	c := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/atp/v3/chats/chat/deliveries":
			var input DeliveryInput
			json.NewDecoder(r.Body).Decode(&input)
			if input.EventID != "event" || input.Output != "exact output" {
				t.Error(input)
			}
			fmt.Fprint(w, `{"event_id":"event","status":"queued","duplicate":false}`)
		case "/api/v1/atp/v3/chats/chat/delegations":
			var input DelegationInput
			json.NewDecoder(r.Body).Decode(&input)
			if input.TargetChatID != "target" || input.Prompt != "prompt" {
				t.Error(input)
			}
			fmt.Fprint(w, `{"task_id":"task","target_chat_id":"target","turn_id":"turn"}`)
		case "/api/v1/atp/v3/chats/chat/tasks/task":
			fmt.Fprint(w, `{"task_id":"task","origin_turn_id":"origin","status":"completed","output":" full output "}`)
		case "/api/v1/atp/v3/chats/chat/tasks/task/cancel":
			var input map[string]bool
			json.NewDecoder(r.Body).Decode(&input)
			if !input["subtree"] {
				t.Error(input)
			}
			fmt.Fprint(w, `{"task_id":"task","status":"cancelled","subtree":true}`)
		case "/api/v1/atp/v3/chats/chat/classify-phase":
			var input ClassificationInput
			json.NewDecoder(r.Body).Decode(&input)
			if input.FinalOutput != "output" || input.RequestID != "request" {
				t.Error(input)
			}
			fmt.Fprint(w, `{"request_id":"request","invocation_id":"invocation","model":"model","decision":"NOT BLOCKED"}`)
		default:
			t.Error(r.URL)
		}
	})
	ctx := context.Background()
	if got, err := c.Deliver(ctx, "chat", DeliveryInput{WorkspaceID: "ws", EventID: "event", OriginTurnID: "origin", TaskID: "task", Status: "completed", Output: "exact output"}); err != nil || got.Status != "queued" {
		t.Fatal(got, err)
	}
	if got, err := c.Delegate(ctx, "chat", DelegationInput{WorkspaceID: "ws", EventID: "event", OriginTurnID: "origin", TargetChatID: "target", Prompt: "prompt"}); err != nil || got.TaskID != "task" {
		t.Fatal(got, err)
	}
	if got, err := c.Task(ctx, "chat", "task"); err != nil || got.Output != " full output " {
		t.Fatal(got, err)
	}
	if got, err := c.CancelTask(ctx, "chat", "task", true); err != nil || !got.Subtree {
		t.Fatal(got, err)
	}
	if got, err := c.ClassifyPhase(ctx, "chat", ClassificationInput{RequestID: "request", PhaseID: "phase", Attempt: 1, TaskContext: json.RawMessage(`{}`), FinalOutput: "output"}); err != nil || got.Decision != "NOT BLOCKED" {
		t.Fatal(got, err)
	}
}
func TestNativeFailClosed(t *testing.T) {
	ctx := context.Background()
	delivery := DeliveryInput{WorkspaceID: "ws", EventID: "event", OriginTurnID: "origin", TaskID: "task", Status: "completed"}
	delegation := DelegationInput{WorkspaceID: "ws", EventID: "event", OriginTurnID: "origin", TargetChatID: "target", Prompt: "prompt"}
	classification := ClassificationInput{RequestID: "request", PhaseID: "phase", Attempt: 1, TaskContext: json.RawMessage(`{}`), FinalOutput: "output"}
	methods := []func(*Client) error{
		func(c *Client) error { _, e := c.Deliver(ctx, "chat", delivery); return e }, func(c *Client) error { _, e := c.Delegate(ctx, "chat", delegation); return e }, func(c *Client) error { _, e := c.Task(ctx, "chat", "task"); return e }, func(c *Client) error { _, e := c.CancelTask(ctx, "chat", "task", false); return e }, func(c *Client) error { _, e := c.ClassifyPhase(ctx, "chat", classification); return e },
	}
	for _, run := range methods {
		c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "capabilities") {
				t.Error("unsupported mutation")
			}
			fmt.Fprint(w, `{"protocol_version":3,"contract_version":3,"features":["background_delegation"]}`)
		})
		if err := run(c); !errors.Is(err, ErrUnsupportedFeature) {
			t.Fatal(err)
		}
		c = nativeFixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
		if err := run(c); err == nil {
			t.Fatal("accepted uncorrelated result")
		}
	}
	c := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid input request") })
	if _, e := c.Deliver(ctx, "chat", DeliveryInput{}); e == nil {
		t.Fatal("missing delivery identity")
	}
	bad := delivery
	bad.Status = "running"
	if _, e := c.Deliver(ctx, "chat", bad); e == nil {
		t.Fatal("bad delivery status")
	}
	if _, e := c.Delegate(ctx, "chat", DelegationInput{}); e == nil {
		t.Fatal("missing delegation identity")
	}
	if _, e := c.Task(ctx, "chat", "/"); e == nil {
		t.Fatal("bad task ID")
	}
	if _, e := c.CancelTask(ctx, "chat", "/", false); e == nil {
		t.Fatal("bad cancellation ID")
	}
	if _, e := c.ClassifyPhase(ctx, "chat", ClassificationInput{}); e == nil {
		t.Fatal("missing classifier identity")
	}
	if _, e := c.Task(ctx, "/", "task"); e == nil {
		t.Fatal("bad chat")
	}
}
func TestClassifierAmbiguousIsNotRetried(t *testing.T) {
	attempts := 0
	c := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(503)
		fmt.Fprint(w, `{"error":{"code":"provider_unavailable","message":"uncertain"}}`)
	})
	_, err := c.ClassifyPhase(context.Background(), "chat", ClassificationInput{RequestID: "request", PhaseID: "phase", Attempt: 1, TaskContext: json.RawMessage(`{}`), FinalOutput: "output"})
	var api *APIError
	if !errors.As(err, &api) || api.Status != 503 || attempts != 1 {
		t.Fatal(err, attempts)
	}
}

func TestClassificationLimits(t *testing.T) {
	c := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid classification reached API") })
	valid := ClassificationInput{RequestID: "request", PhaseID: "phase", Attempt: 1, TaskContext: json.RawMessage(`{}`), FinalOutput: "output"}
	for _, mutate := range []func(*ClassificationInput){func(v *ClassificationInput) { v.RequestID = strings.Repeat("x", 129) }, func(v *ClassificationInput) { v.PhaseID = strings.Repeat("x", 129) }, func(v *ClassificationInput) { v.TaskContext = json.RawMessage(`null`) }, func(v *ClassificationInput) { v.FinalOutput = "" }, func(v *ClassificationInput) { v.FinalOutput = strings.Repeat("x", 1<<20) }} {
		input := valid
		mutate(&input)
		if _, err := c.ClassifyPhase(context.Background(), "chat", input); err == nil {
			t.Fatal("accepted invalid request")
		}
	}
}

func TestFailedDeliveryReceiptIsPreserved(t *testing.T) {
	c := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"event_id":"event","status":"failed","error":"cancelled before delivery","duplicate":true}`)
	})
	receipt, err := c.Deliver(context.Background(), "chat", DeliveryInput{WorkspaceID: "ws", EventID: "event", OriginTurnID: "origin", TaskID: "task", Status: "completed", Output: "output"})
	if err != nil || receipt.Status != "failed" || !receipt.Duplicate || string(receipt.Error) != `"cancelled before delivery"` {
		t.Fatal(receipt, err)
	}
}

func TestDelegationNotifyOriginExplicitFalse(t *testing.T) {
	notify := false
	input := DelegationInput{WorkspaceID: "workspace", EventID: "event", OriginTurnID: "origin", TargetChatID: "target", Prompt: "prompt", NotifyOrigin: &notify}
	encoded, err := json.Marshal(input)
	if err != nil || !strings.Contains(string(encoded), `"notify_origin":false`) {
		t.Fatal(string(encoded), err)
	}
	input.NotifyOrigin = nil
	encoded, err = json.Marshal(input)
	if err != nil || strings.Contains(string(encoded), `"notify_origin"`) {
		t.Fatal("default notification policy overridden", string(encoded), err)
	}
}
