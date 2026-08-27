package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/gateway"
	"github.com/digitalygo/smidja/internal/gateway/telegram"
	"github.com/digitalygo/smidja/internal/gateway/web"
	"github.com/digitalygo/smidja/internal/loopdetector"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/openrouter"
	"github.com/digitalygo/smidja/internal/retry"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/subagent"
	"github.com/digitalygo/smidja/internal/tools"
	"github.com/digitalygo/smidja/internal/workspace"
	"github.com/digitalygo/smidja/sdk"
)

const defaultGatewayListen = "127.0.0.1:8179"

type gatewayServerOptions struct {
	model             string
	system            string
	provider          string
	noWeb             bool
	webAddr           string
	allowWorkspaceMCP bool
}

func runGateway(args []string, d *Deps) error {
	fs := flag.NewFlagSet("gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var opts gatewayServerOptions
	fs.StringVar(&opts.model, "model", "", "override the configured model")
	fs.StringVar(&opts.system, "system", "", "override the default system prompt")
	fs.StringVar(&opts.provider, "provider", "", "select the provider driver (manifest id or OAuth provider)")
	fs.BoolVar(&opts.noWeb, "no-web", false, "disable the web server")
	fs.StringVar(&opts.webAddr, "web-addr", "", "web server listen address (default "+defaultGatewayListen+")")
	fs.BoolVar(&opts.allowWorkspaceMCP, "allow-workspace-mcp", false, "spawn MCP servers defined in the workspace .smidja/mcp.json")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printGatewayUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if fs.NArg() > 0 {
		return fail(d, fmt.Errorf("gateway: unexpected argument %q", fs.Arg(0)))
	}
	return runGatewayServer(d, opts)
}

func runGatewayServer(d *Deps, opts gatewayServerOptions) error {
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
	if opts.model != "" {
		cfg.Model = opts.model
	}
	var client agent.Client
	if opts.provider != "" {
		built, err := buildProviderClient(d, opts.provider)
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
	home := d.Home()
	gatewayDir := d.Env("SMIDJA_GATEWAY_DIR")
	if gatewayDir == "" {
		gatewayDir = filepath.Join(home, ".smidja", "gateway")
	}
	bindings, err := loadBindings(filepath.Join(gatewayDir, "bindings.json"))
	if err != nil {
		return fail(d, err)
	}
	webWorkspaces := map[string]string{"default": cfg.WorkspaceRoot}
	resolveWorkspace := func(chatKey string) string {
		return workspaceRootForChat(chatKey, cfg.WorkspaceRoot, webWorkspaces)
	}
	resolver := gateway.Resolver(func(key string) (string, string) {
		root := workspaceRootForChat(key, cfg.WorkspaceRoot, webWorkspaces)
		if path, ok := bindings.lookup(key); ok && fileExists(path) {
			return root, path
		}
		return root, ""
	})

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

	dirs, err := activePackageDirs(d)
	if err != nil {
		return fail(d, err)
	}
	var snapMu sync.Mutex
	snapshots := make(map[string]content.Snapshot)
	loadSnapshot := func(root string) content.Snapshot {
		snapMu.Lock()
		defer snapMu.Unlock()
		if snap, ok := snapshots[root]; ok {
			return snap
		}
		snap, err := content.Load(content.Options{
			BundleID:       d.Bundle.ID,
			BundleFS:       d.Bundle.FS,
			WorkspaceDir:   root,
			UserHome:       home,
			PackagesDirs:   dirs,
			TrustWorkspace: true,
		})
		if err != nil {
			return content.Snapshot{}
		}
		snapshots[root] = snap
		return snap
	}
	contentFingerprint := func(root string) string {
		return loadSnapshot(root).Fingerprint()
	}
	skillCat, err := snapshotSkillCatalog(loadSnapshot(cfg.WorkspaceRoot))
	if err != nil {
		return fail(d, err)
	}
	registerSkillCommand(commands, skillCat, d.Stderr)

	resolveEnv := func(key string) (string, bool) {
		value := cfg.Default(key)
		if value == "" {
			return "", false
		}
		return value, true
	}
	mcpCfg, workspaceMCP, err := loadMCPConfig(home, cfg.WorkspaceRoot)
	if err != nil {
		return fail(d, err)
	}
	mcpRt, err := startMCP(ctx, mcpCfg, workspaceMCP, opts.allowWorkspaceMCP, catalog, resolveEnv, d.Stderr)
	if err != nil {
		return fail(d, err)
	}
	defer mcpRt.Close()

	modelReg := d.ModelRegistry
	if modelReg == nil {
		modelReg = models.NewRegistry()
	}
	refreshModelRegistry(ctx, d, cfg, modelReg)

	window := cfg.ContextWindowTokens
	if window <= 0 {
		window = modelWindow(modelReg, cfg.Model)
	}
	selector := subagent.NewOpenRouterSelector(client)
	preparer, err := newContextPreparer(*cfg, window, selector)
	if err != nil {
		return fail(d, err)
	}

	sysPrompt := opts.system
	if sysPrompt == "" {
		sysPrompt = defaultSystemPrompt
	}
	providerID := opts.provider
	if providerID == "" {
		providerID = "openrouter"
	}
	runner := newGatewayRunner(gatewayRunnerConfig{
		cfg:                cfg,
		providerID:         providerID,
		model:              cfg.Model,
		system:             sysPrompt,
		home:               home,
		store:              store,
		bindings:           bindings,
		workspace:          resolveWorkspace,
		client:             client,
		tools:              toolSet,
		catalog:            catalog,
		hooks:              hooks,
		preparer:           preparer,
		retry:              retryAdapter,
		isOverflow:         retry.IsContextOverflow,
		detector:           newLoopDetectorAdapter(loopdetector.New(loopdetector.DefaultConfig())),
		contentFingerprint: contentFingerprint,
		showThinking:       envTruthy(d.Env("SMIDJA_SHOW_THINKING")),
		stdout:             d.Stderr,
		stderr:             d.Stderr,
	})

	g, err := gateway.New(gateway.Options{
		Dir:      gatewayDir,
		Resolver: resolver,
		Runner:   runner,
	})
	if err != nil {
		return fail(d, err)
	}

	authStore, err := loadAuthStore(d)
	if err != nil {
		return fail(d, err)
	}
	telegramOn := false
	var telegramCancel context.CancelFunc
	if _, ok := authstore.ResolveCredential("telegram", "TELEGRAM_BOT_TOKEN", authStore, d.Env); ok {
		telegramOn = true
		allowed := parseAllowedUserIDs(d.Env("SMIDJA_TELEGRAM_ALLOWED_IDS"))
		if len(allowed) == 0 {
			fmt.Fprintf(d.Stderr, "smidja: telegram: no allowed user ids configured via SMIDJA_TELEGRAM_ALLOWED_IDS, denying all users\n")
		}
		tr := telegram.New(telegram.Options{
			Gateway:        g,
			Token:          telegram.TokenFromAuth(authStore, d.Env),
			AllowedUserIDs: allowed,
			APIBase:        d.Env("SMIDJA_TELEGRAM_API_BASE"),
		})
		tctx, tcancel := context.WithCancel(ctx)
		telegramCancel = tcancel
		go func() {
			if err := tr.Start(tctx); err != nil {
				fmt.Fprintf(d.Stderr, "smidja: telegram: %v\n", err)
			}
		}()
	}

	gctx, cancelAll := context.WithCancel(ctx)
	if err := g.Start(gctx); err != nil {
		cancelAll()
		return fail(d, err)
	}

	var httpSrv *http.Server
	webURL := "off"
	if !opts.noWeb {
		addr := opts.webAddr
		if addr == "" {
			addr = defaultGatewayListen
		}
		webSrv, err := web.New(web.Config{
			ListenAddr: addr,
			Gateway:    g,
			Workspaces: webWorkspaces,
		})
		if err != nil {
			cancelAll()
			return fail(d, err)
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			cancelAll()
			return fail(d, err)
		}
		webURL = "http://" + ln.Addr().String()
		httpSrv = &http.Server{Handler: webSrv.Handler()}
		go func() {
			if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintf(d.Stderr, "smidja: web: %v\n", err)
			}
		}()
	}

	telegramState := "off"
	if telegramOn {
		telegramState = "on"
	}
	fmt.Fprintf(d.Stderr, "gateway listening telegram=%s web=%s\n", telegramState, webURL)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-ctx.Done():
	case <-sigCh:
	}
	cancelAll()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var shutdownWG sync.WaitGroup
	if httpSrv != nil {
		shutdownWG.Add(1)
		go func() {
			defer shutdownWG.Done()
			_ = httpSrv.Shutdown(shutdownCtx)
		}()
	}
	shutdownErr := g.Shutdown(shutdownCtx)
	if telegramCancel != nil {
		telegramCancel()
	}
	shutdownWG.Wait()
	return shutdownErr
}

