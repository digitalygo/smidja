package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
	"testing"
)

func workflowInvocationKey(chat, call string) string {
	sum := sha256.Sum256([]byte(chat + "\x00" + call))
	return "workflow:" + hex.EncodeToString(sum[:])
}
func bindWorkflowIdentity(t *testing.T, s *Server, id, chat, ref string) {
	t.Helper()
	if s.cfg.CEOTeamID == "" {
		s.cfg.CEOTeamID = "ceo-team"
	}
	teamID := "materialized-" + ref
	if ref == "ceo" {
		teamID = s.cfg.CEOTeamID
	}
	if _, err := s.cfg.Store.AttachSessionIdentity(id, chat, ref, teamID); err != nil {
		t.Fatal(err)
	}
}
func TestWorkflowProvenanceAndPermissions(t *testing.T) {
	s := testServer(t, nil)
	for _, event := range []envai.Event{{}, {ChatID: "unknown"}} {
		if _, err := s.workflowPrincipal(event); err == nil {
			t.Fatal("untracked identity accepted")
		}
	}
	session, _ := s.cfg.Store.CreateSession("untrusted")
	s.cfg.Store.AttachSessionRemote(session.ID, "chat")
	event := envai.Event{ChatID: "chat"}
	if _, err := s.workflowPrincipal(event); err == nil {
		t.Fatal("unverified identity accepted")
	}
	bindWorkflowIdentity(t, s, session.ID, "chat", "private:reviewers")
	allowed := managerstate.Workflow{AllowedInvokers: []string{"team:private:reviewers"}}
	if err := s.authorizeWorkflow(event, allowed); err != nil {
		t.Fatal(err)
	}
	for _, policy := range [][]string{nil, {}, {"ceo"}, {"team:other"}} {
		if s.authorizeWorkflow(event, managerstate.Workflow{AllowedInvokers: policy}) == nil {
			t.Fatal(policy)
		}
	}
	if _, err := s.resolveAgentWorkflow(event, managerstate.Workflow{ID: "inline", AllowedInvokers: allowed.AllowedInvokers}); err == nil {
		t.Fatal("inline self-authorization")
	}
	if result := s.prReviewTool(context.Background(), event, json.RawMessage(`{}`)); result.OK {
		t.Fatal("PR authorization bypass")
	}
	ceo, _ := s.cfg.Store.CreateSession("CEO")
	bindWorkflowIdentity(t, s, ceo.ID, "ceo-chat", "ceo")
	event.ChatID = "ceo-chat"
	if err := s.authorizeWorkflow(event, managerstate.Workflow{}); err != nil {
		t.Fatal(err)
	}
	if s.authorizeWorkflow(event, managerstate.Workflow{AllowedInvokers: []string{}}) == nil {
		t.Fatal("empty allowed list")
	}
	input := workflowInput().Workflow
	if _, err := s.resolveAgentWorkflow(event, input); err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveAgentWorkflow(event, managerstate.Workflow{ID: "missing/workflow"}); err == nil {
		t.Fatal("missing workflow")
	}
	if _, err := s.resolveAgentWorkflow(envai.Event{}, input); err == nil {
		t.Fatal("missing principal")
	}
	s.cfg.CEOTeamID = "changed"
	if _, err := s.workflowPrincipal(event); err == nil {
		t.Fatal("CEO mismatch")
	}
}

