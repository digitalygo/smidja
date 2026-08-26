package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/providers"
	"github.com/digitalygo/smidja/internal/providers/manifest"
	"github.com/digitalygo/smidja/internal/providers/oauth"
	"github.com/digitalygo/smidja/internal/providers/responses"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type oauthProvider struct {
	id      string
	name    string
	model   string
	login   func(context.Context, oauth.Options) (authstore.Entry, error)
	refresh func(context.Context, authstore.Entry, ...oauth.Options) (authstore.Entry, error)
	build   func(*oauthCredential, *http.Client) providers.Driver
}

var oauthProviders = []oauthProvider{
	{
		id: oauth.ProviderOpenRouter, name: "openrouter", model: "anthropic/claude-sonnet-4.5",
		login: oauth.OpenRouterLogin, refresh: oauth.OpenRouterRefresh,
		build: func(cred *oauthCredential, httpClient *http.Client) providers.Driver {
			return providers.NewOpenAICompletions(providers.Config{
				BaseURL:    "https://openrouter.ai/api/v1/chat/completions",
				Auth:       cred.resolve,
				ProviderID: oauth.ProviderOpenRouter,
				API:        "openai-completions",
			}, httpClient)
		},
	},
	{
		id: oauth.ProviderAnthropic, name: "anthropic", model: "claude-sonnet-4-6",
		login: oauth.AnthropicLogin, refresh: oauth.AnthropicRefresh,
		build: func(cred *oauthCredential, httpClient *http.Client) providers.Driver {
			return providers.NewAnthropic(providers.AnthropicConfig{
				BaseURL:    "https://api.anthropic.com/v1/messages",
				APIKey:     cred.resolve,
				OAuth:      true,
				ProviderID: oauth.ProviderAnthropic,
			}, httpClient)
		},
	},
	{
		id: oauth.ProviderCodex, name: "codex", model: "gpt-5.4",
		login: oauth.CodexLogin, refresh: oauth.CodexRefresh,
		build: func(cred *oauthCredential, httpClient *http.Client) providers.Driver {
			return responses.New(responses.Config{
				BaseURL:    "https://chatgpt.com/backend-api",
				Auth:       cred.resolve,
				ProviderID: oauth.ProviderCodex,
				API:        "openai-codex-responses",
				Codex:      true,
			}, httpClient)
		},
	},
	{
		id: oauth.ProviderXAI, name: "xai", model: "grok-4.6",
		login: oauth.XaiLogin, refresh: oauth.XaiRefresh,
		build: func(cred *oauthCredential, httpClient *http.Client) providers.Driver {
			return providers.NewOpenAICompletions(providers.Config{
				BaseURL:    "https://api.x.ai/v1/chat/completions",
				Auth:       cred.resolve,
				ProviderID: oauth.ProviderXAI,
				API:        "openai-completions",
			}, httpClient)
		},
	},
	{
		id: oauth.ProviderKimi, name: "kimi", model: "kimi-for-coding",
		login: oauth.KimiLogin, refresh: oauth.KimiRefresh,
		build: func(cred *oauthCredential, httpClient *http.Client) providers.Driver {
			return providers.NewAnthropic(providers.AnthropicConfig{
				BaseURL:    "https://api.kimi.com/coding/v1/messages",
				APIKey:     cred.resolve,
				ProviderID: oauth.ProviderKimi,
			}, httpClient)
		},
	},
}

func oauthProviderByID(id string) (oauthProvider, bool) {
	for _, p := range oauthProviders {
		if p.id == id || p.name == id {
			return p, true
		}
	}
	return oauthProvider{}, false
}

func oauthStoreKeyOrSelf(name string) string {
	if p, ok := oauthProviderByID(name); ok {
		return p.id
	}
	return name
}

func authStorePath(d *Deps) string {
	return filepath.Join(d.Home(), ".smidja", "auth.json")
}

func loadAuthStore(d *Deps) (*authstore.Store, error) {
	return authstore.Load(authStorePath(d))
}

func runAuth(args []string, d *Deps) error {
	if len(args) == 0 {
		printAuthUsage(d.Stderr)
		return fail(d, errors.New("auth: a subcommand is required"))
	}
	switch args[0] {
	case "login":
		return runAuthLogin(args[1:], d)
	case "logout":
		return runAuthLogout(args[1:], d)
	case "status":
		return runAuthStatus(args[1:], d)
	case "help", "-h", "-help", "--help":
		printAuthUsage(d.Stdout)
		return nil
	default:
		return fail(d, fmt.Errorf("auth: unknown subcommand %q", args[0]))
	}
}

