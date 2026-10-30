package privateconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func teamChange() Change {
	return Change{Action: "create", ID: "private:team", Kind: "team", Payload: json.RawMessage(`{"name":"Odoo","agents":[{"name":"Lead","instructions":"Manage leads"}]}`)}
}
func TestOwnerAndAgentApprovalLifecycle(t *testing.T) {
	s := openTest(t)
	change := teamChange()
	proposal, err := s.Propose(change)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List("", true)) != 0 || len(s.Pending()) != 1 {
		t.Fatal("proposal became active before approval")
	}
	proposal.Change.Payload[0] = 'x'
	stored, err := s.GetProposal(proposal.ID)
	if err != nil || stored.Change.Payload[0] != '{' {
		t.Fatal("caller mutated proposal")
	}
	entity, err := s.Approve(proposal.ID)
	if err != nil || entity.Revision != 1 {
		t.Fatal(entity, err)
	}
	if len(s.Pending()) != 0 || len(s.List("team", false)) != 1 || len(s.List("workflow", true)) != 0 {
		t.Fatal("list mismatch")
	}
	if _, err = s.Approve(proposal.ID); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err = s.Reject(proposal.ID); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	change.Action = "update"
	change.BaseRevision = 1
	change.Payload = json.RawMessage(`{"name":"Updated","agents":[{"name":"Manager"}]}`)
	stale, err := s.Propose(change)
	if err != nil {
		t.Fatal(err)
	}
	entity, err = s.ApplyOwner(change)
	if err != nil || entity.Revision != 2 {
		t.Fatal(entity, err)
	}
	if _, err = s.Approve(stale.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("stale approval accepted", err)
	}
	if err = s.Reject(stale.ID); err != nil {
		t.Fatal(err)
	}
	status, _ := s.GetProposal(stale.ID)
	if status.Status != "rejected" || status.ResolvedAt == nil {
		t.Fatal(status)
	}
	entity.Payload[0] = 'x'
	fresh, _ := s.Get(entity.ID)
	if fresh.Payload[0] != '{' {
		t.Fatal("caller mutated catalog")
	}
	if _, err = s.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.GetProposal("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.Approve("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err = s.Reject("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
func TestArchiveRestoreAndPurge(t *testing.T) {
	s := openTest(t)
	entity, err := s.ApplyOwner(teamChange())
	if err != nil {
		t.Fatal(err)
	}
	change := Change{Action: "archive", ID: entity.ID, Kind: entity.Kind, BaseRevision: 1}
	proposal, err := s.Propose(change)
	if err != nil {
		t.Fatal(err)
	}
	entity, err = s.Approve(proposal.ID)
	if err != nil || entity.ExpiresAt.Sub(*entity.ArchivedAt) != Retention {
		t.Fatal(entity, err)
	}
	if len(s.List("", false)) != 0 || len(s.List("", true)) != 1 {
		t.Fatal("archived visibility")
	}
	change.Action = "restore"
	change.BaseRevision = entity.Revision
	entity, err = s.ApplyOwner(change)
	if err != nil || entity.ArchivedAt != nil {
		t.Fatal(entity, err)
	}
	change.Action = "archive"
	change.BaseRevision = entity.Revision
	entity, err = s.ApplyOwner(change)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.PurgeExpired(entity.ExpiresAt.Add(-time.Second)); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if n, err := s.PurgeExpired(*entity.ExpiresAt); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err = s.Get(entity.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.ApplyOwner(teamChange()); !errors.Is(err, ErrConflict) {
		t.Fatal("purged identity reused", err)
	}
}
func TestTypedPayloadValidation(t *testing.T) {
	s := openTest(t)
	valid := []Change{{Action: "create", ID: "private:workflow", Kind: "workflow", Payload: json.RawMessage(`{"id":"review","version":"1","phases":[{"id":"review","name":"Review","team_ref":"private:team"}]}`)}, {Action: "create", ID: "private:extension", Kind: "extension", Payload: json.RawMessage(`{"name":"Odoo","description":"API","parameters":{"type":"object"},"command":["python3","odoo.py"],"client_id":"laptop"}`)}}
	for _, change := range valid {
		p, err := s.Propose(change)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Approve(p.ID); err != nil {
			t.Fatal(err)
		}
	}
	invalid := []struct{ kind, payload string }{{"team", `{`}, {"team", `{} {}`}, {"team", `{"unknown":1}`}, {"team", `{"name":"x","agents":[]}`}, {"team", `{"name":"x","agents":[{"name":"a"},{"name":"a"}]}`}, {"workflow", `{`}, {"workflow", `{}`}, {"workflow", `{"id":"x","version":"1","phases":[{"id":"x"}]}`}, {"workflow", `{"id":"x","version":"1","phases":[{"id":"x","team_ref":"y"},{"id":"x","team_ref":"y"}]}`}, {"extension", `{`}, {"extension", `{}`}, {"extension", `{"name":"x","command":["run"],"parameters":null}`}, {"unknown", `{}`}}
	for _, tc := range invalid {
		if err := validatePayload(tc.kind, json.RawMessage(tc.payload)); err == nil {
			t.Fatal(tc)
		}
	}
	if err := validatePayload("team", json.RawMessage(strings.Repeat(" ", 1<<20+1))); err == nil {
		t.Fatal("oversize accepted")
	}
	for _, change := range []Change{{Action: "create", ID: "package:org/team", Kind: "team"}, {Action: "create", ID: "private:x", Kind: "invalid"}, {Action: "update", ID: "private:absent", Kind: "team"}, {Action: "invalid", ID: "private:workflow", Kind: "workflow", BaseRevision: 1}, {Action: "update", ID: "private:workflow", Kind: "team", BaseRevision: 1}, {Action: "restore", ID: "private:workflow", Kind: "workflow", BaseRevision: 1}} {
		if _, err := s.ApplyOwner(change); err == nil {
			t.Fatal(change)
		}
	}
}
func TestJournalDurabilityLockAndCorruption(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Open(dir); err == nil {
		t.Fatal("second writer accepted")
	}
	proposal, err := s.Propose(teamChange())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Approve(proposal.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	entity, err := s.Get("private:team")
	if err != nil || entity.Revision != 1 {
		t.Fatal(entity, err)
	}
	p, err := s.GetProposal(proposal.ID)
	if err != nil || p.Status != "approved" {
		t.Fatal(p, err)
	}
	s.Close()
	if _, err = s.Propose(Change{Action: "archive", ID: entity.ID, Kind: entity.Kind, BaseRevision: 1}); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "private-config.jsonl")
	for _, content := range []string{"{", "{}\n", "not-json\n", "{\"version\":1,\"sequence\":9,\"purged\":[\"x\"]}\n"} {
		if err = os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if broken, err := Open(dir); err == nil {
			broken.Close()
			t.Fatal("corruption accepted", content)
		}
	}
	target := filepath.Join(t.TempDir(), "target")
	if err = os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	if err = os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(dir); err == nil {
		t.Fatal("symlink journal accepted")
	}
	if _, err = Open(target); err == nil {
		t.Fatal("file directory accepted")
	}
}
func TestConcurrentOwnerCASAndArchiveConflicts(t *testing.T) {
	s := openTest(t)
	_, err := s.ApplyOwner(teamChange())
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := s.ApplyOwner(Change{Action: "archive", ID: "private:team", Kind: "team", BaseRevision: 1})
			results <- err
		}()
	}
	group.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	for _, change := range []Change{{Action: "archive", ID: "private:team", Kind: "team", BaseRevision: 2}, {Action: "update", ID: "private:team", Kind: "team", BaseRevision: 2, Payload: teamChange().Payload}, {Action: "restore", ID: "private:team", Kind: "team", BaseRevision: 2, Payload: json.RawMessage(`{}`)}} {
		if _, err = s.ApplyOwner(change); err == nil {
			t.Fatal(change)
		}
	}
	s.mu.Lock()
	entity := s.entities["private:team"]
	expired := time.Now().Add(-time.Second)
	entity.ExpiresAt = &expired
	s.entities[entity.ID] = entity
	s.mu.Unlock()
	if _, err = s.ApplyOwner(Change{Action: "restore", ID: entity.ID, Kind: entity.Kind, BaseRevision: 2}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestExtensionEnvironmentReferences(t *testing.T) {
	for _, environment := range []string{`{"API_KEY":"ODOO_API_KEY"}`, `{"_TOKEN2":"SECRET_2"}`, `{}`} {
		payload := json.RawMessage(`{"name":"api","command":["run"],"parameters":{},"environment":` + environment + `}`)
		if err := validatePayload("extension", payload); err != nil {
			t.Fatal(err)
		}
	}
	for _, environment := range []string{`{"API-KEY":"TOKEN"}`, `{"API_KEY":"secret-value"}`, `{"API_KEY":"${TOKEN}"}`, `{"API_KEY":""}`, `{"1KEY":"TOKEN"}`} {
		payload := json.RawMessage(`{"name":"api","command":["run"],"parameters":{},"environment":` + environment + `}`)
		if err := validatePayload("extension", payload); err == nil {
			t.Fatal("literal or invalid environment reference accepted", environment)
		}
	}
}

func TestExtensionTimeoutBounds(t *testing.T) {
	for _, seconds := range []int{0, 1, 30, 600, -1, 601} {
		definition := Extension{Name: "test", Command: []string{"run"}, Parameters: json.RawMessage(`{}`), TimeoutSeconds: seconds}
		payload, _ := json.Marshal(definition)
		err := validatePayload("extension", payload)
		duration, timeoutErr := ExtensionTimeout(seconds)
		if seconds < 0 || seconds > 600 {
			if err == nil || timeoutErr == nil {
				t.Fatal("invalid timeout accepted", seconds)
			}
			continue
		}
		if seconds == 0 {
			seconds = 30
		}
		if err != nil || timeoutErr != nil || duration != time.Duration(seconds)*time.Second {
			t.Fatal(duration, err, timeoutErr)
		}
	}
}
