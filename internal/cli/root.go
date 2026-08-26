// Package cli implements the smidja command-line interface: flag parsing,
// subcommand dispatch, session creation, and the wiring that connects the
// config, workspace, openrouter, tools, and session packages to the agent
// loop.
//
// Run is the thin entry point. root.go owns argument parsing and dispatch
// to the subcommands (run, import, update, version) and to the chat path;
// chat.go owns the single-shot (-p) and interactive (REPL) execution,
// which accept the runtime pieces as interfaces so tests can drive them
// with injected fakes.
//
// RunWithDeps is the injection seam for the harness composition: it
// accepts a Deps with the process seams and optional pre-built components
// (config, client, tools, session store, model registry, extension
// runtime) plus the bundle and build identity, and consumes them instead
// of building its own. Run builds the default deps (real process
// environment, no bundle).
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/buildinfo"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/update"
	"github.com/digitalygo/smidja/sdk"
)

// Version is the build version printed by -version and the version
// subcommand when Deps carries no build identity. cmd/smidja injects its
// link-time version here; the zero value is "dev".
var Version = "dev"

// Deps carries the process seams, the bundle, and the optional pre-built
// components the CLI wiring uses. Run builds the default deps (real
// process environment, no bundle); smidja.Run composes the harness from a
// bundle and injects the pre-built config, client, tools, session store,
// model registry, and extension runtime.
//
// The process-seam fields (Env, Getwd, Home, Stdin, Stdout, Stderr) are
// required: RunWithDeps fills them with the real process values when nil.
// Every component field is optional: a nil or empty value makes the CLI
// build that component itself from the config, keeping Run's behavior
// identical to the pre-injection wiring. The context manager and the
// LineUI are not in Deps: they depend on parse-time state (the -model
// override and the -p mode), so the CLI always assembles them from the
// injected config, client, and registry.
type Deps struct {
	// Context is the base context of the invocation; the session runs on
	// it. Nil means context.Background.
	Context context.Context

	// Env resolves environment variables.
	Env func(string) string
	// Getwd resolves the current working directory.
	Getwd func() (string, error)
	// Home resolves the user's home directory.
	Home func() string
	// Stdin is the session input stream.
	Stdin io.Reader
	// Stdout is the session output stream.
	Stdout io.Writer
	// Stderr is the diagnostics stream.
	Stderr io.Writer

	// FetchModels refreshes the model catalogue from the live OpenRouter
	// endpoint at startup, best-effort and non-fatal. Nil disables the
	// refresh (tests); Run and smidja.Run wire the real fetch.
	FetchModels func(ctx context.Context) ([]models.ModelInfo, error)

	// NewUpdateClient builds the self-update client for the update
	// subcommand. Nil uses the default client (real GitHub API, running
	// binary path); tests substitute a client pointed at an httptest
	// server.
	NewUpdateClient func() *update.Client

	// Bundle is the packaged contents of the build: embedded resources,
	// extensions, and configuration defaults. The bare harness ships an
	// empty Bundle. The bundle's FS is carried through for future
	// content resolution; the CLI does not consume it yet.
	Bundle sdk.Bundle

	// BuildInfo is the build identity of the binary: origin, version,
	// and commit. -version and the version subcommand print it, and the
	// self-update client targets the build's origin repository. When
	// Version is empty the CLI falls back to the Version package
	// variable and buildinfo.Current().
	BuildInfo sdk.BuildInfo

	// Config is the resolved runtime configuration, loaded with the
	// bundle's ConfigDefaults when present. Nil makes the CLI load it
	// from Env, Getwd, and Home.
	Config *config.Config

	// Client is the provider client used for assistant turns. Nil makes
	// the CLI build an OpenRouter client from the config.
	Client agent.Client

	// Tools is the tool set handed to the agent loop. Empty makes the
	// CLI build the built-in tools over the workspace rooted at
	// Config.WorkspaceRoot.
	Tools []agent.Tool

	// Store is the session store. Nil makes the CLI create it at
	// Config.SessionDir.
	Store *session.Store

	// ModelRegistry is the seeded model catalogue. Nil makes the CLI
	// build the default registry (the curated fallback table).
	ModelRegistry *models.Registry

	// ExtensionRuntime is the extension runtime with the bundle's
	// extensions registered. Nil makes the CLI build an empty runtime.
	// The runtime is started by the CLI, exactly once per invocation.
	ExtensionRuntime *extensions.Runtime
}

// Run is the CLI entry point. args holds the command-line arguments
// without the program name (os.Args[1:]). On failure it prints
// "smidja: <err>" to stderr and returns a non-nil error; cmd/smidja maps a
// non-nil return to exit status 1. A nil return means a clean exit.
func Run(args []string) error {
	return RunWithDeps(args, &Deps{
		FetchModels: fetchOpenRouterModels,
	})
}