func runAuthLogin(args []string, d *Deps) error {
	fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var apiKeyMode bool
	fs.BoolVar(&apiKeyMode, "api-key", false, "store an API key instead of running the OAuth flow")
	flags, positionals, err := splitSubcommandArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printAuthUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printAuthUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if len(positionals) != 1 {
		return fail(d, errors.New("auth login: exactly one provider argument is required"))
	}
	provider := positionals[0]
	if apiKeyMode {
		return authLoginAPIKey(d, provider)
	}
	return authLoginOAuth(d, provider)
}

func authLoginOAuth(d *Deps, provider string) error {
	p, ok := oauthProviderByID(provider)
	if !ok {
		return fail(d, fmt.Errorf("auth login: unknown provider %q", provider))
	}
	store, err := loadAuthStore(d)
	if err != nil {
		return fail(d, err)
	}
	entry, err := p.login(context.Background(), authOptionsFor(d, p))
	if err != nil {
		return fail(d, fmt.Errorf("auth login %s: %w", p.name, err))
	}
	if err := store.Set(p.id, entry); err != nil {
		return fail(d, fmt.Errorf("auth login %s: %w", p.name, err))
	}
	fmt.Fprintf(d.Stdout, "smidja: signed in to %s\n", p.name)
	return nil
}

func authLoginAPIKey(d *Deps, provider string) error {
	spec, ok := manifest.Lookup(provider)
	if !ok {
		return fail(d, fmt.Errorf("auth login: %q is not an API-key provider of the manifest", provider))
	}
	key := d.Env(spec.EnvVar)
	if key == "" {
		lineUI := ui.New(d.Stdin, d.Stdout, d.Stderr, sdk.ModeInteractive)
		entered, err := lineUI.Input("Paste the "+spec.EnvVar+" API key", "")
		if err != nil {
			return fail(d, fmt.Errorf("auth login %s: %w", provider, err))
		}
		key = strings.TrimSpace(entered)
		if key == "" {
			return fail(d, errors.New("auth login: empty API key"))
		}
	}
	store, err := loadAuthStore(d)
	if err != nil {
		return fail(d, err)
	}
	if err := store.Set(provider, authstore.Entry{Type: "api_key", Key: key}); err != nil {
		return fail(d, fmt.Errorf("auth login %s: %w", provider, err))
	}
	fmt.Fprintf(d.Stdout, "smidja: stored API key for %s\n", provider)
	return nil
}

