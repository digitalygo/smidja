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

// defaultSystemPrompt is the concise built-in coding-agent prompt used
// when no -system override is given. It describes the tools and the
// workspace discipline the model must follow.
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

// modelFetchTimeout bounds the best-effort model catalogue refresh that
// runs at startup to refresh context windows. A slow or unreachable
// endpoint must never delay a session start for long.
const modelFetchTimeout = 5 * time.Second

// runDeps carries the runtime pieces one turn (or one REPL iteration)
// needs. Every field is an interface or a plain value, so tests can inject
// a fake client, recorder, and writers directly into runOnce and repl.
//
// The wave 3 loop seams (preparer, hooks, retry, isOverflow, detector)
// are all optional: the loop treats a nil seam as disabled, so callers
// that only set client/recorder/stdout keep the pre-wave-3 behavior.
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

// runChat wires the runtime pieces for one chat invocation and dispatches
// to the single-shot or interactive path. It loads the config (or uses the
// injected one), builds the workspace, client, tools, session, extension
// runtime, model registry, and context manager, and then runs one turn for
// prompt when non-empty, or the REPL otherwise. It is called by run after
// flag parsing; tests drive it through run with substituted process seams.
//
// Every component of Deps is consumed when present: smidja.Run injects the
// bundle-composed config, client, tools, session store, model registry,
// and extension runtime, and this function assembles only what depends on
// parse-time state (the -model override, the -p mode): the context
// manager and the LineUI.
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

	// Extension runtime: an injected runtime already carries the bundle's
	// registered extensions; the default path builds an empty registry.
	// Start runs the setup phase exactly once, so the loop's dispatcher
	// is ready. Extension hooks fire from here on; the host API seam is
	// another wave's job, so setup receives nil and extensions that need
	// it are logged and skipped per the per-extension error isolation.
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

	// Model registry: seeded with the curated offline fallback table,
	// then refreshed best-effort from the live OpenRouter catalogue so
	// the context manager's window lookup tracks provider changes. A
	// fetch failure is non-fatal: the fallback windows stay.
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

	// Context manager: policy from config, window from the registry
	// when the config leaves it unset, selector over the openrouter
	// client.
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

	// The LineUI owns the buffered stdin reader: the interactive REPL
	// reads prompts through it, and -p (print) mode gets a print-mode UI
	// whose dialogs return sdk.ErrModeUnsupported.
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

// newContextPreparer builds the context-management stack: the live
// manager from the config, plus a dedicated recovery manager used for the
// forced safety compaction that recovers from a context overflow. The
// policy defaults are resolved here (mirroring contextmanager's own
// withDefaults, via the exported default constants) so the adapter can
// compute the forced-compact anchor from real threshold values.
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

// modelWindow resolves the context window for model from the registry,
// falling back to the built-in default window when the model is unknown.
func modelWindow(reg *models.Registry, model string) int64 {
	if reg != nil {
		if m, ok := reg.Get(model); ok && m.ContextWindow > 0 {
			return m.ContextWindow
		}
	}
	return models.DefaultModelContextWindow
}

// runOnce executes a single assistant turn for prompt and exits cleanly.
// It is separated from run so tests can drive it with an injected client,
// recorder, and writer. The response is streamed to d.stdout by the loop
// as it is generated; a final newline is appended when the stream did not
// end with one.
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

// repl runs the interactive REPL: it reads prompts through the LineUI
// (which owns the buffered stdin reader, so extension dialogs and REPL
// prompts share one stream), runs one turn per prompt, streams responses
// to stdout, and ends on "/quit", "/exit", or EOF. The session path is
// printed after the first turn. The UI masks a clean EOF as a cancelled
// (empty) answer, so an empty prompt ends the session like EOF did in the
// pre-UI REPL.
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

// runTurn runs one user turn through the agent loop, recovering once from
// a context overflow: on a *agent.ContextOverflowError the turn is
// retried after arming the preparer's forced safety compaction, so the
// retry's first context assembly compacts the conversation before the
// provider call. A second overflow surfaces as a clear error. The retry
// reuses the original history, so the user message is appended exactly
// once across both attempts. Compaction entries the turn produced are
// persisted into the session before returning.
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

// persistCompactions writes every compaction entry the preparer captured
// during the last turn into the session, in capture order. It is a no-op
// when no preparer or recorder is wired.
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

