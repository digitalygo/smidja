package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

// stubClient is a programmable agent.Client for selector tests.
type stubClient struct {
	answers   []string
	errs      []error
	turns     []*agent.TurnRequest
	nonNilCbs int
}

func (c *stubClient) StreamTurn(_ context.Context, req *agent.TurnRequest, onText, onThinking func(string)) (*agent.AssistantMessage, error) {
	c.turns = append(c.turns, req)
	if onText != nil || onThinking != nil {
		c.nonNilCbs++
	}
	if len(c.errs) > 0 {
		err := c.errs[0]
		c.errs = c.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	answer := ""
	if len(c.answers) > 0 {
		answer = c.answers[0]
		c.answers = c.answers[1:]
	}
	return &agent.AssistantMessage{
		Role:       string(agent.RoleAssistant),
		Content:    []agent.ContentBlock{{Type: agent.BlockTypeText, Text: answer}},
		StopReason: "stop",
		Timestamp:  1,
	}, nil
}

func cand(ref string, text string) Candidate {
	content, _ := json.Marshal(text)
	return Candidate{Ref: ref, Message: &agent.Message{User: &agent.UserMessage{
		Role: string(agent.RoleUser), Content: content, Timestamp: 1,
	}}}
}

// singleChunk builds a one-chunk request over refs r1..r3 with a given
// budget.
func singleChunk(budget int64) SelectionRequest {
	cs := []Candidate{cand("r1", "alpha"), cand("r2", "beta"), cand("r3", "gamma")}
	return SelectionRequest{
		Model:        "model-x",
		Candidates:   cs,
		Chunks:       [][]Candidate{cs},
		BudgetTokens: budget,
		WindowTokens: 200_000,
	}
}

func TestSelectValid(t *testing.T) {
	req := singleChunk(100)
	client := &stubClient{answers: []string{`{"kept":["r1","r3"]}`}}
	sel := NewOpenRouterSelector(client)
	got, err := sel.Select(context.Background(), req)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(got.KeptIDs) != 2 || got.KeptIDs[0] != "r1" || got.KeptIDs[1] != "r3" {
		t.Fatalf("KeptIDs = %v, want [r1 r3]", got.KeptIDs)
	}
	if len(client.turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(client.turns))
	}
	tr := client.turns[0]
	if tr.Tools != nil {
		t.Fatalf("selector must not pass tools, got %v", tr.Tools)
	}
	if tr.System != selectionSystemPrompt {
		t.Fatalf("system prompt mismatch")
	}
	if client.nonNilCbs != 0 {
		t.Fatalf("streaming callbacks must be nil, got %d non-nil", client.nonNilCbs)
	}
	if len(tr.Messages) != 1 || tr.Messages[0].User == nil {
		t.Fatalf("expected one user message")
	}
	text := userContentText(tr.Messages[0].User.Content)
	for _, want := range []string{"[ref r1]", "[ref r2]", "[ref r3]", "200000", "100"} {
		if !strings.Contains(text, want) {
			t.Fatalf("user message missing %q: %q", want, text)
		}
	}
}

func TestSelectFencedJSON(t *testing.T) {
	req := singleChunk(100)
	client := &stubClient{answers: []string{"```json\n{\"kept\":[\"r2\"]}\n```"}}
	got, err := NewOpenRouterSelector(client).Select(context.Background(), req)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(got.KeptIDs) != 1 || got.KeptIDs[0] != "r2" {
		t.Fatalf("KeptIDs = %v, want [r2]", got.KeptIDs)
	}
}

func TestSelectInvalidJSON(t *testing.T) {
	req := singleChunk(100)
	client := &stubClient{answers: []string{"sure, here is the list: [1, 2, 3]"}}
	_, err := NewOpenRouterSelector(client).Select(context.Background(), req)
	if err == nil {
		t.Fatalf("invalid JSON: nil error, want ErrInvalidSelection")
	}
	if !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("error = %v, want ErrInvalidSelection", err)
	}
}

func TestSelectValidationErrors(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		budget int64
	}{
		{"unknown ref", `{"kept":["nope"]}`, 100},
		{"duplicate ref", `{"kept":["r1","r1"]}`, 100},
		{"empty list", `{"kept":[]}`, 100},
		{"null kept", `{"kept":null}`, 100},
		{"no kept field", `{}`, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := singleChunk(tc.budget)
			client := &stubClient{answers: []string{tc.answer}}
			_, err := NewOpenRouterSelector(client).Select(context.Background(), req)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, ErrInvalidSelection) {
				t.Fatalf("error = %v, want ErrInvalidSelection", err)
			}
		})
	}
}

