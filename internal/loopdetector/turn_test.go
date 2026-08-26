package loopdetector

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

func TestExtractTurn(t *testing.T) {
	msg := &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeThinking, Thinking: "  think about the refactor  "},
			{Type: agent.BlockTypeText, Text: "  hello world  "},
			{Type: agent.BlockTypeToolCall, ID: "call_1", Name: "read", Arguments: json.RawMessage(`{"path":"/etc/hosts"}`)},
			{Type: agent.BlockTypeToolCall, ID: "call_2", Name: "read", Arguments: json.RawMessage(`{"path":"/etc/hosts"}`)},
		},
		StopReason: "toolUse",
	}
	results := []*agent.ToolResultMessage{
		{
			Role:       string(agent.RoleToolResult),
			ToolCallID: "call_1",
			ToolName:   "read",
			Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "127.0.0.1 localhost"}},
			Timestamp:  1,
		},
	}
	tr := ExtractTurn(7, msg, results)
	if tr.TurnIndex != 7 {
		t.Errorf("TurnIndex = %d, want 7", tr.TurnIndex)
	}
	if tr.ThinkingText != "think about the refactor" {
		t.Errorf("ThinkingText = %q, want trimmed thinking", tr.ThinkingText)
	}
	if tr.TextContent != "hello world" {
		t.Errorf("TextContent = %q, want trimmed text", tr.TextContent)
	}
	if len(tr.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(tr.ToolCalls))
	}
	c0, c1 := tr.ToolCalls[0], tr.ToolCalls[1]
	if c0.Name != "read" || c0.DisplaySummary != "read(/etc/hosts)" {
		t.Errorf("call 0 = %+v", c0)
	}
	if !c0.HasResult || c0.IsError {
		t.Errorf("call 0 HasResult = %v, IsError = %v; want true, false", c0.HasResult, c0.IsError)
	}
	if c0.ResultKey == "" {
		t.Error("call 0 ResultKey is empty, want the result fingerprint")
	}
	if c1.HasResult || c1.ResultKey != "" {
		t.Errorf("call 1 HasResult = %v, ResultKey = %q; want false, empty", c1.HasResult, c1.ResultKey)
	}
	if c0.CallKey != c1.CallKey {
		t.Errorf("identical args must fingerprint identically: %q vs %q", c0.CallKey, c1.CallKey)
	}
}

func TestExtractTurnResultRequiresMatchingToolName(t *testing.T) {
	msg := &agent.AssistantMessage{
		Role: string(agent.RoleAssistant),
		Content: []agent.ContentBlock{
			{Type: agent.BlockTypeToolCall, ID: "call_1", Name: "read", Arguments: json.RawMessage(`{"path":"/a"}`)},
		},
		StopReason: "toolUse",
	}
	results := []*agent.ToolResultMessage{
		{Role: string(agent.RoleToolResult), ToolCallID: "call_1", ToolName: "bash", Timestamp: 1},
	}
	tr := ExtractTurn(0, msg, results)
	if tr.ToolCalls[0].HasResult {
		t.Errorf("HasResult = true, want false for a mismatched tool name")
	}
}

func TestExtractTurnNilMessage(t *testing.T) {
	tr := ExtractTurn(3, nil, nil)
	if tr.TurnIndex != 3 || tr.ThinkingText != "" || tr.TextContent != "" || len(tr.ToolCalls) != 0 {
		t.Errorf("zero Turn expected, got %+v", tr)
	}
}

func TestCallFingerprintDeterministic(t *testing.T) {
	k1 := callFingerprint("read", map[string]any{"path": "/a", "mode": "r"})
	k2 := callFingerprint("read", map[string]any{"mode": "r", "path": "/a"})
	if k1 != k2 {
		t.Errorf("key order must not change the fingerprint: %q vs %q", k1, k2)
	}
	if len(k1) != 64 {
		t.Errorf("fingerprint length = %d, want 64 hex characters", len(k1))
	}
	k3 := callFingerprint("read", map[string]any{"path": "/b"})
	if k1 == k3 {
		t.Errorf("different args must fingerprint differently")
	}
	k4 := callFingerprint("bash", map[string]any{"path": "/a", "mode": "r"})
	if k1 == k4 {
		t.Errorf("different tool names must fingerprint differently")
	}
}

func TestResultFingerprint(t *testing.T) {
	res := func(out string, isErr bool) *agent.ToolResultMessage {
		return &agent.ToolResultMessage{
			Role:       string(agent.RoleToolResult),
			ToolCallID: "c",
			ToolName:   "read",
			Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: out}},
			IsError:    isErr,
			Timestamp:  1,
		}
	}
	a := resultFingerprint(res("same output", false))
	b := resultFingerprint(res("same output", false))
	if a != b {
		t.Errorf("identical results must fingerprint identically: %q vs %q", a, b)
	}
	if c := resultFingerprint(res("different output", false)); a == c {
		t.Errorf("different content must fingerprint differently")
	}
	if d := resultFingerprint(res("same output", true)); a == d {
		t.Errorf("the error flag must change the fingerprint")
	}
}