// loopDeps assembles the agent loop dependencies for one turn. Text deltas
// stream to out; thinking deltas are forwarded to out only when the
// caller enabled SMIDJA_SHOW_THINKING, keeping the env handling here in
// the CLI and out of the loop.
func loopDeps(d *runDeps, out io.Writer) *agent.LoopDeps {
	var onThinking func(string)
	if d.showThinking {
		onThinking = func(delta string) { io.WriteString(out, delta) }
	}
	// A nil *contextPreparerAdapter must stay a nil interface: assigning
	// the typed nil pointer would make the loop treat it as a real
	// preparer and call methods on nil.
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

// retryFunc is the loop's Retry seam: a bounded-retry wrapper over one
// assistant-producing call. The CLI wires it with an adapter over
// internal/retry.Retry (host adapter pattern per the ports.go docs).
type retryFunc func(ctx context.Context, produce func(context.Context) (*agent.AssistantMessage, error), policy agent.RetryPolicy, callbacks *agent.RetryCallbacks) (*agent.AssistantMessage, error)

// retryAdapter maps the loop's mirror types onto internal/retry.Retry:
// agent.RetryPolicy and agent.RetryCallbacks are translated to the retry
// package's Policy and Callbacks, and the call is delegated.
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

// loopDetectorAdapter adapts *loopdetector.Detector to the loop's
// LoopDetector seam (host adapter pattern per the ports.go docs): it
// rebuilds the assistant message and tool results from the observed
// agent.Turn, runs them through loopdetector.ExtractTurn and
// Detector.Observe, and maps the verdict, findings, and steer message
// back onto the agent types.
type loopDetectorAdapter struct {
	detector *loopdetector.Detector
}

// Compile-time assertion that the adapter satisfies the loop's seam.
var _ agent.LoopDetector = (*loopDetectorAdapter)(nil)

func newLoopDetectorAdapter(d *loopdetector.Detector) *loopDetectorAdapter {
	return &loopDetectorAdapter{detector: d}
}

// Observe converts one observed turn back into the shape
// loopdetector.ExtractTurn expects and returns the combined verdict plus
// every detector's findings and the rendered steer message.
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

// agentVerdict maps a loopdetector verdict onto the agent mirror type.
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

// contextPreparerAdapter adapts a contextmanager.Manager to the loop's
// ContextPreparer seam and captures the compaction entries every Prepare
// reports, so the CLI can persist them into the session after the turn.
//
// It also supports the forced safety compaction used to recover from a
// context overflow: forceSafety arms the next Prepare to run through a
// dedicated fresh manager whose request anchor input is injected at the
// safety threshold, so the manager's safety path fires unconditionally
// and compacts to the real compact target. The recovery manager is never
// observed (ObserveRequest/ObserveResponse stay on the live manager), so
// its delta estimate always takes the injected anchor path.
type contextPreparerAdapter struct {
	live     *contextmanager.Manager
	recovery *contextmanager.Manager
	// forceTokens is the safety threshold in tokens, ceil(Safety *
	// window). Prepare injects it as the request's LastUsageInput so the
	// recovery manager's occupancy estimate crosses the safety threshold
	// regardless of the true conversation size.
	forceTokens int64

	mu      sync.Mutex
	force   bool // next Prepare runs the recovery safety path
	entries []*agent.CompactionEntry
}

// Compile-time assertion that the adapter satisfies the loop's seam.
var _ agent.ContextPreparer = (*contextPreparerAdapter)(nil)

// newContextPreparerAdapter builds the adapter over the live manager and
// a recovery manager with the same policy. cfg must be the resolved
// policy (thresholds filled), matching what the managers were built from.
// The recovery manager is deliberately selector-less: a forced recovery
// must not depend on the selector working, so it always compacts with
// the deterministic fallback.
func newContextPreparerAdapter(live *contextmanager.Manager, cfg contextmanager.Config) *contextPreparerAdapter {
	recovery, err := contextmanager.New(cfg, nil)
	if err != nil {
		// The live manager was built from the same validated config, so
		// this can only fail on a caller error; fall back to the live
		// manager so recovery degrades to the normal path.
		recovery = live
	}
	return &contextPreparerAdapter{
		live:        live,
		recovery:    recovery,
		forceTokens: int64(math.Ceil(cfg.SafetyCompactThreshold * float64(cfg.ContextWindowTokens))),
	}
}

// Prepare delegates to the live manager, or to the recovery manager when
// a forced safety compaction is armed (see forceSafety). Every reported
// compaction entry is captured for the caller to persist after the turn.
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

// ObserveRequest forwards to the live manager.
func (a *contextPreparerAdapter) ObserveRequest(t time.Time) {
	a.live.ObserveRequest(t)
}

// ObserveResponse forwards to the live manager, so the anchor and cache
// state the loop drives stay on the session's manager.
func (a *contextPreparerAdapter) ObserveResponse(m *agent.AssistantMessage) {
	a.live.ObserveResponse(m)
}

// forceSafety arms the next Prepare to run the recovery manager's safety
// path, forcing a compaction regardless of occupancy. It is a no-op when
// the adapter has no recovery manager.
func (a *contextPreparerAdapter) forceSafety() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.force = true
	a.mu.Unlock()
}

// drain returns and clears the compaction entries captured since the last
// call, in capture order.
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

// compactionSink persists compaction entries into the session; the
// production implementation is *sessionRecorder.
type compactionSink interface {
	appendCompaction(*agent.CompactionEntry) error
}

// sessionRecorder adapts *session.Session to the agent.Recorder seam used
// by the agent loop.
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

// appendCompaction persists one compaction entry produced by the context
// manager into the session, converting the agent entry shape into the
// session codec's CompactionEntry (the summary transcript is stored as
// its raw JSON text).
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

// trailingWriter wraps w and remembers the last byte written, so the CLI
// can append a final newline to a turn's output without duplicating one
// the stream already produced.
type trailingWriter struct {
	w    io.Writer
	last byte // 0 before the first write
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

// envTruthy reports whether an environment value enables a boolean
// setting: non-empty and not a common false spelling.
func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}
