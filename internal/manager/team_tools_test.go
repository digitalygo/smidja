package manager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

type teamDelegationRemote struct {
	nativeFake
	teams           []envai.Team
	listErr         error
	server          *Server
	watcherPrepared bool
}

func (f *teamDelegationRemote) ListTeams(context.Context) ([]envai.Team, error) {
	return f.teams, f.listErr
}
func (f *teamDelegationRemote) StreamEvents(ctx context.Context, _ string, _ int64, _ envai.EventHandler) error {
	<-ctx.Done()
	return ctx.Err()
}
func (f *teamDelegationRemote) Delegate(ctx context.Context, chat string, input envai.DelegationInput) (envai.Delegation, error) {
	if f.server != nil {
		for _, session := range f.server.cfg.Store.ListSessions() {
			if session.ChatID == input.TargetChatID {
				f.server.mu.Lock()
				_, f.watcherPrepared = f.server.active["watch:"+session.ID]
				f.server.mu.Unlock()
			}
		}
	}
	return f.nativeFake.Delegate(ctx, chat, input)
}
func teamToolEvent() envai.Event {
	return envai.Event{ChatID: "origin-chat", TurnID: "origin-turn", Type: "agent.tool.request", Agent: &envai.Agent{InstanceID: "ceo"}, Data: json.RawMessage(`{"call_id":"call","tool_name":"manager.teams_delegate","kind":"client","args":{},"signature":{"alg":"EdDSA","kid":"test","sig":"verified-elsewhere"}}`)}
}
func TestTeamDelegationUsesNativeOriginAndPersistentObserver(t *testing.T) {
	remote := &teamDelegationRemote{nativeFake: nativeFake{Remote: &chatRemote{}}}
	s := testServer(t, func(c *Config) { c.Remote = remote; c.WorkspaceID = "workspace" })
	remote.server = s
	result := s.executeTeamTool(context.Background(), teamToolEvent(), "teams_delegate", json.RawMessage(`{"team_ref":"remote-team","prompt":"Review this change"}`))
	if !result.OK {
		t.Fatal(result)
	}
	if len(remote.delegates) != 1 || !remote.watcherPrepared {
		t.Fatal("delegation admitted without target observer", remote.delegates)
	}
	in := remote.delegates[0]
	if in.StrictModel == nil || !*in.StrictModel || in.OriginTurnID != "origin-turn" || in.TargetChatID != "target-chat" || in.EventID != "call" || in.WorkspaceID != "workspace" || in.Prompt != "Review this change" {
		t.Fatal(in)
	}
	receipt, ok := result.Data.(envai.Delegation)
	if !ok || receipt.TaskID != "native-task" {
		t.Fatal(result.Data)
	}
	if result = s.executeTeamTool(context.Background(), teamToolEvent(), "teams_task", json.RawMessage(`{"task_id":"native-task"}`)); !result.OK {
		t.Fatal(result)
	}
	if result = s.executeTeamTool(context.Background(), teamToolEvent(), "teams_cancel", json.RawMessage(`{"task_id":"native-task"}`)); !result.OK || remote.cancels != 1 {
		t.Fatal(result)
	}
}
func TestTeamToolErrors(t *testing.T) {
	s := testServer(t, nil)
	if result := s.executeTeamTool(context.Background(), teamToolEvent(), "teams_delegate", json.RawMessage(`{}`)); result.OK {
		t.Fatal(result)
	}
	remote := &teamDelegationRemote{nativeFake: nativeFake{Remote: &chatRemote{}}}
	s = testServer(t, func(c *Config) { c.Remote = remote; c.WorkspaceID = "workspace" })
	for _, args := range []string{`{`, `{}`, `{"team_ref":"x"}`, `{"prompt":"x"}`} {
		if result := s.executeTeamTool(context.Background(), teamToolEvent(), "teams_delegate", json.RawMessage(args)); result.OK {
			t.Fatal(args)
		}
	}
	if result := s.executeTeamTool(context.Background(), teamToolEvent(), "unknown", json.RawMessage(`{}`)); result.OK {
		t.Fatal(result)
	}
	args := json.RawMessage(`{"team_ref":"remote-team","prompt":"work"}`)
	remote.readyErr = errors.New("feature unavailable")
	if result := s.executeTeamTool(context.Background(), teamToolEvent(), "teams_delegate", args); result.OK {
		t.Fatal(result)
	}
	remote.readyErr = nil
	if result := s.executeTeamTool(context.Background(), teamToolEvent(), "teams_delegate", json.RawMessage(`{"team_ref":"private:missing","prompt":"work"}`)); result.OK {
		t.Fatal(result)
	}
	remote.createErr = errors.New("create failed")
	if result := s.executeTeamTool(context.Background(), teamToolEvent(), "teams_delegate", args); result.OK {
		t.Fatal(result)
	}
	remote.createErr = nil
	if result := s.executeTeamTool(context.Background(), envai.Event{}, "teams_delegate", args); result.OK {
		t.Fatal(result)
	}
	remote.delegateErr = errors.New("admission unavailable")
	if result := s.executeTeamTool(context.Background(), teamToolEvent(), "teams_delegate", args); result.OK {
		t.Fatal(result)
	}
	remote.cancelErr = errors.New("cancel unavailable")
	if result := s.executeTeamTool(context.Background(), teamToolEvent(), "teams_cancel", json.RawMessage(`{"task_id":"native-task"}`)); result.OK {
		t.Fatal(result)
	}
}
func TestTeamCatalogPrivateRemoteAndPackages(t *testing.T) {
	catalog := privateCatalog(t)
	entity := savePrivate(t, catalog, "private:active", "team", 0, activationTeam("model"))
	archived := savePrivate(t, catalog, "private:archived", "team", 0, activationTeam("model"))
	if _, err := catalog.ApplyOwner(privateconfig.Change{Action: "archive", ID: archived.ID, Kind: "team", BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	fixture := &packageFixtureTransport{}
	setTeamPackage(fixture, "1.0.0", "model")
	packages := packageFixtureStore(t, fixture)
	if _, err := packages.Install(context.Background(), managerpackages.Source{URL: "https://packages.example/release.json"}); err != nil {
		t.Fatal(err)
	}
	remote := &teamDelegationRemote{nativeFake: nativeFake{Remote: &chatRemote{}}, teams: []envai.Team{{ID: "remote", Name: "Remote"}}}
	s := testServer(t, func(c *Config) { c.Remote = remote; c.Private = catalog; c.Packages = packages })
	result := s.executeTeamTool(context.Background(), envai.Event{}, "teams_list", json.RawMessage(`{}`))
	if !result.OK {
		t.Fatal(result)
	}
	entries := result.Data.([]map[string]any)
	if len(entries) != 3 {
		t.Fatal(entries)
	}
	if entries[0]["origin"] != "envai" || entries[1]["id"] != entity.ID || entries[2]["origin"] != "package" {
		t.Fatal(entries)
	}
	remote.listErr = errors.New("listing unavailable")
	if _, err := s.teamCatalog(context.Background()); err == nil {
		t.Fatal("remote listing error hidden")
	}
	empty := testServer(t, nil)
	if list, err := empty.teamCatalog(context.Background()); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	missingFixture := &packageFixtureTransport{}
	missingFixture.set("1.0.0", "remote", []managerpackages.Dependency{{ID: "missing.package", Version: "1.0.0", Digest: strings.Repeat("a", 64)}})
	missingPackages := packageFixtureStore(t, missingFixture)
	if _, err := missingPackages.Install(context.Background(), managerpackages.Source{URL: "https://packages.example/release.json"}); err != nil {
		t.Fatal(err)
	}
	missing := testServer(t, func(c *Config) { c.Packages = missingPackages })
	if list, err := missing.teamCatalog(context.Background()); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
}

func TestTeamDelegationOwnerAPI(t *testing.T) {
	remote := &teamDelegationRemote{nativeFake: nativeFake{Remote: &chatRemote{}}}
	s := testServer(t, func(c *Config) { c.Remote = remote; c.WorkspaceID = "workspace" })
	remote.server = s
	session := newChatSession(t, s)
	call := func(sessionID string) string {
		body, _ := json.Marshal(map[string]string{"session_id": sessionID, "team_ref": "remote", "prompt": "Review"})
		return string(body)
	}
	if w := request(s, "POST", "/api/team-delegations", call(session.ID), nil); w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(s, "POST", "/api/team-delegations", `{`, ownerHeaders()); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(s, "POST", "/api/team-delegations", call(session.ID), ownerHeaders()); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	if _, err := s.cfg.Store.StartSessionTurn(session.ID, "origin", "request"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.cfg.Store.AttachSessionTurn(session.ID, "origin", "origin-chat", "origin-turn"); err != nil {
		t.Fatal(err)
	}
	if w := request(s, "POST", "/api/team-delegations", call(session.ID), ownerHeaders()); w.Code != 202 || !strings.Contains(w.Body.String(), "native-task") {
		t.Fatal(w.Code, w.Body)
	}
	if remote.delegates[0].OriginTurnID != "origin-turn" || !remote.watcherPrepared {
		t.Fatal(remote.delegates)
	}
	remote.delegateErr = errors.New("remote unavailable")
	if w := request(s, "POST", "/api/team-delegations", call(session.ID), ownerHeaders()); w.Code != 502 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(s, "GET", "/api/teams", "", ownerHeaders()); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
}
