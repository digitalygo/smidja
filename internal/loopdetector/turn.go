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

// Turn is one completed assistant turn, the unit the detector observes.
// Build records with ExtractTurn from an assistant message and its tool
// results, or construct them directly.
type Turn struct {
	// TurnIndex is the host's session turn counter, used in detection
	// messages to identify the turns involved.
	TurnIndex int

	// ThinkingText is the concatenated thinking blocks, trimmed.
	ThinkingText string

	// TextContent is the concatenated text blocks, trimmed.
	TextContent string

	// ToolCalls are the tool invocations of the turn, in order.
	ToolCalls []ToolCall
}

// ToolCall is one tool invocation observed in a turn.
type ToolCall struct {
	// ToolCallID matches the toolCall block ID and the result's
	// ToolCallID.
	ToolCallID string

	// Name is the tool name, for example "read" or "bash".
	Name string

	// CallKey is the sha256 fingerprint of the tool name and canonical
	// arguments, mirroring the extension's callFingerprint.
	CallKey string

	// DisplaySummary is the human-readable summary the extension uses in
	// detection messages, mirroring its displaySummary.
	DisplaySummary string

	// ResultKey is the sha256 fingerprint of the tool result, or "" when
	// the call has no result.
	ResultKey string

	// IsError marks a failed execution.
	IsError bool

	// HasResult reports whether a result for this call was observed.
	HasResult bool
}

// ExtractTurn builds a turn record from an assistant message and the tool
// results of the same turn, mirroring the extension's extractFromMessage.
// turnIndex is the host's turn counter for the message. Results are
// matched to toolCall blocks by ToolCallID; a result counts for a call
// only when its ToolName matches the block's Name, exactly as in the
// extension. A nil msg yields a zero Turn.
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

// argsOf parses a toolCall block's raw arguments into a map, mirroring
// the extension's `block.arguments ?? {}`: nil, empty, or invalid
// arguments become an empty map so fingerprints and summaries stay
// deterministic.
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

// sha256Hex returns the lowercase hex sha256 of s, mirroring the
// extension's createHash("sha256").update(input).digest("hex").
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// canonicalJSON serializes v the way the extension's
// JSON.stringify(sortKeys(value)) does: object keys sorted at every
// level, arrays in order, primitives as compact JSON. Go's encoding/json
// sorts map keys deterministically, which matches the recursive
// sortKeys.
func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// callFingerprint mirrors the extension's callFingerprint: the sha256 of
// the canonical JSON of {name, args}.
func callFingerprint(name string, args map[string]any) string {
	return sha256Hex(canonicalJSON(map[string]any{"name": name, "args": args}))
}

// resultFingerprint mirrors the extension's resultFingerprint: the sha256
// of the canonical JSON of {toolName, isError, content}, where content
// maps text blocks to {type:"text", text} and everything else to
// {type:"unknown"}. smidja tool results carry text blocks only, so the
// extension's image-block branch (mimeType + hashed data) has no smidja
// equivalent and is not ported; an image-like block would fingerprint as
// {type:"unknown"} exactly like any other non-text block.
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

// Normalization regexes, ported verbatim from the extension.
var (
	codeFenceRE  = regexp.MustCompile("```[\\s\\S]*?```")
	inlineCodeRE = regexp.MustCompile("`[^`]+`")
	pathRE       = regexp.MustCompile(`/?(?:Users|home|~)[^\s)]+`)
	whitespaceRE = regexp.MustCompile(`\s+`)
	shortWordRE  = regexp.MustCompile(`\b[a-z]{1,4}\b`)
)

// normalizeThinking mirrors the extension's normalizeThinking for
// reasoning-stuck detection: code blocks collapse to "<code>", paths to
// "<path>", whitespace collapses, short lowercase words (1-4 letters) are
// dropped, and the result is trimmed and lowercased. The word removal
// runs before lowercasing, exactly as in the source, so uppercase words
// always survive it.
func normalizeThinking(s string) string {
	s = codeFenceRE.ReplaceAllString(s, "<code>")
	s = inlineCodeRE.ReplaceAllString(s, "<code>")
	s = pathRE.ReplaceAllString(s, "<path>")
	s = whitespaceRE.ReplaceAllString(s, " ")
	s = shortWordRE.ReplaceAllString(s, "")
	return strings.ToLower(strings.TrimSpace(s))
}

// normalizeText mirrors the extension's normalizeText for message-repeat
// detection: code blocks, inline code, and paths become spaces,
// whitespace collapses, and the result is trimmed and lowercased.
func normalizeText(s string) string {
	s = codeFenceRE.ReplaceAllString(s, " ")
	s = inlineCodeRE.ReplaceAllString(s, " ")
	s = pathRE.ReplaceAllString(s, " ")
	s = whitespaceRE.ReplaceAllString(s, " ")
	return strings.ToLower(strings.TrimSpace(s))
}

// similarity mirrors the extension's similarity: Jaccard overlap of the
// whitespace-separated word sets. Empty sets score 0.
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

// wordSet splits s on whitespace (dropping empties) into a set, matching
// the extension's split(/\s+/).filter(Boolean) into a Set.
func wordSet(s string) map[string]bool {
	set := make(map[string]bool)
	for _, w := range strings.Fields(s) {
		set[w] = true
	}
	return set
}

// displaySummary mirrors the extension's displaySummary: the human
// readable one-liner used in detection messages. bash commands are
// truncated to 40 runes, paths render as tool(path), patterns as
// tool("pattern") truncated to 30 runes, and subagent calls summarize
// their agent/task pairs. Unknown shapes fall back to the tool name.
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

// stringArg returns the string value of a map key when present. It
// implements the extension's nullish-coalescing `args.path ??
// args.filePath`: a nil or non-string value counts as missing.
func stringArg(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// truncate truncates s to at most n runes, mirroring the extension's
// string.slice(0, n) on the display strings.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// summarizeSubagent mirrors the extension's summarizeSubagent: one pair
// per subagent call, rendered "subagent(agent#hash8)" and joined by
// " + ", or the bare word "subagent" when no agent argument is present.
// The task hash is the first 8 hex characters of the sha256 of the
// normalized task text.
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

// normalizeTask mirrors the extension's normalizeTask: lowercase,
// whitespace collapsed, only [a-z0-9 ] kept, trimmed.
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
