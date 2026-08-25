package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// ErrNotASession is returned by Load when the file does not parse as a Pi
// v3 session: its first valid JSON line is not a session header with a
// string id, or the file has no valid lines at all. It mirrors the
// validation in Pi's loadEntriesFromFile, which returns an empty entry
// list for such files.
var ErrNotASession = errors.New("session: not a valid session file")

// Loader parses one session file into an in-memory tree: a by-id index,
// per-parent children lists, the set of root entries, and the leaf (the
// last physical entry). It skips blank and malformed lines exactly like
// Pi's loadEntriesFromFile, and requires the first parsed line to be a
// session header.
type Loader struct {
	path     string
	header   *Header
	entries  []Entry // physical order, excludes the header
	byID     map[string]Entry
	children map[string][]Entry
	roots    []Entry
	leaf     Entry // last physical entry with a non-empty id; nil when none
}

// Load parses one session file into a Loader. The first parsed line must
// be a session header (type "session" with a string id); any later line
// with type "session" is ignored, matching Pi's _buildIndex. Blank and
// malformed lines are skipped. A file without a valid header (including
// an empty file) returns ErrNotASession.
func Load(path string) (*Loader, error) {
	rawLines, err := readSessionLines(path)
	if err != nil {
		return nil, err
	}
	l := &Loader{path: path}
	seenHeader := false
	for _, raw := range rawLines {
		if !seenHeader {
			// The first parsed line must be the session header. Lines
			// that do not parse as JSON at all are skipped like Pi;
			// lines that parse as JSON but are not a session header
			// make the whole file invalid.
			var probe struct {
				Type json.RawMessage `json:"type"`
			}
			if err := json.Unmarshal(raw, &probe); err != nil {
				continue
			}
			if !bytes.Equal(probe.Type, []byte(`"`+EntryTypeSession+`"`)) {
				return nil, ErrNotASession
			}
			var h Header
			if err := json.Unmarshal(raw, &h); err != nil {
				return nil, ErrNotASession
			}
			l.header = &h
			seenHeader = true
			continue
		}
		e, err := DecodeEntry(raw)
		if err != nil {
			continue // malformed line, skipped like Pi
		}
		if e.EntryType() == EntryTypeSession {
			continue // stray header after the first one is ignored
		}
		l.entries = append(l.entries, e)
	}
	if l.header == nil {
		return nil, ErrNotASession
	}
	l.buildIndex()
	return l, nil
}

// readSessionLines reads one file and returns every non-blank line with
// the trailing newline (and optional carriage return) stripped. Blank
// lines are dropped here; malformed lines are dropped by the caller.
func readSessionLines(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open %q: %w", path, err)
	}
	defer f.Close()
	br := bufio.NewReader(f)
	var lines [][]byte
	for {
		chunk, err := br.ReadBytes('\n')
		line := bytes.TrimRight(chunk, "\r\n")
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("session: read %q: %w", path, err)
		}
	}
	return lines, nil
}

// buildIndex fills byID, children, roots, and leaf from the physical
// entry list. Entries without an id are excluded from the tree but stay
// in the physical list. Children of one parent are ordered by timestamp
// (oldest first), mirroring Pi's getTree.
func (l *Loader) buildIndex() {
	l.byID = make(map[string]Entry, len(l.entries))
	l.children = make(map[string][]Entry)
	for _, e := range l.entries {
		id, _, _ := envelopeOf(e)
		if id == "" {
			continue
		}
		l.byID[id] = e
		l.leaf = e
	}
	for _, e := range l.entries {
		id, parentID, _ := envelopeOf(e)
		if id == "" {
			continue
		}
		switch {
		case parentID == nil || *parentID == id:
			// Root: first entry of a branch, self-parent, or broken chain.
			l.roots = append(l.roots, e)
		default:
			if _, ok := l.byID[*parentID]; ok {
				l.children[*parentID] = append(l.children[*parentID], e)
			} else {
				l.roots = append(l.roots, e) // orphaned entry
			}
		}
	}
	for parent := range l.children {
		sortChildrenByTimestamp(l.children[parent])
	}
}

// sortChildrenByTimestamp orders children oldest-first by their ISO
// timestamp, matching Pi's getTree. Entries whose timestamp does not
// parse keep their relative order (stable sort).
func sortChildrenByTimestamp(children []Entry) {
	sort.SliceStable(children, func(i, j int) bool {
		_, _, ti := envelopeOf(children[i])
		_, _, tj := envelopeOf(children[j])
		return entryTime(ti).Before(entryTime(tj))
	})
}

