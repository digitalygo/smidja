package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"

	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/sessionimport"
)

func runImport(args []string, d *Deps) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var sessionDir string
	fs.StringVar(&sessionDir, "session-dir", "", "session store directory (default: $SMIDJA_SESSION_DIR or ~/.smidja/sessions)")
	flags, positionals, err := splitSubcommandArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printImportUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printImportUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	rest := positionals
	if len(rest) != 1 {
		return fail(d, errors.New("import: exactly one session file argument is required"))
	}
	src := rest[0]
	if sessionDir == "" {
		cfg, err := config.Load(d.Env, d.Getwd, d.Home)
		if err != nil {
			return fail(d, err)
		}
		sessionDir = cfg.SessionDir
	}
	store, err := session.NewStore(sessionDir)
	if err != nil {
		return fail(d, err)
	}
	dest, stats, err := sessionimport.Import(src, store)
	if err != nil {
		return fail(d, fmt.Errorf("import: %w", err))
	}
	fmt.Fprintf(d.Stdout, "imported %s\n", sanitizeTerm(dest))
	fmt.Fprintf(d.Stdout, "  entries: %d\n", stats.Entries)
	for _, typ := range sortedKeys(stats.PerType) {
		fmt.Fprintf(d.Stdout, "  %s: %d\n", sanitizeTerm(typ), stats.PerType[typ])
	}
	if stats.Opaque > 0 {
		fmt.Fprintf(d.Stdout, "  opaque: %d\n", stats.Opaque)
	}
	if stats.Idempotent {
		fmt.Fprintf(d.Stdout, "  idempotent: destination already held identical content\n")
	}
	return nil
}

func sanitizeTerm(s string) string {
	for _, r := range s {
		if isTermControl(r) {
			return fmt.Sprintf("%q", s)
		}
	}
	return s
}

func isTermControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (0x80 <= r && r <= 0x9f)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
