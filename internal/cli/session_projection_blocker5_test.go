package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

const blocker5Header = `{"type":"session","version":3,"id":"b5a00000-0000-4000-8000-000000000001","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp/blocker5"}`

func blocker5UserLine(id, parent, text string, stamp int) string {
	return `{"type":"message","id":"` + id + `","parentId":` + blocker5Parent(parent) + `,"timestamp":"2026-01-01T00:00:0` + string(rune('0'+stamp)) + `.000Z","message":{"role":"user","content":"` + text + `","timestamp":` + itoa(int64(stamp)) + `}}`
}

func blocker5CompactionLine(id, parent, anchor, summary string, stamp int) string {
	return `{"type":"compaction","id":"` + id + `","parentId":` + blocker5Parent(parent) + `,"timestamp":"2026-01-01T00:00:0` + string(rune('0'+stamp)) + `.000Z","summary":"` + summary + `","firstKeptEntryId":"` + anchor + `","tokensBefore":7}`
}

func blocker5Parent(parent string) string {
	if parent == "" {
		return "null"
	}
	return `"` + parent + `"`
}

func blocker5RawLoader(t *testing.T, lines []string) *session.Loader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p4Load(t, path)
}

func blocker5AppendUser(t *testing.T, sess *session.Session, text string, stamp int64) {
	t.Helper()
	if err := sess.AppendUser(&agent.UserMessage{Role: "user", Content: json.RawMessage(`"` + text + `"`), Timestamp: stamp}); err != nil {
		t.Fatal(err)
	}
}

func blocker5AppendCompaction(t *testing.T, sess *session.Session, anchor, summary string) {
	t.Helper()
	if err := sess.AppendEntry(&session.CompactionEntry{Summary: summary, FirstKeptEntryID: anchor, TokensBefore: 7}); err != nil {
		t.Fatal(err)
	}
}

