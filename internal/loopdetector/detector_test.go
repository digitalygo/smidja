package loopdetector

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Fixture helpers

// thinkingTurn builds a turn whose only content is thinking text.
func thinkingTurn(index int, thinking string) Turn {
	return Turn{TurnIndex: index, ThinkingText: thinking}
}

// textTurn builds a turn whose only content is assistant text.
func textTurn(index int, text string) Turn {
	return Turn{TurnIndex: index, TextContent: text}
}

// callTurn builds a turn whose only content is tool calls.
func callTurn(index int, calls ...ToolCall) Turn {
	return Turn{TurnIndex: index, ToolCalls: calls}
}

// readCall builds a successful read tool call with a deterministic
// fingerprint derived from the path.
func readCall(path string) ToolCall {
	return ToolCall{
		Name:           "read",
		DisplaySummary: "read(" + path + ")",
		CallKey:        "read:" + path,
		ResultKey:      "res:" + path,
		HasResult:      true,
	}
}

// grepCall builds a grep tool call with the given display summary.
func grepCall(display string) ToolCall {
	return ToolCall{
		Name:           "grep",
		DisplaySummary: display,
		CallKey:        "grep:" + display,
		HasResult:      true,
		ResultKey:      "grepres:" + display,
	}
}

// failingSubagent builds a failing subagent call with the given result
// key, mirroring what ExtractTurn produces for a blocked subagent.
func failingSubagent(resultKey string) ToolCall {
	return ToolCall{
		Name:           "subagent",
		DisplaySummary: "subagent(dev#abc12345)",
		CallKey:        "subagent:call",
		ResultKey:      resultKey,
		IsError:        true,
		HasResult:      true,
	}
}

// natoWords is the NATO phonetic alphabet, all uppercase so the
// normalizeThinking short-word removal (which only strips lowercase
// words) leaves them intact.
var natoWords = strings.Fields("ALPHA BRAVO CHARLIE DELTA ECHO FOXTROT GOLF HOTEL INDIA JULIET KILO LIMA MIKE NOVEMBER OSCAR PAPA QUEBEC ROMEO SIERRA TANGO UNIFORM VICTOR WHISKEY XRAY YANKEE ZULU")

// wordList returns the first n NATO words joined by spaces.
func wordList(n int) string {
	return strings.Join(natoWords[:n], " ")
}

const stuckThinking = "Let me consider the approach and the tradeoffs before implementing."

// observeThinking observes n turns that all share the same thinking.
func observeThinking(d *Detector, start, n int) {
	for i := start; i < start+n; i++ {
		d.Observe(thinkingTurn(i, stuckThinking))
	}
}

// ---------------------------------------------------------------------------
// Config

func TestDefaultConfigMatchesSource(t *testing.T) {
	got := DefaultConfig()
	want := Config{
		WindowSize:                        10,
		ReasoningStuckThreshold:           4,
		ReasoningStuckThresholdSimilarity: 0.85,
		RepeatSequenceMinLength:           6,
		RepeatPatternMinReps:              3,
		SubagentNestingThreshold:          3,
		MessageRepeatThreshold:            3,
		MessageRepeatSimilarity:           0.85,
		MessageRepeatMinLength:            80,
		EscalateAfter:                     2,
		EnableReasoningDetection:          true,
		EnableToolRepetitionDetection:     true,
		EnableReadRepetitionDetection:     true,
		EnableSearchSpiralDetection:       true,
		EnableSubagentCycleDetection:      true,
		EnableMessageRepetitionDetection:  true,
	}
	if got != want {
		t.Errorf("DefaultConfig = %+v, want %+v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Reasoning stagnation

func TestReasoningStagnationDetects(t *testing.T) {
	d := New(DefaultConfig())
	observeThinking(d, 0, 3)
	out := d.Observe(thinkingTurn(3, stuckThinking))
	if out.Verdict != VerdictWarn {
		t.Fatalf("Verdict = %v, want %v (findings: %+v)", out.Verdict, VerdictWarn, out.Findings)
	}
	if len(out.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one", out.Findings)
	}
	f := out.Findings[0]
	if f.Type != FindingReasoningStagnation {
		t.Errorf("Type = %q, want %q", f.Type, FindingReasoningStagnation)
	}
	wantMsg := "Stuck reasoning: 4 consecutive turns with ≥85% word overlap in thinking (turns 0-3)."
	if f.Message != wantMsg {
		t.Errorf("Message = %q, want %q", f.Message, wantMsg)
	}
}

func TestReasoningStagnationBelowThreshold(t *testing.T) {
	d := New(DefaultConfig())
	observeThinking(d, 0, 3)
	if out := d.Observe(thinkingTurn(3, "Short.")); out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v (short thinking is not a stuck window)", out.Verdict, VerdictNone)
	}
}

func TestReasoningStagnationDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableReasoningDetection = false
	d := New(cfg)
	observeThinking(d, 0, 4)
	if out := d.Observe(thinkingTurn(4, stuckThinking)); out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v with reasoning detection disabled", out.Verdict, VerdictNone)
	}
}

