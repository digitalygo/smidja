package packages

import (
	"fmt"
	"os"
	"path/filepath"
)

func (s *Store) Activate(id, version string) error {
	if !packageIDPattern.MatchString(id) {
		return fmt.Errorf("packages: activate: invalid id %q", id)
	}
	if !isCanonicalVersion(version) {
		return fmt.Errorf("packages: activate: invalid version %q", version)
	}
	return s.withLock(func() error {
		idx, err := s.readIndex()
		if err != nil {
			return err
		}
		rec, ok := installedRecord(idx, id, version)
		if !ok {
			return fmt.Errorf("%w: %s@%s", ErrNotInstalled, id, version)
		}
		if activeContains(idx.Active, id, version) {
			return nil
		}
		active, err := s.computeActive(idx, rec)
		if err != nil {
			return err
		}
		idx.Active = active
		return s.writeIndex(idx)
	})
}

func (s *Store) Deactivate(id, version string) error {
	if !packageIDPattern.MatchString(id) {
		return fmt.Errorf("packages: deactivate: invalid id %q", id)
	}
	if !isCanonicalVersion(version) {
		return fmt.Errorf("packages: deactivate: invalid version %q", version)
	}
	return s.withLock(func() error {
		idx, err := s.readIndex()
		if err != nil {
			return err
		}
		active := idx.Active[:0]
		for _, a := range idx.Active {
			if a.ID == id && a.Version == version {
				continue
			}
			active = append(active, a)
		}
		if len(active) == len(idx.Active) {
			return nil
		}
		idx.Active = active
		return s.writeIndex(idx)
	})
}

func (s *Store) Remove(id, version string) error {
	if !packageIDPattern.MatchString(id) {
		return fmt.Errorf("packages: remove: invalid id %q", id)
	}
	if !isCanonicalVersion(version) {
		return fmt.Errorf("packages: remove: invalid version %q", version)
	}
	return s.withLock(func() error {
		idx, err := s.readIndex()
		if err != nil {
			return err
		}
		if _, ok := installedRecord(idx, id, version); !ok {
			return fmt.Errorf("%w: %s@%s", ErrNotInstalled, id, version)
		}
		if activeContains(idx.Active, id, version) {
			return fmt.Errorf("%w: %s@%s", ErrActive, id, version)
		}
		for _, rec := range idx.Installed {
			if rec.ID == id && rec.Version == version {
				continue
			}
			for _, dep := range rec.ResolvedDepends {
				if dep.ID == id && dep.Version == version {
					return fmt.Errorf("%w: %s@%s requires %s@%s", ErrHasDependents, rec.ID, rec.Version, id, version)
				}
			}
		}
		if err := os.RemoveAll(filepath.Join(s.root, id, version)); err != nil {
			return fmt.Errorf("packages: remove: %w", err)
		}
		idx.Installed = removeInstalled(idx.Installed, id, version)
		return s.writeIndex(idx)
	})
}

func (s *Store) computeActive(idx Index, newRec InstalledRecord) ([]ActiveEntry, error) {
	records := map[string]InstalledRecord{}
	for _, rec := range idx.Installed {
		records[entryKey(rec.ID, rec.Version)] = rec
	}
	nodes := map[string]bool{}
	preference := []string{}
	for _, a := range idx.Active {
		key := entryKey(a.ID, a.Version)
		if _, ok := records[key]; !ok {
			return nil, fmt.Errorf("packages: activate: active %s@%s is not installed", a.ID, a.Version)
		}
		if nodes[key] {
			continue
		}
		nodes[key] = true
		preference = append(preference, key)
	}
	closure, err := closureRecords(newRec, records)
	if err != nil {
		return nil, err
	}
	for _, rec := range closure {
		key := entryKey(rec.ID, rec.Version)
		if nodes[key] {
			continue
		}
		nodes[key] = true
		preference = append(preference, key)
	}
	key := entryKey(newRec.ID, newRec.Version)
	if !nodes[key] {
		nodes[key] = true
		preference = append(preference, key)
	}
	indeg := map[string]int{}
	dependents := map[string][]string{}
	for k := range nodes {
		rec := records[k]
		for _, dep := range rec.ResolvedDepends {
			dk := entryKey(dep.ID, dep.Version)
			if !nodes[dk] {
				return nil, fmt.Errorf("packages: activate: missing dependency %s", dk)
			}
			indeg[k]++
			dependents[dk] = append(dependents[dk], k)
		}
	}
	placed := map[string]bool{}
	order := []ActiveEntry{}
	for len(placed) < len(nodes) {
		progressed := false
		for _, k := range preference {
			if placed[k] || indeg[k] > 0 {
				continue
			}
			placed[k] = true
			rec := records[k]
			order = append(order, ActiveEntry{ID: rec.ID, Version: rec.Version})
			for _, dep := range dependents[k] {
				indeg[dep]--
			}
			progressed = true
			break
		}
		if !progressed {
			return nil, fmt.Errorf("%w: %s", ErrCycle, key)
		}
	}
	return order, nil
}

func closureRecords(rec InstalledRecord, records map[string]InstalledRecord) ([]InstalledRecord, error) {
	out := []InstalledRecord{}
	seen := map[string]bool{}
	var walk func(r InstalledRecord) error
	walk = func(r InstalledRecord) error {
		for _, dep := range r.ResolvedDepends {
			key := entryKey(dep.ID, dep.Version)
			if seen[key] {
				continue
			}
			child, ok := records[key]
			if !ok {
				return fmt.Errorf("packages: activate: missing installed dependency %s", key)
			}
			seen[key] = true
			out = append(out, child)
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(rec); err != nil {
		return nil, err
	}
	return out, nil
}
