package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestJournalAppendAndGet(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer j.Close()
	rec := sampleRecord("m1")
	if err := j.Append(rec); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, ok := j.Get("m1")
	if !ok {
		t.Fatalf("record not found")
	}
	if got.ID != rec.ID || got.Transport != rec.Transport || got.Status != StatusAccepted {
		t.Fatalf("record mismatch: %+v", got)
	}
	if j.Len() != 1 {
		t.Fatalf("len = %d, want 1", j.Len())
	}
}

func TestJournalAppendUnique(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer j.Close()
	rec := sampleRecord("m1")
	inserted, err := j.AppendUnique(rec)
	if err != nil || !inserted {
		t.Fatalf("first append unique: inserted=%v err=%v", inserted, err)
	}
	inserted, err = j.AppendUnique(rec)
	if err != nil || inserted {
		t.Fatalf("duplicate append unique: inserted=%v err=%v", inserted, err)
	}
	if j.Len() != 1 {
		t.Fatalf("len = %d, want 1", j.Len())
	}
}

func TestJournalPermissions(t *testing.T) {
	jdir := filepath.Join(t.TempDir(), "gw")
	j, err := OpenJournal(jdir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer j.Close()
	if err := j.Append(sampleRecord("m1")); err != nil {
		t.Fatalf("append: %v", err)
	}
	info, err := os.Stat(filepath.Join(jdir, journalFileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("journal perms = %o, want 600", got)
	}
	dirInfo, err := os.Stat(jdir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir perms = %o, want 700", got)
	}
}

func TestJournalReplayPendingAndMarks(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	recs := []Record{sampleRecord("a"), sampleRecord("b"), sampleRecord("c")}
	for _, r := range recs {
		if err := j.Append(r); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := j.MarkStarted("b", "sess-b"); err != nil {
		t.Fatalf("mark started: %v", err)
	}
	if err := j.MarkCompleted("c"); err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	if err := j.MarkFailed("a", "boom"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	pending, err := reopened.ReplayPending()
	if err != nil {
		t.Fatalf("replay pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %d, want 0", len(pending))
	}
	b, ok := reopened.Get("b")
	if !ok || b.Status != StatusStarted || b.SessionID != "sess-b" {
		t.Fatalf("record b = %+v", b)
	}
	a, ok := reopened.Get("a")
	if !ok || a.Status != StatusFailed || a.ErrorClass != "boom" {
		t.Fatalf("record a = %+v", a)
	}
}

func TestJournalCrashReplayToleratesPartialTail(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := j.Append(sampleRecord(id)); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	path := filepath.Join(dir, journalFileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for crash write: %v", err)
	}
	if _, err := f.WriteString(`{"id":"partial"`); err != nil {
		t.Fatalf("crash write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close crash file: %v", err)
	}
	reopened, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("reopen after crash: %v", err)
	}
	defer reopened.Close()
	pending, err := reopened.ReplayPending()
	if err != nil {
		t.Fatalf("replay pending: %v", err)
	}
	if len(pending) != 3 {
		t.Fatalf("pending = %d, want 3", len(pending))
	}
	if pending[0].ID != "a" || pending[1].ID != "b" || pending[2].ID != "c" {
		t.Fatalf("pending order wrong: %+v", pending)
	}
}

func TestJournalStatusTransitions(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer j.Close()
	if err := j.Append(sampleRecord("m1")); err != nil {
		t.Fatalf("append: %v", err)
	}
	cases := []struct {
		name string
		mark func() error
		ok   bool
	}{
		{"accepted to started", func() error { return j.MarkStarted("m1", "") }, true},
		{"started to completed", func() error { return j.MarkCompleted("m1") }, true},
		{"completed to completed", func() error { return j.MarkCompleted("m1") }, true},
		{"completed to failed rejected", func() error { return j.MarkFailed("m1", "x") }, false},
		{"completed to cancelled rejected", func() error { return j.MarkCancelled("m1") }, false},
	}
	for _, tc := range cases {
		err := tc.mark()
		if tc.ok && err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("%s: expected ErrInvalidTransition, got %v", tc.name, err)
		}
	}
}

func TestJournalTransitionMatrix(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer j.Close()
	allowed := map[string][]string{
		StatusAccepted:       {StatusAccepted, StatusStarted, StatusCompleted, StatusFailed, StatusCancelled, StatusOutcomeUnknown},
		StatusStarted:        {StatusStarted, StatusCompleted, StatusFailed, StatusCancelled, StatusOutcomeUnknown},
		StatusCompleted:      {StatusCompleted},
		StatusFailed:         {StatusFailed},
		StatusCancelled:      {StatusCancelled},
		StatusOutcomeUnknown: {StatusOutcomeUnknown},
	}
	for from := range statusRank {
		for to := range statusRank {
			id := from + "-" + to
			rec := sampleRecord(id)
			rec.Status = from
			if err := j.Append(rec); err != nil {
				t.Fatalf("append %s: %v", id, err)
			}
			err := j.mark(id, to, "", "")
			want := contains(allowed[from], to)
			if want && err != nil {
				t.Fatalf("transition %s -> %s rejected: %v", from, to, err)
			}
			if !want && err == nil {
				t.Fatalf("transition %s -> %s should be rejected", from, to)
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestJournalMarkUnknownID(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer j.Close()
	err = j.MarkStarted("nope", "")
	if err == nil {
		t.Fatalf("expected error for unknown id")
	}
	if !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound, got %v", err)
	}
}

func TestJournalCompactionKeepsLastN(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 3)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if err := j.Append(sampleRecord(id)); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	if err := j.MarkStarted("d", ""); err != nil {
		t.Fatalf("mark started d: %v", err)
	}
	if err := j.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if j.Len() != 3 {
		t.Fatalf("len = %d, want 3", j.Len())
	}
	for _, id := range []string{"a", "b"} {
		if _, ok := j.Get(id); ok {
			t.Fatalf("record %s should be compacted away", id)
		}
	}
	if rec, ok := j.Get("d"); !ok || rec.Status != StatusStarted {
		t.Fatalf("record d should be kept with latest status: %+v", rec)
	}
	if _, ok := j.Get("e"); !ok {
		t.Fatalf("record e should be kept")
	}
	if err := j.Append(sampleRecord("f")); err != nil {
		t.Fatalf("append after compact: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := OpenJournal(dir, 3)
	if err != nil {
		t.Fatalf("reopen after compact: %v", err)
	}
	defer reopened.Close()
	if reopened.Len() != 4 {
		t.Fatalf("reopened len = %d, want 4", reopened.Len())
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".journal-*.tmp"))
	if err != nil {
		t.Fatalf("glob temp: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}
}

func TestJournalCompactNoopWhenWithinKeep(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 5)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer j.Close()
	for _, id := range []string{"a", "b", "c"} {
		if err := j.Append(sampleRecord(id)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := j.Compact(); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if j.Len() != 3 {
		t.Fatalf("len = %d, want 3", j.Len())
	}
}

func TestJournalRejectsAfterClose(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := j.Append(sampleRecord("m1")); !errors.Is(err, ErrClosed) {
		t.Fatalf("append after close: %v", err)
	}
	if _, err := j.ReplayPending(); !errors.Is(err, ErrClosed) {
		t.Fatalf("replay after close: %v", err)
	}
}

func TestJournalReplaySkipsSupersededStatuses(t *testing.T) {
	dir := t.TempDir()
	j, err := OpenJournal(dir, 100)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer j.Close()
	if err := j.Append(sampleRecord("a")); err != nil {
		t.Fatalf("append a: %v", err)
	}
	if err := j.Append(sampleRecord("b")); err != nil {
		t.Fatalf("append b: %v", err)
	}
	if err := j.MarkStarted("b", "sess"); err != nil {
		t.Fatalf("mark b: %v", err)
	}
	pending, err := j.ReplayPending()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "a" {
		t.Fatalf("pending = %+v, want [a]", pending)
	}
}