func TestReasoningSimilarityBoundary(t *testing.T) {
	// Jaccard 0.85 = 17/20: a 20-word set against its 17-word subset
	// must be stuck (>= threshold).
	at85 := []Turn{
		thinkingTurn(0, wordList(20)),
		thinkingTurn(1, wordList(17)),
		thinkingTurn(2, wordList(17)),
		thinkingTurn(3, wordList(17)),
	}
	d := New(DefaultConfig())
	for _, tr := range at85[:3] {
		d.Observe(tr)
	}
	if out := d.Observe(at85[3]); out.Verdict != VerdictWarn {
		t.Fatalf("at 0.85: Verdict = %v, want %v (findings: %+v)", out.Verdict, VerdictWarn, out.Findings)
	}

	// Jaccard 0.84 = 21/25: a 25-word set against its 21-word subset
	// must NOT be stuck (< threshold on the first adjacent pair).
	d2 := New(DefaultConfig())
	below := []Turn{
		thinkingTurn(0, wordList(25)),
		thinkingTurn(1, wordList(21)),
		thinkingTurn(2, wordList(21)),
		thinkingTurn(3, wordList(21)),
	}
	for _, tr := range below[:3] {
		d2.Observe(tr)
	}
	if out := d2.Observe(below[3]); out.Verdict != VerdictNone {
		t.Fatalf("at 0.84: Verdict = %v, want %v (findings: %+v)", out.Verdict, VerdictNone, out.Findings)
	}
}

