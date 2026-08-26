package packages

import (
	"errors"
	"strings"
	"testing"
)

func resolveIDs(t *testing.T, nodes []Node) []string {
	t.Helper()
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

func assertIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("resolution = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("resolution = %v, want %v", got, want)
		}
	}
}

func TestResolveChain(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", []Dependency{{ID: "base", Owner: "acme", Repo: "base", MinimumVersion: "v1.0.0"}})
	base := graphManifest("base", "v1.0.0", "acme", "base", nil)
	fetch, calls := universeFetch(t, root, base)
	nodes, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, nil, fetch)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertIDs(t, resolveIDs(t, nodes), []string{"base", "app"})
	if *calls != 2 {
		t.Errorf("fetch calls = %d, want 2", *calls)
	}
}

func TestResolveDiamond(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", []Dependency{
		{ID: "a", Owner: "acme", Repo: "a", MinimumVersion: "v1.0.0"},
		{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"},
	})
	a := graphManifest("a", "v1.0.0", "acme", "a", []Dependency{{ID: "c", Owner: "acme", Repo: "c", MinimumVersion: "v1.0.0"}})
	b := graphManifest("b", "v1.0.0", "acme", "b", []Dependency{{ID: "c", Owner: "acme", Repo: "c", MinimumVersion: "v1.0.0"}})
	c := graphManifest("c", "v1.0.0", "acme", "c", nil)
	fetch, _ := universeFetch(t, root, a, b, c)
	nodes, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, nil, fetch)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertIDs(t, resolveIDs(t, nodes), []string{"c", "a", "b", "app"})
}

func TestResolveGreatestMinimum(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", []Dependency{
		{ID: "a", Owner: "acme", Repo: "a", MinimumVersion: "v1.0.0"},
		{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"},
	})
	a1 := graphManifest("a", "v1.0.0", "acme", "a", nil)
	a2 := graphManifest("a", "v2.0.0", "acme", "a", nil)
	b := graphManifest("b", "v1.0.0", "acme", "b", []Dependency{{ID: "a", Owner: "acme", Repo: "a", MinimumVersion: "v2.0.0"}})
	fetch, _ := universeFetch(t, root, a1, a2, b)
	nodes, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, nil, fetch)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, n := range nodes {
		if n.ID == "a" && n.Version != "v2.0.0" {
			t.Errorf("a resolved to %s, want v2.0.0 (greatest minimum)", n.Version)
		}
	}
	assertIDs(t, resolveIDs(t, nodes), []string{"a", "b", "app"})
}

func TestResolveExactAgreement(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", []Dependency{
		{ID: "a", Owner: "acme", Repo: "a", ExactVersion: "v1.0.0"},
		{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"},
	})
	a := graphManifest("a", "v1.0.0", "acme", "a", nil)
	b := graphManifest("b", "v1.0.0", "acme", "b", []Dependency{{ID: "a", Owner: "acme", Repo: "a", ExactVersion: "v1.0.0"}})
	fetch, _ := universeFetch(t, root, a, b)
	nodes, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, nil, fetch)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, n := range nodes {
		if n.ID == "a" && n.Version != "v1.0.0" {
			t.Errorf("a resolved to %s, want v1.0.0", n.Version)
		}
	}
}

