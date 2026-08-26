package packages

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const maxResolutionRounds = 100

var (
	ErrLocked           = errors.New("packages: store is locked")
	ErrNotInstalled     = errors.New("packages: package not installed")
	ErrAlreadyInstalled = errors.New("packages: package already installed")
	ErrActive           = errors.New("packages: package is active")
	ErrHasDependents    = errors.New("packages: package has dependents")
	ErrConflict         = errors.New("packages: dependency conflict")
	ErrCycle            = errors.New("packages: dependency cycle")
)

type Request struct {
	Owner   string
	Repo    string
	ID      string
	Version string
}

type InstalledInfo struct {
	ID       string
	Version  string
	Owner    string
	Repo     string
	Manifest Manifest
}

type Node struct {
	ID        string
	Owner     string
	Repo      string
	Version   string
	Manifest  Manifest
	Installed bool
}

type FetchFunc func(req Request) (Node, error)

type pickKey struct {
	owner   string
	repo    string
	id      string
	version string
}

type resolver struct {
	index map[string]InstalledInfo
	fetch FetchFunc
	cache map[pickKey]Node
}

func Resolve(root Request, index map[string]InstalledInfo, fetch FetchFunc) ([]Node, error) {
	if root.ID == "" || root.Version == "" || root.Owner == "" || root.Repo == "" {
		return nil, errors.New("packages: resolve: incomplete root request")
	}
	r := &resolver{index: index, fetch: fetch, cache: map[pickKey]Node{}}
	rootDep := Dependency{ID: root.ID, Owner: root.Owner, Repo: root.Repo, ExactVersion: root.Version}
	var resolved map[string]Node
	for round := 0; ; round++ {
		if round >= maxResolutionRounds {
			return nil, errors.New("packages: resolve: failed to converge")
		}
		constraints := map[string][]Dependency{}
		if round == 0 {
			constraints[root.ID] = []Dependency{rootDep}
		} else {
			for id, n := range resolved {
				for _, dep := range n.Manifest.Depends {
					if dep.ID == id {
						return nil, fmt.Errorf("%w: %s depends on itself", ErrCycle, id)
					}
					constraints[dep.ID] = append(constraints[dep.ID], dep)
				}
			}
			constraints[root.ID] = append([]Dependency{rootDep}, constraints[root.ID]...)
		}
		next := make(map[string]Node, len(constraints))
		for id, deps := range constraints {
			req, err := requiredRequest(id, deps)
			if err != nil {
				return nil, err
			}
			n, err := r.pick(req)
			if err != nil {
				return nil, err
			}
			next[id] = n
		}
		if round > 0 && sameResolution(resolved, next) {
			resolved = next
			break
		}
		resolved = next
	}
	nodes := make([]Node, 0, len(resolved))
	for _, n := range resolved {
		nodes = append(nodes, n)
	}
	if err := detectCycles(nodes); err != nil {
		return nil, err
	}
	return depsFirstOrder(nodes)
}

func requiredRequest(id string, deps []Dependency) (Request, error) {
	req := Request{ID: id}
	var exact string
	var maxMin string
	for _, d := range deps {
		if d.Owner == "" || d.Repo == "" {
			return Request{}, fmt.Errorf("packages: resolve: %s: dependency missing owner or repo", id)
		}
		if req.Owner == "" {
			req.Owner, req.Repo = d.Owner, d.Repo
		} else if d.Owner != req.Owner || d.Repo != req.Repo {
			return Request{}, fmt.Errorf("%w: %s: owner/repo %s/%s vs %s/%s", ErrConflict, id, d.Owner, d.Repo, req.Owner, req.Repo)
		}
		switch {
		case d.ExactVersion != "" && d.MinimumVersion != "":
			return Request{}, fmt.Errorf("packages: resolve: %s: both exact and minimum", id)
		case d.ExactVersion != "":
			if exact == "" {
				exact = d.ExactVersion
			} else if exact != d.ExactVersion {
				return Request{}, fmt.Errorf("%w: %s: exact %s vs %s", ErrConflict, id, exact, d.ExactVersion)
			}
		case d.MinimumVersion != "":
			if maxMin == "" || compareVersions(d.MinimumVersion, maxMin) > 0 {
				maxMin = d.MinimumVersion
			}
		default:
			return Request{}, fmt.Errorf("packages: resolve: %s: no version constraint", id)
		}
	}
	switch {
	case exact != "":
		if maxMin != "" && compareVersions(exact, maxMin) < 0 {
			return Request{}, fmt.Errorf("%w: %s: exact %s below minimum %s", ErrConflict, id, exact, maxMin)
		}
		req.Version = exact
	case maxMin != "":
		req.Version = maxMin
	default:
		return Request{}, fmt.Errorf("packages: resolve: %s: no version constraint", id)
	}
	return req, nil
}

