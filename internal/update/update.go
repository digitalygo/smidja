// Package update implements deterministic self-update for the smidja
// binary from GitHub Releases. It is Linux-only and uses only the
// standard library.
//
// The protocol, in order:
//
//  1. Resolve the running executable with os.Executable and
//     filepath.EvalSymlinks, then acquire an exclusive lock file next to
//     it (<binary>.update.lock, created with O_CREATE|O_EXCL; locks older
//     than ten minutes are treated as stale and reclaimed).
//  2. Resolve the release: /releases/latest by default, or
//     /releases/tags/<version> for an explicit target version.
//  3. Select the platform asset, smidja-<goos>-<goarch>, and download it
//     into a temp file in the same directory as the target binary.
//  4. Download checksums.txt for the same release, require exactly one
//     entry for the asset name, and compare SHA-256 digests.
//  5. Copy the target's file mode, then rename the temp file over the
//     target (atomic on the same filesystem) and fsync the directory.
//
// The exec-based self-check an architect suggested is deliberately
// optional (Client.SelfCheck). The checksum already binds the downloaded
// bytes to the release, and executing the downloaded binary adds failure
// modes (missing runtime libraries, sandbox restrictions) without
// strengthening that guarantee, so the default path skips it.
//
// Any failure before the rename leaves the old binary untouched and
// removes the temp file. Rollback is `smidja update --version <previous>`.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/buildinfo"
)

// Sentinel errors returned by the updater. Callers use errors.Is to map
// them to user-facing messages.
var (
	// ErrUnsupportedPlatform reports that the updater only supports
	// linux; any other GOOS fails before any network access.
	ErrUnsupportedPlatform = errors.New("update: only linux is supported")
	// ErrInvalidOrigin reports that Origin.Origin is not of the form
	// "github.com/owner/repo".
	ErrInvalidOrigin = errors.New("update: origin must be github.com/owner/repo")
	// ErrNoRelease reports that the release (latest or tagged) does not
	// exist.
	ErrNoRelease = errors.New("update: release not found")
	// ErrAssetNotFound reports that the release has no asset for the
	// current platform.
	ErrAssetNotFound = errors.New("update: release asset not found")
	// ErrChecksumsMissing reports that the release has no checksums.txt
	// asset, which the updater requires.
	ErrChecksumsMissing = errors.New("update: checksums.txt asset missing")
	// ErrChecksumEntry reports a missing, duplicated, or malformed
	// checksum entry for the asset.
	ErrChecksumEntry = errors.New("update: checksum entry missing or malformed")
	// ErrChecksumMismatch reports that the downloaded asset does not
	// match its checksum.
	ErrChecksumMismatch = errors.New("update: checksum mismatch")
	// ErrLocked reports that another update is in progress.
	ErrLocked = errors.New("update: another update is in progress")
)

// staleLockAge is how old an update lock must be before it is considered
// abandoned and reclaimed. Locks are removed on completion, so one that
// outlives this age is assumed to come from a crashed updater.
const staleLockAge = 10 * time.Minute

// defaultBaseURL is the GitHub REST API base used when Client.BaseURL is
// empty.
const defaultBaseURL = "https://api.github.com"

// assetPrefix is the prefix of the conventional release asset names.
const assetPrefix = "smidja-"

// Latest describes the most recent release available for the current
// platform.
type Latest struct {
	Version     string // release tag, e.g. "v1.2.3"
	URL         string // HTML URL of the release
	PublishedAt string // publish timestamp as reported by GitHub
	Available   bool   // true when Version is newer than the running version
	Asset       string // selected asset name, e.g. "smidja-linux-amd64"
}

// Client drives the GitHub Releases updater. The zero value is not
// usable: Origin must be set. All other fields have defaults.
type Client struct {
	// HTTP is the HTTP client used for API and asset requests. When nil,
	// http.DefaultClient is used.
	HTTP *http.Client

	// BaseURL overrides the GitHub API base, which defaults to
	// https://api.github.com. Tests point it at an httptest server.
	BaseURL string

	// Origin is the build identity of the running binary. Origin.Origin
	// must be "github.com/owner/repo"; Origin.Version is the running
	// version that Check compares against the latest release.
	Origin buildinfo.Info

	// SelfCheck optionally runs the downloaded binary with `version
	// --json` before replacing the running executable, verifying that it
	// reports Origin.Origin and the release version. It defaults to
	// false: the checksum is the integrity gate, and executing the
	// downloaded bytes adds failure modes (missing runtime libraries,
	// sandbox restrictions) without improving the guarantee the checksum
	// already provides.
	SelfCheck bool

	// GOOS and GOARCH override the platform used for asset selection and
	// the linux-only guard. They default to runtime.GOOS and
	// runtime.GOARCH; tests set them to exercise asset selection.
	GOOS   string
	GOARCH string

	// ExecPath resolves the path of the running executable, defaulting to
	// os.Executable followed by filepath.EvalSymlinks. Tests override it
	// so Apply replaces a scratch file instead of the test binary.
	ExecPath func() (string, error)
}