func TestResolveConflicts(t *testing.T) {
	cases := []struct {
		name string
		root func() Manifest
		idx  map[string]InstalledInfo
		want error
	}{
		{
			name: "conflicting exact versions",
			root: func() Manifest {
				return graphManifest("app", "v1.0.0", "acme", "app", []Dependency{
					{ID: "a", Owner: "acme", Repo: "a", ExactVersion: "v1.0.0"},
					{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"},
				})
			},
			want: ErrConflict,
		},
		{
			name: "exact below minimum",
			root: func() Manifest {
				return graphManifest("app", "v1.0.0", "acme", "app", []Dependency{
					{ID: "a", Owner: "acme", Repo: "a", ExactVersion: "v1.0.0"},
					{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"},
				})
			},
			want: ErrConflict,
		},
		{
			name: "owner repo disagreement",
			root: func() Manifest {
				return graphManifest("app", "v1.0.0", "acme", "app", []Dependency{
					{ID: "a", Owner: "acme", Repo: "a", MinimumVersion: "v1.0.0"},
					{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"},
				})
			},
			want: ErrConflict,
		},
		{
			name: "root pinned below dependency minimum",
			root: func() Manifest {
				return graphManifest("app", "v1.0.0", "acme", "app", []Dependency{
					{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"},
				})
			},
			want: ErrConflict,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := tc.root()
			var fetch FetchFunc
			switch tc.name {
			case "conflicting exact versions":
				a := graphManifest("a", "v1.0.0", "acme", "a", nil)
				a2 := graphManifest("a", "v2.0.0", "acme", "a", nil)
				b := graphManifest("b", "v1.0.0", "acme", "b", []Dependency{{ID: "a", Owner: "acme", Repo: "a", ExactVersion: "v2.0.0"}})
				fetch, _ = universeFetch(t, root, a, a2, b)
			case "exact below minimum":
				a := graphManifest("a", "v1.0.0", "acme", "a", nil)
				b := graphManifest("b", "v1.0.0", "acme", "b", []Dependency{{ID: "a", Owner: "acme", Repo: "a", MinimumVersion: "v2.0.0"}})
				fetch, _ = universeFetch(t, root, a, b)
			case "owner repo disagreement":
				a := graphManifest("a", "v1.0.0", "acme", "a", nil)
				aOther := graphManifest("a", "v1.0.0", "other", "a", nil)
				b := graphManifest("b", "v1.0.0", "acme", "b", []Dependency{{ID: "a", Owner: "other", Repo: "a", MinimumVersion: "v1.0.0"}})
				fetch, _ = universeFetch(t, root, a, aOther, b)
			case "root pinned below dependency minimum":
				b := graphManifest("b", "v1.0.0", "acme", "b", []Dependency{{ID: "app", Owner: "acme", Repo: "app", MinimumVersion: "v2.0.0"}})
				fetch, _ = universeFetch(t, root, b)
			}
			_, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, tc.idx, fetch)
			if !errors.Is(err, tc.want) {
				t.Errorf("Resolve error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestResolveCycle(t *testing.T) {
	a := graphManifest("a", "v1.0.0", "acme", "a", []Dependency{{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"}})
	b := graphManifest("b", "v1.0.0", "acme", "b", []Dependency{{ID: "a", Owner: "acme", Repo: "a", MinimumVersion: "v1.0.0"}})
	fetch, _ := universeFetch(t, a, b)
	_, err := Resolve(Request{Owner: "acme", Repo: "a", ID: "a", Version: "v1.0.0"}, nil, fetch)
	if !errors.Is(err, ErrCycle) {
		t.Errorf("Resolve error = %v, want ErrCycle", err)
	}
}

func TestResolveSelfDependency(t *testing.T) {
	a := graphManifest("a", "v1.0.0", "acme", "a", []Dependency{{ID: "a", Owner: "acme", Repo: "a", MinimumVersion: "v1.0.0"}})
	fetch, _ := universeFetch(t, a)
	_, err := Resolve(Request{Owner: "acme", Repo: "a", ID: "a", Version: "v1.0.0"}, nil, fetch)
	if !errors.Is(err, ErrCycle) {
		t.Errorf("Resolve error = %v, want ErrCycle", err)
	}
}

func TestResolveConvergence(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", []Dependency{
		{ID: "a", Owner: "acme", Repo: "a", MinimumVersion: "v1.0.0"},
		{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"},
	})
	a := graphManifest("a", "v1.0.0", "acme", "a", []Dependency{{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v2.0.0"}})
	b1 := graphManifest("b", "v1.0.0", "acme", "b", nil)
	b2 := graphManifest("b", "v2.0.0", "acme", "b", nil)
	fetch, _ := universeFetch(t, root, a, b1, b2)
	nodes, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, nil, fetch)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, n := range nodes {
		if n.ID == "b" && n.Version != "v2.0.0" {
			t.Errorf("b resolved to %s, want v2.0.0 (fixed point bump)", n.Version)
		}
	}
}

func TestResolveIndexHit(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", []Dependency{{ID: "base", Owner: "acme", Repo: "base", MinimumVersion: "v1.0.0"}})
	base := graphManifest("base", "v1.0.0", "acme", "base", nil)
	index := map[string]InstalledInfo{
		"base": {ID: "base", Version: "v1.0.0", Owner: "acme", Repo: "base", Manifest: base},
	}
	fetch, calls := universeFetch(t, root)
	nodes, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, index, fetch)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if *calls != 1 {
		t.Errorf("fetch calls = %d, want 1 (only root)", *calls)
	}
	for _, n := range nodes {
		if n.ID == "base" && !n.Installed {
			t.Error("base should be marked Installed from index")
		}
	}
}

func TestResolveRootFromIndex(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", nil)
	index := map[string]InstalledInfo{
		"app": {ID: "app", Version: "v1.0.0", Owner: "acme", Repo: "app", Manifest: root},
	}
	fetch, calls := universeFetch(t, root)
	nodes, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, index, fetch)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if *calls != 0 {
		t.Errorf("fetch calls = %d, want 0", *calls)
	}
	if len(nodes) != 1 || !nodes[0].Installed {
		t.Errorf("nodes = %+v, want single installed root", nodes)
	}
}

func TestResolveIndexVersionMissFetches(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", []Dependency{{ID: "base", Owner: "acme", Repo: "base", MinimumVersion: "v2.0.0"}})
	base1 := graphManifest("base", "v1.0.0", "acme", "base", nil)
	base2 := graphManifest("base", "v2.0.0", "acme", "base", nil)
	index := map[string]InstalledInfo{
		"base": {ID: "base", Version: "v1.0.0", Owner: "acme", Repo: "base", Manifest: base1},
	}
	fetch, calls := universeFetch(t, root, base2)
	nodes, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, index, fetch)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if *calls != 2 {
		t.Errorf("fetch calls = %d, want 2 (root + base v2)", *calls)
	}
	for _, n := range nodes {
		if n.ID == "base" {
			if n.Version != "v2.0.0" {
				t.Errorf("base resolved to %s, want v2.0.0", n.Version)
			}
			if n.Installed {
				t.Error("base must not be marked Installed when index version differs")
			}
		}
	}
}

func TestResolveFetchMismatch(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", []Dependency{{ID: "base", Owner: "acme", Repo: "base", MinimumVersion: "v1.0.0"}})
	fetch := func(req Request) (Node, error) {
		if req.ID == "app" {
			return nodeFromManifest(root), nil
		}
		return nodeFromManifest(graphManifest("base", "v9.0.0", "acme", "base", nil)), nil
	}
	_, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, nil, fetch)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("Resolve error = %v, want ErrConflict for fetch mismatch", err)
	}
}

func TestResolveManifestIdentityMismatch(t *testing.T) {
	fetch := func(req Request) (Node, error) {
		return Node{ID: req.ID, Version: req.Version, Owner: req.Owner, Repo: req.Repo}, nil
	}
	_, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, nil, fetch)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("Resolve error = %v, want ErrConflict for missing manifest", err)
	}
}

func TestResolveIncompleteRoot(t *testing.T) {
	_, err := Resolve(Request{ID: "app"}, nil, func(Request) (Node, error) { return Node{}, nil })
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("Resolve error = %v, want incomplete root request", err)
	}
}

func TestResolveNoFetchNeededWithoutDeps(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", nil)
	fetch, calls := universeFetch(t, root)
	nodes, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, nil, fetch)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != "app" || nodes[0].Version != "v1.0.0" {
		t.Errorf("nodes = %+v, want single app@v1.0.0", nodes)
	}
	if *calls != 1 {
		t.Errorf("fetch calls = %d, want 1", *calls)
	}
}

func TestResolveNoFetchFunc(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", []Dependency{{ID: "base", Owner: "acme", Repo: "base", MinimumVersion: "v2.0.0"}})
	index := map[string]InstalledInfo{
		"app":  {ID: "app", Version: "v1.0.0", Owner: "acme", Repo: "app", Manifest: root},
		"base": {ID: "base", Version: "v1.0.0", Owner: "acme", Repo: "base", Manifest: graphManifest("base", "v1.0.0", "acme", "base", nil)},
	}
	_, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, index, nil)
	if err == nil || !strings.Contains(err.Error(), "no fetch func") {
		t.Errorf("Resolve error = %v, want no fetch func", err)
	}
}

func TestClosureFor(t *testing.T) {
	root := graphManifest("app", "v1.0.0", "acme", "app", []Dependency{
		{ID: "a", Owner: "acme", Repo: "a", MinimumVersion: "v1.0.0"},
		{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"},
	})
	a := graphManifest("a", "v1.0.0", "acme", "a", []Dependency{{ID: "c", Owner: "acme", Repo: "c", MinimumVersion: "v1.0.0"}})
	b := graphManifest("b", "v1.0.0", "acme", "b", nil)
	c := graphManifest("c", "v1.0.0", "acme", "c", nil)
	fetch, _ := universeFetch(t, root, a, b, c)
	nodes, err := Resolve(Request{Owner: "acme", Repo: "app", ID: "app", Version: "v1.0.0"}, nil, fetch)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := closureFor(nodes, "app")
	want := []ResolvedDependency{{ID: "b", Version: "v1.0.0"}, {ID: "c", Version: "v1.0.0"}, {ID: "a", Version: "v1.0.0"}}
	if len(got) != len(want) {
		t.Fatalf("closureFor(app) = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("closureFor(app) = %+v, want %+v", got, want)
			break
		}
	}
	gotA := closureFor(nodes, "a")
	if len(gotA) != 1 || gotA[0].ID != "c" {
		t.Errorf("closureFor(a) = %+v, want [c]", gotA)
	}
	if gotB := closureFor(nodes, "b"); len(gotB) != 0 {
		t.Errorf("closureFor(b) = %+v, want []", gotB)
	}
}
