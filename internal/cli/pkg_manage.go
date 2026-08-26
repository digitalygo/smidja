package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/digitalygo/smidja/internal/packages"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

func runPkgList(args []string, d *Deps) error {
	fs := flag.NewFlagSet("pkg list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "print the index as JSON")
	flags, positionals, err := splitSubcommandArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPkgUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPkgUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if len(positionals) != 0 {
		return fail(d, fmt.Errorf("pkg list: unexpected argument %q", positionals[0]))
	}
	store, err := pkgStore(d)
	if err != nil {
		return fail(d, err)
	}
	idx, err := store.Index()
	if err != nil {
		return fail(d, err)
	}
	rows := make([]pkgRow, 0, len(idx.Installed))
	for _, rec := range idx.Installed {
		rows = append(rows, pkgRow{
			ID: rec.ID, Version: rec.Version, Owner: rec.Owner, Repo: rec.Repo,
			Commit: rec.Commit, Active: activeContainsVersion(idx.Active, rec.ID, rec.Version),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ID != rows[j].ID {
			return rows[i].ID < rows[j].ID
		}
		return packages.CompareVersions(rows[i].Version, rows[j].Version) > 0
	})
	if asJSON {
		return printJSON(d.Stdout, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(d.Stdout, "no packages installed")
		return nil
	}
	for _, r := range rows {
		marker := " "
		if r.Active {
			marker = "*"
		}
		fmt.Fprintf(d.Stdout, "%s %s@%s  %s/%s  %s\n", marker, r.ID, r.Version, r.Owner, r.Repo, shortCommit(r.Commit))
	}
	return nil
}

type pkgRow struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	Commit  string `json:"commit"`
	Active  bool   `json:"active"`
}

func runPkgInspect(args []string, d *Deps) error {
	fs := flag.NewFlagSet("pkg inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var version string
	var asJSON bool
	fs.StringVar(&version, "version", "", "inspect this version instead of the latest installed")
	fs.BoolVar(&asJSON, "json", false, "print the inspection as JSON")
	flags, positionals, err := splitSubcommandArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPkgUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPkgUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if len(positionals) != 1 {
		return fail(d, errors.New("pkg inspect: exactly one package id is required"))
	}
	store, err := pkgStore(d)
	if err != nil {
		return fail(d, err)
	}
	rec, err := resolveInstalledRecord(store, positionals[0], version)
	if err != nil {
		return fail(d, err)
	}
	m, err := store.Manifest(rec.ID, rec.Version)
	if err != nil {
		return fail(d, err)
	}
	agents, err := listContentFiles(store, rec.ID, rec.Version, m.Contents["agents"])
	if err != nil {
		return fail(d, err)
	}
	idx, err := store.Index()
	if err != nil {
		return fail(d, err)
	}
	info := pkgInfo{
		ID:             rec.ID,
		Version:        rec.Version,
		Owner:          rec.Owner,
		Repo:           rec.Repo,
		Commit:         rec.Commit,
		Description:    m.Description,
		MinimumHarness: m.MinimumHarness,
		Active:         activeContainsVersion(idx.Active, rec.ID, rec.Version),
		Contents:       sortedContents(m.Contents),
		Depends:        dependencyRows(m.Depends),
		Files:          len(m.Files),
		Agents:         agents,
	}
	if asJSON {
		return printJSON(d.Stdout, info)
	}
	fmt.Fprintf(d.Stdout, "id: %s\n", info.ID)
	fmt.Fprintf(d.Stdout, "version: %s\n", info.Version)
	fmt.Fprintf(d.Stdout, "repo: %s/%s\n", info.Owner, info.Repo)
	fmt.Fprintf(d.Stdout, "commit: %s\n", info.Commit)
	fmt.Fprintf(d.Stdout, "status: %s\n", statusWord(info.Active))
	if info.Description != "" {
		fmt.Fprintf(d.Stdout, "description: %s\n", info.Description)
	}
	fmt.Fprintf(d.Stdout, "minimumHarness: %s\n", info.MinimumHarness)
	for _, kind := range info.Contents {
		fmt.Fprintf(d.Stdout, "contents: %s\n", kind)
	}
	for _, dep := range info.Depends {
		fmt.Fprintf(d.Stdout, "depends: %s@%s\n", dep.ID, dep.Version)
	}
	fmt.Fprintf(d.Stdout, "files: %d\n", info.Files)
	for _, a := range info.Agents {
		fmt.Fprintf(d.Stdout, "agent: %s (deferred)\n", a)
	}
	return nil
}

type pkgInfo struct {
	ID             string   `json:"id"`
	Version        string   `json:"version"`
	Owner          string   `json:"owner"`
	Repo           string   `json:"repo"`
	Commit         string   `json:"commit"`
	Description    string   `json:"description,omitempty"`
	MinimumHarness string   `json:"minimumHarness"`
	Active         bool     `json:"active"`
	Contents       []string `json:"contents"`
	Depends        []pkgDep `json:"depends"`
	Files          int      `json:"files"`
	Agents         []string `json:"agents"`
}

type pkgDep struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

func runPkgActivate(args []string, d *Deps) error {
	return runPkgSetActive(args, d, true)
}

func runPkgDeactivate(args []string, d *Deps) error {
	return runPkgSetActive(args, d, false)
}

func runPkgSetActive(args []string, d *Deps, activate bool) error {
	verb := "deactivate"
	if activate {
		verb = "activate"
	}
	fs := flag.NewFlagSet("pkg "+verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var version string
	fs.StringVar(&version, "version", "", "use this version instead of the latest installed")
	flags, positionals, err := splitSubcommandArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPkgUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPkgUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if len(positionals) != 1 {
		return fail(d, fmt.Errorf("pkg %s: exactly one package id is required", verb))
	}
	store, err := pkgStore(d)
	if err != nil {
		return fail(d, err)
	}
	rec, err := resolveInstalledRecord(store, positionals[0], version)
	if err != nil {
		return fail(d, err)
	}
	if activate {
		err = store.Activate(rec.ID, rec.Version)
	} else {
		err = store.Deactivate(rec.ID, rec.Version)
	}
	if err != nil {
		return fail(d, fmt.Errorf("pkg %s %s@%s: %w", verb, rec.ID, rec.Version, err))
	}
	done := "deactivated"
	if activate {
		done = "activated"
	}
	fmt.Fprintf(d.Stdout, "%s %s@%s\n", done, rec.ID, rec.Version)
	return nil
}

func runPkgVerify(args []string, d *Deps) error {
	fs := flag.NewFlagSet("pkg verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var version string
	fs.StringVar(&version, "version", "", "verify this version instead of the latest installed")
	flags, positionals, err := splitSubcommandArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPkgUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPkgUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	store, err := pkgStore(d)
	if err != nil {
		return fail(d, err)
	}
	targets, err := verifyTargets(store, positionals, version)
	if err != nil {
		return fail(d, err)
	}
	for _, rec := range targets {
		if err := store.Verify(rec.ID, rec.Version); err != nil {
			return fail(d, err)
		}
		fmt.Fprintf(d.Stdout, "%s@%s ok\n", rec.ID, rec.Version)
	}
	return nil
}

func runPkgUninstall(args []string, d *Deps) error {
	fs := flag.NewFlagSet("pkg uninstall", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var version string
	var yes bool
	fs.StringVar(&version, "version", "", "uninstall this version instead of the latest installed")
	fs.BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	flags, positionals, err := splitSubcommandArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPkgUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printPkgUsage(d.Stderr)
			return nil
		}
		return fail(d, err)
	}
	if len(positionals) != 1 {
		return fail(d, errors.New("pkg uninstall: exactly one package id is required"))
	}
	store, err := pkgStore(d)
	if err != nil {
		return fail(d, err)
	}
	rec, err := resolveInstalledRecord(store, positionals[0], version)
	if err != nil {
		return fail(d, err)
	}
	idx, err := store.Index()
	if err != nil {
		return fail(d, err)
	}
	active := activeContainsVersion(idx.Active, rec.ID, rec.Version)
	if !yes {
		lineUI := ui.New(d.Stdin, d.Stdout, d.Stderr, sdk.ModeInteractive)
		message := "uninstall " + rec.ID + "@" + rec.Version + "?"
		if active {
			message = "uninstall active package " + rec.ID + "@" + rec.Version + "?"
		}
		confirmed, cerr := lineUI.Confirm("pkg uninstall", message)
		if cerr != nil {
			return fail(d, cerr)
		}
		if !confirmed {
			return nil
		}
	}
	if active {
		if err := store.Deactivate(rec.ID, rec.Version); err != nil {
			return fail(d, err)
		}
	}
	if err := store.Remove(rec.ID, rec.Version); err != nil {
		return fail(d, err)
	}
	fmt.Fprintf(d.Stdout, "uninstalled %s@%s\n", rec.ID, rec.Version)
	return nil
}

func resolveInstalledRecord(store *packages.Store, id, version string) (packages.InstalledRecord, error) {
	if version != "" && !packages.IsCanonicalVersion(version) {
		return packages.InstalledRecord{}, fmt.Errorf("pkg: version %q must be canonical vMAJOR.MINOR.PATCH", version)
	}
	idx, err := store.Index()
	if err != nil {
		return packages.InstalledRecord{}, err
	}
	var best packages.InstalledRecord
	found := false
	for _, rec := range idx.Installed {
		if rec.ID != id {
			continue
		}
		if version != "" && rec.Version != version {
			continue
		}
		if !found || packages.CompareVersions(rec.Version, best.Version) > 0 {
			best = rec
			found = true
		}
	}
	if !found {
		return packages.InstalledRecord{}, fmt.Errorf("%w: %s", packages.ErrNotInstalled, id)
	}
	return best, nil
}

func verifyTargets(store *packages.Store, ids []string, version string) ([]packages.InstalledRecord, error) {
	idx, err := store.Index()
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []packages.InstalledRecord
	for _, rec := range idx.Installed {
		if len(want) > 0 && !want[rec.ID] {
			continue
		}
		if version != "" && rec.Version != version {
			continue
		}
		out = append(out, rec)
	}
	if len(want) > 0 {
		for id := range want {
			found := false
			for _, rec := range out {
				if rec.ID == id {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("%w: %s", packages.ErrNotInstalled, id)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return packages.CompareVersions(out[i].Version, out[j].Version) > 0
	})
	return out, nil
}

func listContentFiles(store *packages.Store, id, version, root string) ([]string, error) {
	if root == "" {
		return nil, nil
	}
	dir := filepath.Join(store.Root(), id, version, filepath.FromSlash(root))
	var out []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func sortedContents(contents map[string]string) []string {
	out := make([]string, 0, len(contents))
	for kind, root := range contents {
		out = append(out, kind+"="+root)
	}
	sort.Strings(out)
	return out
}

func dependencyRows(deps []packages.Dependency) []pkgDep {
	out := make([]pkgDep, 0, len(deps))
	for _, dep := range deps {
		version := dep.ExactVersion
		if version == "" {
			version = ">=" + dep.MinimumVersion
		}
		out = append(out, pkgDep{ID: dep.ID, Version: version})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func statusWord(active bool) string {
	if active {
		return "active"
	}
	return "installed"
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printPkgUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: smidja pkg <install|list|inspect|activate|deactivate|update|verify|uninstall>

Manage optional packages in $SMIDJA_PACKAGES_DIR or ~/.smidja/packages.

commands:
  install <owner/repo@version> [--yes] [--pin <commit>]
                    install a package from a public GitHub repository,
                    print the config defaults diff and offer to activate it
  list [--json]     list installed packages, marking the active ones
  inspect <id> [--version v] [--json]
                    show manifest details; agent files are listed as
                    deferred until the subagent runtime lands
  activate <id> [--version v]
                    activate an installed package
  deactivate <id> [--version v]
                    deactivate an installed package
  update [<id>...] [--yes]
                    update installed packages to their latest version tags
  verify [<id>...] [--version v]
                    re-check file hashes and receipts of installed packages
  uninstall <id> [--version v] [--yes]
                    deactivate if needed and remove an installed package

Archives are fetched from codeload.github.com by commit; tags are
resolved through api.github.com. Public repositories only, no
credentials are sent.
`)
}
