package gateway

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const journalFileName = "journal.jsonl"

type Record struct {
	ID              string    `json:"id"`
	Ts              time.Time `json:"ts"`
	Transport       string    `json:"transport"`
	ExternalChatKey string    `json:"externalChatKey"`
	UserIDHash      string    `json:"userIDHash"`
	Text            string    `json:"text"`
	Status          string    `json:"status"`
	SessionID       string    `json:"sessionID,omitempty"`
	ErrorClass      string    `json:"errorClass,omitempty"`
}

var statusRank = map[string]int{
	StatusAccepted:       1,
	StatusStarted:        2,
	StatusCompleted:      3,
	StatusFailed:         3,
	StatusCancelled:      3,
	StatusOutcomeUnknown: 3,
}

type Journal struct {
	path   string
	keep   int
	mu     sync.Mutex
	file   *os.File
	recs   []Record
	byID   map[string]int
	closed bool
}

func OpenJournal(dir string, keep int) (*Journal, error) {
	if dir == "" {
		return nil, errors.New("gateway: empty journal dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("gateway: create journal dir %q: %w", dir, err)
	}
	path := filepath.Join(dir, journalFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("gateway: open journal %q: %w", path, err)
	}
	j := &Journal{path: path, keep: keep, file: f, byID: make(map[string]int)}
	if err := j.load(); err != nil {
		f.Close()
		return nil, err
	}
	return j, nil
}

func (j *Journal) load() error {
	if _, err := j.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("gateway: seek journal %q: %w", j.path, err)
	}
	br := bufio.NewReader(j.file)
	var recs []Record
	byID := make(map[string]int)
	for {
		line, err := br.ReadBytes('\n')
		line = trimLine(line)
		if len(line) > 0 {
			var r Record
			if json.Unmarshal(line, &r) == nil && r.ID != "" && knownStatus(r.Status) {
				recs = append(recs, r)
				byID[r.ID] = len(recs) - 1
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("gateway: read journal %q: %w", j.path, err)
		}
	}
	j.recs = recs
	j.byID = byID
	return nil
}

func trimLine(line []byte) []byte {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line
}

func knownStatus(s string) bool {
	_, ok := statusRank[s]
	return ok
}

func validTransition(from, to string) bool {
	fr, fok := statusRank[from]
	tr, tok := statusRank[to]
	if !fok || !tok {
		return false
	}
	if from == to {
		return true
	}
	return fr < tr
}

func (j *Journal) Append(r Record) error {
	if !knownStatus(r.Status) {
		return fmt.Errorf("gateway: unknown status %q: %w", r.Status, ErrInvalidTransition)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrClosed
	}
	return j.appendLocked(r)
}

func (j *Journal) AppendUnique(r Record) (bool, error) {
	if !knownStatus(r.Status) {
		return false, fmt.Errorf("gateway: unknown status %q: %w", r.Status, ErrInvalidTransition)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return false, ErrClosed
	}
	if _, ok := j.byID[r.ID]; ok {
		return false, nil
	}
	if err := j.appendLocked(r); err != nil {
		return false, err
	}
	return true, nil
}

func (j *Journal) appendLocked(r Record) error {
	if r.ID == "" {
		return fmt.Errorf("%w: empty record id", ErrInvalidMessage)
	}
	if r.Ts.IsZero() {
		r.Ts = time.Now().UTC()
	}
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("gateway: marshal journal record: %w", err)
	}
	if _, err := j.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("gateway: append journal record: %w", err)
	}
	if err := j.file.Sync(); err != nil {
		return fmt.Errorf("gateway: sync journal: %w", err)
	}
	j.recs = append(j.recs, r)
	j.byID[r.ID] = len(j.recs) - 1
	return nil
}

func (j *Journal) MarkStarted(id, sessionID string) error {
	return j.mark(id, StatusStarted, sessionID, "")
}

func (j *Journal) MarkCompleted(id string) error {
	return j.mark(id, StatusCompleted, "", "")
}

func (j *Journal) MarkFailed(id, errClass string) error {
	return j.mark(id, StatusFailed, "", errClass)
}

