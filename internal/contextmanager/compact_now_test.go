package contextmanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

func compactNowConfig() Config {
	return Config{
		Enabled:             true,
		ContextWindowTokens: 1000,
		CompactTarget:       0.1,
		KeepRecentMessages:  1,
	}
}

func TestCompactNowCanceledContext(t *testing.T) {
	manager, err := New(compactNowConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := manager.CompactNow(ctx, "system", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestCompactNowDisabledManager(t *testing.T) {
	cfg := compactNowConfig()
	cfg.Enabled = false
	manager, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.CompactNow(context.Background(), "system", nil, nil); !errors.Is(err, ErrDisabled) {
		t.Fatalf("error = %v, want ErrDisabled", err)
	}
}

func TestCompactNowNilContextAndNoCandidates(t *testing.T) {
	manager, err := New(compactNowConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	big := ""
	for i := 0; i < 2000; i++ {
		big += "token "
	}
	longUser := []byte(`"` + strings.Repeat("x", 8000) + `"`)
	messages := []*agent.Message{
		{User: &agent.UserMessage{Role: "user", Content: longUser}},
		{User: &agent.UserMessage{Role: "user", Content: longUser}},
		{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: big}}}},
	}
	kept, entry, err := manager.CompactNow(nil, "system", messages, nil)
	if err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if entry == nil {
		t.Fatal("expected a compaction entry for an oversized context")
	}
	if len(kept) == 0 {
		t.Fatal("compaction removed every message")
	}
	if entry.TokensBefore <= 0 || entry.FirstKeptEntryID == "" {
		t.Fatalf("entry = %+v", entry)
	}
	short := []*agent.Message{{User: &agent.UserMessage{Role: "user", Content: []byte(`"hi"`)}}}
	kept, entry, err = manager.CompactNow(context.Background(), "system", short, nil)
	if err != nil {
		t.Fatalf("CompactNow(short): %v", err)
	}
	if entry != nil {
		t.Fatalf("short context compacted unexpectedly: %+v", entry)
	}
	if len(kept) != 1 {
		t.Fatalf("kept = %d messages, want the short history unchanged", len(kept))
	}
}
