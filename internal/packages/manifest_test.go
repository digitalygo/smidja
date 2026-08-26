package packages

import (
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	data := []byte(`{
		"schemaVersion": 0,
		"id": "acme-tools",
		"version": "v1.2.3",
		"owner": "acme",
		"repo": "tools",
		"description": "test",
		"contents": {"skills": "skills", "agents": "agents"},
		"depends": [{"id": "base", "owner": "acme", "repo": "base", "minimumVersion": "v1.0.0"}],
		"minimumHarness": "v0.1.0",
		"files": [
			{"path": "agents/a.md", "sha256": "` + digest + `", "size": 12},
			{"path": "skills/read.md", "sha256": "` + digest + `", "size": 10}
		]
	}`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.SchemaVersion != 0 || m.ID != "acme-tools" || m.Version != "v1.2.3" || m.Owner != "acme" || m.Repo != "tools" {
		t.Errorf("identity fields not populated: %+v", m)
	}
	if m.Description != "test" || m.MinimumHarness != "v0.1.0" {
		t.Errorf("description or minimumHarness wrong: %+v", m)
	}
	if len(m.Contents) != 2 || m.Contents["skills"] != "skills" {
		t.Errorf("contents wrong: %+v", m.Contents)
	}
	if len(m.Depends) != 1 || m.Depends[0].ID != "base" || m.Depends[0].MinimumVersion != "v1.0.0" {
		t.Errorf("depends wrong: %+v", m.Depends)
	}
	if len(m.Files) != 2 || m.Files[0].Path != "agents/a.md" || m.Files[1].Path != "skills/read.md" {
		t.Errorf("files wrong: %+v", m.Files)
	}
}

func TestParseMinimal(t *testing.T) {
	digest := strings.Repeat("cd", 32)
	data := []byte(`{"schemaVersion":0,"id":"x","version":"v0.0.0","owner":"o","repo":"r","minimumHarness":"v0.1.0","files":[{"path":"skills/a.md","sha256":"` + digest + `","size":1}]}`)
	m, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Description != "" || len(m.Contents) != 0 || len(m.Depends) != 0 {
		t.Errorf("optional fields not zero: %+v", m)
	}
}

func TestParseRejects(t *testing.T) {
	digest := strings.Repeat("ef", 32)
	base := `{"schemaVersion":0,"id":"x","version":"v0.0.0","owner":"o","repo":"r","minimumHarness":"v0.1.0","contents":{"skills":"skills"},"files":[{"path":"skills/a.md","sha256":"` + digest + `","size":1}]}`
	cases := []struct {
		name string
		data string
		want string
	}{
		{"unknown top-level field", `{"schemaVersion":0,"id":"x","version":"v0.0.0","owner":"o","repo":"r","minimumHarness":"v0.1.0","files":[],"bogus":1}`, "unknown field"},
		{"unknown file field", `{"schemaVersion":0,"id":"x","version":"v0.0.0","owner":"o","repo":"r","minimumHarness":"v0.1.0","files":[{"path":"skills/a.md","sha256":"` + digest + `","size":1,"extra":2}]}`, "unknown field"},
		{"unknown dependency field", `{"schemaVersion":0,"id":"x","version":"v0.0.0","owner":"o","repo":"r","minimumHarness":"v0.1.0","depends":[{"id":"b","owner":"o","repo":"b","minimumVersion":"v1.0.0","extra":3}],"files":[]}`, "unknown field"},
		{"duplicate top-level field", `{"schemaVersion":0,"id":"x","version":"v0.0.0","owner":"o","repo":"r","minimumHarness":"v0.1.0","files":[],"id":"y"}`, "duplicate field"},
		{"duplicate nested field", `{"schemaVersion":0,"id":"x","version":"v0.0.0","owner":"o","repo":"r","minimumHarness":"v0.1.0","files":[{"path":"skills/a.md","path":"skills/b.md","sha256":"` + digest + `","size":1}]}`, "duplicate field"},
		{"duplicate dependency field", `{"schemaVersion":0,"id":"x","version":"v0.0.0","owner":"o","repo":"r","minimumHarness":"v0.1.0","depends":[{"id":"b","id":"c","owner":"o","repo":"b","minimumVersion":"v1.0.0"}],"files":[]}`, "duplicate field"},
		{"array top level", `[1,2]`, "must be an object"},
		{"null top level", `null`, "must be an object"},
		{"number top level", `42`, "must be an object"},
		{"string top level", `"x"`, "must be an object"},
		{"trailing data", base + ` garbage`, "invalid JSON"},
		{"trailing object", base + ` {}`, "trailing data"},
		{"broken json", `{"schemaVersion":0,`, "invalid JSON"},
		{"empty input", ``, "invalid JSON"},
		{"wrong type", `{"schemaVersion":"zero"}`, "cannot unmarshal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.data))
			if err == nil {
				t.Fatal("Parse succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Parse error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestParseDuplicateContentKey(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	data := []byte(`{"schemaVersion":0,"id":"x","version":"v0.0.0","owner":"o","repo":"r","minimumHarness":"v0.1.0","contents":{"skills":"skills","skills":"agents"},"files":[{"path":"skills/a.md","sha256":"` + digest + `","size":1}]}`)
	if _, err := Parse(data); err == nil || !strings.Contains(err.Error(), "duplicate field") {
		t.Errorf("Parse error = %v, want duplicate field", err)
	}
}

func TestValidate(t *testing.T) {
	valid := func() Manifest {
		return testManifest("acme-tools", "v1.2.3", "acme", "tools", map[string]string{"skills": "skills", "agents": "agents"}, map[string]string{
			"agents/a.md":    "hello",
			"skills/read.md": "world",
		}, []Dependency{{ID: "base", Owner: "acme", Repo: "base", MinimumVersion: "v1.0.0"}})
	}
	cases := []struct {
		name   string
		mutate func(m *Manifest)
		want   string
	}{
		{"valid", func(m *Manifest) {}, ""},
		{"schema version", func(m *Manifest) { m.SchemaVersion = 1 }, "schemaVersion"},
		{"empty id", func(m *Manifest) { m.ID = "" }, "id"},
		{"uppercase id", func(m *Manifest) { m.ID = "Acme" }, "id"},
		{"long id", func(m *Manifest) { m.ID = strings.Repeat("a", 65) }, "id"},
		{"invalid chars id", func(m *Manifest) { m.ID = "a_b" }, "id"},
		{"empty version", func(m *Manifest) { m.Version = "" }, "version"},
		{"version no v prefix", func(m *Manifest) { m.Version = "1.2.3" }, "version"},
		{"version two parts", func(m *Manifest) { m.Version = "v1.2" }, "version"},
		{"version leading zero", func(m *Manifest) { m.Version = "v01.2.3" }, "version"},
		{"version prerelease", func(m *Manifest) { m.Version = "v1.2.3-beta" }, "version"},
		{"empty minimum harness", func(m *Manifest) { m.MinimumHarness = "" }, "minimumHarness"},
		{"bad minimum harness", func(m *Manifest) { m.MinimumHarness = "v1" }, "minimumHarness"},
		{"empty owner", func(m *Manifest) { m.Owner = "" }, "owner"},
		{"empty repo", func(m *Manifest) { m.Repo = "" }, "repo"},
		{"long description", func(m *Manifest) { m.Description = strings.Repeat("é", 201) }, "description"},
		{"description boundary", func(m *Manifest) { m.Description = strings.Repeat("é", 200) }, ""},
		{"unknown content kind", func(m *Manifest) { m.Contents["templates"] = "templates" }, "unknown content kind"},
		{"absolute content path", func(m *Manifest) { m.Contents["skills"] = "/abs" }, "clean relative"},
		{"traversal content path", func(m *Manifest) { m.Contents["skills"] = "../skills" }, "clean relative"},
		{"dot content path", func(m *Manifest) { m.Contents["skills"] = "./skills" }, "clean relative"},
		{"trailing slash content path", func(m *Manifest) { m.Contents["skills"] = "skills/" }, "clean relative"},
		{"overlapping content roots", func(m *Manifest) { m.Contents["config"] = "skills/sub" }, "overlap"},
		{"duplicate content values", func(m *Manifest) { m.Contents["agents"] = "skills" }, "overlap"},
		{"dependency both versions", func(m *Manifest) {
			m.Depends = []Dependency{{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0", ExactVersion: "v1.0.0"}}
		}, "both"},
		{"dependency no version", func(m *Manifest) {
			m.Depends = []Dependency{{ID: "b", Owner: "acme", Repo: "b"}}
		}, "exactly one"},
		{"dependency bad version", func(m *Manifest) {
			m.Depends = []Dependency{{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1"}}
		}, "canonical"},
		{"dependency no owner", func(m *Manifest) {
			m.Depends = []Dependency{{ID: "b", Repo: "b", MinimumVersion: "v1.0.0"}}
		}, "owner"},
		{"dependency bad id", func(m *Manifest) {
			m.Depends = []Dependency{{ID: "B!", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"}}
		}, "dependency id"},
		{"self reference", func(m *Manifest) {
			m.Depends = []Dependency{{ID: "acme-tools", Owner: "acme", Repo: "tools", MinimumVersion: "v1.0.0"}}
		}, "itself"},
		{"duplicate dependency id", func(m *Manifest) {
			m.Depends = []Dependency{
				{ID: "b", Owner: "acme", Repo: "b", MinimumVersion: "v1.0.0"},
				{ID: "b", Owner: "acme", Repo: "b", ExactVersion: "v2.0.0"},
			}
		}, "duplicate dependency"},
		{"unsorted files", func(m *Manifest) {
			m.Files[0], m.Files[1] = m.Files[1], m.Files[0]
		}, "sorted"},
		{"duplicate file path", func(m *Manifest) {
			m.Files = append(m.Files, m.Files[0])
		}, "sorted"},
		{"file outside contents", func(m *Manifest) {
			m.Files = append(m.Files, FileEntry{Path: "other/x.md", SHA256: strings.Repeat("a", 64), Size: 1})
		}, "not under a content root"},
		{"file equal content root", func(m *Manifest) {
			m.Files[0].Path = "skills"
		}, "not under a content root"},
		{"file traversal", func(m *Manifest) {
			m.Files[0].Path = "skills/../../etc/passwd"
		}, "clean relative"},
		{"file absolute", func(m *Manifest) {
			m.Files[0].Path = "/etc/passwd"
		}, "clean relative"},
		{"file no contents", func(m *Manifest) {
			m.Contents = map[string]string{}
		}, "not under a content root"},
		{"bad sha length", func(m *Manifest) {
			m.Files[0].SHA256 = strings.Repeat("a", 63)
		}, "64 hex"},
		{"bad sha chars", func(m *Manifest) {
			m.Files[0].SHA256 = strings.Repeat("z", 64)
		}, "64 hex"},
		{"zero size", func(m *Manifest) {
			m.Files[0].Size = 0
		}, "positive"},
		{"negative size", func(m *Manifest) {
			m.Files[0].Size = -1
		}, "positive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := valid()
			tc.mutate(&m)
			err := m.Validate()
			if tc.want == "" {
				if err != nil {
					t.Errorf("Validate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate succeeded, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate error = %v, want containing %q", err, tc.want)
			}
		})
	}
}