func (r *resolver) pick(req Request) (Node, error) {
	if info, ok := r.index[req.ID]; ok && info.Version == req.Version && info.Owner == req.Owner && info.Repo == req.Repo {
		if info.Manifest.ID == "" {
			return Node{}, fmt.Errorf("packages: resolve: installed %s@%s has no manifest", req.ID, req.Version)
		}
		return Node{ID: info.ID, Version: info.Version, Owner: info.Owner, Repo: info.Repo, Manifest: info.Manifest, Installed: true}, nil
	}
	key := pickKey{owner: req.Owner, repo: req.Repo, id: req.ID, version: req.Version}
	if n, ok := r.cache[key]; ok {
		return n, nil
	}
	if r.fetch == nil {
		return Node{}, fmt.Errorf("packages: resolve: %s: no fetch func", req.ID)
	}
	n, err := r.fetch(req)
	if err != nil {
		return Node{}, err
	}
	if n.ID != req.ID || n.Version != req.Version || n.Owner != req.Owner || n.Repo != req.Repo {
		return Node{}, fmt.Errorf("%w: %s: fetch returned %s@%s from %s/%s", ErrConflict, req.ID, n.ID, n.Version, n.Owner, n.Repo)
	}
	if n.Manifest.ID == "" || n.Manifest.ID != n.ID || n.Manifest.Version != n.Version || n.Manifest.Owner != n.Owner || n.Manifest.Repo != n.Repo {
		return Node{}, fmt.Errorf("%w: %s: fetch manifest mismatch", ErrConflict, req.ID)
	}
	r.cache[key] = n
	return n, nil
}

func sameResolution(a, b map[string]Node) bool {
	if len(a) != len(b) {
		return false
	}
	for id, na := range a {
		nb, ok := b[id]
		if !ok || na.Version != nb.Version || na.Owner != nb.Owner || na.Repo != nb.Repo {
			return false
		}
	}
	return true
}

func detectCycles(nodes []Node) error {
	adj := map[string][]string{}
	for _, n := range nodes {
		for _, d := range n.Manifest.Depends {
			adj[n.ID] = append(adj[n.ID], d.ID)
		}
	}
	state := map[string]int{}
	var visit func(id string, path []string) error
	visit = func(id string, path []string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("%w: %s", ErrCycle, strings.Join(append(path, id), " -> "))
		case 2:
			return nil
		}
		state[id] = 1
		for _, d := range adj[id] {
			if err := visit(d, append(path, id)); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range adj {
		if err := visit(id, nil); err != nil {
			return err
		}
	}
	return nil
}

func depsFirstOrder(nodes []Node) ([]Node, error) {
	byID := map[string]Node{}
	dependents := map[string][]string{}
	indeg := map[string]int{}
	for _, n := range nodes {
		byID[n.ID] = n
		indeg[n.ID] = len(n.Manifest.Depends)
		for _, d := range n.Manifest.Depends {
			dependents[d.ID] = append(dependents[d.ID], n.ID)
		}
	}
	ready := []string{}
	for _, n := range nodes {
		if indeg[n.ID] == 0 {
			ready = append(ready, n.ID)
		}
	}
	sort.Strings(ready)
	order := make([]Node, 0, len(nodes))
	placed := map[string]bool{}
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		if placed[id] {
			continue
		}
		placed[id] = true
		order = append(order, byID[id])
		for _, dep := range dependents[id] {
			indeg[dep]--
			if indeg[dep] == 0 {
				ready = insertSorted(ready, dep)
			}
		}
	}
	if len(order) != len(nodes) {
		return nil, fmt.Errorf("%w: not a DAG", ErrCycle)
	}
	return order, nil
}

func insertSorted(list []string, v string) []string {
	i := sort.SearchStrings(list, v)
	list = append(list, "")
	copy(list[i+1:], list[i:])
	list[i] = v
	return list
}

func closureFor(nodes []Node, id string) []ResolvedDependency {
	byID := map[string]Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	reach := map[string]bool{}
	var visit func(cur string)
	visit = func(cur string) {
		for _, dep := range byID[cur].Manifest.Depends {
			if !reach[dep.ID] {
				reach[dep.ID] = true
				visit(dep.ID)
			}
		}
	}
	visit(id)
	out := []ResolvedDependency{}
	for _, n := range nodes {
		if n.ID != id && reach[n.ID] {
			out = append(out, ResolvedDependency{ID: n.ID, Version: n.Version})
		}
	}
	return out
}
