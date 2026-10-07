package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

type rootOptions struct {
	prompt            string
	model             string
	system            string
	provider          string
	continuePath      string
	tuiModeFlag       string
	useThemeFlag      string
	version           bool
	allowWorkspaceMCP bool
}

func newRootFlagSet(opts *rootOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("smidja", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	fs.StringVar(&opts.prompt, "p", "", "run one turn with the given prompt and exit")
	fs.StringVar(&opts.model, "model", "", "override the configured model")
	fs.StringVar(&opts.system, "system", "", "override the default system prompt")
	fs.StringVar(&opts.provider, "provider", "", "select the provider driver (manifest id or OAuth provider)")
	fs.StringVar(&opts.continuePath, "continue", "", "resume the session at the given path or id")
	fs.StringVar(&opts.tuiModeFlag, "tui-mode", "", "select the interactive renderer (regular|fullscreen)")
	fs.StringVar(&opts.useThemeFlag, "use-theme", "", "set the interactive theme (name or lightTheme/darkTheme)")
	fs.BoolVar(&opts.version, "version", false, "print the version and exit")
	fs.BoolVar(&opts.allowWorkspaceMCP, "allow-workspace-mcp", false, "spawn MCP servers defined in the workspace .smidja/mcp.json")
	return fs
}

func defaultProviderModel(opts *rootOptions, d *Deps) {
	if opts.provider != "" && opts.model == "" && d.Env("SMIDJA_MODEL") == "" {
		if def, ok := providerDefaultModel(opts.provider); ok {
			opts.model = def
		}
	}
}

func runRun(args []string, d *Deps) error {
	var opts rootOptions
	fs := newRootFlagSet(&opts)
	flags, positionals, err := splitSubcommandArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printRunUsage(d.Stderr)
			return nil
		}
		fmt.Fprintf(d.Stderr, "smidja: %v\n", err)
		printRunUsage(d.Stderr)
		return err
	}
	if err := fs.Parse(flags); err != nil {
		fmt.Fprintf(d.Stderr, "smidja: %v\n", err)
		printRunUsage(d.Stderr)
		return err
	}
	if opts.version {
		fmt.Fprintf(d.Stdout, "smidja %s\n", versionFor(d))
		return nil
	}
	promptSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "p" {
			promptSet = true
		}
	})
	if err := validateTUIModeFlag(opts.tuiModeFlag, d); err != nil {
		return err
	}
	if err := validateThemeFlag(opts.useThemeFlag, d); err != nil {
		return err
	}
	if err := resolveRunPrompt(&opts, positionals, promptSet); err != nil {
		fmt.Fprintf(d.Stderr, "smidja: %v\n", err)
		printRunUsage(d.Stderr)
		return err
	}
	defaultProviderModel(&opts, d)
	return runChat(d, opts.prompt, opts.model, opts.system, opts.provider, opts.allowWorkspaceMCP, opts.continuePath, opts.tuiModeFlag, opts.useThemeFlag)
}

func resolveRunPrompt(opts *rootOptions, positionals []string, promptSet bool) error {
	switch {
	case promptSet && len(positionals) > 0:
		return errors.New("run: use either -p <prompt> or one positional prompt, not both")
	case !promptSet && len(positionals) == 0:
		return errors.New("run: a prompt is required, use smidja run <prompt> or smidja run -p <prompt>")
	case len(positionals) > 1:
		return fmt.Errorf("run: expected exactly one prompt, got %d", len(positionals))
	}
	if len(positionals) == 1 {
		opts.prompt = positionals[0]
	}
	if strings.TrimSpace(opts.prompt) == "" {
		return errors.New("run: the prompt must not be empty")
	}
	return nil
}

func printRunUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: smidja run [-p prompt] <prompt> [flags]

Run one turn with the given prompt and exit, exactly like the root command
with -p. Provide the prompt either as one positional argument or with -p,
never both. The interactive TUI is never started by run.

flags:
  -p prompt       run one turn with the given prompt and exit
  -continue path  resume the session at the given path or id instead of
                  creating a new one
  -model string   override the configured model (default: SMIDJA_MODEL)
  -provider id    select the provider driver (default: openrouter)
  -system string  override the default system prompt
  -tui-mode mode  select the interactive renderer (regular|fullscreen)
  -use-theme name[/name]
                  set the interactive theme for this run
  -version        print "smidja <version>" and exit
  -allow-workspace-mcp
                  spawn MCP servers defined in .smidja/mcp.json
`)
}