func TestSimilarityDirect(t *testing.T) {
	// Exactly 0.85: 17 shared of (20, 17).
	a := wordList(20)
	b := wordList(17)
	if got := similarity(normalizeThinking(a), normalizeThinking(b)); got != 0.85 {
		t.Errorf("similarity = %v, want 0.85", got)
	}
	// Below 0.85: 21 shared of (25, 21).
	a2 := wordList(25)
	b2 := wordList(21)
	if got := similarity(normalizeThinking(a2), normalizeThinking(b2)); got >= 0.85 {
		t.Errorf("similarity = %v, want < 0.85", got)
	}
	// Identical and disjoint.
	if got := similarity("alpha bravo", "alpha bravo"); got != 1 {
		t.Errorf("identical similarity = %v, want 1", got)
	}
	if got := similarity("alpha bravo", "charlie delta"); got != 0 {
		t.Errorf("disjoint similarity = %v, want 0", got)
	}
	// Empty sets score 0.
	if got := similarity("", "alpha"); got != 0 {
		t.Errorf("empty similarity = %v, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Window eviction

func TestWindowEviction(t *testing.T) {
	// A window of 3 can never hold the 4 consecutive thinking turns the
	// reasoning detector needs, so the oldest turn must have been
	// evicted.
	cfg := DefaultConfig()
	cfg.WindowSize = 3
	d := New(cfg)
	observeThinking(d, 0, 4)
	if out := d.Observe(thinkingTurn(4, stuckThinking)); out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v (window 3 evicts before the threshold is met)", out.Verdict, VerdictNone)
	}

	// With the default window of 10 the same input detects.
	d2 := New(DefaultConfig())
	observeThinking(d2, 0, 3)
	if out := d2.Observe(thinkingTurn(3, stuckThinking)); out.Verdict != VerdictWarn {
		t.Fatalf("Verdict = %v, want %v with the default window", out.Verdict, VerdictWarn)
	}
}

// ---------------------------------------------------------------------------
// Message repetition

func TestMessageRepetitionDetects(t *testing.T) {
	d := New(DefaultConfig())
	base := "We need to refactor the session loader to use streaming writes and atomic file replacement with a lock file for crash safety."
	similar := "We need to refactor the session loader to use streaming writes and atomic file replacement with a lock file for crash safety, please."
	unrelated := "The weather in Milan today is sunny with some clouds and a light breeze in the afternoon hours, ideal for a walk."
	if len(base) < 80 || len(similar) < 80 || len(unrelated) < 80 {
		t.Fatal("fixture messages must clear the 80-rune length floor")
	}
	d.Observe(textTurn(0, base))
	d.Observe(textTurn(1, similar))
	d.Observe(textTurn(2, unrelated))
	out := d.Observe(textTurn(3, base))
	if out.Verdict != VerdictWarn {
		t.Fatalf("Verdict = %v, want %v (findings: %+v)", out.Verdict, VerdictWarn, out.Findings)
	}
	if len(out.Findings) != 1 || out.Findings[0].Type != FindingMessageRepetition {
		t.Fatalf("findings = %+v, want exactly one message-repetition", out.Findings)
	}
	wantMsg := "Message repetition: 3 of the last 4 assistant messages are ≥85% similar to the most recent one (turn 3)."
	if got := out.Findings[0].Message; got != wantMsg {
		t.Errorf("Message = %q, want %q", got, wantMsg)
	}
}

func TestMessageRepetitionBelowThreshold(t *testing.T) {
	d := New(DefaultConfig())
	base := "We need to refactor the session loader to use streaming writes and atomic file replacement with a lock file for crash safety."
	unrelated := "The weather in Milan today is sunny with some clouds and a light breeze in the afternoon hours, ideal for a walk."
	d.Observe(textTurn(0, base))
	d.Observe(textTurn(1, unrelated))
	out := d.Observe(textTurn(2, base))
	if out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v (only 2 of 3 similar)", out.Verdict, VerdictNone)
	}
}

func TestMessageRepetitionLengthFloor(t *testing.T) {
	d := New(DefaultConfig())
	short := "short message"
	if len(short) >= 80 {
		t.Fatal("fixture must sit below the 80-rune floor")
	}
	for i := 0; i < 3; i++ {
		if out := d.Observe(textTurn(i, short)); out.Verdict != VerdictNone {
			t.Fatalf("Verdict = %v, want %v (short messages never count)", out.Verdict, VerdictNone)
		}
	}
}

// ---------------------------------------------------------------------------
// Tool repetition

func TestToolRepetitionDetects(t *testing.T) {
	d := New(DefaultConfig())
	d.Observe(callTurn(0, readCall("/a"), readCall("/b")))
	d.Observe(callTurn(1, readCall("/a"), readCall("/b")))
	out := d.Observe(callTurn(2, readCall("/a"), readCall("/b")))
	if out.Verdict != VerdictWarn {
		t.Fatalf("Verdict = %v, want %v (findings: %+v)", out.Verdict, VerdictWarn, out.Findings)
	}
	if len(out.Findings) != 1 || out.Findings[0].Type != FindingToolRepetition {
		t.Fatalf("findings = %+v, want exactly one tool-repetition", out.Findings)
	}
	wantMsg := `Tool call repetition: "read(/a) → read(/b)" repeated 3 times in a row with identical output.`
	if got := out.Findings[0].Message; got != wantMsg {
		t.Errorf("Message = %q, want %q", got, wantMsg)
	}
}

func TestToolRepetitionBelowMinReps(t *testing.T) {
	// Six calls total, but the third repetition differs, so reps stay at
	// 2 and no 2- or 3-call pattern reaches the threshold.
	d := New(DefaultConfig())
	calls := []ToolCall{readCall("/a"), readCall("/b"), readCall("/a"), readCall("/b"), readCall("/a"), readCall("/c")}
	d.Observe(callTurn(0, calls[0], calls[1]))
	d.Observe(callTurn(1, calls[2], calls[3]))
	if out := d.Observe(callTurn(2, calls[4], calls[5])); out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v (2 reps below the 3-rep threshold)", out.Verdict, VerdictNone)
	}
}

