package agents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

func TestToolExecValidatesArguments(t *testing.T) {
	invoked := ""
	tool := NewTool(func(ctx context.Context, name, task string) agent.Result {
		invoked = name + "|" + task
		return agent.TextResult("invoked")
	})
	cases := []struct {
		name  string
		args  string
		match string
	}{
		{"invalid json", `{"name":`, "invalid arguments"},
		{"missing name", `{"task":"t"}`, "missing required argument 'name'"},
		{"blank name", `{"name":"  ","task":"t"}`, "missing required argument 'name'"},
		{"missing task", `{"name":"reader"}`, "missing required argument 'task'"},
		{"blank task", `{"name":"reader","task":" \n"}`, "missing required argument 'task'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tool.Exec(context.Background(), json.RawMessage(tc.args))
			if !res.IsError || !strings.Contains(blockText(res.Content), tc.match) {
				t.Fatalf("result = %+v", res)
			}
		})
	}
	if invoked != "" {
		t.Fatalf("invalid arguments reached the executor: %q", invoked)
	}
	task := "line one\nline \"two\" $(no shell)"
	res := tool.Exec(context.Background(), json.RawMessage(`{"name":"reader","task":`+mustJSON(t, task)+`}`))
	if res.IsError || invoked != "reader|"+task {
		t.Fatalf("invoke = %q res = %+v", invoked, res)
	}
}

func TestToolExecAndRebindAreSynchronized(t *testing.T) {
	tool := NewTool(func(context.Context, string, string) agent.Result { return agent.TextResult("first") })
	const workers = 4
	const iterations = 250
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []string
	)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < iterations; j++ {
				res := tool.Exec(context.Background(), json.RawMessage(`{"name":"reader","task":"t"}`))
				value := blockText(res.Content)
				if res.IsError {
					value = "error: " + value
				}
				mu.Lock()
				results = append(results, value)
				mu.Unlock()
			}
		}()
	}
	close(start)
	for i := 0; i < 500; i++ {
		switch i % 3 {
		case 0:
			tool.SetInvoke(func(context.Context, string, string) agent.Result { return agent.TextResult("first") })
		case 1:
			tool.SetInvoke(func(context.Context, string, string) agent.Result { return agent.TextResult("second") })
		default:
			tool.SetInvoke(nil)
		}
	}
	wg.Wait()
	for _, result := range results {
		switch {
		case result == "first", result == "second":
		case strings.Contains(result, "delegation is not available"):
		default:
			t.Fatalf("concurrent rebind produced an unstable result: %q", result)
		}
	}
	tool.SetInvoke(func(context.Context, string, string) agent.Result { return agent.TextResult("stable") })
	res := tool.Exec(context.Background(), json.RawMessage(`{"name":"reader","task":"t"}`))
	if res.IsError || blockText(res.Content) != "stable" {
		t.Fatalf("final binding result = %+v", res)
	}
}

func TestToolExecWithoutBinding(t *testing.T) {
	var tool *Tool = NewTool(nil)
	res := tool.Exec(context.Background(), json.RawMessage(`{"name":"reader","task":"t"}`))
	if !res.IsError || !strings.Contains(blockText(res.Content), "not available") {
		t.Fatalf("unbound result = %+v", res)
	}
}

func TestToolResultFormatting(t *testing.T) {
	res := Result{
		Name:        "reader",
		Tier:        "bundle",
		Origin:      "bundle:agents",
		Depth:       2,
		Model:       "test/model",
		Answer:      "the answer",
		SessionPath: "/tmp/child.jsonl",
	}
	okResult := ToolResult(res, nil)
	text := blockText(okResult.Content)
	if okResult.IsError {
		t.Fatal("successful result marked as error")
	}
	if !strings.Contains(text, "[subagent reader tier=bundle") || !strings.Contains(text, "depth=2") {
		t.Fatalf("identity missing: %q", text)
	}
	if !strings.Contains(text, "the answer") || !strings.Contains(text, "/tmp/child.jsonl") {
		t.Fatalf("answer or session missing: %q", text)
	}
	failure := ToolResult(Result{Name: "reader", Answer: "partial"}, errors.New("stream failed"))
	failureText := blockText(failure.Content)
	if !failure.IsError || !strings.Contains(failureText, "error: stream failed") || !strings.Contains(failureText, "partial") {
		t.Fatalf("failure result = %+v", failure)
	}
	empty := ToolResult(Result{Name: "reader"}, nil)
	if !strings.Contains(blockText(empty.Content), "(no output)") {
		t.Fatalf("empty result = %+v", empty)
	}
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func blockText(blocks []agent.ContentBlock) string {
	var out strings.Builder
	for _, block := range blocks {
		out.WriteString(block.Text)
	}
	return out.String()
}
