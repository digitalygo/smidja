package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
	"strings"

	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/packages"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

func runPkg(args []string, d *Deps) error {
	if len(args) == 0 {
		printPkgUsage(d.Stderr)
		return fail(d, errors.New("pkg: a subcommand is required"))
	}
	switch args[0] {
	case "install":
		return runPkgInstall(args[1:], d)
	case "list":
		return runPkgList(args[1:], d)
	case "inspect":
		return runPkgInspect(args[1:], d)
	case "activate":
		return runPkgActivate(args[1:], d)
	case "deactivate":
		return runPkgDeactivate(args[1:], d)
	case "update":
		return runPkgUpdate(args[1:], d)
	case "verify":
		return runPkgVerify(args[1:], d)
	case "uninstall":
		return runPkgUninstall(args[1:], d)
	case "help", "-h", "-help", "--help":
		printPkgUsage(d.Stdout)
		return nil
	default:
		return fail(d, fmt.Errorf("pkg: unknown subcommand %q", args[0]))
	}
}

func runPkgInstall(args []string, d *Deps) error {
	fs := flag.NewFlagSet("pkg install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var yes bool
	var pin string
	fs.BoolVar(&yes, "yes", false, "skip the activation confirmation")
	fs.StringVar(&pin, "pin", "", "require the resolved commit to match")
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
		return fail(d, errors.New("pkg install: exactly one owner/repo@version argument is required"))
	}
	owner, repo, version, err := parseOwnerRepoVersion(positionals[0])
	if err != nil {
		return fail(d, err)
	}
	store, err := pkgStore(d)
	if err != nil {
		return fail(d, err)
	}
	ctx := d.Context
	if ctx == nil {
		ctx = context.Background()
	}
	rec, err := installPackage(ctx, d, store, pkgFetch(d), owner, repo, version, pin)
	if err != nil {
		return fail(d, fmt.Errorf("pkg install %s: %w", positionals[0], err))
	}
	fmt.Fprintf(d.Stdout, "installed %s@%s (commit %s)\n", rec.ID, rec.Version, rec.Commit)

	pkgDefaults, err := store.ConfigDefaults(rec.ID, rec.Version)
	if err != nil {
		return fail(d, err)
	}
	diff, err := configDefaultsDiff(d, store, pkgDefaults)
	if err != nil {
		return fail(d, err)
	}
	printConfigDiff(d.Stdout, diff)
	if !yes {
		lineUI := ui.New(d.Stdin, d.Stdout, d.Stderr, sdk.ModeInteractive)
		confirmed, cerr := lineUI.Confirm("pkg activate", "activate "+rec.ID+"@"+rec.Version+"?")
		if cerr != nil {
			return fail(d, cerr)
		}
		if !confirmed {
			fmt.Fprintln(d.Stdout, "not activated")
			return nil
		}
	}
	if err := store.Activate(rec.ID, rec.Version); err != nil {
		return fail(d, fmt.Errorf("pkg activate %s@%s: %w", rec.ID, rec.Version, err))
	}
	fmt.Fprintf(d.Stdout, "activated %s@%s\n", rec.ID, rec.Version)
	return nil
}

func installPackage(ctx context.Context, d *Deps, store *packages.Store, fetch packages.FetchArchive, owner, repo, version, pin string) (packages.InstalledRecord, error) {
	_, probeArchive, err := fetch(owner, repo, version)
	if err != nil {
		return packages.InstalledRecord{}, err
	}
	defer os.Remove(probeArchive)
	m, err := packages.ReadManifestFromArchive(probeArchive)
	if err != nil {
		return packages.InstalledRecord{}, err
	}
	req := packages.Request{Owner: owner, Repo: repo, ID: m.ID, Version: version}
	var nodes []packages.Node
	if len(m.Depends) > 0 {
		index, err := store.InstalledIndex()
		if err != nil {
			return packages.InstalledRecord{}, err
		}
		nodes, err = packages.Resolve(req, index, manifestFetch(fetch))
		if err != nil {
			return packages.InstalledRecord{}, err
		}
	}
	installer := &packages.Installer{Store: store, Fetch: fetch}
	for _, node := range nodes {
		if node.Installed || node.ID == req.ID {
			continue
		}
		depReq := packages.Request{Owner: node.Owner, Repo: node.Repo, ID: node.ID, Version: node.Version}
		if _, err := installer.Install(ctx, depReq, nodes, packages.InstallOptions{}); err != nil {
			return packages.InstalledRecord{}, err
		}
	}
	return installer.Install(ctx, req, nodes, packages.InstallOptions{PinCommit: pin})
}

func manifestFetch(fetch packages.FetchArchive) packages.FetchFunc {
	return func(req packages.Request) (packages.Node, error) {
		_, archive, err := fetch(req.Owner, req.Repo, req.Version)
		if err != nil {
			return packages.Node{}, err
		}
		defer os.Remove(archive)
		m, err := packages.ReadManifestFromArchive(archive)
		if err != nil {
			return packages.Node{}, err
		}
		return packages.Node{ID: m.ID, Owner: m.Owner, Repo: m.Repo, Version: m.Version, Manifest: m}, nil
	}
}