func refreshModelRegistry(ctx context.Context, d *Deps, cfg *config.Config, modelReg *models.Registry) {
	if d.FetchModels != nil {
		fctx, cancel := context.WithTimeout(ctx, modelFetchTimeout)
		infos, ferr := d.FetchModels(fctx)
		cancel()
		if ferr == nil {
			modelReg.Merge(infos)
		}
	}
	if d.ModelsCatalog != nil {
		storePath := models.StorePathFor(cfg.SessionDir)
		_ = d.ModelsCatalog.RefreshTo(storePath)
		if store, err := models.LoadStore(storePath); err == nil {
			modelReg.MergeRefreshed(store, localModelOverrides(d, cfg.WorkspaceRoot))
		}
	}
}

func workspaceRootForChat(chatKey, defaultRoot string, webWorkspaces map[string]string) string {
	if strings.HasPrefix(chatKey, "web:") && len(webWorkspaces) == 1 {
		for _, root := range webWorkspaces {
			return root
		}
	}
	return defaultRoot
}

func parseAllowedUserIDs(raw string) []int64 {
	var out []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, err := strconv.ParseInt(part, 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func printGatewayUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: smidja gateway [flags]

Run the headless gateway: it exposes the kernel over Telegram (when a bot
token is configured) and a local web server. Each chat key keeps its own
persistent session across gateway restarts.

flags:
  -model string       override the configured model
  -provider id        select the provider driver
  -system string      override the default system prompt
  -no-web             disable the web server
  -web-addr string    web server listen address (default 127.0.0.1:8179)
  -allow-workspace-mcp
                      spawn MCP servers defined in .smidja/mcp.json

environment:
  TELEGRAM_BOT_TOKEN  enables the Telegram transport
  SMIDJA_TELEGRAM_ALLOWED_IDS
                      comma-separated Telegram user ids allowed to talk
  SMIDJA_TELEGRAM_API_BASE
                      Telegram bot API base URL override (tests, proxies)
  SMIDJA_WEB_TOKEN    web login token
  SMIDJA_GATEWAY_DIR  journal and chat bindings directory
                      (default: ~/.smidja/gateway)
`)
}
