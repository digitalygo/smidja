package manager

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

type activatingRemote struct {
	chatRemote
	teamRemote
}

func privateCatalog(t *testing.T) *privateconfig.Store {
	t.Helper()
	store, err := privateconfig.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}
func savePrivate(t *testing.T, store *privateconfig.Store, id, kind string, revision uint64, payload any) privateconfig.Entity {
	t.Helper()
	raw, _ := json.Marshal(payload)
	action := "create"
	if revision > 0 {
		action = "update"
	}
	entity, err := store.ApplyOwner(privateconfig.Change{Action: action, ID: id, Kind: kind, BaseRevision: revision, Payload: raw})
	if err != nil {
		t.Fatal(err)
	}
	return entity
}
func activationTeam(model string) privateconfig.Team {
	return privateconfig.Team{Name: "Reviewers", Agents: []privateconfig.Agent{{Name: "manager", Model: model, Instructions: "Review precisely"}}}
}
func TestPrivateWorkflowPinsTeamBeforeUpdate(t *testing.T) {
	catalog := privateCatalog(t)
	savePrivate(t, catalog, "private:reviewers", "team", 0, activationTeam("original-model"))
	workflow := managerstate.Workflow{ID: "private:review", Version: "1", Phases: []managerstate.Phase{{ID: "review", TeamRef: "private:reviewers"}}}
	savePrivate(t, catalog, "private:review", "workflow", 0, workflow)
	remote := &activatingRemote{}
	s := testServer(t, func(c *Config) { c.Remote = remote; c.Private = catalog })
	pinned, err := s.privateWorkflow("private:review")
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.cfg.Store.CreateTask(managerstate.StartTask{Workflow: pinned, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	savePrivate(t, catalog, "private:reviewers", "team", 1, activationTeam("updated-model"))
	current, err := s.privateWorkflow("private:review")
	if err != nil || current.Version == pinned.Version {
		t.Fatal(current, err)
	}
	saved, err := s.cfg.Store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ensureWorkflowTeam(context.Background(), saved.Workflow, "private:reviewers"); err != nil {
		t.Fatal(err)
	}
	if len(remote.agents) != 1 || remote.agents[0].Model != "original-model" {
		t.Fatal(remote.agents)
	}
	if _, err = s.ensureWorkflowTeam(context.Background(), saved.Workflow, "private:reviewers"); err != nil || len(remote.agents) != 1 {
		t.Fatal(err, remote.agents)
	}
	if _, err = s.ensureWorkflowTeam(context.Background(), current, "private:reviewers"); err != nil || len(remote.agents) != 2 || remote.agents[1].Model != "updated-model" {
		t.Fatal(err, remote.agents)
	}
	if _, err = s.ensureTeam(context.Background(), "private:reviewers"); err != nil {
		t.Fatal(err)
	}
}
func TestTeamMaterializationUnknownOutcomeIsNotRetried(t *testing.T) {
	catalog := privateCatalog(t)
	savePrivate(t, catalog, "private:reviewers", "team", 0, activationTeam("model"))
	remote := &activatingRemote{teamRemote: teamRemote{failAt: 1}}
	s := testServer(t, func(c *Config) { c.Remote = remote; c.Private = catalog })
	if _, err := s.ensureTeam(context.Background(), "private:reviewers"); err == nil {
		t.Fatal("failed remote accepted")
	}
	if _, err := s.ensureTeam(context.Background(), "private:reviewers"); err == nil || !strings.Contains(err.Error(), "uncertain") || remote.calls != 1 {
		t.Fatal(err, remote.calls)
	}
}
func TestTeamActivationValidation(t *testing.T) {
	s := testServer(t, nil)
	if team, err := s.ensureTeam(context.Background(), "remote-team"); err != nil || team != "remote-team" {
		t.Fatal(team, err)
	}
	for _, ref := range []string{"private:missing", "package/team"} {
		if _, err := s.ensureTeam(context.Background(), ref); err == nil {
			t.Fatal(ref)
		}
	}
	if _, err := s.materialize(context.Background(), "id", "1", activationTeam("model")); err == nil {
		t.Fatal("missing materializer")
	}
	if _, err := s.ensureWorkflowTeam(context.Background(), managerstate.Workflow{TeamDefinitions: map[string]json.RawMessage{"team": json.RawMessage(`bad`)}}, "team"); err == nil {
		t.Fatal("invalid snapshot")
	}
	catalog := privateCatalog(t)
	workflow := managerstate.Workflow{ID: "private:review", Version: "1", Phases: []managerstate.Phase{{ID: "review", TeamRef: "private:missing"}}}
	savePrivate(t, catalog, "private:review", "workflow", 0, workflow)
	s = testServer(t, func(c *Config) { c.Private = catalog })
	if _, err := s.privateWorkflow("private:review"); err == nil {
		t.Fatal("missing referenced team")
	}
	if _, err := s.ensureTeam(context.Background(), "private:review"); err == nil {
		t.Fatal("workflow used as team")
	}
	savePrivate(t, catalog, "private:missing", "team", 0, activationTeam("model"))
	if _, err := catalog.ApplyOwner(privateconfig.Change{Action: "archive", ID: "private:missing", Kind: "team", BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.privateWorkflow("private:review"); err == nil {
		t.Fatal("archived team snapshot")
	}
	if _, err := s.ensureTeam(context.Background(), "private:missing"); err == nil {
		t.Fatal("archived team activation")
	}
}
func setTeamPackage(f *packageFixtureTransport, version, model string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	team, _ := json.Marshal(struct {
		ID string `json:"id"`
		privateconfig.Team
	}{"example.coding/reviewers", activationTeam(model)})
	workflow, _ := json.Marshal(managerstate.Workflow{ID: "example.coding/review", Version: "ignored", Phases: []managerstate.Phase{{ID: "review", TeamRef: "example.coding/reviewers"}}})
	files := map[string][]byte{"teams/reviewers.json": team, "workflows/review.json": workflow}
	manifest := managerpackages.Manifest{SchemaVersion: 1, ID: "example.coding", Version: version, Publisher: "Example", RequiresSmidja: ">=1.0.0", Targets: []string{"linux/amd64"}, Scopes: []string{"manager"}, Teams: []string{"teams/reviewers.json"}, Workflows: []string{"workflows/review.json"}}
	for path, data := range files {
		sum := sha256.Sum256(data)
		manifest.Files = append(manifest.Files, managerpackages.File{Path: path, SHA256: hex.EncodeToString(sum[:])})
	}
	raw, _ := json.Marshal(manifest)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, _ := writer.Create(managerpackages.ManifestFilename)
	entry.Write(raw)
	for path, data := range files {
		entry, _ := writer.Create(path)
		entry.Write(data)
	}
	writer.Close()
	f.archive = buffer.Bytes()
	sum := sha256.Sum256(f.archive)
	f.release = managerpackages.Release{ID: manifest.ID, Version: version, ArchiveURL: "https://packages.example/package.zip", SHA256: hex.EncodeToString(sum[:])}
}
func TestPackageWorkflowPinsTeamBeforeUpdate(t *testing.T) {
	fixture := &packageFixtureTransport{}
	setTeamPackage(fixture, "1.0.0", "old-model")
	packages := packageFixtureStore(t, fixture)
	if _, err := packages.Install(context.Background(), managerpackages.Source{URL: "https://packages.example/release.json"}); err != nil {
		t.Fatal(err)
	}
	remote := &activatingRemote{}
	s := testServer(t, func(c *Config) { c.Packages = packages; c.Remote = remote })
	workflows, err := s.packageWorkflows()
	if err != nil || len(workflows) != 1 {
		t.Fatal(workflows, err)
	}
	old := workflows[0]
	setTeamPackage(fixture, "1.1.0", "new-model")
	if _, err = packages.Update(context.Background(), "example.coding"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ensureWorkflowTeam(context.Background(), old, "example.coding/reviewers"); err != nil || remote.agents[0].Model != "old-model" {
		t.Fatal(err, remote.agents)
	}
	workflows, err = s.packageWorkflows()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ensureWorkflowTeam(context.Background(), workflows[0], "example.coding/reviewers"); err != nil || len(remote.agents) != 2 || remote.agents[1].Model != "new-model" {
		t.Fatal(err, remote.agents)
	}
	if _, err = s.ensureTeam(context.Background(), "example.coding/reviewers"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ensureTeam(context.Background(), "example.coding/unknown"); err == nil {
		t.Fatal("unknown package team")
	}
	catalog := privateCatalog(t)
	savePrivate(t, catalog, "private:review", "workflow", 0, managerstate.Workflow{ID: "private:review", Version: "1", Phases: []managerstate.Phase{{ID: "review", TeamRef: "example.coding/reviewers"}}})
	s.cfg.Private = catalog
	pinned, err := s.privateWorkflow("private:review")
	if err != nil || !strings.Contains(string(pinned.TeamDefinitions["example.coding/reviewers"]), "new-model") {
		t.Fatal(pinned, err)
	}
}

func TestPrivateWorkflowUnavailableReferences(t *testing.T) {
	s := testServer(t, nil)
	if _, err := s.privateWorkflow("private:workflow"); err == nil {
		t.Fatal("missing catalog")
	}
	catalog := privateCatalog(t)
	s.cfg.Private = catalog
	if _, err := s.privateWorkflow("private:workflow"); err == nil {
		t.Fatal("missing workflow")
	}
	savePrivate(t, catalog, "private:team", "team", 0, activationTeam("model"))
	if _, err := s.privateWorkflow("private:team"); err == nil {
		t.Fatal("team accepted as workflow")
	}
	workflow := managerstate.Workflow{ID: "private:workflow", Version: "1", Phases: []managerstate.Phase{{ID: "review", TeamRef: "example.coding/absent"}}}
	savePrivate(t, catalog, "private:workflow", "workflow", 0, workflow)
	if _, err := s.privateWorkflow("private:workflow"); err == nil {
		t.Fatal("missing package catalog")
	}
	fixture := &packageFixtureTransport{}
	setTeamPackage(fixture, "1.0.0", "model")
	packages := packageFixtureStore(t, fixture)
	if _, err := packages.Install(context.Background(), managerpackages.Source{URL: "https://packages.example/release.json"}); err != nil {
		t.Fatal(err)
	}
	s.cfg.Packages = packages
	if _, err := s.privateWorkflow("private:workflow"); err == nil {
		t.Fatal("missing package team")
	}
}
func TestPrivateEntitiesHaveDistinctRemoteNamespace(t *testing.T) {
	catalog := privateCatalog(t)
	savePrivate(t, catalog, "private:first", "team", 0, activationTeam("model"))
	savePrivate(t, catalog, "private:second", "team", 0, activationTeam("model"))
	remote := &activatingRemote{}
	s := testServer(t, func(c *Config) {
		c.Private = catalog
		c.Remote = remote
		c.WorkspaceID = "workspace"
		c.CEOTeamID = "ceo"
	})
	if _, err := s.ensureTeam(context.Background(), "private:first"); err != nil {
		t.Fatal(err)
	}
	first := remote.team.Name
	if _, err := s.ensureTeam(context.Background(), "private:second"); err != nil {
		t.Fatal(err)
	}
	if first == remote.team.Name {
		t.Fatal("different entities reused remote names")
	}
}

func TestInlineDefinitionsCannotBypassTeamApproval(t *testing.T) {
	s := testServer(t, nil)
	input := managerstate.Workflow{ID: "inline", Version: "1", Phases: []managerstate.Phase{{ID: "review", TeamRef: "forged-team"}}, TeamDefinitions: map[string]json.RawMessage{"forged-team": json.RawMessage(`{"name":"unapproved","agents":[{"name":"manager","model":"model","instructions":"run"}]}`)}}
	if _, err := s.resolveWorkflow(input); err == nil || !strings.Contains(err.Error(), "approval") {
		t.Fatal("inline snapshot bypassed approval", err)
	}
	input.TeamDefinitions = nil
	input.Phases[0].TeamRef = "existing-remote-team"
	if resolved, err := s.resolveWorkflow(input); err != nil || resolved.Phases[0].TeamRef != "existing-remote-team" {
		t.Fatal(resolved, err)
	}
}
func TestStartupPausesUncertainRunningAndStartsQueued(t *testing.T) {
	directory := t.TempDir()
	store, err := managerstate.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.CreateTask(workflowInput())
	if err != nil {
		t.Fatal(err)
	}
	running, err := store.CreateTask(workflowInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.StartPhase(running.ID, "before-restart"); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = managerstate.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runtime := &workflowFake{}
	s, err := New(Config{Token: testToken, Store: store, WorkflowRuntime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.wg.Wait()
	saved, err := store.GetTask(running.ID)
	if err != nil || saved.Status != managerstate.Paused || saved.Phases[0].Attempts[0].RequestID != "before-restart" {
		t.Fatal(saved, err)
	}
	completed, err := store.GetTask(queued.ID)
	if err != nil || completed.Status != managerstate.Completed {
		t.Fatal(completed, err)
	}
	if len(runtime.starts) != len(queued.Phases) {
		t.Fatal("unexpected work restarted", runtime.starts)
	}
	for _, started := range runtime.starts {
		if started.ID != queued.ID {
			t.Fatal("uncertain phase automatically rerun", started.ID)
		}
	}
}