func TestSelectOverBudget(t *testing.T) {
	// A single candidate costs more than the budget: keeping r1 exceeds
	// it and must be rejected.
	big := cand("r1", strings.Repeat("x", 3000))
	cost := estimateTokens(big.Message)
	req := SelectionRequest{
		Model:        "model-x",
		Candidates:   []Candidate{big},
		Chunks:       [][]Candidate{{big}},
		BudgetTokens: cost - 1,
		WindowTokens: 200_000,
	}
	client := &stubClient{answers: []string{`{"kept":["r1"]}`}}
	_, err := NewOpenRouterSelector(client).Select(context.Background(), req)
	if err == nil {
		t.Fatalf("over-budget kept set: nil error")
	}
	if !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("error = %v, want ErrInvalidSelection", err)
	}
}

func TestSelectMultipleChunks(t *testing.T) {
	c1 := []Candidate{cand("r1", "one"), cand("r2", "two")}
	c2 := []Candidate{cand("r3", "three"), cand("r4", "four")}
	req := SelectionRequest{
		Model:        "model-x",
		Candidates:   append(append([]Candidate{}, c1...), c2...),
		Chunks:       [][]Candidate{c1, c2},
		BudgetTokens: 200,
		WindowTokens: 200_000,
	}
	client := &stubClient{answers: []string{`{"kept":["r2"]}`, `{"kept":["r4"]}`}}
	got, err := NewOpenRouterSelector(client).Select(context.Background(), req)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(got.KeptIDs) != 2 || got.KeptIDs[0] != "r2" || got.KeptIDs[1] != "r4" {
		t.Fatalf("KeptIDs = %v, want [r2 r4]", got.KeptIDs)
	}
	if len(client.turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(client.turns))
	}
}

func TestSelectNoChunksUsesAllCandidates(t *testing.T) {
	cs := []Candidate{cand("r1", "one"), cand("r2", "two")}
	req := SelectionRequest{
		Model:        "model-x",
		Candidates:   cs,
		BudgetTokens: 100,
		WindowTokens: 200_000,
	}
	client := &stubClient{answers: []string{`{"kept":["r1"]}`}}
	got, err := NewOpenRouterSelector(client).Select(context.Background(), req)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(got.KeptIDs) != 1 || got.KeptIDs[0] != "r1" {
		t.Fatalf("KeptIDs = %v, want [r1]", got.KeptIDs)
	}
	if len(client.turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(client.turns))
	}
}

func TestSelectRetriesTransientError(t *testing.T) {
	req := singleChunk(100)
	client := &stubClient{
		errs:    []error{errors.New("transport failed")},
		answers: []string{`{"kept":["r1"]}`},
	}
	got, err := NewOpenRouterSelector(client).Select(context.Background(), req)
	if err != nil {
		t.Fatalf("Select after retry: %v", err)
	}
	if len(got.KeptIDs) != 1 {
		t.Fatalf("KeptIDs = %v", got.KeptIDs)
	}
	if len(client.turns) != 2 {
		t.Fatalf("turns = %d, want 2 (one retry)", len(client.turns))
	}
}

func TestSelectAllRetriesFail(t *testing.T) {
	req := singleChunk(100)
	client := &stubClient{
		errs: []error{errors.New("e1"), errors.New("e2"), errors.New("e3")},
	}
	_, err := NewOpenRouterSelector(client).Select(context.Background(), req)
	if err == nil {
		t.Fatalf("all retries failed: nil error")
	}
	if len(client.turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(client.turns))
	}
}

func TestSelectRequestValidation(t *testing.T) {
	ok := singleChunk(100)
	cases := []struct {
		name string
		fn   func() SelectionRequest
	}{
		{"empty model", func() SelectionRequest { r := ok; r.Model = ""; return r }},
		{"no candidates", func() SelectionRequest { r := ok; r.Candidates = nil; return r }},
		{"zero budget", func() SelectionRequest { r := ok; r.BudgetTokens = 0; return r }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &stubClient{answers: []string{`{"kept":["r1"]}`}}
			if _, err := NewOpenRouterSelector(client).Select(context.Background(), tc.fn()); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestSelectNilClient(t *testing.T) {
	_, err := NewOpenRouterSelector(nil).Select(context.Background(), singleChunk(100))
	if err == nil {
		t.Fatalf("nil client: nil error")
	}
}

func TestSelectCancelledContext(t *testing.T) {
	req := singleChunk(100)
	client := &stubClient{errs: []error{errors.New("would fail anyway")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewOpenRouterSelector(client).Select(ctx, req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if len(client.turns) != 0 {
		t.Fatalf("no turn must run on a cancelled context, got %d", len(client.turns))
	}
}
