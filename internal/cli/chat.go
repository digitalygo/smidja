package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/loopdetector"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/openrouter"
	"github.com/digitalygo/smidja/internal/retry"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/subagent"
	"github.com/digitalygo/smidja/internal/tools"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/internal/workspace"
	"github.com/digitalygo/smidja/sdk"
)

const defaultSystemPrompt = `You are smidja, an autonomous coding agent working inside a workspace.

You help with code tasks. Explore before you act: list the files, read the
relevant sources, run the build and tests. Make minimal, correct changes
and summarize what you did.

Your tools: read (view files), write (create or replace files), edit
(replace literal text), exec (run shell commands). Every path must stay
inside the workspace; .git internals are off limits. exec is not a
sandbox: it runs with the user's full privileges, so use it only for what
the task needs.

If a task is ambiguous, state your assumption and proceed with the safest
interpretation.`

const modelFetchTimeout = 5 * time.Second

type runDeps struct {
	model        string
	system       string
	showThinking bool
	sessionPath  string

	client   agent.Client
	tools    []agent.Tool
	recorder agent.Recorder
	stdout   io.Writer
	stderr   io.Writer

	preparer   *contextPreparerAdapter
	hooks      agent.HookDispatcher
	retry      retryFunc
	isOverflow func(string) bool
	detector   agent.LoopDetector
}

