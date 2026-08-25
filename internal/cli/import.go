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

// runImport implements "smidja import <file> [--session-dir <dir>]": it
// imports a Pi-format session file into the session store and prints the
// destination plus a per-type breakdown. It exits 0 on success (including
// an idempotent import whose destination already held identical content)
// and 1 on a conflict or any error, matching the CLI's general error
// mapping in cmd/smidja.
func runImport(args []string, d *cliDeps) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var sessionDir string
	fs.StringVar(&sessionDir, "session-dir", "", "session store directory (default: $SMIDJA_SESSION_DIR or ~/.smidja/sessions)")
	flags, positionals, err := splitSubcommandArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printImportUsage(d.stderr)
			return nil
		}
		return fail(d, err)
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printImportUsage(d.stderr)
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
		cfg, err := config.Load(d.env, d.getwd, d.home)
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
	fmt.Fprintf(d.stdout, "imported %s\n", sanitizeTerm(dest))
	fmt.Fprintf(d.stdout, "  entries: %d\n", stats.Entries)
	for _, typ := range sortedKeys(stats.PerType) {
		fmt.Fprintf(d.stdout, "  %s: %d\n", sanitizeTerm(typ), stats.PerType[typ])
	}
	if stats.Opaque > 0 {
		fmt.Fprintf(d.stdout, "  opaque: %d\n", stats.Opaque)
	}
	if stats.Idempotent {
		fmt.Fprintf(d.stdout, "  idempotent: destination already held identical content\n")
	}
	return nil
}

// sanitizeTerm renders a string safe to print to a terminal. Strings
// without control characters pass through unchanged; strings containing
// C0 or C1 control characters (including ESC 0x1b, the CSI/OSC
// introducer) are quoted with %q so the bytes print as inert escapes
// instead of terminal control sequences. An entry type or destination
// path parsed from a hostile JSONL can carry such bytes, and printing
// them verbatim would let the source spoof or hijack the terminal.
func sanitizeTerm(s string) string {
	for _, r := range s {
		if isTermControl(r) {
			return fmt.Sprintf("%q", s)
		}
	}
	return s
}

// isTermControl reports whether r is a C0 control (< 0x20, including ESC
// 0x1b), DEL (0x7f), or a C1 control (0x80-0x9f, including CSI 0x9b).
func isTermControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (0x80 <= r && r <= 0x9f)
}

// sortedKeys returns the keys of m in sorted order, for deterministic
// output.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
