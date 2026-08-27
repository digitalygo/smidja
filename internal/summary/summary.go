package summary

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

const (
	defaultMaxIntents   = 5
	defaultIntentRunes  = 200
	defaultExcerptRunes = 400
)

type Options struct {
	MaxLastIntents  int
	MaxIntentRunes  int
	MaxExcerptRunes int
}

type Digest struct {
	ShortID             string
	Workspace           string
	StartedAt           time.Time
	LastActivity        time.Time
	LastIntents         []string
	AssistantTurns      int
	ToolCallsByTool     map[string]int
	LastResponseExcerpt string
	Compacted           bool
}

type Entryish interface {
	EntryType() string
}

var (
	jwtSecretRe  = regexp.MustCompile(`[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
	skSecretRe   = regexp.MustCompile(`sk-[A-Za-z0-9_-]+`)
	ghpSecretRe  = regexp.MustCompile(`ghp_[A-Za-z0-9]{4,}`)
	akiaSecretRe = regexp.MustCompile(`AKIA[A-Z0-9]{8,}`)
)

func Build(entries []Entryish, opts Options) Digest {
	opts = opts.withDefaults()
	d := Digest{ToolCallsByTool: map[string]int{}}
	var intents []string
	var excerpt string
	for _, e := range entries {
		ts := envelopeTimestamp(e)
		if ts.After(d.LastActivity) {
			d.LastActivity = ts
		}
		switch v := e.(type) {
		case *session.Header:
			d.ShortID = shortID(v.ID)
			d.Workspace = v.Cwd
			if t, err := time.Parse(time.RFC3339Nano, v.Timestamp); err == nil {
				d.StartedAt = t
			}
		case *session.MessageEntry:
			switch v.MessageRole() {
			case "user":
				if text := extractUserText(v); text != "" {
					intents = append(intents, text)
				}
			case "assistant":
				d.AssistantTurns++
				text := extractAssistantText(v)
				if text != "" {
					excerpt = text
				}
				for name, count := range extractToolCalls(v) {
					d.ToolCallsByTool[name] += count
				}
			}
		case *session.CompactionEntry:
			d.Compacted = true
		}
	}
	if len(intents) > opts.MaxLastIntents {
		intents = intents[len(intents)-opts.MaxLastIntents:]
	}
	for _, in := range intents {
		d.LastIntents = append(d.LastIntents, truncateRunes(redact(in), opts.MaxIntentRunes))
	}
	if excerpt != "" {
		d.LastResponseExcerpt = truncateRunes(redact(excerpt), opts.MaxExcerptRunes)
	}
	return d
}

func (o Options) withDefaults() Options {
	if o.MaxLastIntents <= 0 {
		o.MaxLastIntents = defaultMaxIntents
	}
	if o.MaxIntentRunes <= 0 {
		o.MaxIntentRunes = defaultIntentRunes
	}
	if o.MaxExcerptRunes <= 0 {
		o.MaxExcerptRunes = defaultExcerptRunes
	}
	return o
}

func shortID(id string) string {
	r := []rune(id)
	if len(r) > 8 {
		r = r[:8]
	}
	return string(r)
}

func envelopeTimestamp(e Entryish) time.Time {
	b, err := json.Marshal(e)
	if err != nil {
		return time.Time{}
	}
	var env struct {
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, env.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return t
}

func extractUserText(e *session.MessageEntry) string {
	var payload struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(e.Message, &payload); err != nil {
		return ""
	}
	return contentText(payload.Content)
}

func extractAssistantText(e *session.MessageEntry) string {
	var payload struct {
		Content []agent.ContentBlock `json:"content"`
	}
	if err := json.Unmarshal(e.Message, &payload); err != nil {
		return ""
	}
	var parts []string
	for _, b := range payload.Content {
		if b.Type == agent.BlockTypeText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractToolCalls(e *session.MessageEntry) map[string]int {
	var payload struct {
		Content []agent.ContentBlock `json:"content"`
	}
	if err := json.Unmarshal(e.Message, &payload); err != nil {
		return nil
	}
	counts := map[string]int{}
	for _, b := range payload.Content {
		if b.Type == agent.BlockTypeToolCall && b.Name != "" {
			counts[b.Name]++
		}
	}
	return counts
}

func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []agent.ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == agent.BlockTypeText && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	var single agent.ContentBlock
	if err := json.Unmarshal(raw, &single); err == nil && single.Type == agent.BlockTypeText {
		return single.Text
	}
	return ""
}

func redact(s string) string {
	s = jwtSecretRe.ReplaceAllString(s, "JWT-REDACTED")
	s = skSecretRe.ReplaceAllString(s, "sk-REDACTED")
	s = ghpSecretRe.ReplaceAllString(s, "ghp_REDACTED")
	s = akiaSecretRe.ReplaceAllString(s, "AKIA-REDACTED")
	return s
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