func TestWorkflowCatalogAuthorityAndPinnedResume(t *testing.T) {
	s := privateServer(t)
	s.cfg.WorkflowRuntime = &workflowFake{}
	s.cancel()
	policy := managerstate.Workflow{ID: "private:review", Version: "1", AllowedInvokers: []string{"team:private:reviewers"}, InputSchema: json.RawMessage(`{"type":"object","required":["request"]}`), Phases: []managerstate.Phase{{ID: "review", TeamRef: "remote-team"}}}
	put := func(action string, revision int, w managerstate.Workflow) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"action": action, "id": w.ID, "kind": "workflow", "base_revision": revision, "payload": w})
		response := request(s, "POST", "/api/private", string(payload), ownerHeaders())
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body)
		}
	}
	put("create", 0, policy)
	session, _ := s.cfg.Store.CreateSession("reviewers")
	bindWorkflowIdentity(t, s, session.ID, "tool-chat", "private:reviewers")
	event := managerToolEvent()
	input := managerstate.StartTask{Workflow: managerstate.Workflow{ID: policy.ID, AllowedInvokers: []string{"ceo"}, InputSchema: json.RawMessage(`{}`)}, Inputs: json.RawMessage(`{}`)}
	args, _ := json.Marshal(input)
	if result := s.executeManagerTool(context.Background(), event, "tasks_start", args); result.OK {
		t.Fatal("schema spoof accepted")
	}
	if len(s.cfg.Store.ListTasks()) != 0 {
		t.Fatal("invalid task persisted")
	}
	input.Inputs = json.RawMessage(`{"request":"review"}`)
	args, _ = json.Marshal(input)
	result := s.executeManagerTool(context.Background(), event, "tasks_start", args)
	if !result.OK {
		t.Fatal(result)
	}
	task := result.Data.(managerstate.Task)
	if task.Workflow.AllowedInvokers[0] != "team:private:reviewers" {
		t.Fatal("spoofed policy persisted")
	}
	policy.AllowedInvokers = []string{"ceo"}
	put("update", 1, policy)
	if repeated := s.executeManagerTool(context.Background(), event, "tasks_start", args); repeated.OK {
		t.Fatal("unauthorized replay")
	}
	if _, err := s.cfg.Store.Pause(task.ID, "needs input"); err != nil {
		t.Fatal(err)
	}
	resume, _ := json.Marshal(map[string]string{"id": task.ID, "clarification": "resolved"})
	if result := s.executeManagerTool(context.Background(), event, "tasks_resume", resume); !result.OK {
		t.Fatal("pinned permission lost", result)
	}
	stranger, _ := s.cfg.Store.CreateSession("stranger")
	bindWorkflowIdentity(t, s, stranger.ID, "stranger-chat", "private:other")
	event.ChatID = "stranger-chat"
	if result := s.executeManagerTool(context.Background(), event, "tasks_resume", resume); result.OK {
		t.Fatal("unauthorized resume")
	}
	ownerInput, _ := json.Marshal(input)
	response := request(s, "POST", "/api/tasks", string(ownerInput), ownerHeaders())
	if response.Code != 202 {
		t.Fatal("owner could not invoke", response.Code, response.Body)
	}
	input.Inputs = json.RawMessage(`{}`)
	ownerInput, _ = json.Marshal(input)
	if response = request(s, "POST", "/api/tasks", string(ownerInput), ownerHeaders()); response.Code >= 200 && response.Code < 300 {
		t.Fatal("owner bypassed schema")
	}
}
func TestClientWorkflowPolicyNamespace(t *testing.T) {
	s := testServer(t, nil)
	inventory := capabilityInventory()
	var workflow managerstate.Workflow
	json.Unmarshal(inventory.WorkflowDefinitions[0].Definition, &workflow)
	workflow.AllowedInvokers = []string{"ceo", "team:example.coding/reviewers"}
	workflow.InputSchema = json.RawMessage(`{"type":"object","required":["request"]}`)
	inventory.WorkflowDefinitions[0].Definition, _ = json.Marshal(workflow)
	connectCapability(t, s.cfg.Clients, "laptop", inventory)
	resolved, err := s.clientWorkflow("client:laptop:example.coding/review")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.AllowedInvokers[1] != "team:client:laptop:example.coding/reviewers" {
		t.Fatal(resolved)
	}
	for _, client := range []string{"laptop", "desktop"} {
		session, _ := s.cfg.Store.CreateSession(client)
		bindWorkflowIdentity(t, s, session.ID, client, "client:"+client+":example.coding/reviewers")
		err := s.authorizeWorkflow(envai.Event{ChatID: client}, resolved)
		if (err == nil) != (client == "laptop") {
			t.Fatal(client, err)
		}
	}
	if managerstate.ValidateWorkflowInputs(resolved.InputSchema, json.RawMessage(`{}`)) == nil {
		t.Fatal("schema lost")
	}
}