func TestNormalizeThinking(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"code fence collapses", "explain ```go\nfmt.Println(x)\n``` now", "explain <>"},
		{"inline code collapses", "use `strings.Fields` here", "<>"},
		{"paths collapse", "read /home/luca/file.go and ~/.env now", "<>  <>"},
		{"short words dropped", "The quick brown fox", "the quick brown"},
		{"short words only lowercase", "The QUICK brown FOX", "the quick brown fox"},
		{"case folded", "Hello WORLD", "hello world"},
		{"whitespace collapsed", "a    b\t\tc\n\nd", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeThinking(tt.in); got != tt.want {
				t.Errorf("normalizeThinking(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeText(t *testing.T) {
	in := "Run `go build` now.\n\n```sh\nmake test\n```\nRead /home/luca/a.go please."
	want := "run now. read please."
	if got := normalizeText(in); got != want {
		t.Errorf("normalizeText(%q) = %q, want %q", in, got, want)
	}
}

func TestDisplaySummary(t *testing.T) {
	t.Run("bash truncated to 40", func(t *testing.T) {
		cmd := "echo " + strings.Repeat("x", 60)
		got := displaySummary("bash", map[string]any{"command": cmd})
		want := "echo " + strings.Repeat("x", 35)
		if got != want {
			t.Errorf("displaySummary = %q, want %q", got, want)
		}
	})
	t.Run("path takes precedence", func(t *testing.T) {
		if got := displaySummary("read", map[string]any{"path": "/a/b", "pattern": "x"}); got != "read(/a/b)" {
			t.Errorf("displaySummary = %q, want %q", got, "read(/a/b)")
		}
	})
	t.Run("filePath fallback", func(t *testing.T) {
		if got := displaySummary("read", map[string]any{"filePath": "/c"}); got != "read(/c)" {
			t.Errorf("displaySummary = %q, want %q", got, "read(/c)")
		}
	})
	t.Run("pattern quoted", func(t *testing.T) {
		if got := displaySummary("grep", map[string]any{"pattern": "TODO"}); got != `grep("TODO")` {
			t.Errorf("displaySummary = %q, want %q", got, `grep("TODO")`)
		}
	})
	t.Run("long pattern truncated to 30", func(t *testing.T) {
		got := displaySummary("grep", map[string]any{"pattern": strings.Repeat("y", 40)})
		if len([]rune(got)) != 30 {
			t.Errorf("displaySummary length = %d, want 30 (got %q)", len([]rune(got)), got)
		}
		if !strings.HasPrefix(got, `grep("`) {
			t.Errorf("displaySummary = %q, want a grep(\"... prefix", got)
		}
	})
	t.Run("non-string command ignored", func(t *testing.T) {
		if got := displaySummary("bash", map[string]any{"command": 42}); got != "bash" {
			t.Errorf("displaySummary = %q, want %q", got, "bash")
		}
	})
	t.Run("unknown tool falls back to name", func(t *testing.T) {
		if got := displaySummary("write", map[string]any{"path": "/a"}); got != "write(/a)" {
			t.Errorf("displaySummary = %q, want %q", got, "write(/a)")
		}
		if got := displaySummary("write", map[string]any{}); got != "write" {
			t.Errorf("displaySummary = %q, want %q", got, "write")
		}
	})
}

func TestSummarizeSubagent(t *testing.T) {
	t.Run("single agent and task", func(t *testing.T) {
		got := displaySummary("subagent", map[string]any{"agent": "dev", "task": "Refactor the loader"})
		want := "subagent(dev#" + sha256Hex(normalizeTask("Refactor the loader"))[:8] + ")"
		if got != want {
			t.Errorf("displaySummary = %q, want %q", got, want)
		}
	})
	t.Run("tasks array", func(t *testing.T) {
		got := displaySummary("subagent", map[string]any{
			"tasks": []any{
				map[string]any{"agent": "qa", "task": "Test the change"},
				map[string]any{"agent": "dev", "task": "Build the change"},
			},
		})
		if !strings.HasPrefix(got, "subagent(qa#") || !strings.Contains(got, " + subagent(dev#") {
			t.Errorf("displaySummary = %q, want two joined pairs", got)
		}
	})
	t.Run("empty agent falls back to default", func(t *testing.T) {
		got := displaySummary("subagent", map[string]any{"agent": "", "task": "Do a thing"})
		if !strings.HasPrefix(got, "subagent(default#") {
			t.Errorf("displaySummary = %q, want a default agent pair", got)
		}
	})
	t.Run("no agent argument", func(t *testing.T) {
		if got := displaySummary("subagent", map[string]any{}); got != "subagent" {
			t.Errorf("displaySummary = %q, want %q", got, "subagent")
		}
	})
	t.Run("chain group", func(t *testing.T) {
		got := displaySummary("subagent", map[string]any{
			"chain": []any{map[string]any{"agent": "dev", "task": "Step one"}},
		})
		if !strings.HasPrefix(got, "subagent(dev#") {
			t.Errorf("displaySummary = %q, want a chain pair", got)
		}
	})
}

func TestNormalizeTask(t *testing.T) {
	if got := normalizeTask("  Refactor the LOADER!!  "); got != "refactor the loader" {
		t.Errorf("normalizeTask = %q, want %q", got, "refactor the loader")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short = %q, want unchanged", got)
	}
	if got := truncate("abcdefghijklm", 5); got != "abcde" {
		t.Errorf("truncate = %q, want %q", got, "abcde")
	}
	if got := truncate("héllo", 3); got != "hél" {
		t.Errorf("truncate runes = %q, want %q", got, "hél")
	}
}
