// Package loopdetector implements smidja's core loop detector: a faithful
// Go port of the user's loop-detector Pi extension (index.ts), restructured
// as a library. It keeps the extension's detection semantics and defaults
// exactly and re-exposes the escalation state machine so hosts can craft
// the same warning and force-stop steer messages the extension sends.
//
// The host feeds completed turns (built with ExtractTurn) into a Detector
// with Observe, which returns the combined verdict plus every detector's
// findings. The escalation ladder mirrors the extension: the first turn
// with findings produces a warning, consecutive detection turns escalate
// to a force-stop verdict, and a clean turn resets the counter.
//
// Adaptations from the extension:
//   - The extension runs its subagent-cycle detector only in subagent
//     child processes (PI_SUBAGENT_CHILD=1); smidja has no process
//     boundary today, so the detector runs wherever the host feeds it
//     turns. Hosts that want the extension's process-scoped behavior
//     should enable EnableSubagentCycleDetection only when observing
//     nested contexts.
//   - smidja tool results carry text blocks only, so the fingerprinting
//     of image result blocks from the extension has no equivalent (see
//     resultFingerprint).
//   - The extension's command and config UI is replaced by the exported
//     Config and DefaultConfig; the package never sends messages on its
//     own, it only reports verdicts and findings.
package loopdetector

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
)

// Config carries the detection policy, ported field-for-field from the
// extension's Config and DEFAULT_CONFIG. Build a configuration from
// DefaultConfig() and override the fields you need; New uses the
// configuration as given, so the zero Config disables every detector.
type Config struct {
	// WindowSize is how many most recent turns the detectors examine.
	// Default 10.
	WindowSize int

	// ReasoningStuckThreshold is how many consecutive similar-thinking
	// turns count as stuck reasoning. Default 4.
	ReasoningStuckThreshold int

	// ReasoningStuckThresholdSimilarity is the minimum Jaccard similarity
	// between adjacent normalized thinkings in a stuck window. Default
	// 0.85.
	ReasoningStuckThresholdSimilarity float64

	// RepeatSequenceMinLength is the minimum total tool calls across the
	// window before sequence repetition is examined. Default 6.
	RepeatSequenceMinLength int

	// RepeatPatternMinReps is the minimum consecutive repetitions of a
	// 2- or 3-call pattern with identical output. Default 3.
	RepeatPatternMinReps int

	// SubagentNestingThreshold is how many trailing identical failing
	// subagent calls count as a cycle. Default 3.
	SubagentNestingThreshold int

	// MessageRepeatThreshold is how many similar assistant messages count
	// as repetition. Default 3.
	MessageRepeatThreshold int

	// MessageRepeatSimilarity is the minimum Jaccard similarity for
	// message repetition. Default 0.85.
	MessageRepeatSimilarity float64

	// MessageRepeatMinLength is the minimum message length in runes for a
	// message to count. Default 80.
	MessageRepeatMinLength int

	// EscalateAfter is how many consecutive detection turns until the
	// verdict escalates to block. Default 2: the second consecutive
	// detection force-stops, mirroring the extension.
	EscalateAfter int

	// EnableReasoningDetection turns reasoning-stuck detection on.
	// Default true.
	EnableReasoningDetection bool

	// EnableToolRepetitionDetection turns repeated tool-call sequence
	// detection on. Default true.
	EnableToolRepetitionDetection bool

	// EnableReadRepetitionDetection turns repeated read detection on.
	// Default true.
	EnableReadRepetitionDetection bool

	// EnableSearchSpiralDetection turns search-spiral detection on.
	// Default true.
	EnableSearchSpiralDetection bool

	// EnableSubagentCycleDetection turns subagent cycle detection on.
	// Default true.
	EnableSubagentCycleDetection bool

	// EnableMessageRepetitionDetection turns message repetition detection
	// on. Default true.
	EnableMessageRepetitionDetection bool
}