func TestToolRepetitionBelowSequenceMinLength(t *testing.T) {
	// Two repetitions of a 2-call pattern: only 4 calls, below the
	// 6-call sequence floor.
	d := New(DefaultConfig())
	d.Observe(callTurn(0, readCall("/a"), readCall("/b")))
	if out := d.Observe(callTurn(1, readCall("/a"), readCall("/b"))); out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v (sequence below min length)", out.Verdict, VerdictNone)
	}
}

func TestToolRepetitionThreeCallPattern(t *testing.T) {
	// A 3-call pattern repeated 3 times; the 2-call loop must not fire
	// first because every adjacent 2-pattern differs.
	d := New(DefaultConfig())
	seq := []ToolCall{readCall("/a"), readCall("/b"), readCall("/c")}
	d.Observe(callTurn(0, seq[0], seq[1], seq[2]))
	d.Observe(callTurn(1, seq[0], seq[1], seq[2]))
	out := d.Observe(callTurn(2, seq[0], seq[1], seq[2]))
	if out.Verdict != VerdictWarn {
		t.Fatalf("Verdict = %v, want %v (findings: %+v)", out.Verdict, VerdictWarn, out.Findings)
	}
	if len(out.Findings) != 1 || out.Findings[0].Type != FindingToolRepetition {
		t.Fatalf("findings = %+v, want exactly one tool-repetition", out.Findings)
	}
	wantMsg := `Tool call repetition: "read(/a) → read(/b) → read(/c)" repeated 3 times in a row with identical output.`
	if got := out.Findings[0].Message; got != wantMsg {
		t.Errorf("Message = %q, want %q", got, wantMsg)
	}
}

func TestToolRepetitionSkipsCallsWithoutResults(t *testing.T) {
	// Pattern calls without results are skipped: three repetitions where
	// every call lacks a result must not fire.
	noResult := func(path string) ToolCall {
		c := readCall(path)
		c.HasResult = false
		c.ResultKey = ""
		return c
	}
	d := New(DefaultConfig())
	d.Observe(callTurn(0, noResult("/a"), noResult("/b")))
	d.Observe(callTurn(1, noResult("/a"), noResult("/b")))
	if out := d.Observe(callTurn(2, noResult("/a"), noResult("/b"))); out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v (result-less calls are skipped)", out.Verdict, VerdictNone)
	}
}

// ---------------------------------------------------------------------------
// Read repetition

func TestReadRepetitionDetects(t *testing.T) {
	d := New(DefaultConfig())
	for i := 0; i < 3; i++ {
		d.Observe(callTurn(i, readCall("/etc/hosts")))
	}
	out := d.Observe(callTurn(3, readCall("/etc/hosts")))
	if out.Verdict != VerdictWarn {
		t.Fatalf("Verdict = %v, want %v (findings: %+v)", out.Verdict, VerdictWarn, out.Findings)
	}
	if len(out.Findings) != 1 || out.Findings[0].Type != FindingReadRepetition {
		t.Fatalf("findings = %+v, want exactly one read-repetition", out.Findings)
	}
	wantMsg := `Read repetition: "hosts" read 4 times across the window.`
	if got := out.Findings[0].Message; got != wantMsg {
		t.Errorf("Message = %q, want %q", got, wantMsg)
	}
}

func TestReadRepetitionBelowThreshold(t *testing.T) {
	d := New(DefaultConfig())
	for i := 0; i < 3; i++ {
		if out := d.Observe(callTurn(i, readCall("/etc/hosts"))); out.Verdict != VerdictNone {
			t.Fatalf("Verdict = %v, want %v (3 reads below the 4-read threshold)", out.Verdict, VerdictNone)
		}
	}
}

// ---------------------------------------------------------------------------
// Search spiral