func TestP4Blocker5CompactionAnchorOrdering(t *testing.T) {
	cases := []struct {
		name        string
		build       func(t *testing.T) *session.Loader
		wantAnchor  string
		wantCount   int
		wantTexts   []string
		wantMissing []string
		fullBranch  bool
	}{
		{
			name: "valid prior anchor keeps the compacted context",
			build: func(t *testing.T) *session.Loader {
				_, _, sess := p4SeedStore(t)
				blocker5AppendUser(t, sess, "first", 1)
				firstID := session.EntryID(p4Load(t, sess.Path()).Leaf())
				blocker5AppendUser(t, sess, "second", 2)
				blocker5AppendCompaction(t, sess, firstID, "kept summary")
				blocker5AppendUser(t, sess, "after", 3)
				path := sess.Path()
				if err := sess.Close(); err != nil {
					t.Fatal(err)
				}
				return p4Load(t, path)
			},
			wantCount: 4,
			wantTexts: []string{"kept summary", "first", "second", "after"},
		},
		{
			name: "self anchor recovers the uncompacted branch",
			build: func(t *testing.T) *session.Loader {
				return blocker5RawLoader(t, []string{
					blocker5Header,
					blocker5UserLine("b5a00001", "", "first", 1),
					blocker5CompactionLine("b5a00002", "b5a00001", "b5a00002", "self summary", 2),
					blocker5UserLine("b5a00003", "b5a00002", "after", 3),
				})
			},
			wantAnchor: "b5a00002",
			wantCount:  3,
			wantTexts:  []string{"first", "self summary", "after"},
			fullBranch: true,
		},
		{
			name: "forward anchor recovers the uncompacted branch",
			build: func(t *testing.T) *session.Loader {
				return blocker5RawLoader(t, []string{
					blocker5Header,
					blocker5UserLine("b5b00001", "", "first", 1),
					blocker5CompactionLine("b5b00002", "b5b00001", "b5b00003", "later summary", 2),
					blocker5UserLine("b5b00003", "b5b00002", "after", 3),
				})
			},
			wantAnchor: "b5b00003",
			wantCount:  3,
			wantTexts:  []string{"first", "later summary", "after"},
			fullBranch: true,
		},
		{
			name: "missing anchor recovers the uncompacted branch",
			build: func(t *testing.T) *session.Loader {
				_, _, sess := p4SeedStore(t)
				blocker5AppendUser(t, sess, "first", 1)
				blocker5AppendCompaction(t, sess, "b5missing", "lost summary")
				blocker5AppendUser(t, sess, "after", 2)
				path := sess.Path()
				if err := sess.Close(); err != nil {
					t.Fatal(err)
				}
				return p4Load(t, path)
			},
			wantAnchor: "b5missing",
			wantCount:  3,
			wantTexts:  []string{"first", "lost summary", "after"},
			fullBranch: true,
		},
		{
			name: "superseded bad anchor does not trigger recovery",
			build: func(t *testing.T) *session.Loader {
				_, _, sess := p4SeedStore(t)
				blocker5AppendUser(t, sess, "first", 1)
				firstID := session.EntryID(p4Load(t, sess.Path()).Leaf())
				blocker5AppendCompaction(t, sess, "b5ghost", "stale summary")
				blocker5AppendUser(t, sess, "second", 2)
				blocker5AppendCompaction(t, sess, firstID, "fresh summary")
				blocker5AppendUser(t, sess, "after", 3)
				path := sess.Path()
				if err := sess.Close(); err != nil {
					t.Fatal(err)
				}
				return p4Load(t, path)
			},
			wantCount: 5,
			wantTexts: []string{"fresh summary", "stale summary", "first", "second", "after"},
		},
		{
			name: "latest bad anchor recovers despite older valid compaction",
			build: func(t *testing.T) *session.Loader {
				_, _, sess := p4SeedStore(t)
				blocker5AppendUser(t, sess, "first", 1)
				firstID := session.EntryID(p4Load(t, sess.Path()).Leaf())
				blocker5AppendCompaction(t, sess, firstID, "old summary")
				blocker5AppendUser(t, sess, "second", 2)
				blocker5AppendCompaction(t, sess, "b5ghost", "broken summary")
				blocker5AppendUser(t, sess, "after", 3)
				path := sess.Path()
				if err := sess.Close(); err != nil {
					t.Fatal(err)
				}
				return p4Load(t, path)
			},
			wantAnchor: "b5ghost",
			wantCount:  5,
			wantTexts:  []string{"first", "old summary", "second", "broken summary", "after"},
			fullBranch: true,
		},
		{
			name: "anchor on an inactive branch recovers the active branch",
			build: func(t *testing.T) *session.Loader {
				return blocker5RawLoader(t, []string{
					blocker5Header,
					blocker5UserLine("b5c00001", "", "first", 1),
					blocker5UserLine("b5c00002", "b5c00001", "side branch", 2),
					blocker5UserLine("b5c00003", "b5c00001", "main line", 3),
					blocker5CompactionLine("b5c00004", "b5c00003", "b5c00002", "crossed summary", 4),
					blocker5UserLine("b5c00005", "b5c00004", "after", 5),
				})
			},
			wantAnchor:  "b5c00002",
			wantCount:   4,
			wantTexts:   []string{"first", "main line", "crossed summary", "after"},
			wantMissing: []string{"side branch"},
			fullBranch:  true,
		},
		{
			name: "empty firstKept keeps the compacted context",
			build: func(t *testing.T) *session.Loader {
				_, _, sess := p4SeedStore(t)
				blocker5AppendUser(t, sess, "head one", 1)
				blocker5AppendUser(t, sess, "head two", 2)
				blocker5AppendCompaction(t, sess, "", "full summary")
				blocker5AppendUser(t, sess, "after", 3)
				path := sess.Path()
				if err := sess.Close(); err != nil {
					t.Fatal(err)
				}
				return p4Load(t, path)
			},
			wantCount:   2,
			wantTexts:   []string{"full summary", "after"},
			wantMissing: []string{"head one", "head two"},
		},
		{
			name: "no compaction keeps the full branch",
			build: func(t *testing.T) *session.Loader {
				_, _, sess := p4SeedStore(t)
				blocker5AppendUser(t, sess, "first", 1)
				blocker5AppendUser(t, sess, "second", 2)
				blocker5AppendUser(t, sess, "after", 3)
				path := sess.Path()
				if err := sess.Close(); err != nil {
					t.Fatal(err)
				}
				return p4Load(t, path)
			},
			wantCount:  3,
			wantTexts:  []string{"first", "second", "after"},
			fullBranch: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loader := tc.build(t)
			history, entryIDs, warnings, err := projectModelHistoryWithIDs(loader)
			if err != nil {
				t.Fatal(err)
			}
			if len(history) == 0 {
				t.Fatal("history fell back to an empty context")
			}
			if len(history) != len(entryIDs) {
				t.Fatalf("history %d entryIDs %d must align", len(history), len(entryIDs))
			}
			if len(history) != tc.wantCount {
				text := p4HistoryText(history)
				t.Fatalf("history = %d entries:\n%s\nwant %d", len(history), text, tc.wantCount)
			}
			for _, id := range entryIDs {
				if id == "" || strings.Contains(id, "#") || strings.Contains(id, ":") {
					t.Fatalf("entry id %q looks invented", id)
				}
			}
			text := p4HistoryText(history)
			for _, want := range tc.wantTexts {
				if !strings.Contains(text, want) {
					t.Fatalf("history missing %q:\n%s", want, text)
				}
			}
			for _, gone := range tc.wantMissing {
				if strings.Contains(text, gone) {
					t.Fatalf("history leaked %q:\n%s", gone, text)
				}
			}
			if tc.wantAnchor == "" {
				if len(warnings) != 0 {
					t.Fatalf("warnings = %v, want none", warnings)
				}
			} else {
				found := false
				for _, warning := range warnings {
					if strings.Contains(warning, "compaction anchor "+tc.wantAnchor) &&
						strings.Contains(warning, "unresolved") &&
						strings.Contains(warning, "uncompacted") &&
						strings.Contains(warning, "fresh") {
						found = true
					}
				}
				if !found {
					t.Fatalf("warnings = %v, want unresolved uncompacted fresh recovery warning", warnings)
				}
			}
			if tc.fullBranch {
				branch, err := loader.ActiveBranch()
				if err != nil {
					t.Fatal(err)
				}
				branchIDs := make(map[string]bool, len(branch))
				for _, entry := range branch {
					branchIDs[session.EntryID(entry)] = true
				}
				if len(entryIDs) != len(branchIDs) {
					t.Fatalf("entry ids %v do not cover the active branch with %d entries", entryIDs, len(branchIDs))
				}
				for _, id := range entryIDs {
					if !branchIDs[id] {
						t.Fatalf("entry id %q is not on the active branch", id)
					}
				}
			}
		})
	}
}
