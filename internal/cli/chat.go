package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/internal/loopdetector"
	"github.com/digitalygo/smidja/internal/mcp"
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
	uiRegistry   *extensionui.Registry
	model        string
	wireModel    string
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

	provider       string
	catalog        *extensions.ToolCatalog
	commands       *extensions.CommandCatalog
	handlerContext func(context.Context) sdk.HandlerContext
	modelRegistry  *models.Registry
	reprepare      func(string, string) (*contextPreparerAdapter, error)
	persistModel   func(string) error

	store *session.Store
	sess  *session.Session
	cwd   string

	controller     *sessionController
	env            *sessionBuildEnv
	resumedSession bool
	host           *hostRuntime

	prompts       content.PromptCatalog
	promptAliases map[string]string
	promptCommand string
}

func runChat(d *Deps, prompt, model, system, provider string, allowWorkspaceMCP bool, continuePath, tuiModeFlag, themeFlag string) error {
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
	tuiMode, err := resolveTUIMode(tuiModeFlag, cfg.TUIMode)
	if err != nil {
		return fail(d, err)
	}
	theme, err := resolveThemeSetting(themeFlag, cfg.Theme)
	if err != nil {
		return fail(d, err)
	}
	useTUI := prompt == "" && ui.ShouldUseTUI(d.Stdin, d.Stdout, prompt)

	var (
		cwd          string
		mcpCfg       *mcp.FileConfig
		workspaceMCP map[string]bool
		prepared     = &tuiStartupPlan{trustWorkspace: true}
	)
	if useTUI {
		prepared, err = prepareTUIStartup(ctx, d, cfg, tuiStartupOptions{
			mode:              tuiMode,
			theme:             theme,
			provider:          provider,
			allowWorkspaceMCP: allowWorkspaceMCP,
		})
		if err != nil {
			if errors.Is(err, errTUIStartupAborted) {
				return nil
			}
			return fail(d, err)
		}
		if prepared.runner != nil {
			defer prepared.runner.abort()
		}
		if !prepared.enabled {
			useTUI = false
		}
	}
	cwd = prepared.cwd
	mcpCfg = prepared.mcpCfg
	workspaceMCP = prepared.workspaceMCP
	trustWorkspace := prepared.trustWorkspace
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
	if cwd == "" {
		cwd, err = d.Getwd()
		if err != nil {
			return fail(d, err)
		}
	}
	var sess *session.Session
	if continuePath != "" {
		sess, err = store.Open(continuePath, session.OpenOptions{Strict: true})
	} else {
		sess, err = createLockedSession(store, cwd)
	}
	if err != nil {
		return fail(d, err)
	}
	controller := newSessionController(store, cwd)
	controller.Hold(sess)
	defer controller.Close()

	catalog := extensions.NewToolCatalog()
	for _, t := range toolSet {
		if err := catalog.Register(t); err != nil {
			return fail(d, err)
		}
	}
	commands := extensions.NewCommandCatalog()
	uiRegistry := extensionui.NewRegistry()

	runtime := d.ExtensionRuntime
	if runtime == nil {
		runtime = extensions.NewRuntime(extensions.NewRegistry())
	}
	host := newHostRuntime(ctx, cwd, controller, catalog)
	api := extensions.NewAPI(extensions.APIOptions{
		Catalog:       catalog,
		Commands:      commands,
		ResolveConfig: cfg.Default,
		UI:            uiRegistry,
		Host:          host.hostOptions(),
	})
	host.bindAPI(api)
	host.setExecLimits(time.Duration(cfg.ExecTimeoutSecs)*time.Second, cfg.MaxOutputBytes)
	host.setExecCwd(cfg.WorkspaceRoot)
	runtime.SetAPI(func() sdk.API { return api })
	runtime.SetUIRegistry(uiRegistry)
	runtime.SetContext(func() sdk.HandlerContext { return host.context() })

	initialName := ""
	if continuePath != "" {
		if loader, lerr := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true}); lerr == nil {
			initialName = sessionDisplayName(loader)
		}
	}
	recorder := &sessionRecorder{sess}
	if current := controller.Current(); current != nil && current.recorder != nil {
		recorder = current.recorder
	}
	host.bindSession(sess, recorder, sess.ID(), sess.Path(), cwd, initialName)

	snapshot, err := buildContentSnapshot(d, cfg.WorkspaceRoot, trustWorkspace)
	if err != nil {
		return fail(d, err)
	}
	promptCat := content.NewPromptCatalog(snapshot)
	skillOut := &switchWriter{target: d.Stdout}
	promptCommand := registerPromptHostCommand(commands, promptCat, skillOut)
	var promptAliases map[string]string
	host.setPromptExpander(func(input string) (string, error) {
		return (&runDeps{prompts: promptCat, promptAliases: promptAliases, promptCommand: promptCommand}).expandPromptInput(input)
	})

	if err := runtime.Start(); err != nil {
		return fail(d, err)
	}
	hooks := runtime.Dispatcher()

	skillCat, err := snapshotSkillCatalog(snapshot)
	if err != nil {
		return fail(d, err)
	}
	registerSkillCommand(commands, skillCat, skillOut)
	promptAliases = registerPromptAliases(commands, promptCat, promptShorthandReserved)

	resolveEnv := func(key string) (string, bool) {
		value := cfg.Default(key)
		if value == "" {
			return "", false
		}
		return value, true
	}
	if prepared.runner == nil {
		mcpCfg, workspaceMCP, err = loadMCPConfig(d.Home(), cwd)
		if err != nil {
			return fail(d, err)
		}
	}
	mcpRt, err := startMCP(ctx, mcpCfg, workspaceMCP, allowWorkspaceMCP && trustWorkspace, catalog, resolveEnv, d.Stderr)
	if err != nil {
		return fail(d, err)
	}
	defer mcpRt.Close()

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
	preparer, err := newContextPreparer(*cfg, window, cfg.Model, selector)
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
		SkipWorkspace: !trustWorkspace,
	})
	if err == nil {
		if suffix := instr.Suffix(); suffix != "" {
			sysPrompt = sysPrompt + "\n\n" + suffix
		}
	}

	providerID := selectedProvider
	if providerID == "" {
		providerID = openrouterProviderName
	}
	if !useTUI {
		_ = hooks.SessionStart(ctx, string(sdk.SessionStartStartup))
		defer hooks.SessionShutdown(ctx, string(sdk.SessionShutdownQuit))
	}
	host.setModel(modelReg, cfg.Model, cfg.Model, providerID)
	host.setSystem(sysPrompt)
	host.setWindow(window)
	host.attachPreparer(preparer)
	if continuePath != "" && prompt != "" {
		if loader, lerr := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true}); lerr == nil {
			if _, _, _, verr := projectModelHistoryWithIDs(loader); verr != nil {
				sess.Close()
				return fail(d, verr)
			}
		}
	}
	if continuePath != "" {
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
		wireModel:    cfg.Model,
		system:       sysPrompt,
		showThinking: envTruthy(d.Env("SMIDJA_SHOW_THINKING")),
		sessionPath:  sess.Path(),
		client:       client,
		tools:        toolSet,
		recorder:     &sessionRecorder{sess},
		stdout:       d.Stdout,
		stderr:       d.Stderr,
		store:        store,
		sess:         sess,
		cwd:          cwd,
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
		modelRegistry: modelReg,
		provider:      providerID,
		uiRegistry:    uiRegistry,
		prompts:       promptCat,
		promptAliases: promptAliases,
		promptCommand: promptCommand,
		host:          host,
	}
	rd.reprepare = func(model, wireModel string) (*contextPreparerAdapter, error) {
		built, err := newModelPreparer(*cfg, modelReg, model, wireModel, selector)
		if err != nil {
			return nil, err
		}
		host.attachPreparer(built)
		return built, nil
	}
	rd.controller = controller
	rd.resumedSession = continuePath != ""
	rd.persistModel = newModelPersister(controller, cfg, providerID, sysPrompt, catalog, toolSet, cfg.WorkspaceRoot, func() string { return snapshot.Fingerprint() })

	mode := sdk.ModeInteractive
	if prompt != "" {
		mode = sdk.ModePrint
	}
	lineUI := ui.New(d.Stdin, d.Stdout, d.Stderr, mode)

	runCtx := ctx
	if !useTUI {
		var cancelRun context.CancelFunc
		runCtx, cancelRun = context.WithCancel(ctx)
		defer host.waitCallbacks()
		defer host.waitCompacts()
		defer host.shutdown()
		host.bindRunContext(runCtx, cancelRun)
		host.setLifecycle(hostLifecycle{
			shutdown: cancelRun,
			runScheduled: func(turn hostScheduledTurn) {
				runScheduledHostTurn(runCtx, rd, turn)
			},
		})
	}

	host.waitMailbox()
	if prompt != "" {
		if continuePath != "" {
			err := runOnceContinued(runCtx, rd, sess, prompt)
			host.waitMailbox()
			if err != nil {
				return fail(d, err)
			}
			return nil
		}
		err := runOnce(runCtx, rd, prompt)
		host.waitMailbox()
		if err != nil {
			return fail(d, err)
		}
		return nil
	}
	if useTUI {
		rd.env = &sessionBuildEnv{
			cfg:         cfg,
			providerID:  providerID,
			system:      sysPrompt,
			tools:       toolSet,
			catalog:     catalog,
			modelReg:    modelReg,
			selector:    selector,
			fingerprint: func() string { return snapshot.Fingerprint() },
		}
		if err := runTUI(ctx, d, rd, lineUI, tuiMode, cfg.WorkspaceRoot, cwd, skillOut, nil, runtime, prepared.runner); err != nil {
			return fail(d, err)
		}
		return nil
	}
	err = repl(runCtx, lineUI, rd)
	host.waitMailbox()
	if err != nil {
		return fail(d, err)
	}
	return nil
}