func TestSearchSpiralSamePatternAcrossPaths(t *testing.T) {
	d := New(DefaultConfig())
	// Host-provided display summaries with per-path tails (see the
	// detector doc note about the comma-dependent path extraction).
	d.Observe(callTurn(0, grepCall(`search_files("TODO", /src/a)`)))
	d.Observe(callTurn(1, grepCall(`search_files("TODO", /src/b)`)))
	out := d.Observe(callTurn(2, grepCall(`search_files("TODO", /src/c)`)))
	if out.Verdict != VerdictWarn {
		t.Fatalf("Verdict = %v, want %v (findings: %+v)", out.Verdict, VerdictWarn, out.Findings)
	}
	if len(out.Findings) != 1 || out.Findings[0].Type != FindingSearchSpiral {
		t.Fatalf("findings = %+v, want exactly one search-spiral", out.Findings)
	}
	wantMsg := `Search spiral: pattern "todo" searched across 3 different paths.`
	if got := out.Findings[0].Message; got != wantMsg {
		t.Errorf("Message = %q, want %q", got, wantMsg)
	}
}

func TestSearchSpiralExpandingPatterns(t *testing.T) {
	d := New(DefaultConfig())
	d.Observe(callTurn(0, grepCall(`grep("abcd")`)))
	d.Observe(callTurn(1, grepCall(`grep("abcdef")`)))
	out := d.Observe(callTurn(2, grepCall(`grep("abcdefgh")`)))
	if out.Verdict != VerdictWarn {
		t.Fatalf("Verdict = %v, want %v (findings: %+v)", out.Verdict, VerdictWarn, out.Findings)
	}
	wantMsg := `Search spiral: patterns expanding "abcd" → "abcdef" → "abcdefgh".`
	if got := out.Findings[0].Message; got != wantMsg {
		t.Errorf("Message = %q, want %q", got, wantMsg)
	}
}

func TestSearchSpiralBelowSearchCount(t *testing.T) {
	d := New(DefaultConfig())
	d.Observe(callTurn(0, grepCall(`grep("abcd")`)))
	if out := d.Observe(callTurn(1, grepCall(`grep("abcdef")`))); out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v (2 searches below the 3-search floor)", out.Verdict, VerdictNone)
	}
}

func TestSearchSpiralNotExpanding(t *testing.T) {
	d := New(DefaultConfig())
	d.Observe(callTurn(0, grepCall(`grep("abcdefgh")`)))
	d.Observe(callTurn(1, grepCall(`grep("abcd")`)))
	if out := d.Observe(callTurn(2, grepCall(`grep("ab")`))); out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v (patterns shrink, not expand)", out.Verdict, VerdictNone)
	}
}

// ---------------------------------------------------------------------------
// Subagent cycle

func TestSubagentCycleDetects(t *testing.T) {
	d := New(DefaultConfig())
	d.Observe(callTurn(0, failingSubagent("res1")))
	d.Observe(callTurn(1, failingSubagent("res1")))
	out := d.Observe(callTurn(2, failingSubagent("res1")))
	if out.Verdict != VerdictWarn {
		t.Fatalf("Verdict = %v, want %v (findings: %+v)", out.Verdict, VerdictWarn, out.Findings)
	}
	if len(out.Findings) != 1 || out.Findings[0].Type != FindingSubagentCycle {
		t.Fatalf("findings = %+v, want exactly one subagent-cycle", out.Findings)
	}
	wantMsg := "Nested subagent loop: 3 blocked subagent attempts with identical error output."
	if got := out.Findings[0].Message; got != wantMsg {
		t.Errorf("Message = %q, want %q", got, wantMsg)
	}
}

func TestSubagentCycleBelowThreshold(t *testing.T) {
	d := New(DefaultConfig())
	d.Observe(callTurn(0, failingSubagent("res1")))
	if out := d.Observe(callTurn(1, failingSubagent("res1"))); out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v (2 identical failures below the 3 threshold)", out.Verdict, VerdictNone)
	}
}

func TestSubagentCycleBreaksOnDifferentResults(t *testing.T) {
	d := New(DefaultConfig())
	d.Observe(callTurn(0, failingSubagent("res1")))
	d.Observe(callTurn(1, failingSubagent("res2")))
	if out := d.Observe(callTurn(2, failingSubagent("res1"))); out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v (trailing run is 1, not 3)", out.Verdict, VerdictNone)
	}
}

