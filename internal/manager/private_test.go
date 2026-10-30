package manager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

func privateServer(t *testing.T) *Server {
	t.Helper()
	store, err := privateconfig.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return testServer(t, func(c *Config) { c.Private = store })
}

const privateTeamJSON = `{"action":"create","id":"private:odoo","kind":"team","base_revision":0,"payload":{"name":"Odoo","agents":[{"name":"Manager","instructions":"Manage tasks"}]}}`

func TestPrivateOwnerAPI(t *testing.T) {
	s := privateServer(t)
	for _, path := range []string{"/api/private", "/api/private?archived=true", "/api/approvals"} {
		w := request(s, "GET", path, "", ownerHeaders())
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
	}
	w := request(s, "POST", "/api/private", privateTeamJSON, ownerHeaders())
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var entity privateconfig.Entity
	if err := json.Unmarshal(w.Body.Bytes(), &entity); err != nil {
		t.Fatal(err)
	}
	if entity.ID != "private:odoo" || entity.Revision != 1 {
		t.Fatal(entity)
	}
	if w = request(s, "POST", "/api/private", privateTeamJSON, ownerHeaders()); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	if w = request(s, "POST", "/api/private", `{`, ownerHeaders()); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	w = request(s, "POST", "/api/private", `{"action":"archive","id":"private:odoo","kind":"team","base_revision":1}`, ownerHeaders())
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = request(s, "GET", "/api/private", "", ownerHeaders()); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body)
	}
	if w = request(s, "GET", "/api/private?archived=true", "", ownerHeaders()); !strings.Contains(w.Body.String(), "archived_at") {
		t.Fatal(w.Body)
	}
	w = request(s, "POST", "/api/private", `{"action":"restore","id":"private:odoo","kind":"team","base_revision":2}`, ownerHeaders())
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	w = request(s, "POST", "/api/private", `{"action":"create","id":"package:org/team","kind":"team"}`, ownerHeaders())
	if w.Code != 409 {
		t.Fatal(w.Body)
	}
	if w = request(testServer(t, nil), "GET", "/api/private", "", ownerHeaders()); w.Code != 503 {
		t.Fatal(w.Body)
	}
}
func TestAgentProposalOwnerApproval(t *testing.T) {
	s := privateServer(t)
	result := s.executeManagerTool(context.Background(), envai.Event{}, "manager.config_propose", json.RawMessage(privateTeamJSON))
	if !result.OK {
		t.Fatal(result)
	}
	proposal, ok := result.Data.(privateconfig.Proposal)
	if !ok {
		t.Fatal(result.Data)
	}
	if len(s.cfg.Private.List("", false)) != 0 {
		t.Fatal("agent proposal activated without approval")
	}
	listed := s.executeManagerTool(context.Background(), envai.Event{}, "config_list", json.RawMessage(`{}`))
	if !listed.OK || len(listed.Data.([]privateconfig.Entity)) != 0 {
		t.Fatal(listed)
	}
	w := request(s, "GET", "/api/approvals", "", ownerHeaders())
	if w.Code != 200 || !strings.Contains(w.Body.String(), proposal.ID) {
		t.Fatal(w.Body)
	}
	if w = request(s, "POST", "/api/approvals/"+proposal.ID+"/approve", `{}`, nil); w.Code != 401 {
		t.Fatal("unowned approval", w.Body)
	}
	if w = request(s, "POST", "/api/approvals/"+proposal.ID+"/approve", `{}`, ownerHeaders()); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w = request(s, "POST", "/api/approvals/"+proposal.ID+"/approve", `{}`, ownerHeaders()); w.Code != 409 {
		t.Fatal(w.Body)
	}
	listed = s.executeManagerTool(context.Background(), envai.Event{}, "config_list", json.RawMessage(`{}`))
	if !listed.OK || len(listed.Data.([]privateconfig.Entity)) != 1 {
		t.Fatal(listed)
	}
	change := json.RawMessage(`{"action":"archive","id":"private:odoo","kind":"team","base_revision":1}`)
	result = s.executeManagerTool(context.Background(), envai.Event{}, "config_propose", change)
	if !result.OK {
		t.Fatal(result)
	}
	proposal = result.Data.(privateconfig.Proposal)
	if w = request(s, "POST", "/api/approvals/"+proposal.ID+"/reject", `{}`, ownerHeaders()); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w = request(s, "POST", "/api/approvals/"+proposal.ID+"/reject", `{}`, ownerHeaders()); w.Code != 409 {
		t.Fatal(w.Body)
	}
	if len(s.cfg.Private.List("", false)) != 1 {
		t.Fatal("rejected archive changed catalog")
	}
	for _, path := range []string{"/api/approvals/nope/approve", "/api/approvals/nope/reject"} {
		if w = request(s, "POST", path, `{}`, ownerHeaders()); w.Code != 409 {
			t.Fatal(w.Body)
		}
	}
	for _, path := range []string{"/api/approvals/nope", "/api/approvals/nope/unknown", "/api/private/unknown"} {
		if w = request(s, "POST", path, `{}`, ownerHeaders()); w.Code != 404 {
			t.Fatal(w.Body)
		}
	}
	for _, name := range []string{"config_propose", "config_list"} {
		if result := testServer(t, nil).executeManagerTool(context.Background(), envai.Event{}, name, json.RawMessage(`{}`)); result.OK {
			t.Fatal("missing catalog accepted")
		}
	}
	if result = s.executeManagerTool(context.Background(), envai.Event{}, "config_propose", json.RawMessage(`{"base_revision":"bad"}`)); result.OK {
		t.Fatal("malformed proposal accepted")
	}
	if result = s.executeManagerTool(context.Background(), envai.Event{}, "config_propose", json.RawMessage(`{}`)); result.OK {
		t.Fatal("invalid proposal accepted")
	}
	for _, definition := range ManagerToolDefinitions() {
		if strings.Contains(definition.Name, "approve") || strings.Contains(definition.Name, "apply_owner") {
			t.Fatal("agent may approve itself", definition.Name)
		}
	}
}