func runPkgUpdate(args []string, d *Deps) error {
	fs := flag.NewFlagSet("pkg update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var yes bool
	fs.BoolVar(&yes, "yes", false, "skip confirmation prompts")
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
	idx, err := store.Index()
	if err != nil {
		return fail(d, err)
	}
	ctx := d.Context
	if ctx == nil {
		ctx = context.Background()
	}
	targets := map[string]bool{}
	for _, p := range positionals {
		targets[p] = true
	}
	if len(targets) == 0 {
		for _, rec := range idx.Installed {
			targets[rec.ID] = true
		}
	}
	byID := map[string]packages.InstalledRecord{}
	for _, rec := range idx.Installed {
		cur, ok := byID[rec.ID]
		if !ok || packages.CompareVersions(rec.Version, cur.Version) > 0 {
			byID[rec.ID] = rec
		}
	}
	fetch := pkgFetch(d)
	latest := pkgLatest(d)
	for _, id := range sortedKeys(targets) {
		rec, ok := byID[id]
		if !ok {
			return fail(d, fmt.Errorf("%w: %s", packages.ErrNotInstalled, id))
		}
		tag, lerr := latest(rec.Owner, rec.Repo)
		if lerr != nil {
			return fail(d, lerr)
		}
		if packages.CompareVersions(tag, rec.Version) <= 0 {
			fmt.Fprintf(d.Stdout, "%s@%s is up to date\n", id, rec.Version)
			continue
		}
		if !yes {
			lineUI := ui.New(d.Stdin, d.Stdout, d.Stderr, sdk.ModeInteractive)
			confirmed, cerr := lineUI.Confirm("pkg update", "update "+id+" from "+rec.Version+" to "+tag+"?")
			if cerr != nil {
				return fail(d, cerr)
			}
			if !confirmed {
				continue
			}
		}
		updated, ierr := installPackage(ctx, d, store, fetch, rec.Owner, rec.Repo, tag, "")
		if ierr != nil {
			return fail(d, fmt.Errorf("pkg update %s: %w", id, ierr))
		}
		if activeContainsVersion(idx.Active, id, rec.Version) {
			if err := store.Activate(updated.ID, updated.Version); err != nil {
				return fail(d, fmt.Errorf("pkg update %s: %w", id, err))
			}
			if err := store.Deactivate(rec.ID, rec.Version); err != nil {
				return fail(d, fmt.Errorf("pkg update %s: %w", id, err))
			}
			fmt.Fprintf(d.Stdout, "updated %s %s -> %s (active)\n", id, rec.Version, updated.Version)
		} else {
			fmt.Fprintf(d.Stdout, "updated %s %s -> %s\n", id, rec.Version, updated.Version)
		}
	}
	return nil
}

func pkgStore(d *Deps) (*packages.Store, error) {
	return packages.Open(packageStoreRoot(d))
}

func pkgFetch(d *Deps) packages.FetchArchive {
	if d != nil && d.FetchArchive != nil {
		return d.FetchArchive
	}
	return githubFetchArchive
}

func pkgLatest(d *Deps) func(owner, repo string) (string, error) {
	if d != nil && d.FetchLatestVersion != nil {
		return d.FetchLatestVersion
	}
	return githubLatestVersion
}

func parseOwnerRepoVersion(spec string) (owner, repo, version string, err error) {
	at := strings.LastIndexByte(spec, '@')
	if at <= 0 {
		return "", "", "", fmt.Errorf("pkg: %q must be owner/repo@version", spec)
	}
	ownerRepo := spec[:at]
	version = spec[at+1:]
	slash := strings.IndexByte(ownerRepo, '/')
	if slash <= 0 || slash == len(ownerRepo)-1 {
		return "", "", "", fmt.Errorf("pkg: %q must be owner/repo@version", spec)
	}
	if !packages.IsCanonicalVersion(version) {
		return "", "", "", fmt.Errorf("pkg: version %q must be canonical vMAJOR.MINOR.PATCH", version)
	}
	return ownerRepo[:slash], ownerRepo[slash+1:], version, nil
}

func activeContainsVersion(active []packages.ActiveEntry, id, version string) bool {
	for _, a := range active {
		if a.ID == id && a.Version == version {
			return true
		}
	}
	return false
}

type configDiffRow struct {
	Key  string
	From string
	To   string
}

func configDefaultsDiff(d *Deps, store *packages.Store, pkgDefaults map[string]string) ([]configDiffRow, error) {
	if len(pkgDefaults) == 0 {
		return nil, nil
	}
	cwd, err := d.Getwd()
	if err != nil {
		return nil, err
	}
	dotenv := config.LoadDotEnv(cwd)
	current, err := store.ActiveConfigDefaults()
	if err != nil {
		return nil, err
	}
	after := maps.Clone(current)
	for k, v := range pkgDefaults {
		after[k] = v
	}
	keys := make([]string, 0, len(pkgDefaults))
	for k := range pkgDefaults {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([]configDiffRow, 0, len(keys))
	for _, key := range keys {
		before := resolveConfigValue(d.Env, dotenv, current, key)
		afterValue := resolveConfigValue(d.Env, dotenv, after, key)
		if afterValue == before {
			continue
		}
		rows = append(rows, configDiffRow{Key: key, From: before, To: afterValue})
	}
	return rows, nil
}

func resolveConfigValue(env func(string) string, dotenv, pkgDefaults map[string]string, key string) string {
	if env != nil {
		if v := env(key); v != "" {
			return v
		}
	}
	if v := dotenv[key]; v != "" {
		return v
	}
	if v := pkgDefaults[key]; v != "" {
		return v
	}
	return ""
}

func printConfigDiff(w io.Writer, rows []configDiffRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "config defaults: none")
		return
	}
	fmt.Fprintln(w, "config defaults that will change:")
	for _, row := range rows {
		from := row.From
		if from == "" {
			from = "(unset)"
		}
		fmt.Fprintf(w, "  %s: %s -> %s\n", row.Key, from, row.To)
	}
}
