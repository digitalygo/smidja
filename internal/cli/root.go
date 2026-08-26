package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/buildinfo"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/packages"
	"github.com/digitalygo/smidja/internal/providers/oauth"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/update"
	"github.com/digitalygo/smidja/sdk"
)

var Version = "dev"

type Deps struct {
	Context context.Context

	Env    func(string) string
	Getwd  func() (string, error)
	Home   func() string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	FetchModels func(ctx context.Context) ([]models.ModelInfo, error)

	NewUpdateClient func() *update.Client

	HTTPClient *http.Client

	AuthOptions func(provider string) oauth.Options

	Bundle sdk.Bundle

	BuildInfo sdk.BuildInfo

	Config *config.Config

	Client agent.Client

	Tools []agent.Tool

	Store *session.Store

	ModelRegistry *models.Registry

	ExtensionRuntime *extensions.Runtime

	FetchArchive packages.FetchArchive

	FetchLatestVersion func(owner, repo string) (string, error)
}

func Run(args []string) error {
	return RunWithDeps(args, &Deps{
		FetchModels: fetchOpenRouterModels,
	})
}

func fetchOpenRouterModels(ctx context.Context) ([]models.ModelInfo, error) {
	return models.FetchOpenRouterModels(ctx, nil)
}

func RunWithDeps(args []string, deps *Deps) error {
	d := &Deps{}
	if deps != nil {
		*d = *deps
	}
	if d.Context == nil {
		d.Context = context.Background()
	}
	if d.Env == nil {
		d.Env = os.Getenv
	}
	if d.Getwd == nil {
		d.Getwd = os.Getwd
	}
	if d.Home == nil {
		d.Home = func() string {
			h, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			return h
		}
	}
	if d.Stdin == nil {
		d.Stdin = os.Stdin
	}
	if d.Stdout == nil {
		d.Stdout = os.Stdout
	}
	if d.Stderr == nil {
		d.Stderr = os.Stderr
	}
	return run(args, d)
}

func run(args []string, d *Deps) error {
	if len(args) > 0 && isSubcommand(args[0]) {
		return runSubcommand(args[0], args[1:], d)
	}

	fs := flag.NewFlagSet("smidja", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var (
		prompt   string
		model    string
		system   string
		provider string
		version  bool
	)
	fs.StringVar(&prompt, "p", "", "run one turn with the given prompt and exit")
	fs.StringVar(&model, "model", "", "override the configured model")
	fs.StringVar(&system, "system", "", "override the default system prompt")
	fs.StringVar(&provider, "provider", "", "select the provider driver (manifest id or OAuth provider)")
	fs.BoolVar(&version, "version", false, "print the version and exit")
	var allowWorkspaceMCP bool
	fs.BoolVar(&allowWorkspaceMCP, "allow-workspace-mcp", false, "spawn MCP servers defined in the workspace .smidja/mcp.json")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(d.Stderr)
			return nil
		}
		fmt.Fprintf(d.Stderr, "smidja: %v\n", err)
		printUsage(d.Stderr)
		return err
	}
	if version {
		fmt.Fprintf(d.Stdout, "smidja %s\n", versionFor(d))
		return nil
	}
	if fs.NArg() > 0 {
		err := fmt.Errorf("unexpected argument %q", fs.Arg(0))
		fmt.Fprintf(d.Stderr, "smidja: %v\n", err)
		printUsage(d.Stderr)
		return err
	}
	if provider != "" && model == "" && d.Env("SMIDJA_MODEL") == "" {
		if def, ok := providerDefaultModel(provider); ok {
			model = def
		}
	}
	return runChat(d, prompt, model, system, provider, allowWorkspaceMCP)
}

func versionFor(d *Deps) string {
	if d != nil && d.BuildInfo.Version != "" {
		return d.BuildInfo.Version
	}
	if Version != "" {
		return Version
	}
	return buildinfo.Current().Version
}

func buildIdentity(d *Deps) buildinfo.Info {
	if d != nil && d.BuildInfo.Version != "" {
		return buildinfo.Info{
			Origin:  d.BuildInfo.Origin,
			Version: d.BuildInfo.Version,
			Commit:  d.BuildInfo.Commit,
		}
	}
	return buildinfo.Current()
}

var subcommands = map[string]bool{
	"run":     true,
	"auth":    true,
	"import":  true,
	"update":  true,
	"version": true,
	"pkg":     true,
}

func isSubcommand(name string) bool {
	return subcommands[name]
}

func runSubcommand(name string, args []string, d *Deps) error {
	switch name {
	case "version":
		return runVersion(args, d)
	case "auth":
		return runAuth(args, d)
	case "import":
		return runImport(args, d)
	case "update":
		return runUpdate(args, d)
	case "pkg":
		return runPkg(args, d)
	case "run":
		return fail(d, fmt.Errorf("%s: not implemented yet", name))
	default:
		return fail(d, fmt.Errorf("unknown subcommand %q", name))
	}
}

func fail(d *Deps, err error) error {
	fmt.Fprintf(d.Stderr, "smidja: %v\n", err)
	return err
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: smidja [flags] [-p prompt] [subcommand]

Smidja is an agentic coding harness. With no arguments it starts an
interactive session that reads prompts from stdin and streams responses
to stdout. With -p it runs a single turn and exits.

flags:
  -p prompt       run one turn with the given prompt and exit
  -model string   override the configured model (default: SMIDJA_MODEL)
  -provider id    select the provider driver (default: openrouter)
  -system string  override the default system prompt
  -version        print "smidja <version>" and exit
  -allow-workspace-mcp
                  spawn MCP servers defined in .smidja/mcp.json
                  (default: only ~/.smidja/mcp.json servers run)

subcommands:
  auth     manage provider credentials
  import   import Pi sessions into the session store
  pkg      manage optional packages (install, list, inspect, activate,
           deactivate, update, verify, uninstall)
  run      run a single turn, not implemented yet
  update   update the harness binary from GitHub releases
  version  print the version; use --json for the full build identity
`)
}