// fetchOpenRouterModels refreshes the model catalogue from the live
// OpenRouter endpoint at startup.
func fetchOpenRouterModels(ctx context.Context) ([]models.ModelInfo, error) {
	return models.FetchOpenRouterModels(ctx, nil)
}

// RunWithDeps is Run with an injected Deps. Process seams left nil are
// filled with the real process values (os.Getenv, os.Getwd, the user home
// directory, os.Stdin, os.Stdout, os.Stderr); optional components left
// nil keep the CLI's self-built defaults. deps may be nil, equivalent to
// an empty Deps (no model fetch, no bundle).
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

// run parses args and dispatches: a leading subcommand name goes to
// runSubcommand, otherwise the flags are parsed and the chat path (single
// shot or REPL) runs. It is separated from RunWithDeps so tests can
// substitute the process seams.
func run(args []string, d *Deps) error {
	if len(args) > 0 && isSubcommand(args[0]) {
		return runSubcommand(args[0], args[1:], d)
	}

	fs := flag.NewFlagSet("smidja", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // run renders flag errors itself, once
	fs.Usage = func() {}     // usage is printed by run, not by flag
	var (
		prompt  string
		model   string
		system  string
		version bool
	)
	fs.StringVar(&prompt, "p", "", "run one turn with the given prompt and exit")
	fs.StringVar(&model, "model", "", "override the configured model")
	fs.StringVar(&system, "system", "", "override the default system prompt")
	fs.BoolVar(&version, "version", false, "print the version and exit")
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
		// A stray positional argument is not a valid invocation (the
		// subcommands dispatch before flag parsing); reject it with usage
		// instead of silently starting a session.
		err := fmt.Errorf("unexpected argument %q", fs.Arg(0))
		fmt.Fprintf(d.Stderr, "smidja: %v\n", err)
		printUsage(d.Stderr)
		return err
	}
	return runChat(d, prompt, model, system)
}

// versionFor resolves the version string printed by -version and the
// version subcommand: the injected build identity first, then the Version
// package variable (cmd/smidja's link-time injection), then the buildinfo
// package identity.
func versionFor(d *Deps) string {
	if d != nil && d.BuildInfo.Version != "" {
		return d.BuildInfo.Version
	}
	if Version != "" {
		return Version
	}
	return buildinfo.Current().Version
}

// buildIdentity resolves the full build identity reported by "version
// --json" and used by the self-update client: the injected build info
// when the caller provides one, otherwise the buildinfo package identity
// as injected at link time.
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

// subcommands is the set of top-level subcommands the root parser
// dispatches. The check happens before flag parsing so that "smidja run"
// reaches the subcommand handler instead of being swallowed as a stray
// positional argument.
var subcommands = map[string]bool{
	"run":     true,
	"import":  true,
	"update":  true,
	"version": true,
}

// isSubcommand reports whether name is a registered top-level subcommand.
func isSubcommand(name string) bool {
	return subcommands[name]
}

// runSubcommand dispatches one top-level subcommand. The chat subcommand
// (plain invocation, -p, REPL) is handled by run itself; the remaining
// subcommands are implemented in commands.go, import.go, and update.go,
// except run, which remains a placeholder for a future explicit
// single-shot subcommand.
func runSubcommand(name string, args []string, d *Deps) error {
	switch name {
	case "version":
		return runVersion(args, d)
	case "import":
		return runImport(args, d)
	case "update":
		return runUpdate(args, d)
	case "run":
		return fail(d, fmt.Errorf("%s: not implemented yet", name))
	default:
		return fail(d, fmt.Errorf("unknown subcommand %q", name))
	}
}

// fail prints the standard "smidja: <err>" line to stderr and returns a
// non-nil error so cmd/smidja exits 1 without printing again.
func fail(d *Deps, err error) error {
	fmt.Fprintf(d.Stderr, "smidja: %v\n", err)
	return err
}

// printUsage writes the command usage to w.
func printUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: smidja [flags] [-p prompt] [subcommand]

Smidja is an agentic coding harness. With no arguments it starts an
interactive session that reads prompts from stdin and streams responses
to stdout. With -p it runs a single turn and exits.

flags:
  -p prompt      run one turn with the given prompt and exit
  -model string  override the configured model (default: SMIDJA_MODEL)
  -system string override the default system prompt
  -version       print "smidja <version>" and exit

subcommands:
  import   import Pi sessions into the session store
  run      run a single turn, not implemented yet
  update   update the harness binary from GitHub releases
  version  print the version; use --json for the full build identity
`)
}