func runAuthLogout(args []string, d *Deps) error {
	fs := flag.NewFlagSet("auth logout", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	flags, positionals, err := splitSubcommandArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printAuthUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printAuthUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if len(positionals) != 1 {
		return fail(d, errors.New("auth logout: exactly one provider argument is required"))
	}
	provider := positionals[0]
	store, err := loadAuthStore(d)
	if err != nil {
		return fail(d, err)
	}
	id := oauthStoreKeyOrSelf(provider)
	if _, ok := store.Get(id); !ok {
		fmt.Fprintf(d.Stdout, "smidja: no stored credential for %s\n", provider)
		return nil
	}
	if err := store.Remove(id); err != nil {
		return fail(d, fmt.Errorf("auth logout %s: %w", provider, err))
	}
	fmt.Fprintf(d.Stdout, "smidja: signed out of %s\n", provider)
	return nil
}

type statusRow struct {
	provider string
	kind     string
	status   string
}

func runAuthStatus(args []string, d *Deps) error {
	fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	flags, positionals, err := splitSubcommandArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printAuthUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printAuthUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if len(positionals) != 0 {
		return fail(d, fmt.Errorf("auth status: unexpected argument %q", positionals[0]))
	}
	store, err := loadAuthStore(d)
	if err != nil {
		return fail(d, err)
	}
	rows := make([]statusRow, 0, len(manifest.All)+len(oauthProviders))
	for _, spec := range manifest.All {
		rows = append(rows, statusRow{provider: spec.ID, kind: "api_key", status: manifestStatus(spec, store, d.Env)})
	}
	for _, p := range oauthProviders {
		rows = append(rows, statusRow{provider: p.id, kind: "oauth", status: oauthStatus(p, store)})
	}
	printStatusTable(d.Stdout, rows)
	return nil
}

func manifestStatus(spec manifest.Spec, store *authstore.Store, env func(string) string) string {
	envSet := env != nil && env(spec.EnvVar) != ""
	return combinedStatus(envSet, storeHasKey(store, spec.ID))
}

func oauthStatus(p oauthProvider, store *authstore.Store) string {
	return combinedStatus(false, storeHasAccess(store, p.id))
}

func combinedStatus(envSet, storeSet bool) string {
	switch {
	case envSet && storeSet:
		return "configured (env + store)"
	case envSet:
		return "configured (env)"
	case storeSet:
		return "configured (store)"
	default:
		return "not configured"
	}
}

func storeHasKey(store *authstore.Store, id string) bool {
	if store == nil {
		return false
	}
	e, ok := store.Get(id)
	return ok && e.Key != ""
}

func storeHasAccess(store *authstore.Store, id string) bool {
	if store == nil {
		return false
	}
	e, ok := store.Get(id)
	return ok && e.Access != ""
}

func printStatusTable(w io.Writer, rows []statusRow) {
	width := len("provider")
	for _, r := range rows {
		if len(r.provider) > width {
			width = len(r.provider)
		}
	}
	fmt.Fprintf(w, "%-*s  %-8s  %s\n", width, "provider", "type", "status")
	for _, r := range rows {
		fmt.Fprintf(w, "%-*s  %-8s  %s\n", width, r.provider, r.kind, r.status)
	}
}

func authOptionsFor(d *Deps, p oauthProvider) oauth.Options {
	if d != nil && d.AuthOptions != nil {
		return d.AuthOptions(p.id)
	}
	lineUI := ui.New(d.Stdin, d.Stdout, d.Stderr, sdk.ModeInteractive)
	return oauth.Options{
		OpenBrowser: openBrowserURL,
		ManualCode: func(ctx context.Context, prompt string) (string, error) {
			return lineUI.Input("Paste the authorization code or redirect URL", "")
		},
		DeviceCode: func(device oauth.DeviceCode) {
			fmt.Fprintf(d.Stderr, "smidja: open %s and enter code %s\n", device.VerificationURI, device.UserCode)
		},
	}
}

func openBrowserURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

type oauthCredential struct {
	mu      sync.Mutex
	store   *authstore.Store
	id      string
	name    string
	refresh func(context.Context, authstore.Entry, ...oauth.Options) (authstore.Entry, error)
	opts    []oauth.Options
}

func newOAuthCredential(store *authstore.Store, p oauthProvider, opts []oauth.Options) *oauthCredential {
	return &oauthCredential{store: store, id: p.id, name: p.name, refresh: p.refresh, opts: opts}
}

func (c *oauthCredential) resolve(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.store.Get(c.id)
	if !ok {
		return "", fmt.Errorf("no oauth credential for %s: run smidja auth login %s", c.name, c.name)
	}
	if entry.Expires > 0 && time.Now().UnixMilli() >= entry.Expires {
		refreshed, err := c.refresh(ctx, entry, c.opts...)
		if err != nil {
			return "", fmt.Errorf("%s: refresh token: %w", c.name, err)
		}
		if refreshed.Access == "" {
			return "", fmt.Errorf("%s: refresh returned an empty token", c.name)
		}
		if err := c.store.Set(c.id, refreshed); err != nil {
			return "", fmt.Errorf("%s: store refreshed token: %w", c.name, err)
		}
		entry = refreshed
	}
	return entry.Access, nil
}

func buildProviderClient(d *Deps, provider string) (agent.Client, error) {
	store, err := loadAuthStore(d)
	if err != nil {
		return nil, err
	}
	if p, ok := oauthProviderByID(provider); ok {
		if storeHasAccess(store, p.id) {
			return p.build(newOAuthCredential(store, p, nil), d.HTTPClient), nil
		}
		if _, ok := manifest.Lookup(provider); !ok {
			return nil, fmt.Errorf("no oauth credential for %s: run smidja auth login %s", p.name, p.name)
		}
	}
	drv, err := manifest.Build(provider, manifest.Deps{Env: d.Env, Store: store, HTTP: d.HTTPClient})
	if err != nil {
		return nil, err
	}
	return drv, nil
}

func providerDefaultModel(provider string) (string, bool) {
	if p, ok := oauthProviderByID(provider); ok {
		return p.model, true
	}
	if spec, ok := manifest.Lookup(provider); ok {
		return spec.DefaultModel, true
	}
	return "", false
}

func printAuthUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: smidja auth <login|logout|status> [args]

Manage provider credentials in ~/.smidja/auth.json. Environment variables
always win over stored credentials at request time.

commands:
  login <provider> [--api-key]  run the provider OAuth flow, or store an
                                API key read from stdin or the provider
                                environment variable with --api-key
  logout <provider>             remove the stored credential
  status                        show which providers are configured

OAuth providers: openrouter, anthropic, codex, xai, kimi
API-key providers: any provider of the manifest, for example deepseek or openai
`)
}
