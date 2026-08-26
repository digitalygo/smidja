package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/digitalygo/smidja/internal/packages"
)

var (
	githubAPIBase = "https://api.github.com"

	codeloadBase = "https://codeload.github.com"
)

func githubFetchArchive(owner, repo, version string) (string, string, error) {
	commit, err := resolveTagToCommit(owner, repo, version)
	if err != nil {
		return "", "", err
	}
	url := fmt.Sprintf("%s/%s/%s/tar.gz/%s", codeloadBase, owner, repo, commit)
	resp, err := http.Get(url)
	if err != nil {
		return "", "", fmt.Errorf("pkg: download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("pkg: download %s: %s (missing, private or rate-limited)", url, resp.Status)
	}
	tmp, err := os.CreateTemp("", "smidja-pkg-*.tar.gz")
	if err != nil {
		return "", "", fmt.Errorf("pkg: temp archive: %w", err)
	}
	name := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(name)
		return "", "", fmt.Errorf("pkg: save archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", "", fmt.Errorf("pkg: close archive: %w", err)
	}
	return commit, name, nil
}

func resolveTagToCommit(owner, repo, tag string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/git/refs/tags/%s", githubAPIBase, owner, repo, tag)
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("pkg: resolve tag %s: %w", tag, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("pkg: resolve tag %s: repository %s/%s not found or private (public repositories only)", tag, owner, repo)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pkg: resolve tag %s: %s", tag, resp.Status)
	}
	var ref struct {
		Object struct {
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"object"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ref); err != nil {
		return "", fmt.Errorf("pkg: resolve tag %s: %w", tag, err)
	}
	if ref.Object.SHA == "" {
		return "", fmt.Errorf("pkg: resolve tag %s: empty commit", tag)
	}
	if ref.Object.Type != "tag" {
		return ref.Object.SHA, nil
	}
	return derefTagObject(owner, repo, ref.Object.SHA)
}

func derefTagObject(owner, repo, sha string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/git/tags/%s", githubAPIBase, owner, repo, sha)
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("pkg: resolve tag object %s: %w", sha, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pkg: resolve tag object %s: %s", sha, resp.Status)
	}
	var tag struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tag); err != nil {
		return "", fmt.Errorf("pkg: resolve tag object %s: %w", sha, err)
	}
	if tag.Object.SHA == "" {
		return "", fmt.Errorf("pkg: resolve tag object %s: empty commit", sha)
	}
	return tag.Object.SHA, nil
}

func githubLatestVersion(owner, repo string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/tags", githubAPIBase, owner, repo)
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("pkg: list tags for %s/%s: %w", owner, repo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pkg: list tags for %s/%s: %s (public repositories only)", owner, repo, resp.Status)
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", fmt.Errorf("pkg: list tags for %s/%s: %w", owner, repo, err)
	}
	for _, t := range tags {
		if packages.IsCanonicalVersion(t.Name) {
			return t.Name, nil
		}
	}
	return "", fmt.Errorf("pkg: no canonical version tags found for %s/%s", owner, repo)
}
