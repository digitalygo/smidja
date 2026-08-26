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

var ErrNotASession = errors.New("session: not a valid session file")

type Loader struct {
	path     string
	header   *Header
	entries  []Entry
	byID     map[string]Entry
	children map[string][]Entry
	roots    []Entry
	leaf     Entry
}

func Load(path string) (*Loader, error) {
	rawLines, err := readSessionLines(path)
	if err != nil {
		return nil, err
	}
	l := &Loader{path: path}
	seenHeader := false
	for _, raw := range rawLines {
		if !seenHeader {
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
			continue
		}
		if e.EntryType() == EntryTypeSession {
			continue
		}
		l.entries = append(l.entries, e)
	}
	if l.header == nil {
		return nil, ErrNotASession
	}
	l.buildIndex()
	return l, nil
}

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
			l.roots = append(l.roots, e)
		default:
			if _, ok := l.byID[*parentID]; ok {
				l.children[*parentID] = append(l.children[*parentID], e)
			} else {
				l.roots = append(l.roots, e)
			}
		}
	}
	for parent := range l.children {
		sortChildrenByTimestamp(l.children[parent])
	}
}

func sortChildrenByTimestamp(children []Entry) {
	sort.SliceStable(children, func(i, j int) bool {
		_, _, ti := envelopeOf(children[i])
		_, _, tj := envelopeOf(children[j])
		return entryTime(ti).Before(entryTime(tj))
	})
}

func entryTime(ts string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (l *Loader) Path() string { return l.path }

func (l *Loader) Header() *Header { return l.header }

func (l *Loader) Entries() []Entry {
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

func (l *Loader) Leaf() Entry { return l.leaf }

func (l *Loader) Get(id string) (Entry, bool) {
	e, ok := l.byID[id]
	return e, ok
}

func (l *Loader) Roots() []Entry {
	out := make([]Entry, len(l.roots))
	copy(out, l.roots)
	return out
}

func (l *Loader) Children(id string) []Entry {
	children := l.children[id]
	out := make([]Entry, len(children))
	copy(out, children)
	return out
}

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
			break
		}
		current = next
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, nil
}

func (l *Loader) ActiveBranch() ([]Entry, error) {
	if l.leaf == nil {
		return []Entry{}, nil
	}
	id, _, _ := envelopeOf(l.leaf)
	return l.Branch(id)
}

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