func (j *Journal) MarkCancelled(id string) error {
	return j.mark(id, StatusCancelled, "", "")
}

func (j *Journal) MarkOutcomeUnknown(id string) error {
	return j.mark(id, StatusOutcomeUnknown, "", "")
}

func (j *Journal) mark(id, status, sessionID, errClass string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrClosed
	}
	idx, ok := j.byID[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRecordNotFound, id)
	}
	cur := j.recs[idx]
	if !validTransition(cur.Status, status) {
		return fmt.Errorf("%w: %q from %q to %q", ErrInvalidTransition, id, cur.Status, status)
	}
	next := cur
	next.Status = status
	if sessionID != "" {
		next.SessionID = sessionID
	}
	if errClass != "" {
		next.ErrorClass = errClass
	}
	return j.appendLocked(next)
}

func (j *Journal) ReplayPending() ([]Record, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil, ErrClosed
	}
	var out []Record
	for i, r := range j.recs {
		if j.byID[r.ID] == i && r.Status == StatusAccepted {
			out = append(out, r)
		}
	}
	return out, nil
}

func (j *Journal) Get(id string) (Record, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	idx, ok := j.byID[id]
	if !ok {
		return Record{}, false
	}
	return j.recs[idx], true
}

func (j *Journal) Records() []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Record, len(j.recs))
	copy(out, j.recs)
	return out
}

func (j *Journal) Len() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.byID)
}

func (j *Journal) Compact() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrClosed
	}
	if j.keep <= 0 {
		return nil
	}
	var ids []string
	seen := make(map[string]bool, len(j.byID))
	for _, r := range j.recs {
		if !seen[r.ID] {
			seen[r.ID] = true
			ids = append(ids, r.ID)
		}
	}
	if len(ids) <= j.keep {
		return nil
	}
	keptSet := make(map[string]bool, j.keep)
	for _, id := range ids[len(ids)-j.keep:] {
		keptSet[id] = true
	}
	var kept []Record
	for i, r := range j.recs {
		if keptSet[r.ID] && j.byID[r.ID] == i {
			kept = append(kept, r)
		}
	}
	dir := filepath.Dir(j.path)
	tmp, err := os.CreateTemp(dir, ".journal-*.tmp")
	if err != nil {
		return fmt.Errorf("gateway: create journal temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func(wrapErr error) error {
		tmp.Close()
		os.Remove(tmpName)
		return wrapErr
	}
	if err := tmp.Chmod(0o600); err != nil {
		return cleanup(fmt.Errorf("gateway: chmod journal temp: %w", err))
	}
	for _, r := range kept {
		line, err := json.Marshal(r)
		if err != nil {
			return cleanup(fmt.Errorf("gateway: marshal journal record: %w", err))
		}
		if _, err := tmp.Write(append(line, '\n')); err != nil {
			return cleanup(fmt.Errorf("gateway: write journal temp: %w", err))
		}
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(fmt.Errorf("gateway: sync journal temp: %w", err))
	}
	if err := tmp.Close(); err != nil {
		return cleanup(fmt.Errorf("gateway: close journal temp: %w", err))
	}
	if err := j.file.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("gateway: close journal: %w", err)
	}
	if err := os.Rename(tmpName, j.path); err != nil {
		os.Remove(tmpName)
		reopened, rerr := os.OpenFile(j.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
		if rerr != nil {
			return fmt.Errorf("gateway: rename journal temp: %w", err)
		}
		j.file = reopened
		return fmt.Errorf("gateway: rename journal temp: %w", err)
	}
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("gateway: reopen journal %q: %w", j.path, err)
	}
	j.file = f
	j.recs = kept
	j.byID = make(map[string]int, len(kept))
	for i, r := range kept {
		j.byID[r.ID] = i
	}
	return nil
}

func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	if j.file == nil {
		return nil
	}
	err := j.file.Close()
	j.file = nil
	if err != nil {
		return fmt.Errorf("gateway: close journal %q: %w", j.path, err)
	}
	return nil
}
