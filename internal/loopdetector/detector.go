package loopdetector

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
)

type Config struct {
	WindowSize int

	ReasoningStuckThreshold int

	ReasoningStuckThresholdSimilarity float64

	RepeatSequenceMinLength int

	RepeatPatternMinReps int

	SubagentNestingThreshold int

	MessageRepeatThreshold int

	MessageRepeatSimilarity float64

	MessageRepeatMinLength int

	EscalateAfter int

	EnableReasoningDetection bool

	EnableToolRepetitionDetection bool

	EnableReadRepetitionDetection bool

	EnableSearchSpiralDetection bool

	EnableSubagentCycleDetection bool

	EnableMessageRepetitionDetection bool
}

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

const (
	FindingSubagentCycle = "subagent-cycle"

	FindingMessageRepetition = "message-repetition"

	FindingReasoningStagnation = "reasoning-stagnation"

	FindingToolRepetition = "tool-repetition"

	FindingReadRepetition = "read-repetition"

	FindingSearchSpiral = "search-spiral"
)

type Finding struct {
	Type string

	Message string
}

type Verdict int

const (
	VerdictNone Verdict = iota

	VerdictWarn

	VerdictBlock
)

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

type Outcome struct {
	Verdict Verdict

	Findings []Finding
}

const (
	SteerTypeWarning = "loop-detector-warning"

	SteerTypeForceStop = "loop-detector-force-stop"

	SteerPrefix = "[smidja] "

	SteerTextWarning = SteerPrefix + "You are repeating the same actions with the same results. Stop, summarize the current state briefly, and choose a different approach."

	SteerTextForceStop = SteerPrefix + "Execution stopped: repeated identical tool calls detected. Summarize progress so far."
)

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

type Detector struct {
	mu          sync.Mutex
	cfg         Config
	window      []Turn
	consecutive int
}

func New(cfg Config) *Detector {
	return &Detector{cfg: cfg}
}

func (d *Detector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = nil
	d.consecutive = 0
}

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
	readPathRE = regexp.MustCompile(`^read\((.+)\)$`)

	searchPatternRE = regexp.MustCompile(`^\w+\("([^"]+)"`)

	searchPathRE = regexp.MustCompile(`(?:grep|find|search_files)\([^,]+,\s*(.+)`)
)

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

func normSearchPattern(p string) string {
	p = strings.ReplaceAll(p, `"`, "")
	p = strings.ReplaceAll(p, "'", "")
	p = strings.ReplaceAll(p, `\`, "")
	p = strings.ReplaceAll(p, "*", "WILD")
	p = strings.ReplaceAll(p, ".", "DOT")
	return truncate(strings.ToLower(p), 40)
}
