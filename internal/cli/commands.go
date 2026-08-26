package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

func runVersion(args []string, d *Deps) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "print the build identity as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if asJSON {
		fmt.Fprintln(d.Stdout, buildIdentity(d).JSON())
		return nil
	}
	fmt.Fprintf(d.Stdout, "smidja %s\n", versionFor(d))
	return nil
}

func splitSubcommandArgs(fs *flag.FlagSet, args []string) (flags, positionals []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positionals = append(positionals, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if j := strings.IndexByte(name, '='); j >= 0 {
			name = name[:j]
		}
		f := fs.Lookup(name)
		if f == nil {
			if name == "h" || name == "help" {
				return nil, nil, flag.ErrHelp
			}
			return nil, nil, fmt.Errorf("flag provided but not defined: -%s", name)
		}
		flags = append(flags, a)
		if !strings.Contains(a, "=") && !isBoolFlag(f) {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("flag needs an argument: -%s", name)
			}
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, positionals, nil
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

func printImportUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: smidja import <file> [--session-dir <dir>]

Import a Pi-format session file into the smidja session store. The
destination is computed from the session header exactly like the store
would name it; an existing destination with different content is never
overwritten.

flags:
  -session-dir string  session store directory
                       (default: $SMIDJA_SESSION_DIR or ~/.smidja/sessions)
`)
}

func printUpdateUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: smidja update [--check] [--version <version>]

Update the smidja binary from its GitHub releases. With no version the
latest release is used; the binary is replaced atomically after its
checksum is verified.

flags:
  -check        check for an available update without applying it
  -version v    update to a specific release version (default: latest)
`)
}