// Check resolves the latest release for the current platform and reports
// whether it is newer than the running version. It never writes to disk.
func (c *Client) Check(ctx context.Context) (Latest, error) {
	if err := c.checkPlatform(); err != nil {
		return Latest{}, err
	}
	owner, repo, err := parseOrigin(c.Origin.Origin)
	if err != nil {
		return Latest{}, err
	}
	rel, err := c.getRelease(ctx, owner, repo, "")
	if err != nil {
		return Latest{}, err
	}
	asset := c.selectAsset(rel.Assets)
	if asset == "" {
		return Latest{}, fmt.Errorf("%w: %s", ErrAssetNotFound, c.assetName())
	}
	return Latest{
		Version:     rel.TagName,
		URL:         rel.HTMLURL,
		PublishedAt: rel.PublishedAt,
		Asset:       asset,
		Available:   CompareVersions(rel.TagName, c.Origin.Version) > 0,
	}, nil
}

// Apply downloads the release for targetVersion (the latest release when
// empty) and atomically replaces the running executable with it.
//
// The target binary is only touched by the final rename: every earlier
// step either fails cleanly or leaves the old file in place. Rollback is
// running Apply again with the previous version.
func (c *Client) Apply(ctx context.Context, targetVersion string) error {
	if err := c.checkPlatform(); err != nil {
		return err
	}
	owner, repo, err := parseOrigin(c.Origin.Origin)
	if err != nil {
		return err
	}
	rel, err := c.getRelease(ctx, owner, repo, targetVersion)
	if err != nil {
		return err
	}
	asset, assetURL := c.pickAsset(rel)
	if asset == "" {
		return fmt.Errorf("%w: %s", ErrAssetNotFound, c.assetName())
	}
	checksumsURL := c.pickChecksums(rel)
	if checksumsURL == "" {
		return fmt.Errorf("%w: checksums.txt", ErrChecksumsMissing)
	}

	exePath, err := c.execPath()
	if err != nil {
		return fmt.Errorf("update: resolve executable: %w", err)
	}
	unlock, err := acquireLock(exePath + ".update.lock")
	if err != nil {
		return err
	}
	defer unlock()

	st, err := os.Stat(exePath)
	if err != nil {
		return fmt.Errorf("update: stat target: %w", err)
	}

	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, ".smidja-update-*")
	if err != nil {
		return fmt.Errorf("update: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op after a successful rename
	}()

	if err := download(ctx, c, assetURL, tmp); err != nil {
		return err
	}
	checksums, err := c.downloadBytes(ctx, checksumsURL)
	if err != nil {
		return err
	}
	if err := verifyChecksum(checksums, asset, tmpName); err != nil {
		return err
	}
	if err := tmp.Chmod(st.Mode()); err != nil {
		return fmt.Errorf("update: chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("update: close temp file: %w", err)
	}
	if c.SelfCheck {
		if err := c.selfCheck(ctx, tmpName, rel.TagName); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpName, exePath); err != nil {
		return fmt.Errorf("update: rename over target: %w", err)
	}
	return syncDir(dir)
}

// release is the subset of the GitHub releases API response the updater
// needs.
type release struct {
	TagName     string         `json:"tag_name"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt string         `json:"published_at"`
	Assets      []releaseAsset `json:"assets"`
}

// releaseAsset is one entry of a release's assets array.
type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// getRelease fetches the release for targetVersion, or the latest release
// when targetVersion is empty.
func (c *Client) getRelease(ctx context.Context, owner, repo, targetVersion string) (release, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/releases/latest", c.baseURL(), owner, repo)
	if targetVersion != "" {
		u = fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", c.baseURL(), owner, repo, targetVersion)
	}
	resp, err := c.do(ctx, u)
	if err != nil {
		return release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		what := "latest release"
		if targetVersion != "" {
			what = "release " + targetVersion
		}
		return release{}, fmt.Errorf("%w: %s", ErrNoRelease, what)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return release{}, fmt.Errorf("update: fetch release: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return release{}, fmt.Errorf("update: decode release: %w", err)
	}
	return rel, nil
}

// download streams the URL into w, failing on any non-200 response.
func download(ctx context.Context, c *Client, url string, w io.Writer) error {
	resp, err := c.do(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update: download %s: %s", url, resp.Status)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("update: download %s: %w", url, err)
	}
	return nil
}

// downloadBytes fetches a small text asset (checksums.txt) into memory.
func (c *Client) downloadBytes(ctx context.Context, url string) ([]byte, error) {
	resp, err := c.do(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: download %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", url, err)
	}
	return b, nil
}

// do performs a GET request with the client's HTTP stack.
func (c *Client) do(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("update: request %s: %w", url, err)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: request %s: %w", url, err)
	}
	return resp, nil
}

// checkPlatform enforces the linux-only contract before any network work.
func (c *Client) checkPlatform() error {
	if c.goos() != "linux" {
		return fmt.Errorf("%w: got %s", ErrUnsupportedPlatform, c.goos())
	}
	return nil
}

func (c *Client) goos() string {
	if c.GOOS != "" {
		return c.GOOS
	}
	return runtime.GOOS
}

func (c *Client) goarch() string {
	if c.GOARCH != "" {
		return c.GOARCH
	}
	return runtime.GOARCH
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) execPath() (string, error) {
	if c.ExecPath != nil {
		return c.ExecPath()
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// assetName returns the conventional asset name for the current platform,
// e.g. "smidja-linux-amd64".
func (c *Client) assetName() string {
	return assetPrefix + c.goos() + "-" + c.goarch()
}

// selectAsset returns the name of the platform asset in the release, or
// "" when the release has none.
func (c *Client) selectAsset(assets []releaseAsset) string {
	want := c.assetName()
	for _, a := range assets {
		if a.Name == want {
			return a.Name
		}
	}
	return ""
}

// pickAsset is selectAsset plus the asset's download URL.
func (c *Client) pickAsset(rel release) (name, url string) {
	want := c.assetName()
	for _, a := range rel.Assets {
		if a.Name == want {
			return a.Name, a.BrowserDownloadURL
		}
	}
	return "", ""
}

// pickChecksums returns the download URL of the release's checksums.txt
// asset, or "" when the release has none.
func (c *Client) pickChecksums(rel release) string {
	for _, a := range rel.Assets {
		if a.Name == "checksums.txt" {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

// acquireLock creates the lock file at path exclusively (O_CREATE|O_EXCL).
// When the file already exists it is treated as stale, and reclaimed, if
// its mtime is older than staleLockAge. The returned unlock function
// removes the lock and must be called exactly once.
func acquireLock(path string) (unlock func(), err error) {
	f, err := lockFile(path)
	if err == nil {
		return lockRelease(path, f), nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("update: acquire lock: %w", err)
	}
	// Lock exists: reclaim it only when it is stale.
	st, statErr := os.Stat(path)
	if statErr == nil && time.Since(st.ModTime()) > staleLockAge {
		if rmErr := os.Remove(path); rmErr == nil {
			f, err = lockFile(path)
			if err == nil {
				return lockRelease(path, f), nil
			}
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrLocked, path)
}

func lockFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
}

// lockRelease records the holder in the lock file and returns the unlock
// function that removes it.
func lockRelease(path string, f *os.File) func() {
	fmt.Fprintf(f, "pid=%d time=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	f.Close()
	return func() { os.Remove(path) }
}

// selfCheck executes the downloaded binary with `version --json` and
// verifies that it reports the configured origin and the release version.
// It is opt-in because it depends on the binary supporting that output.
func (c *Client) selfCheck(ctx context.Context, path, tag string) error {
	cmd := exec.CommandContext(ctx, path, "version", "--json")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("update: self-check: %w", err)
	}
	var got struct {
		Origin  string `json:"origin"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		return fmt.Errorf("update: self-check: parse output: %w", err)
	}
	if got.Origin != c.Origin.Origin {
		return fmt.Errorf("update: self-check: origin mismatch: got %q, want %q", got.Origin, c.Origin.Origin)
	}
	if CompareVersions(got.Version, tag) != 0 {
		return fmt.Errorf("update: self-check: version mismatch: got %q, want %q", got.Version, tag)
	}
	return nil
}

// syncDir fsyncs a directory so a rename performed inside it is durable
// across a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("update: open directory: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("update: sync directory: %w", err)
	}
	return nil
}

// parseOrigin splits an origin of the form "github.com/owner/repo" into
// owner and repo. Only GitHub origins are supported.
func parseOrigin(origin string) (owner, repo string, err error) {
	parts := strings.Split(strings.TrimSpace(origin), "/")
	if len(parts) != 3 || parts[0] != "github.com" || parts[1] == "" || parts[2] == "" {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidOrigin, origin)
	}
	return parts[1], parts[2], nil
}