func TestSubagentCycleSkipsNonErrors(t *testing.T) {
	d := New(DefaultConfig())
	okCall := failingSubagent("res1")
	okCall.IsError = false
	d.Observe(callTurn(0, okCall))
	d.Observe(callTurn(1, okCall))
	if out := d.Observe(callTurn(2, okCall)); out.Verdict != VerdictNone {
		t.Fatalf("Verdict = %v, want %v (successful subagent calls are not cycles)", out.Verdict, VerdictNone)
	}
}

// ---------------------------------------------------------------------------
// Escalation and state machine
//
// The extension clears the window after every detection and resets the
// consecutive counter on any turn without findings, so escalation is only
// reachable for detectors whose evidence fits in a single turn. These tests
// use the tool-repetition detector: one turn carrying six calls (a 2-call
// pattern repeated three times) is enough to detect.

// detectTurn observes one turn with a repeated 2-call pattern and returns
// the outcome.
func detectTurn(d *Detector, index int) Outcome {
	return d.Observe(callTurn(index, readCall("/a"), readCall("/b"), readCall("/a"), readCall("/b"), readCall("/a"), readCall("/b")))
}

func TestEscalationWarnThenBlockThenWarn(t *testing.T) {
	d := New(DefaultConfig())
	if out := detectTurn(d, 0); out.Verdict != VerdictWarn {
		t.Fatalf("first detection Verdict = %v, want %v", out.Verdict, VerdictWarn)
	}
	// The next detection turn finds the counter at 1, so 1+1 >= 2
	// escalates to block.
	if out := detectTurn(d, 1); out.Verdict != VerdictBlock {
		t.Fatalf("second detection Verdict = %v, want %v", out.Verdict, VerdictBlock)
	}
	// Block resets the counter: the next detection warns again.
	if out := detectTurn(d, 2); out.Verdict != VerdictWarn {
		t.Fatalf("third detection Verdict = %v, want %v", out.Verdict, VerdictWarn)
	}
}

func TestEscalationCleanTurnResets(t *testing.T) {
	d := New(DefaultConfig())
	if out := detectTurn(d, 0); out.Verdict != VerdictWarn {
		t.Fatalf("first detection Verdict = %v, want %v", out.Verdict, VerdictWarn)
	}
	// A clean turn (content but no findings) resets the counter, so the
	// next detection warns instead of blocking.
	d.Observe(callTurn(1, readCall("/x"), readCall("/y"), readCall("/z"), readCall("/w"), readCall("/v")))
	if out := detectTurn(d, 2); out.Verdict != VerdictWarn {
		t.Fatalf("detection after clean turn Verdict = %v, want %v (counter was reset)", out.Verdict, VerdictWarn)
	}
}

func TestEmptyTurnDoesNotResetCounter(t *testing.T) {
	d := New(DefaultConfig())
	if out := detectTurn(d, 0); out.Verdict != VerdictWarn {
		t.Fatalf("first detection Verdict = %v, want %v", out.Verdict, VerdictWarn)
	}
	// An empty turn is skipped entirely, mirroring the extension's early
	// return: the counter stays at 1, so the next detection blocks.
	if out := d.Observe(Turn{}); out.Verdict != VerdictNone {
		t.Fatalf("empty turn Verdict = %v, want %v", out.Verdict, VerdictNone)
	}
	if out := detectTurn(d, 1); out.Verdict != VerdictBlock {
		t.Fatalf("detection after empty turn Verdict = %v, want %v (counter was not reset)", out.Verdict, VerdictBlock)
	}
}

func TestEscalationLadderWithEscalateAfterThree(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EscalateAfter = 3
	d := New(cfg)
	// Detection 1: warn.
	if out := detectTurn(d, 0); out.Verdict != VerdictWarn {
		t.Fatalf("first detection Verdict = %v, want %v", out.Verdict, VerdictWarn)
	}
	// Detection 2: silent middle step (findings reported, no verdict).
	out2 := detectTurn(d, 1)
	if out2.Verdict != VerdictNone || len(out2.Findings) == 0 {
		t.Fatalf("second detection = %+v, want VerdictNone with findings", out2)
	}
	// Detection 3: block.
	if out := detectTurn(d, 2); out.Verdict != VerdictBlock {
		t.Fatalf("third detection Verdict = %v, want %v", out.Verdict, VerdictBlock)
	}
}