func newModelPersister(controller *sessionController, cfg *config.Config, providerID, systemPrompt string, catalog agent.ToolCatalog, tools []agent.Tool, affinityRoot string, contentFingerprint func() string) func(string) error {
	return func(model string) error {
		active := controller.Current()
		if active == nil || active.sess == nil {
			return errors.New("session: no active session for the model change")
		}
		updated := *cfg
		updated.Model = model
		cur := currentRuntimeProfile(&updated, providerID, systemPrompt, toolsetFingerprint(catalog, tools), affinityRoot)
		_, err := syncRuntimeProfile(active.sess, cur, contentFingerprint)
		return err
	}
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

func buildContentSnapshot(d *Deps, workspaceRoot string, trustWorkspace bool) (content.Snapshot, error) {
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
		TrustWorkspace: trustWorkspace,
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

func newContextPreparer(cfg config.Config, window int64, wireModel string, selector subagent.Selector) (*contextPreparerAdapter, error) {
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
		cmCfg.SelectorModel = wireModel
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

func newModelPreparer(cfg config.Config, reg *models.Registry, model, wireModel string, selector subagent.Selector) (*contextPreparerAdapter, error) {
	updated := cfg
	updated.Model = model
	window := updated.ContextWindowTokens
	if window <= 0 {
		window = modelWindow(reg, model)
	}
	return newContextPreparer(updated, window, wireModel, selector)
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
	expanded, err := d.expandPromptInput(prompt)
	if err != nil {
		return err
	}
	out := &trailingWriter{w: d.stdout}
	deps := loopDeps(d, out)
	d.attachProjectedEntryIDs(deps)
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	history, err := runTurn(withHostTurnCancel(turnCtx, cancelTurn), d, deps, nil, expanded)
	if err != nil {
		return err
	}
	if d.host != nil {
		d.host.setMessages(history)
	}
	if !out.endsWithNewline() {
		fmt.Fprintln(out.w)
	}
	return nil
}

func runOnceContinued(ctx context.Context, d *runDeps, sess *session.Session, prompt string) error {
	expanded, err := d.expandPromptInput(prompt)
	if err != nil {
		return err
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		return err
	}
	history, entryIDs, _, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		return err
	}
	rd := *d
	if rd.sessionPath == "" {
		rd.sessionPath = sess.Path()
	}
	if rd.sess == nil {
		rd.sess = sess
	}
	out := &trailingWriter{w: rd.stdout}
	deps := loopDeps(&rd, out)
	deps.SessionEntryIDs = entryIDs
	rd.attachProjectedEntryIDs(deps)
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	updated, err := runTurn(withHostTurnCancel(turnCtx, cancelTurn), &rd, deps, history, expanded)
	if err != nil {
		return err
	}
	if rd.host != nil {
		rd.host.setMessages(updated)
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
		turnCtx, cancelTurn := context.WithCancel(ctx)
		history, err = runTurn(withHostTurnCancel(turnCtx, cancelTurn), d, loopDeps(d, out), history, input)
		cancelTurn()
		if err != nil {
			return err
		}
		if d.host != nil {
			d.host.setMessages(history)
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

func (d *runDeps) wireModelID() string {
	if strings.TrimSpace(d.wireModel) == "" {
		return d.model
	}
	return d.wireModel
}

func runTurn(ctx context.Context, d *runDeps, deps *agent.LoopDeps, history []*agent.Message, input string, scheduled ...hostScheduledTurn) (result []*agent.Message, resultErr error) {
	if d.host != nil {
		d.host.loopMu.Lock()
		defer d.host.loopMu.Unlock()
		if len(scheduled) > 0 && !d.host.scheduledTurnCurrent(scheduled[0]) {
			return history, nil
		}
		projected, entryIDs := d.host.modelHistorySnapshot()
		if projected != nil {
			history = projected
			deps.SessionEntryIDs = entryIDs
		}
		d.host.setMessages(history)
		d.host.beginTurn()
		defer func() {
			if err := d.host.endTurn(ctx.Err() != nil); resultErr == nil && err != nil {
				resultErr = err
			}
		}()
	}
	h, err := agent.RunTurn(ctx, deps, d.wireModelID(), d.system, history, input)
	if err != nil {
		var overflow *agent.ContextOverflowError
		if errors.As(err, &overflow) && d.preparer != nil {
			if d.stderr != nil {
				fmt.Fprintln(d.stderr, "smidja: context overflow, compacting and retrying once")
			}
			d.preparer.forceSafety()
			h, err = d.continueTurn(ctx, deps)
		}
	}
	if perr := d.persistCompactions(); perr != nil {
		return h, &persistError{err: perr}
	}
	return h, err
}

func runContinuation(ctx context.Context, d *runDeps, deps *agent.LoopDeps, history []*agent.Message, scheduled ...hostScheduledTurn) (result []*agent.Message, resultErr error) {
	if d.host != nil {
		d.host.loopMu.Lock()
		defer d.host.loopMu.Unlock()
		if len(scheduled) > 0 && !d.host.scheduledTurnCurrent(scheduled[0]) {
			return history, nil
		}
		projected, entryIDs := d.host.modelHistorySnapshot()
		if projected != nil {
			history = projected
			deps.SessionEntryIDs = entryIDs
		}
		d.host.setMessages(history)
		d.host.beginTurn()
		defer func() {
			if err := d.host.endTurn(ctx.Err() != nil); resultErr == nil && err != nil {
				resultErr = err
			}
		}()
	}
	updated, err := agent.ContinueTurn(ctx, deps, d.wireModelID(), d.system, history)
	if err != nil {
		var overflow *agent.ContextOverflowError
		if errors.As(err, &overflow) && d.preparer != nil {
			d.preparer.forceSafety()
			updated, err = d.continueTurn(ctx, deps)
		}
	}
	if persistErr := d.persistCompactions(); persistErr != nil {
		return updated, &persistError{err: persistErr}
	}
	return updated, err
}

func (d *runDeps) continueTurn(ctx context.Context, deps *agent.LoopDeps) ([]*agent.Message, error) {
	contHistory, contIDs, err := d.loadProjectedContext()
	if err != nil {
		return nil, err
	}
	contDeps := *deps
	contDeps.SessionEntryIDs = contIDs
	contDeps.RefreshSessionEntryIDs = d.refreshProjectedEntryIDs
	cont, err := agent.ContinueTurn(ctx, &contDeps, d.wireModelID(), d.system, contHistory)
	if err != nil {
		var again *agent.ContextOverflowError
		if errors.As(err, &again) {
			return cont, fmt.Errorf("context still overflows the model window after compaction: %w", err)
		}
	}
	return cont, err
}

func (d *runDeps) attachProjectedEntryIDs(deps *agent.LoopDeps) {
	if deps == nil || deps.RefreshSessionEntryIDs != nil {
		return
	}
	if d.controller == nil && d.sess == nil && d.sessionPath == "" {
		return
	}
	deps.RefreshSessionEntryIDs = d.refreshProjectedEntryIDs
}

func (d *runDeps) loadProjectedContext() ([]*agent.Message, []string, error) {
	if d.controller != nil {
		active, err := d.controller.Refresh()
		if err != nil {
			return nil, nil, fmt.Errorf("session: refresh the active session: %w", err)
		}
		return active.history, active.entryIDs, nil
	}
	path := d.sessionPath
	if d.sess != nil && d.sess.Path() != "" {
		path = d.sess.Path()
	}
	if path == "" {
		return nil, nil, errors.New("session: no active session")
	}
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		return nil, nil, fmt.Errorf("session: reload %q: %w", path, err)
	}
	projected, entryIDs, _, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		return nil, nil, fmt.Errorf("session: project %q: %w", path, err)
	}
	return projected, entryIDs, nil
}

func (d *runDeps) refreshProjectedEntryIDs(history []*agent.Message) ([]string, error) {
	_, entryIDs, err := d.loadProjectedContext()
	if err != nil {
		return nil, err
	}
	if len(entryIDs) != len(history) {
		return nil, fmt.Errorf("session: %d entry ids do not align with %d context messages", len(entryIDs), len(history))
	}
	return entryIDs, nil
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
		Recorder:          hostRecorder(d, d.recorder),
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
	if d.host != nil {
		deps.Mailbox = d.host.mailboxBoundary
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

type persistError struct {
	err error
}

func (e *persistError) Error() string { return e.err.Error() }

func (e *persistError) Unwrap() error { return e.err }

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