func runChat(d *Deps, prompt, model, system, provider string) error {
	ctx := d.Context
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := d.Config
	if cfg == nil {
		var err error
		cfg, err = config.Load(d.Env, d.Getwd, d.Home)
		if err != nil {
			return fail(d, err)
		}
	}
	if model != "" {
		cfg.Model = model
	}
	var client agent.Client
	if provider != "" {
		built, err := buildProviderClient(d, provider)
		if err != nil {
			return fail(d, err)
		}
		client = built
	} else if d.Client != nil {
		client = d.Client
	} else {
		client = openrouter.New(cfg.OpenRouterURL, cfg.APIKey, nil)
	}
	toolSet := d.Tools
	if len(toolSet) == 0 {
		ws, err := workspace.New(cfg.WorkspaceRoot)
		if err != nil {
			return fail(d, err)
		}
		toolSet = tools.All(tools.Deps{
			Workspace:      ws,
			ExecTimeoutSec: cfg.ExecTimeoutSecs,
			MaxOutputBytes: cfg.MaxOutputBytes,
		})
	}
	store := d.Store
	if store == nil {
		var err error
		store, err = session.NewStore(cfg.SessionDir)
		if err != nil {
			return fail(d, err)
		}
	}
	cwd, err := d.Getwd()
	if err != nil {
		return fail(d, err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		return fail(d, err)
	}
	defer sess.Close()

	runtime := d.ExtensionRuntime
	if runtime == nil {
		runtime = extensions.NewRuntime(extensions.NewRegistry())
	}
	if err := runtime.Start(); err != nil {
		return fail(d, err)
	}
	hooks := runtime.Dispatcher()
	_ = hooks.SessionStart(ctx, "startup")
	defer hooks.SessionShutdown(ctx, "quit")

	modelReg := d.ModelRegistry
	if modelReg == nil {
		modelReg = models.NewRegistry()
	}
	if d.FetchModels != nil {
		fctx, cancel := context.WithTimeout(ctx, modelFetchTimeout)
		infos, ferr := d.FetchModels(fctx)
		cancel()
		if ferr == nil {
			modelReg.Merge(infos)
		}
	}

	window := cfg.ContextWindowTokens
	if window <= 0 {
		window = modelWindow(modelReg, cfg.Model)
	}
	selector := subagent.NewOpenRouterSelector(client)
	preparer, err := newContextPreparer(*cfg, window, selector)
	if err != nil {
		return fail(d, err)
	}

	sysPrompt := system
	if sysPrompt == "" {
		sysPrompt = defaultSystemPrompt
	}

	rd := &runDeps{
		model:        cfg.Model,
		system:       sysPrompt,
		showThinking: envTruthy(d.Env("SMIDJA_SHOW_THINKING")),
		sessionPath:  sess.Path(),
		client:       client,
		tools:        toolSet,
		recorder:     &sessionRecorder{sess},
		stdout:       d.Stdout,
		stderr:       d.Stderr,
		preparer:     preparer,
		hooks:        hooks,
		retry:        retryAdapter,
		isOverflow:   retry.IsContextOverflow,
		detector:     newLoopDetectorAdapter(loopdetector.New(loopdetector.DefaultConfig())),
	}

	mode := sdk.ModeInteractive
	if prompt != "" {
		mode = sdk.ModePrint
	}
	lineUI := ui.New(d.Stdin, d.Stdout, d.Stderr, mode)

	if prompt != "" {
		if err := runOnce(ctx, rd, prompt); err != nil {
			return fail(d, err)
		}
		return nil
	}
	if err := repl(ctx, lineUI, rd); err != nil {
		return fail(d, err)
	}
	return nil
}

func newContextPreparer(cfg config.Config, window int64, selector subagent.Selector) (*contextPreparerAdapter, error) {
	cmCfg := contextmanager.Config{
		Enabled:                cfg.ContextEnabled,
		ContextWindowTokens:    window,
		CacheMissAfter:         cfg.ContextCacheMissAfter,
		PruneThreshold:         cfg.ContextPruneThreshold,
		CompactThreshold:       cfg.ContextCompactThreshold,
		SafetyCompactThreshold: cfg.ContextSafetyThreshold,
		CompactTarget:          cfg.ContextCompactTarget,
		KeepRecentMessages:     cfg.ContextKeepRecentMessages,
		SelectorChunkTokens:    cfg.ContextSelectorChunkTokens,
		SelectorModel:          cfg.ContextSelectorModel,
	}
	if cmCfg.SelectorModel == "" {
		cmCfg.SelectorModel = cfg.Model
	}
	if cmCfg.CacheMissAfter <= 0 {
		cmCfg.CacheMissAfter = contextmanager.DefaultCacheMissAfter
	}
	if cmCfg.PruneThreshold <= 0 {
		cmCfg.PruneThreshold = contextmanager.DefaultPruneThreshold
	}
	if cmCfg.CompactThreshold <= 0 {
		cmCfg.CompactThreshold = contextmanager.DefaultCompactThreshold
	}
	if cmCfg.SafetyCompactThreshold <= 0 {
		cmCfg.SafetyCompactThreshold = contextmanager.DefaultSafetyCompactThreshold
	}
	if cmCfg.CompactTarget <= 0 {
		cmCfg.CompactTarget = contextmanager.DefaultCompactTarget
	}
	if cmCfg.KeepRecentMessages <= 0 {
		cmCfg.KeepRecentMessages = contextmanager.DefaultKeepRecentMessages
	}
	if cmCfg.SelectorChunkTokens <= 0 {
		cmCfg.SelectorChunkTokens = contextmanager.DefaultSelectorChunkTokens
	}
	live, err := contextmanager.New(cmCfg, selector)
	if err != nil {
		return nil, err
	}
	return newContextPreparerAdapter(live, cmCfg), nil
}

func modelWindow(reg *models.Registry, model string) int64 {
	if reg != nil {
		if m, ok := reg.Get(model); ok && m.ContextWindow > 0 {
			return m.ContextWindow
		}
	}
	return models.DefaultModelContextWindow
}

func runOnce(ctx context.Context, d *runDeps, prompt string) error {
	out := &trailingWriter{w: d.stdout}
	if _, err := runTurn(ctx, d, loopDeps(d, out), nil, prompt); err != nil {
		return err
	}
	if !out.endsWithNewline() {
		fmt.Fprintln(out.w)
	}
	return nil
}

func repl(ctx context.Context, lineUI *ui.LineUI, d *runDeps) error {
	var history []*agent.Message
	first := true
	for {
		line, err := lineUI.Input(">", "")
		if err != nil {
			return err
		}
		input := strings.TrimSpace(line)
		if input == "" {
			return nil
		}
		if input == "/quit" || input == "/exit" {
			return nil
		}
		out := &trailingWriter{w: d.stdout}
		history, err = runTurn(ctx, d, loopDeps(d, out), history, input)
		if err != nil {
			return err
		}
		if !out.endsWithNewline() {
			fmt.Fprintln(d.stdout)
		}
		if first {
			fmt.Fprintf(d.stdout, "session: %s\n", d.sessionPath)
			first = false
		}
	}
}

func runTurn(ctx context.Context, d *runDeps, deps *agent.LoopDeps, history []*agent.Message, input string) ([]*agent.Message, error) {
	h, err := agent.RunTurn(ctx, deps, d.model, d.system, history, input)
	if err != nil {
		var overflow *agent.ContextOverflowError
		if errors.As(err, &overflow) && d.preparer != nil {
			if d.stderr != nil {
				fmt.Fprintln(d.stderr, "smidja: context overflow, compacting and retrying once")
			}
			d.preparer.forceSafety()
			h, err = agent.RunTurn(ctx, deps, d.model, d.system, history, input)
			if err != nil {
				var again *agent.ContextOverflowError
				if errors.As(err, &again) {
					return h, fmt.Errorf("context still overflows the model window after compaction: %w", err)
				}
			}
		}
	}
	if perr := d.persistCompactions(); perr != nil {
		return h, perr
	}
	return h, err
}

func (d *runDeps) persistCompactions() error {
	if d == nil || d.preparer == nil || d.recorder == nil {
		return nil
	}
	sink, ok := d.recorder.(compactionSink)
	if !ok {
		return nil
	}
	for _, e := range d.preparer.drain() {
		if err := sink.appendCompaction(e); err != nil {
			return fmt.Errorf("persist compaction entry: %w", err)
		}
	}
	return nil
}

func loopDeps(d *runDeps, out io.Writer) *agent.LoopDeps {
	var onThinking func(string)
	if d.showThinking {
		onThinking = func(delta string) { io.WriteString(out, delta) }
	}
	var preparer agent.ContextPreparer
	if d.preparer != nil {
		preparer = d.preparer
	}
	return &agent.LoopDeps{
		Client:            d.client,
		Tools:             d.tools,
		Recorder:          d.recorder,
		Stdout:            out,
		OnThinking:        onThinking,
		Preparer:          preparer,
		Hooks:             d.hooks,
		Retry:             d.retry,
		IsContextOverflow: d.isOverflow,
		Detector:          d.detector,
	}
}

type retryFunc func(ctx context.Context, produce func(context.Context) (*agent.AssistantMessage, error), policy agent.RetryPolicy, callbacks *agent.RetryCallbacks) (*agent.AssistantMessage, error)

func retryAdapter(ctx context.Context, produce func(context.Context) (*agent.AssistantMessage, error), policy agent.RetryPolicy, callbacks *agent.RetryCallbacks) (*agent.AssistantMessage, error) {
	var cb retry.Callbacks
	if callbacks != nil {
		cb = &retry.CallbacksFunc{
			Scheduled:    callbacks.Scheduled,
			AttemptStart: callbacks.AttemptStart,
			Finished:     callbacks.Finished,
		}
	}
	return retry.Retry(ctx, produce, retry.Policy{
		Enabled:     policy.Enabled,
		MaxRetries:  policy.MaxRetries,
		BaseDelayMs: policy.BaseDelayMs,
	}, cb)
}

type loopDetectorAdapter struct {
	detector *loopdetector.Detector
}

var _ agent.LoopDetector = (*loopDetectorAdapter)(nil)

func newLoopDetectorAdapter(d *loopdetector.Detector) *loopDetectorAdapter {
	return &loopDetectorAdapter{detector: d}
}

func (a *loopDetectorAdapter) Observe(turn agent.Turn) agent.Outcome {
	var content []agent.ContentBlock
	if turn.ThinkingText != "" {
		content = append(content, agent.ContentBlock{Type: agent.BlockTypeThinking, Thinking: turn.ThinkingText})
	}
	if turn.TextContent != "" {
		content = append(content, agent.ContentBlock{Type: agent.BlockTypeText, Text: turn.TextContent})
	}
	var results []*agent.ToolResultMessage
	for _, c := range turn.ToolCalls {
		content = append(content, agent.ContentBlock{
			Type:      agent.BlockTypeToolCall,
			ID:        c.ToolCallID,
			Name:      c.Name,
			Arguments: c.Arguments,
		})
		if c.Result != nil {
			results = append(results, c.Result)
		}
	}
	out := a.detector.Observe(loopdetector.ExtractTurn(turn.TurnIndex, &agent.AssistantMessage{Content: content}, results))

	res := agent.Outcome{Verdict: agentVerdict(out.Verdict)}
	res.SteerCustomType, res.SteerText = out.SteerMessage()
	for _, f := range out.Findings {
		res.Findings = append(res.Findings, agent.Finding{Type: f.Type, Message: f.Message})
	}
	return res
}

func agentVerdict(v loopdetector.Verdict) agent.Verdict {
	switch v {
	case loopdetector.VerdictWarn:
		return agent.VerdictWarn
	case loopdetector.VerdictBlock:
		return agent.VerdictBlock
	default:
		return agent.VerdictNone
	}
}

type contextPreparerAdapter struct {
	live        *contextmanager.Manager
	recovery    *contextmanager.Manager
	forceTokens int64

	mu      sync.Mutex
	force   bool
	entries []*agent.CompactionEntry
}

var _ agent.ContextPreparer = (*contextPreparerAdapter)(nil)

func newContextPreparerAdapter(live *contextmanager.Manager, cfg contextmanager.Config) *contextPreparerAdapter {
	recovery, err := contextmanager.New(cfg, nil)
	if err != nil {
		recovery = live
	}
	return &contextPreparerAdapter{
		live:        live,
		recovery:    recovery,
		forceTokens: int64(math.Ceil(cfg.SafetyCompactThreshold * float64(cfg.ContextWindowTokens))),
	}
}

func (a *contextPreparerAdapter) Prepare(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	a.mu.Lock()
	force := a.force
	a.force = false
	a.mu.Unlock()

	var res agent.ContextResult
	var err error
	if force {
		req.LastUsageInput = a.forceTokens
		res, err = a.recovery.Prepare(ctx, req)
	} else {
		res, err = a.live.Prepare(ctx, req)
	}
	if err != nil {
		return res, err
	}
	if res.Compaction != nil {
		a.mu.Lock()
		a.entries = append(a.entries, res.Compaction)
		a.mu.Unlock()
	}
	return res, nil
}

func (a *contextPreparerAdapter) ObserveRequest(t time.Time) {
	a.live.ObserveRequest(t)
}

func (a *contextPreparerAdapter) ObserveResponse(m *agent.AssistantMessage) {
	a.live.ObserveResponse(m)
}

func (a *contextPreparerAdapter) forceSafety() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.force = true
	a.mu.Unlock()
}

