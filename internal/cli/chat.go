package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/loopdetector"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/packages"
	"github.com/digitalygo/smidja/internal/retry"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/skills"
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

	retryPolicy    agent.RetryPolicy
	retryPolicySet bool
	preparer       *contextPreparerAdapter
	hooks          agent.HookDispatcher
	retry          retryFunc
	isOverflow     func(string) bool
	detector       agent.LoopDetector

	catalog        *extensions.ToolCatalog
	commands       *extensions.CommandCatalog
	handlerContext func(context.Context) sdk.HandlerContext
}

func runChat(d *Deps, prompt, model, system, provider string, allowWorkspaceMCP bool, continuePath string) error {
	ctx := d.Context
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := d.Config
	if cfg == nil {
		var err error
		cfg, err = loadChatConfig(d)
		if err != nil {
			return fail(d, err)
		}
	}
	if model != "" {
		cfg.Model = model
	}
	client, selectedProvider, err := selectChatClient(d, cfg, provider)
	if err != nil {
		return fail(d, err)
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
	var sess *session.Session
	if continuePath != "" {
		sess, err = store.Open(continuePath, session.OpenOptions{Strict: true})
	} else {
		sess, err = store.Create(cwd)
	}
	if err != nil {
		return fail(d, err)
	}
	defer sess.Close()

	catalog := extensions.NewToolCatalog()
	for _, t := range toolSet {
		if err := catalog.Register(t); err != nil {
			return fail(d, err)
		}
	}
	commands := extensions.NewCommandCatalog()

	runtime := d.ExtensionRuntime
	if runtime == nil {
		runtime = extensions.NewRuntime(extensions.NewRegistry())
	}
	api := extensions.NewAPI(extensions.APIOptions{
		Catalog:       catalog,
		Commands:      commands,
		ResolveConfig: cfg.Default,
	})
	runtime.SetAPI(func() sdk.API { return api })
	if err := runtime.Start(); err != nil {
		return fail(d, err)
	}
	hooks := runtime.Dispatcher()

	snapshot, err := buildContentSnapshot(d, cfg.WorkspaceRoot)
	if err != nil {
		return fail(d, err)
	}
	skillCat, err := snapshotSkillCatalog(snapshot)
	if err != nil {
		return fail(d, err)
	}
	registerSkillCommand(commands, skillCat, d.Stdout)

	resolveEnv := func(key string) (string, bool) {
		value := cfg.Default(key)
		if value == "" {
			return "", false
		}
		return value, true
	}
	mcpCfg, workspaceMCP, err := loadMCPConfig(d.Home(), cwd)
	if err != nil {
		return fail(d, err)
	}
	mcpRt, err := startMCP(ctx, mcpCfg, workspaceMCP, allowWorkspaceMCP, catalog, resolveEnv, d.Stderr)
	if err != nil {
		return fail(d, err)
	}
	defer mcpRt.Close()

	_ = hooks.SessionStart(ctx, string(sdk.SessionStartStartup))
	defer hooks.SessionShutdown(ctx, string(sdk.SessionShutdownQuit))

	modelReg := d.ModelRegistry
	if modelReg == nil {
		modelReg = models.NewRegistry()
	}
	if err := refreshModelRegistry(ctx, d, cfg, modelReg); err != nil {
		return fail(d, err)
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
	instr, err := content.DiscoverInstructions(cwd, content.InstructionsOptions{
		BundleFS:      d.Bundle.FS,
		WorkspaceRoot: cfg.WorkspaceRoot,
		UserHome:      d.Home(),
	})
	if err == nil {
		if suffix := instr.Suffix(); suffix != "" {
			sysPrompt = sysPrompt + "\n\n" + suffix
		}
	}

	if continuePath != "" {
		providerID := selectedProvider
		if providerID == "" {
			providerID = "openrouter"
		}
		cur := currentRuntimeProfile(cfg, providerID, sysPrompt, toolsetFingerprint(catalog, toolSet), cfg.WorkspaceRoot)
		reset, err := syncRuntimeProfile(sess, cur, func() string { return snapshot.Fingerprint() })
		if err != nil {
			return fail(d, err)
		}
		if reset {
			fmt.Fprintf(d.Stderr, "smidja: runtime profile changed, context cache reset\n")
		}
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
		retryPolicy: agent.RetryPolicy{
			Enabled:     cfg.RetryEnabled,
			MaxRetries:  cfg.RetryMaxRetries,
			BaseDelayMs: cfg.RetryBaseDelayMs,
		},
		retryPolicySet: true,
		retry:          retryAdapter,
		isOverflow:     retry.IsContextOverflow,
		detector:       newLoopDetectorAdapter(loopdetector.New(loopdetector.DefaultConfig())),
		catalog:        catalog,
		commands:       commands,
		handlerContext: func(signal context.Context) sdk.HandlerContext {
			return runtime.HandlerContext(signal)
		},
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

func loadChatConfig(d *Deps) (*config.Config, error) {
	store, err := packages.Open(packageStoreRoot(d))
	if err != nil {
		return nil, err
	}
	pkgDefaults, err := store.ActiveConfigDefaults()
	if err != nil {
		return nil, err
	}
	bundleSettings, err := config.ReadBundleSettings(d.Bundle.FS)
	if err != nil {
		return nil, err
	}
	return config.LoadWithSources(d.Env, d.Getwd, d.Home, config.DefaultsFromAny(d.Bundle.ConfigDefaults), bundleSettings, pkgDefaults)
}

func buildContentSnapshot(d *Deps, workspaceRoot string) (content.Snapshot, error) {
	dirs, err := activePackageDirs(d)
	if err != nil {
		return content.Snapshot{}, err
	}
	return content.Load(content.Options{
		BundleID:       d.Bundle.ID,
		BundleFS:       d.Bundle.FS,
		WorkspaceDir:   workspaceRoot,
		UserHome:       d.Home(),
		PackagesDirs:   dirs,
		TrustWorkspace: true,
	})
}

func activePackageDirs(d *Deps) ([]string, error) {
	store, err := packages.Open(packageStoreRoot(d))
	if err != nil {
		return nil, err
	}
	active, err := store.Active()
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(active))
	for _, a := range active {
		dirs = append(dirs, filepath.Join(store.Root(), a.ID, a.Version))
	}
	return dirs, nil
}

func snapshotSkillCatalog(snapshot content.Snapshot) (*skills.Catalog, error) {
	c := skills.New()
	for _, ref := range snapshot.Skills {
		if err := c.Add(ref.Package, ref.Name, ref.Content); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func localModelOverrides(d *Deps, workspaceRoot string) []models.ModelInfo {
	path := models.LocalOverridesPath(workspaceRoot, d.Home())
	if path == "" {
		return nil
	}
	overrides, err := models.LoadLocalOverrides(path)
	if err != nil {
		return nil
	}
	return overrides
}

func packageStoreRoot(d *Deps) string {
	if d != nil && d.Env != nil {
		if v := d.Env("SMIDJA_PACKAGES_DIR"); v != "" {
			return v
		}
	}
	return packages.DefaultRoot()
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
		if strings.HasPrefix(input, "/") {
			name, args := splitCommandInput(input)
			if name == "help" {
				printCommandHelp(d.stdout, d.commands)
				continue
			}
			cmd, ok := d.commands.Get(name)
			if !ok {
				fmt.Fprintf(d.stderr, "smidja: unknown command /%s\n", name)
				continue
			}
			hctx := &commandContext{
				HandlerContext: d.handlerContext(ctx),
				d:              d,
				ctx:            ctx,
				history:        &history,
			}
			if err := cmd.Handler(hctx, args); err != nil {
				fmt.Fprintf(d.stderr, "smidja: /%s: %v\n", name, err)
			}
			continue
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
	var catalog agent.ToolCatalog
	if d.catalog != nil {
		catalog = d.catalog
	}
	deps := &agent.LoopDeps{
		Client:            d.client,
		Tools:             d.tools,
		Catalog:           catalog,
		Recorder:          d.recorder,
		Stdout:            out,
		OnThinking:        onThinking,
		Preparer:          preparer,
		Hooks:             d.hooks,
		Retry:             d.retry,
		IsContextOverflow: d.isOverflow,
		Detector:          d.detector,
		RetryPolicy:       d.retryPolicy,
		RetryPolicySet:    d.retryPolicySet,
	}
	return deps
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
