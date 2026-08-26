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

var (
	ErrUnsupportedPlatform = errors.New("update: only linux is supported")
	ErrInvalidOrigin       = errors.New("update: origin must be github.com/owner/repo")
	ErrNoRelease           = errors.New("update: release not found")
	ErrAssetNotFound       = errors.New("update: release asset not found")
	ErrChecksumsMissing    = errors.New("update: checksums.txt asset missing")
	ErrChecksumEntry       = errors.New("update: checksum entry missing or malformed")
	ErrChecksumMismatch    = errors.New("update: checksum mismatch")
	ErrLocked              = errors.New("update: another update is in progress")
)

const staleLockAge = 10 * time.Minute

const defaultBaseURL = "https://api.github.com"

const assetPrefix = "smidja-"

type Latest struct {
	Version     string
	URL         string
	PublishedAt string
	Available   bool
	Asset       string
}

type Client struct {
	HTTP *http.Client

	BaseURL string

	Origin buildinfo.Info

	SelfCheck bool

	GOOS   string
	GOARCH string

	ExecPath func() (string, error)
}

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
		os.Remove(tmpName)
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

type release struct {
	TagName     string         `json:"tag_name"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt string         `json:"published_at"`
	Assets      []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

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

func (c *Client) assetName() string {
	return assetPrefix + c.goos() + "-" + c.goarch()
}

func (c *Client) selectAsset(assets []releaseAsset) string {
	want := c.assetName()
	for _, a := range assets {
		if a.Name == want {
			return a.Name
		}
	}
	return ""
}

func (c *Client) pickAsset(rel release) (name, url string) {
	want := c.assetName()
	for _, a := range rel.Assets {
		if a.Name == want {
			return a.Name, a.BrowserDownloadURL
		}
	}
	return "", ""
}

func (c *Client) pickChecksums(rel release) string {
	for _, a := range rel.Assets {
		if a.Name == "checksums.txt" {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

func acquireLock(path string) (unlock func(), err error) {
	f, err := lockFile(path)
	if err == nil {
		return lockRelease(path, f), nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("update: acquire lock: %w", err)
	}
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

func lockRelease(path string, f *os.File) func() {
	fmt.Fprintf(f, "pid=%d time=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	f.Close()
	return func() { os.Remove(path) }
}

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

func parseOrigin(origin string) (owner, repo string, err error) {
	parts := strings.Split(strings.TrimSpace(origin), "/")
	if len(parts) != 3 || parts[0] != "github.com" || parts[1] == "" || parts[2] == "" {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidOrigin, origin)
	}
	return parts[1], parts[2], nil
}
