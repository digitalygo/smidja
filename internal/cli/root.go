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
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/update"
)

// Version is the build version printed by -version and the version
// subcommand. cmd/smidja injects its link-time version here; the zero
// value is "dev".
var Version = "dev"

// cliDeps carries the process seams Run is wired to. Tests replace every
// field with a fake so the whole CLI runs without touching the real
// process environment.
type cliDeps struct {
	env    func(string) string
	getwd  func() (string, error)
	home   func() string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	// fetchModels refreshes the model catalogue from the live OpenRouter
	// endpoint at startup, best-effort and non-fatal. Nil disables the
	// refresh (tests); Run wires the real fetch.
	fetchModels func(ctx context.Context) ([]models.ModelInfo, error)

	// newUpdateClient builds the self-update client for the update
	// subcommand. Nil uses the default client (real GitHub API, running
	// binary path); tests substitute a client pointed at an httptest
	// server.
	newUpdateClient func() *update.Client
}

// Run is the CLI entry point. args holds the command-line arguments
// without the program name (os.Args[1:]). On failure it prints
// "smidja: <err>" to stderr and returns a non-nil error; cmd/smidja maps a
// non-nil return to exit status 1. A nil return means a clean exit.
func Run(args []string) error {
	return run(args, &cliDeps{
		env:   os.Getenv,
		getwd: os.Getwd,
		home: func() string {
			h, err := os.UserHomeDir()
			if err != nil {
				return ""
			}
			return h
		},
		stdin:  os.Stdin,
		stdout: os.Stdout,
		stderr: os.Stderr,
		fetchModels: func(ctx context.Context) ([]models.ModelInfo, error) {
			return models.FetchOpenRouterModels(ctx, nil)
		},
	})
}

// run parses args and dispatches: a leading subcommand name goes to
// runSubcommand, otherwise the flags are parsed and the chat path (single
// shot or REPL) runs. It is separated from Run so tests can substitute the
// process seams.
func run(args []string, d *cliDeps) error {
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
			printUsage(d.stderr)
			return nil
		}
		fmt.Fprintf(d.stderr, "smidja: %v\n", err)
		printUsage(d.stderr)
		return err
	}
	if version {
		fmt.Fprintf(d.stdout, "smidja %s\n", Version)
		return nil
	}
	if fs.NArg() > 0 {
		// A stray positional argument is not a valid invocation (the
		// subcommands dispatch before flag parsing); reject it with usage
		// instead of silently starting a session.
		err := fmt.Errorf("unexpected argument %q", fs.Arg(0))
		fmt.Fprintf(d.stderr, "smidja: %v\n", err)
		printUsage(d.stderr)
		return err
	}
	return runChat(d, prompt, model, system)
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
func runSubcommand(name string, args []string, d *cliDeps) error {
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
func fail(d *cliDeps, err error) error {
	fmt.Fprintf(d.stderr, "smidja: %v\n", err)
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
