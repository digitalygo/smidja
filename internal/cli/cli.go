// Package cli implements the smidja command-line interface: flag parsing,
// session creation, and the wiring that connects the config, workspace,
// openrouter, tools, and session packages to the agent loop.
//
// Run is the thin entry point. The single-shot (-p) and interactive
// (REPL) paths live in runOnce and repl, which accept the runtime pieces
// as interfaces so tests can drive them with injected fakes.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/openrouter"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tools"
	"github.com/digitalygo/smidja/internal/workspace"
)

// Version is the build version printed by -version. cmd/smidja injects its
// link-time version here; the zero value is "dev".
var Version = "dev"

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
}

// runDeps carries the runtime pieces one turn (or one REPL iteration)
// needs. Every field is an interface or a plain value, so tests can inject
// a fake client, recorder, and writers directly into runOnce and repl.
type runDeps struct {
	model        string
	system       string
	showThinking bool
	sessionPath  string

	client   agent.Client
	tools    []agent.Tool
	recorder agent.Recorder
	stdout   io.Writer
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
	})
}

// run parses args and dispatches to the single-shot or interactive path.
// It is separated from Run so tests can substitute the process seams.
func run(args []string, d *cliDeps) error {
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

	cfg, err := config.Load(d.env, d.getwd, d.home)
	if err != nil {
		return fail(d, err)
	}
	if model != "" {
		cfg.Model = model
	}
	ws, err := workspace.New(cfg.WorkspaceRoot)
	if err != nil {
		return fail(d, err)
	}
	client := openrouter.New(cfg.OpenRouterURL, cfg.APIKey, nil)
	toolSet := tools.All(tools.Deps{
		Workspace:      ws,
		ExecTimeoutSec: cfg.ExecTimeoutSecs,
		MaxOutputBytes: cfg.MaxOutputBytes,
	})
	store, err := session.NewStore(cfg.SessionDir)
	if err != nil {
		return fail(d, err)
	}
	cwd, err := d.getwd()
	if err != nil {
		return fail(d, err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		return fail(d, err)
	}
	defer sess.Close()

	sysPrompt := system
	if sysPrompt == "" {
		sysPrompt = defaultSystemPrompt
	}

	rd := &runDeps{
		model:        cfg.Model,
		system:       sysPrompt,
		showThinking: envTruthy(d.env("SMIDJA_SHOW_THINKING")),
		sessionPath:  sess.Path(),
		client:       client,
		tools:        toolSet,
		recorder:     &sessionRecorder{sess},
		stdout:       d.stdout,
	}

	if prompt != "" {
		if err := runOnce(context.Background(), rd, prompt); err != nil {
			return fail(d, err)
		}
		return nil
	}
	if err := repl(context.Background(), d.stdin, rd); err != nil {
		return fail(d, err)
	}
	return nil
}

// fail prints the standard "smidja: <err>" line to stderr and returns a
// non-nil error so cmd/smidja exits 1 without printing again.
func fail(d *cliDeps, err error) error {
	fmt.Fprintf(d.stderr, "smidja: %v\n", err)
	return err
}

// runOnce executes a single assistant turn for prompt and exits cleanly.
// It is separated from run so tests can drive it with an injected client,
// recorder, and writer. The response is streamed to d.stdout by the loop
// as it is generated; a final newline is appended when the stream did not
// end with one.
func runOnce(ctx context.Context, d *runDeps, prompt string) error {
	out := &trailingWriter{w: d.stdout}
	if _, err := agent.RunTurn(ctx, loopDeps(d, out), d.model, d.system, nil, prompt); err != nil {
		return err
	}
	if !out.endsWithNewline() {
		fmt.Fprintln(out.w)
	}
	return nil
}

// repl runs the interactive REPL: it reads prompts from stdin, runs one
// turn per prompt, streams responses to stdout, and ends on "/quit",
// "/exit", or EOF. The session path is printed after the first turn.
func repl(ctx context.Context, stdin io.Reader, d *runDeps) error {
	reader := bufio.NewReader(stdin)
	var history []*agent.Message
	first := true
	for {
		fmt.Fprint(d.stdout, "> ")
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		input := strings.TrimSpace(line)
		if input == "" {
			if err != nil {
				fmt.Fprintln(d.stdout) // clear the prompt line on EOF
				return nil
			}
			continue
		}
		if input == "/quit" || input == "/exit" {
			return nil
		}
		out := &trailingWriter{w: d.stdout}
		history, err = agent.RunTurn(ctx, loopDeps(d, out), d.model, d.system, history, input)
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
		if err != nil { // EOF right after the last partial line
			return nil
		}
	}
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
	return &agent.LoopDeps{
		Client:     d.client,
		Tools:      d.tools,
		Recorder:   d.recorder,
		Stdout:     out,
		OnThinking: onThinking,
	}
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

// printUsage writes the command usage to w.
func printUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: smidja [flags] [-p prompt]

Smidja is an agentic coding harness. With no arguments it starts an
interactive session that reads prompts from stdin and streams responses
to stdout. With -p it runs a single turn and exits.

flags:
  -p prompt      run one turn with the given prompt and exit
  -model string  override the configured model (default: SMIDJA_MODEL)
  -system string override the default system prompt
  -version       print "smidja <version>" and exit
`)
}
