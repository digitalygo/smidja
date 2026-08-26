package packages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type FetchArchive func(owner, repo, tagOrVersion string) (commitSHA string, archivePath string, err error)

type InstallOptions struct {
	PinCommit string
}

type Installer struct {
	Store *Store
	Fetch FetchArchive
}

func (i *Installer) Install(ctx context.Context, req Request, nodes []Node, opts InstallOptions) (InstalledRecord, error) {
	if i.Store == nil {
		return InstalledRecord{}, errors.New("packages: install: store is nil")
	}
	if i.Fetch == nil {
		return InstalledRecord{}, errors.New("packages: install: fetch is nil")
	}
	if !packageIDPattern.MatchString(req.ID) {
		return InstalledRecord{}, fmt.Errorf("packages: install: invalid id %q", req.ID)
	}
	if !isCanonicalVersion(req.Version) {
		return InstalledRecord{}, fmt.Errorf("packages: install: invalid version %q", req.Version)
	}
	if req.Owner == "" || req.Repo == "" {
		return InstalledRecord{}, errors.New("packages: install: owner and repo are required")
	}
	var record InstalledRecord
	err := i.Store.withLock(func() error {
		idx, err := i.Store.readIndex()
		if err != nil {
			return err
		}
		if _, ok := installedRecord(idx, req.ID, req.Version); ok {
			return fmt.Errorf("%w: %s@%s", ErrAlreadyInstalled, req.ID, req.Version)
		}
		dest := filepath.Join(i.Store.root, req.ID, req.Version)
		if _, err := os.Lstat(dest); err == nil {
			return fmt.Errorf("%w: %s@%s", ErrAlreadyInstalled, req.ID, req.Version)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("packages: install: stat %s: %w", dest, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		commit, archive, err := i.Fetch(req.Owner, req.Repo, req.Version)
		if err != nil {
			return err
		}
		if opts.PinCommit != "" && commit != opts.PinCommit {
			return fmt.Errorf("packages: install: %s@%s resolved commit %s does not match pin %s", req.ID, req.Version, commit, opts.PinCommit)
		}
		staging, err := os.MkdirTemp(filepath.Join(i.Store.root, ".staging"), "install-*")
		if err != nil {
			return fmt.Errorf("packages: install: stage: %w", err)
		}
		defer os.RemoveAll(staging)
		if err := extractArchive(archive, staging, ManifestFilename); err != nil {
			return err
		}
		manifestData, err := os.ReadFile(filepath.Join(staging, ManifestFilename))
		if err != nil {
			return fmt.Errorf("packages: install: %s@%s: %w", req.ID, req.Version, err)
		}
		m, err := Parse(manifestData)
		if err != nil {
			return err
		}
		if err := m.Validate(); err != nil {
			return err
		}
		if m.ID != req.ID || m.Version != req.Version || m.Owner != req.Owner || m.Repo != req.Repo {
			return fmt.Errorf("packages: install: manifest identity %s@%s from %s/%s does not match request %s@%s from %s/%s", m.ID, m.Version, m.Owner, m.Repo, req.ID, req.Version, req.Owner, req.Repo)
		}
		if err := ValidateTree(staging, m); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(i.Store.root, req.ID), 0o755); err != nil {
			return fmt.Errorf("packages: install: mkdir: %w", err)
		}
		if err := os.Rename(staging, dest); err != nil {
			return fmt.Errorf("packages: install: rename to %s: %w", dest, err)
		}
		if err := syncDir(filepath.Join(i.Store.root, req.ID)); err != nil {
			return err
		}
		sum := sha256.Sum256(manifestData)
		record = InstalledRecord{
			ID:              req.ID,
			Version:         req.Version,
			Owner:           req.Owner,
			Repo:            req.Repo,
			Commit:          commit,
			ManifestSHA256:  hex.EncodeToString(sum[:]),
			InstalledAt:     time.Now().UTC().Format(time.RFC3339),
			Integrity:       IntegrityOK,
			Authenticity:    AuthenticityUnverified,
			ResolvedDepends: closureFor(nodes, req.ID),
		}
		if err := writeReceipt(dest, record); err != nil {
			return err
		}
		idx.Installed = append(idx.Installed, record)
		return i.Store.writeIndex(idx)
	})
	if err != nil {
		return InstalledRecord{}, err
	}
	return record, nil
}