func (a *contextPreparerAdapter) drain() []*agent.CompactionEntry {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.entries
	a.entries = nil
	return out
}

type compactionSink interface {
	appendCompaction(*agent.CompactionEntry) error
}

type sessionRecorder struct {
	sess *session.Session
}

var _ agent.Recorder = (*sessionRecorder)(nil)

func (r *sessionRecorder) AppendUser(m *agent.UserMessage) error {
	return r.sess.AppendUser(m)
}

func (r *sessionRecorder) AppendAssistant(m *agent.AssistantMessage) error {
	return r.sess.AppendAssistant(m)
}

func (r *sessionRecorder) AppendToolResult(m *agent.ToolResultMessage) error {
	return r.sess.AppendToolResult(m)
}

func (r *sessionRecorder) appendCompaction(e *agent.CompactionEntry) error {
	if e == nil {
		return nil
	}
	return r.sess.AppendEntry(&session.CompactionEntry{
		Summary:          string(e.Summary),
		FirstKeptEntryID: e.FirstKeptEntryID,
		TokensBefore:     e.TokensBefore,
	})
}

type trailingWriter struct {
	w    io.Writer
	last byte
}

func (t *trailingWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if n > 0 {
		t.last = p[n-1]
	}
	return n, err
}

func (t *trailingWriter) endsWithNewline() bool {
	return t.last == '\n'
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}