// entryTime parses an ISO timestamp; unparseable timestamps map to the
// zero time so they sort first without disturbing relative order.
func entryTime(ts string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Path returns the session file path the loader read.
func (l *Loader) Path() string { return l.path }

// Header returns the session header.
func (l *Loader) Header() *Header { return l.header }

// Entries returns a copy of the entries in physical order, excluding the
// header, mirroring Pi's getEntries.
func (l *Loader) Entries() []Entry {
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Leaf returns the last physical entry with a non-empty id, or nil when
// the file has no entries. The leaf is the current position of the
// session's main branch, exactly like Pi's getLeafId on an un-branched
// session.
func (l *Loader) Leaf() Entry { return l.leaf }

// Get returns the entry with the given id, mirroring Pi's getEntry.
func (l *Loader) Get(id string) (Entry, bool) {
	e, ok := l.byID[id]
	return e, ok
}

// Roots returns the root entries: entries with a null or self parent and
// entries whose parent is missing from the file (orphans), mirroring
// Pi's getTree.
func (l *Loader) Roots() []Entry {
	out := make([]Entry, len(l.roots))
	copy(out, l.roots)
	return out
}

// Children returns the direct children of the entry with the given id,
// oldest first by timestamp, mirroring Pi's getChildren plus getTree's
// child ordering. It returns an empty slice for unknown ids.
func (l *Loader) Children(id string) []Entry {
	children := l.children[id]
	out := make([]Entry, len(children))
	copy(out, children)
	return out
}

// Branch walks from the entry with the given id to the root, returning
// the entries in path order (root first). The walk stops at a broken
// parent chain (a parent id that is not in the file), leaving the branch
// truncated at the gap, like Pi's getBranch. Parent cycles, which would
// hang Pi, return an error. An unknown fromID returns an error.
func (l *Loader) Branch(fromID string) ([]Entry, error) {
	current, ok := l.byID[fromID]
	if !ok {
		return nil, fmt.Errorf("session: entry %q not found", fromID)
	}
	var path []Entry
	seen := make(map[string]bool)
	for current != nil {
		id, parentID, _ := envelopeOf(current)
		if seen[id] {
			return nil, fmt.Errorf("session: parent cycle at entry %q", id)
		}
		seen[id] = true
		path = append(path, current)
		if parentID == nil {
			break
		}
		next, ok := l.byID[*parentID]
		if !ok {
			break // broken parent chain: truncate the branch
		}
		current = next
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, nil
}

// ActiveBranch returns the branch from the leaf to the root, the active
// path of the session. It returns an error when the leaf is missing or
// its chain is cyclic; an empty slice when the file has no entries.
func (l *Loader) ActiveBranch() ([]Entry, error) {
	if l.leaf == nil {
		return []Entry{}, nil
	}
	id, _, _ := envelopeOf(l.leaf)
	return l.Branch(id)
}

// BuildContextEntries returns the compaction-aware active entry list:
// the active branch with the summarized prefix removed. When the branch
// contains a compaction entry, the result is the compaction entry itself,
// followed by the kept entries starting at FirstKeptEntryID (inclusive)
// and everything after the compaction entry; entries before the first
// kept entry are omitted. Without a compaction entry the full active
// branch is returned. This mirrors Pi's buildContextEntries.
func (l *Loader) BuildContextEntries() ([]Entry, error) {
	path, err := l.ActiveBranch()
	if err != nil {
		return nil, err
	}
	var compaction *CompactionEntry
	for _, e := range path {
		if c, ok := e.(*CompactionEntry); ok {
			compaction = c
		}
	}
	if compaction == nil {
		return path, nil
	}
	compactionIdx := -1
	for i, e := range path {
		if e == Entry(compaction) {
			compactionIdx = i
			break
		}
	}
	if compactionIdx < 0 {
		return path, nil
	}
	contextEntries := []Entry{compaction}
	foundFirstKept := false
	for i := 0; i < compactionIdx; i++ {
		e := path[i]
		id, _, _ := envelopeOf(e)
		if id == compaction.FirstKeptEntryID {
			foundFirstKept = true
		}
		if foundFirstKept {
			contextEntries = append(contextEntries, e)
		}
	}
	contextEntries = append(contextEntries, path[compactionIdx+1:]...)
	return contextEntries, nil
}