// DefaultConfig returns the detection configuration ported verbatim from
// the extension's DEFAULT_CONFIG.
func DefaultConfig() Config {
	return Config{
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
}

// Finding types, matching the extension's detection result types.
const (
	// FindingSubagentCycle is a run of identical failing subagent calls.
	FindingSubagentCycle = "subagent-cycle"

	// FindingMessageRepetition is repeated near-identical assistant text.
	FindingMessageRepetition = "message-repetition"

	// FindingReasoningStagnation is consecutive similar thinking turns.
	FindingReasoningStagnation = "reasoning-stagnation"

	// FindingToolRepetition is a repeated tool-call sequence with
	// identical output.
	FindingToolRepetition = "tool-repetition"

	// FindingReadRepetition is the same file read repeatedly.
	FindingReadRepetition = "read-repetition"

	// FindingSearchSpiral is the same search pattern across paths or
	// expanding search patterns.
	FindingSearchSpiral = "search-spiral"
)

// Finding is one detector's finding: its type and the human-readable
// message the extension attaches, ported verbatim.
type Finding struct {
	// Type is one of the Finding* constants.
	Type string

	// Message is the detection message text.
	Message string
}

// Verdict is the combined loop-detector verdict for one observed turn,
// mirroring the extension's escalation states: none (no detection, or the
// silent middle step of a longer ladder), warn (first detection), or
// block (force-stop after consecutive detections).
type Verdict int

// Verdict values.
const (
	// VerdictNone means no detection, or the silent step of the
	// escalation ladder between warn and block.
	VerdictNone Verdict = iota

	// VerdictWarn is the first detection: the extension sends its
	// loop-detector-warning steer message.
	VerdictWarn

	// VerdictBlock is the escalated force-stop: the extension aborts the
	// run and sends its loop-detector-force-stop steer message.
	VerdictBlock
)

// String returns the verdict's name.
func (v Verdict) String() string {
	switch v {
	case VerdictWarn:
		return "warning"
	case VerdictBlock:
		return "force-stop"
	default:
		return "none"
	}
}

// Outcome is the result of observing one turn: the combined verdict plus
// every detector's findings in the extension's detector order
// (subagent-cycle, message-repetition, reasoning-stagnation,
// tool-repetition, read-repetition, search-spiral).
type Outcome struct {
	// Verdict is the combined verdict.
	Verdict Verdict

	// Findings lists every detector finding of this observation, in
	// detector order. Empty when Verdict is VerdictNone.
	Findings []Finding
}

// Steer message custom types and fixed texts. The texts are host-owned
// templates: they contain no model-controlled values, so model-influenced
// content can never enter the user trust tier through a steer message.
// The structured findings stay in Outcome.Findings for logging and UI
// rendering and are never interpolated into the delivered text.
const (
	// SteerTypeWarning is the customType of the first-detection steer
	// message.
	SteerTypeWarning = "loop-detector-warning"

	// SteerTypeForceStop is the customType of the force-stop steer
	// message.
	SteerTypeForceStop = "loop-detector-force-stop"

	// SteerPrefix marks injected steering messages so they are
	// distinguishable from real user input.
	SteerPrefix = "[smidja] "

	// SteerTextWarning is the fixed warning steer message.
	SteerTextWarning = SteerPrefix + "You are repeating the same actions with the same results. Stop, summarize the current state briefly, and choose a different approach."

	// SteerTextForceStop is the fixed force-stop steer message.
	SteerTextForceStop = SteerPrefix + "Execution stopped: repeated identical tool calls detected. Summarize progress so far."
)

// SteerMessage renders the intervention message the host should deliver:
// customType is SteerTypeWarning or SteerTypeForceStop, and text is the
// fixed host-owned template for the verdict, prefixed with SteerPrefix so
// injected steering messages are distinguishable from real user input.
// The text carries no model-controlled values; the structured findings
// stay in Outcome.Findings for logging and UI rendering. For VerdictNone
// it returns ("", "").
//
// TODO(session wave): the session integration wave persists the steering
// message as a custom entry type instead of a plain user-role message;
// until then the "[smidja] " prefix keeps it distinguishable from real
// user input.
func (o Outcome) SteerMessage() (customType, text string) {
	switch o.Verdict {
	case VerdictWarn:
		return SteerTypeWarning, SteerTextWarning
	case VerdictBlock:
		return SteerTypeForceStop, SteerTextForceStop
	default:
		return "", ""
	}
}

// Detector observes completed turns and reports when the session looks
// stuck in a loop, mirroring the extension's per-session state machine: a
// sliding window of turn records, the six detectors, and the escalation
// ladder (none -> warn -> block).
//
// A Detector is safe for concurrent use; Observe and Reset serialize
// state changes.
type Detector struct {
	mu          sync.Mutex
	cfg         Config
	window      []Turn
	consecutive int
}

// New creates a detector with the given configuration. Build the
// configuration from DefaultConfig() and override the fields you need.
// New uses the configuration as given: the extension's flags default to
// true, so a zero-means-default merge would make them impossible to
// disable; here the zero Config disables every detector instead.
func New(cfg Config) *Detector {
	return &Detector{cfg: cfg}
}

// Reset clears the turn window and the consecutive-detection counter,
// mirroring the extension's session_start handler.
func (d *Detector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = nil
	d.consecutive = 0
}

// Observe feeds one completed turn into the detector and returns the
// outcome: the combined verdict plus every detector's findings. It
// mirrors the extension's turn_end handler: the turn is pushed into the
// sliding window (evicting past WindowSize), the detectors run over the
// window, and the escalation counter advances. Turns with no content at
// all are skipped without touching the counter, exactly like the
// extension's early return.
func (d *Detector) Observe(turn Turn) Outcome {
	d.mu.Lock()
	defer d.mu.Unlock()
	if turn.ThinkingText == "" && turn.TextContent == "" && len(turn.ToolCalls) == 0 {
		return Outcome{}
	}
	d.window = append(d.window, turn)
	for len(d.window) > d.cfg.WindowSize {
		d.window = d.window[1:]
	}
	findings := d.runDetectors(d.window)
	if len(findings) == 0 {
		d.consecutive = 0
		return Outcome{}
	}
	switch {
	case d.consecutive == 0:
		d.consecutive = 1
		d.window = nil
		return Outcome{Verdict: VerdictWarn, Findings: findings}
	case d.consecutive+1 >= d.cfg.EscalateAfter:
		d.consecutive = 0
		d.window = nil
		return Outcome{Verdict: VerdictBlock, Findings: findings}
	default:
		d.consecutive++
		d.window = nil
		return Outcome{Verdict: VerdictNone, Findings: findings}
	}
}

// runDetectors runs every enabled detector over the window and returns
// the findings in the extension's detector order.
func (d *Detector) runDetectors(recs []Turn) []Finding {
	var findings []Finding
	if f := d.detectSubagentCycle(recs); f != nil {
		findings = append(findings, *f)
	}
	if f := d.detectMessageRepetition(recs); f != nil {
		findings = append(findings, *f)
	}
	if f := d.detectReasoningStagnation(recs); f != nil {
		findings = append(findings, *f)
	}
	if f := d.detectToolRepetition(recs); f != nil {
		findings = append(findings, *f)
	}
	if f := d.detectReadRepetition(recs); f != nil {
		findings = append(findings, *f)
	}
	if f := d.detectSearchSpiral(recs); f != nil {
		findings = append(findings, *f)
	}
	return findings
}

// detectSubagentCycle mirrors the extension's detectSubagentCycle: a run
// of trailing identical failing subagent calls at or above the nesting
// threshold counts as a cycle. The extension guards this detector with
// PI_SUBAGENT_CHILD=1 (only subagent child processes run it); smidja has
// no process boundary, so the detector runs wherever the host feeds it
// turns, and hosts that want the extension's scoping enable the flag only
// when observing nested contexts.
func (d *Detector) detectSubagentCycle(recs []Turn) *Finding {
	if !d.cfg.EnableSubagentCycleDetection {
		return nil
	}
	var failing []string
	for _, rec := range recs {
		for _, call := range rec.ToolCalls {
			if call.Name != "subagent" || !call.HasResult || !call.IsError || call.ResultKey == "" {
				continue
			}
			failing = append(failing, call.ResultKey)
		}
	}
	if len(failing) == 0 {
		return nil
	}
	last := failing[len(failing)-1]
	run := 0
	for i := len(failing) - 1; i >= 0; i-- {
		if failing[i] == last {
			run++
		} else {
			break
		}
	}
	if run >= d.cfg.SubagentNestingThreshold {
		return &Finding{
			Type:    FindingSubagentCycle,
			Message: fmt.Sprintf("Nested subagent loop: %d blocked subagent attempts with identical error output.", run),
		}
	}
	return nil
}

// detectMessageRepetition mirrors the extension's detectMessageRepetition:
// at least MessageRepeatThreshold of the long-enough assistant messages in
// the window are at least MessageRepeatSimilarity-similar to the most
// recent one.
func (d *Detector) detectMessageRepetition(recs []Turn) *Finding {
	if !d.cfg.EnableMessageRepetitionDetection {
		return nil
	}
	type msgRec struct {
		turnIndex int
		text      string
	}
	var messages []msgRec
	for _, r := range recs {
		t := strings.TrimSpace(r.TextContent)
		if len(t) >= d.cfg.MessageRepeatMinLength {
			messages = append(messages, msgRec{r.TurnIndex, t})
		}
	}
	if len(messages) < 2 {
		return nil
	}
	target := messages[len(messages)-1]
	targetNorm := normalizeText(target.text)
	matches := 0
	for _, m := range messages {
		if similarity(targetNorm, normalizeText(m.text)) >= d.cfg.MessageRepeatSimilarity {
			matches++
		}
	}
	if matches >= d.cfg.MessageRepeatThreshold {
		return &Finding{
			Type: FindingMessageRepetition,
			Message: fmt.Sprintf("Message repetition: %d of the last %d assistant messages are ≥%d%% similar to the most recent one (turn %d).",
				matches, len(messages), int(math.Round(d.cfg.MessageRepeatSimilarity*100)), target.turnIndex),
		}
	}
	return nil
}

// detectReasoningStagnation mirrors the extension's
// detectReasoningStagnation: ReasoningStuckThreshold consecutive turns
// with thinking longer than 10 runes whose adjacent normalized thinkings
// are at least ReasoningStuckThresholdSimilarity-similar count as stuck.
func (d *Detector) detectReasoningStagnation(recs []Turn) *Finding {
	if !d.cfg.EnableReasoningDetection {
		return nil
	}
	var withThinking []Turn
	for _, r := range recs {
		if len(r.ThinkingText) > 10 {
			withThinking = append(withThinking, r)
		}
	}
	if len(withThinking) < d.cfg.ReasoningStuckThreshold {
		return nil
	}
	normed := make([]string, len(withThinking))
	for i, r := range withThinking {
		normed[i] = normalizeThinking(r.ThinkingText)
	}
	for i := 0; i <= len(normed)-d.cfg.ReasoningStuckThreshold; i++ {
		win := normed[i : i+d.cfg.ReasoningStuckThreshold]
		stuck := true
		for j := 0; j < len(win)-1; j++ {
			if similarity(win[j], win[j+1]) < d.cfg.ReasoningStuckThresholdSimilarity {
				stuck = false
				break
			}
		}
		if stuck {
			winRecs := withThinking[i : i+len(win)]
			return &Finding{
				Type: FindingReasoningStagnation,
				Message: fmt.Sprintf("Stuck reasoning: %d consecutive turns with ≥%d%% word overlap in thinking (turns %d-%d).",
					len(winRecs), int(math.Round(d.cfg.ReasoningStuckThresholdSimilarity*100)),
					winRecs[0].TurnIndex, winRecs[len(winRecs)-1].TurnIndex),
			}
		}
	}
	return nil
}

// detectToolRepetition mirrors the extension's detectToolRepetition: the
// window's tool calls flatten into one sequence, and a 2- or 3-call
// pattern repeating RepeatPatternMinReps times in a row with identical
// call and result fingerprints counts as repetition.
func (d *Detector) detectToolRepetition(recs []Turn) *Finding {
	if !d.cfg.EnableToolRepetitionDetection {
		return nil
	}
	var seq []ToolCall
	for _, rec := range recs {
		seq = append(seq, rec.ToolCalls...)
	}
	if len(seq) < d.cfg.RepeatSequenceMinLength {
		return nil
	}
	for _, pLen := range []int{2, 3} {
		for s := 0; s <= len(seq)-pLen*d.cfg.RepeatPatternMinReps; s++ {
			pat := seq[s : s+pLen]
			allHaveResults := true
			for _, c := range pat {
				if !c.HasResult {
					allHaveResults = false
					break
				}
			}
			if !allHaveResults {
				continue
			}
			reps := 1
			pos := s + pLen
			for pos+pLen <= len(seq) {
				next := seq[pos : pos+pLen]
				match := true
				for i := range pat {
					c := next[i]
					if !c.HasResult || c.CallKey != pat[i].CallKey || c.ResultKey != pat[i].ResultKey {
						match = false
						break
					}
				}
				if !match {
					break
				}
				reps++
				pos += pLen
			}
			if reps >= d.cfg.RepeatPatternMinReps {
				parts := make([]string, 0, len(pat))
				for _, c := range pat {
					parts = append(parts, c.DisplaySummary)
				}
				return &Finding{
					Type:    FindingToolRepetition,
					Message: fmt.Sprintf("Tool call repetition: \"%s\" repeated %d times in a row with identical output.", strings.Join(parts, " → "), reps),
				}
			}
		}
	}
	return nil
}

// detectReadRepetition mirrors the extension's detectReadRepetition: a
// path read 4 or more times across the window counts as repetition. Paths
// are extracted from the read display summary and counted in first-seen
// order, so the reported path is deterministic.
func (d *Detector) detectReadRepetition(recs []Turn) *Finding {
	if !d.cfg.EnableReadRepetitionDetection {
		return nil
	}
	var order []string
	counts := make(map[string]int)
	for _, rec := range recs {
		for _, call := range rec.ToolCalls {
			if call.Name != "read" {
				continue
			}
			m := readPathRE.FindStringSubmatch(call.DisplaySummary)
			if m == nil {
				continue
			}
			p := m[1]
			if _, ok := counts[p]; !ok {
				order = append(order, p)
			}
			counts[p]++
		}
	}
	for _, p := range order {
		n := counts[p]
		if n >= 4 {
			base := p
			if i := strings.LastIndex(base, "/"); i >= 0 {
				base = base[i+1:]
			}
			return &Finding{
				Type:    FindingReadRepetition,
				Message: fmt.Sprintf("Read repetition: \"%s\" read %d times across the window.", base, n),
			}
		}
	}
	return nil
}

var (
	// readPathRE matches the extension's /^read\((.+)\)$/ used to pull
	// the path out of a read display summary.
	readPathRE = regexp.MustCompile(`^read\((.+)\)$`)

	// searchPatternRE matches the extension's /^\w+\("([^"]+)"/ used to
	// pull the quoted pattern out of a search display summary.
	searchPatternRE = regexp.MustCompile(`^\w+\("([^"]+)"`)

	// searchPathRE matches the extension's
	// /(?:grep|find|search_files)\([^,]+,\s*(.+)/ used to pull the path
	// tail out of a search display summary.
	searchPathRE = regexp.MustCompile(`(?:grep|find|search_files)\([^,]+,\s*(.+)`)
)

// detectSearchSpiral mirrors the extension's detectSearchSpiral: a
// normalized search pattern searched across 3 or more different paths, or
// three consecutive searches whose normalized patterns strictly expand in
// length, count as a spiral. Note that under the extension's own display
// summaries the different-paths branch rarely fires (paths extract as
// "current" unless the display text contains a comma); it is ported
// verbatim and fires for host-provided display summaries that carry
// per-path tails.
func (d *Detector) detectSearchSpiral(recs []Turn) *Finding {
	if !d.cfg.EnableSearchSpiralDetection {
		return nil
	}
	type searchCall struct{ norm, path string }
	var searches []searchCall
	for _, rec := range recs {
		for _, call := range rec.ToolCalls {
			if call.Name != "grep" && call.Name != "find" && call.Name != "glob" && call.Name != "search_files" {
				continue
			}
			pattern := call.DisplaySummary
			if m := searchPatternRE.FindStringSubmatch(pattern); m != nil {
				pattern = m[1]
			}
			path := "current"
			if m := searchPathRE.FindStringSubmatch(call.DisplaySummary); m != nil {
				path = m[1]
			}
			searches = append(searches, searchCall{norm: normSearchPattern(pattern), path: path})
		}
	}
	if len(searches) < 3 {
		return nil
	}
	// First pass: a normalized pattern searched across 3+ paths. Paths
	// are tracked in first-seen order so the report is deterministic.
	var order []string
	paths := make(map[string][]string)
	seen := make(map[string]map[string]bool)
	for _, s := range searches {
		if _, ok := paths[s.norm]; !ok {
			order = append(order, s.norm)
			seen[s.norm] = make(map[string]bool)
		}
		if !seen[s.norm][s.path] {
			seen[s.norm][s.path] = true
			paths[s.norm] = append(paths[s.norm], s.path)
		}
	}
	for _, pNorm := range order {
		if len(paths[pNorm]) >= 3 {
			return &Finding{
				Type:    FindingSearchSpiral,
				Message: fmt.Sprintf("Search spiral: pattern \"%s\" searched across %d different paths.", pNorm, len(paths[pNorm])),
			}
		}
	}
	// Second pass: three consecutive searches with strictly expanding
	// normalized patterns.
	for i := 0; i <= len(searches)-3; i++ {
		n1, n2, n3 := searches[i].norm, searches[i+1].norm, searches[i+2].norm
		if len(n1) < len(n2) && len(n2) < len(n3) && len(n1) > 3 && n1 != n2 && n2 != n3 {
			return &Finding{
				Type:    FindingSearchSpiral,
				Message: fmt.Sprintf("Search spiral: patterns expanding \"%s\" → \"%s\" → \"%s\".", n1, n2, n3),
			}
		}
	}
	return nil
}

// normSearchPattern mirrors the extension's search-spiral pattern
// normalization: quotes and backslashes removed, "*" replaced by "WILD",
// "." replaced by "DOT", lowercased, truncated to 40 runes.
func normSearchPattern(p string) string {
	p = strings.ReplaceAll(p, `"`, "")
	p = strings.ReplaceAll(p, "'", "")
	p = strings.ReplaceAll(p, `\`, "")
	p = strings.ReplaceAll(p, "*", "WILD")
	p = strings.ReplaceAll(p, ".", "DOT")
	return truncate(strings.ToLower(p), 40)
}
