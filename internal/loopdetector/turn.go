package loopdetector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
)

type Turn struct {
	TurnIndex int

	ThinkingText string

	TextContent string

	ToolCalls []ToolCall
}

type ToolCall struct {
	ToolCallID string

	Name string

	CallKey string

	DisplaySummary string

	ResultKey string

	IsError bool

	HasResult bool
}

func ExtractTurn(turnIndex int, msg *agent.AssistantMessage, results []*agent.ToolResultMessage) Turn {
	if msg == nil {
		return Turn{TurnIndex: turnIndex}
	}
	resultByID := make(map[string]*agent.ToolResultMessage, len(results))
	for _, r := range results {
		if r != nil {
			resultByID[r.ToolCallID] = r
		}
	}
	var thinking, text strings.Builder
	var calls []ToolCall
	for _, b := range msg.Content {
		switch b.Type {
		case agent.BlockTypeThinking:
			thinking.WriteString(b.Thinking)
		case agent.BlockTypeText:
			text.WriteString(b.Text)
		case agent.BlockTypeToolCall:
			args := argsOf(b.Arguments)
			res := resultByID[b.ID]
			hasResult := res != nil && res.ToolName == b.Name
			call := ToolCall{
				ToolCallID:     b.ID,
				Name:           b.Name,
				CallKey:        callFingerprint(b.Name, args),
				DisplaySummary: displaySummary(b.Name, args),
				HasResult:      hasResult,
			}
			if hasResult {
				call.ResultKey = resultFingerprint(res)
				call.IsError = res.IsError
			}
			calls = append(calls, call)
		}
	}
	return Turn{
		TurnIndex:    turnIndex,
		ThinkingText: strings.TrimSpace(thinking.String()),
		TextContent:  strings.TrimSpace(text.String()),
		ToolCalls:    calls,
	}
}

func argsOf(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func callFingerprint(name string, args map[string]any) string {
	return sha256Hex(canonicalJSON(map[string]any{"name": name, "args": args}))
}

func resultFingerprint(res *agent.ToolResultMessage) string {
	content := make([]any, 0, len(res.Content))
	for _, b := range res.Content {
		if b.Type == agent.BlockTypeText {
			content = append(content, map[string]any{"type": "text", "text": b.Text})
		} else {
			content = append(content, map[string]any{"type": "unknown"})
		}
	}
	return sha256Hex(canonicalJSON(map[string]any{
		"toolName": res.ToolName,
		"isError":  res.IsError,
		"content":  content,
	}))
}

var (
	codeFenceRE  = regexp.MustCompile("```[\\s\\S]*?```")
	inlineCodeRE = regexp.MustCompile("`[^`]+`")
	pathRE       = regexp.MustCompile(`/?(?:Users|home|~)[^\s)]+`)
	whitespaceRE = regexp.MustCompile(`\s+`)
	shortWordRE  = regexp.MustCompile(`\b[a-z]{1,4}\b`)
)

func normalizeThinking(s string) string {
	s = codeFenceRE.ReplaceAllString(s, "<code>")
	s = inlineCodeRE.ReplaceAllString(s, "<code>")
	s = pathRE.ReplaceAllString(s, "<path>")
	s = whitespaceRE.ReplaceAllString(s, " ")
	s = shortWordRE.ReplaceAllString(s, "")
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeText(s string) string {
	s = codeFenceRE.ReplaceAllString(s, " ")
	s = inlineCodeRE.ReplaceAllString(s, " ")
	s = pathRE.ReplaceAllString(s, " ")
	s = whitespaceRE.ReplaceAllString(s, " ")
	return strings.ToLower(strings.TrimSpace(s))
}

func similarity(a, b string) float64 {
	wA := wordSet(a)
	wB := wordSet(b)
	if len(wA) == 0 || len(wB) == 0 {
		return 0
	}
	inter := 0
	for w := range wA {
		if wB[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(wA)+len(wB)-inter)
}

func wordSet(s string) map[string]bool {
	set := make(map[string]bool)
	for _, w := range strings.Fields(s) {
		set[w] = true
	}
	return set
}

func displaySummary(toolName string, args map[string]any) string {
	if toolName == "bash" {
		if cmd, ok := stringArg(args, "command"); ok {
			return truncate(cmd, 40)
		}
	}
	if toolName == "subagent" {
		return summarizeSubagent(args)
	}
	if p, ok := stringArg(args, "path"); ok {
		return toolName + "(" + p + ")"
	}
	if p, ok := stringArg(args, "filePath"); ok {
		return toolName + "(" + p + ")"
	}
	if pattern, ok := stringArg(args, "pattern"); ok {
		return truncate(fmt.Sprintf("%s(%q)", toolName, pattern), 30)
	}
	return toolName
}

func stringArg(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func summarizeSubagent(args map[string]any) string {
	type pair struct{ agent, task string }
	var pairs []pair
	if a, ok := stringArg(args, "agent"); ok {
		task, _ := stringArg(args, "task")
		pairs = append(pairs, pair{a, task})
	}
	for _, group := range []string{"tasks", "chain"} {
		list, ok := args[group].([]any)
		if !ok {
			continue
		}
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			a, ok := stringArg(m, "agent")
			if !ok {
				continue
			}
			task, _ := stringArg(m, "task")
			pairs = append(pairs, pair{a, task})
		}
	}
	if len(pairs) == 0 {
		return "subagent"
	}
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		name := p.agent
		if name == "" {
			name = "default"
		}
		parts = append(parts, fmt.Sprintf("subagent(%s#%s)", name, sha256Hex(normalizeTask(p.task))[:8]))
	}
	return strings.Join(parts, " + ")
}

func normalizeTask(s string) string {
	s = strings.ToLower(s)
	s = whitespaceRE.ReplaceAllString(s, " ")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