func TestMultiTurnEvidenceResetsCounter(t *testing.T) {
	// Detectors that need several turns of evidence cannot escalate: the
	// intermediate evidence turns find nothing and reset the counter,
	// exactly as in the extension. After a reasoning-stuck warning, three
	// more thinking turns each reset, so the fourth turn warns again.
	d := New(DefaultConfig())
	observeThinking(d, 0, 3)
	if out := d.Observe(thinkingTurn(3, stuckThinking)); out.Verdict != VerdictWarn {
		t.Fatalf("first detection Verdict = %v, want %v", out.Verdict, VerdictWarn)
	}
	observeThinking(d, 4, 3)
	if out := d.Observe(thinkingTurn(7, stuckThinking)); out.Verdict != VerdictWarn {
		t.Fatalf("detection after a fresh evidence batch Verdict = %v, want %v (counter was reset by the partial turns)", out.Verdict, VerdictWarn)
	}
}

func TestResetClearsState(t *testing.T) {
	d := New(DefaultConfig())
	observeThinking(d, 0, 3)
	if out := d.Observe(thinkingTurn(3, stuckThinking)); out.Verdict != VerdictWarn {
		t.Fatalf("first detection Verdict = %v, want %v", out.Verdict, VerdictWarn)
	}
	d.Reset()
	observeThinking(d, 4, 3)
	if out := d.Observe(thinkingTurn(7, stuckThinking)); out.Verdict != VerdictWarn {
		t.Fatalf("detection after Reset Verdict = %v, want %v (counter and window cleared)", out.Verdict, VerdictWarn)
	}
}

// ---------------------------------------------------------------------------
// Steer messages

func TestSteerMessageWarning(t *testing.T) {
	out := Outcome{
		Verdict: VerdictWarn,
		Findings: []Finding{
			{Type: FindingReasoningStagnation, Message: "Stuck reasoning: 4 consecutive turns with ≥85% word overlap in thinking (turns 0-3)."},
			{Type: FindingToolRepetition, Message: `Tool call repetition: "read(/a) → read(/b)" repeated 3 times in a row with identical output.`},
		},
	}
	ct, text := out.SteerMessage()
	if ct != SteerTypeWarning {
		t.Errorf("customType = %q, want %q", ct, SteerTypeWarning)
	}
	want := "**Loop detected**\n\n" +
		"- reasoning-stagnation: Stuck reasoning: 4 consecutive turns with ≥85% word overlap in thinking (turns 0-3).\n" +
		"- tool-repetition: Tool call repetition: \"read(/a) → read(/b)\" repeated 3 times in a row with identical output.\n\n" +
		"It looks like you are in a loop. Stop repeating the same approach.\nSummarize your current state, close any open work, and complete your session properly."
	if text != want {
		t.Errorf("text = %q\nwant %q", text, want)
	}
}

func TestSteerMessageBlock(t *testing.T) {
	out := Outcome{Verdict: VerdictBlock, Findings: []Finding{{Type: FindingReadRepetition, Message: `Read repetition: "hosts" read 4 times across the window.`}}}
	ct, text := out.SteerMessage()
	if ct != SteerTypeForceStop {
		t.Errorf("customType = %q, want %q", ct, SteerTypeForceStop)
	}
	want := "**Loop persists**\n\nThe loop continued after the previous warning. This run is being force-stopped.\nStop all activity now and close/complete your session."
	if text != want {
		t.Errorf("text = %q\nwant %q", text, want)
	}
}

func TestSteerMessageNone(t *testing.T) {
	if ct, text := (Outcome{}).SteerMessage(); ct != "" || text != "" {
		t.Errorf("SteerMessage for VerdictNone = (%q, %q), want empty", ct, text)
	}
}

func TestVerdictString(t *testing.T) {
	tests := map[Verdict]string{
		VerdictNone:  "none",
		VerdictWarn:  "warning",
		VerdictBlock: "force-stop",
	}
	for v, want := range tests {
		if got := v.String(); got != want {
			t.Errorf("Verdict(%d).String() = %q, want %q", int(v), got, want)
		}
	}
}
