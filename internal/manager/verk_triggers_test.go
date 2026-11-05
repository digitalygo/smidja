package manager

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
	"github.com/digitalygo/smidja/internal/verk"
	"github.com/digitalygo/smidja/internal/verktriggers"
)

const triggerSecret = "fixture-owner-configured-webhook-secret"
const triggerPush = `{"ref":"refs/heads/main","before":"old","after":"new","repository":{"full_name":"studio/repo"}}`

func triggerSign(body string) string {
	mac := hmac.New(sha256.New, []byte(triggerSecret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
func triggerHeaders(body, delivery string) map[string]string {
	return map[string]string{"X-GitHub-Event": "push", "X-GitHub-Delivery": delivery, "X-Hub-Signature-256": triggerSign(body)}
}
func customTriggerHeaders(body, source, delivery string) map[string]string {
	return map[string]string{"X-Smidja-Source": source, "X-Smidja-Delivery": delivery, "X-Smidja-Signature-256": triggerSign(source + "\n" + delivery + "\n" + body)}
}
func triggerServer(t *testing.T) *Server {
	t.Helper()
	store, err := privateconfig.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return testServer(t, func(c *Config) {
		c.Private = store
		c.LookupEnv = func(key string) string {
			if key == "SMIDJA_WEBHOOK_SECRET" {
				return triggerSecret
			}
			return ""
		}
	})
}
func triggerWorkflow(trigger verk.Trigger) managerstate.Workflow {
	return managerstate.Workflow{ID: "private:trigger", Version: "1", SchemaVersion: 2, Graph: &verk.Graph{SchemaVersion: 1, Nodes: []verk.Node{{ID: "entry", Type: "entry", Triggers: []verk.Trigger{trigger}, Outputs: []verk.Port{{ID: "next", Label: "Next"}}}, {ID: "end", Type: "end", ExitActions: &verk.ExitActions{SetState: true, State: "completed"}}}, Edges: []verk.Edge{{ID: "next", From: "entry", Port: "next", To: "end"}}}}
}
func saveTriggerWorkflow(t *testing.T, s *Server, workflow managerstate.Workflow) {
	t.Helper()
	raw, err := json.Marshal(workflow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Private.ApplyOwner(privateconfig.Change{Action: "create", ID: workflow.ID, Kind: "workflow", Payload: raw}); err != nil {
		t.Fatal(err)
	}
}

func TestVerkWebhookAuthenticationBeforeDecode(t *testing.T) {
	s := triggerServer(t)
	for _, tc := range []struct {
		body    string
		headers map[string]string
		status  int
	}{{"{", nil, 401}, {"{", triggerHeaders("{", "id"), 400}, {triggerPush, triggerHeaders(triggerPush+" ", "id"), 401}, {strings.Repeat("x", 1<<20+1), nil, 413}} {
		got := request(s, "POST", "/hooks/verk", tc.body, tc.headers)
		if got.Code != tc.status {
			t.Fatal(got.Code, got.Body)
		}
	}
	s.cfg.LookupEnv = nil
	if got := request(s, "POST", "/hooks/verk", triggerPush, triggerHeaders(triggerPush, "id")); got.Code != 503 {
		t.Fatal(got.Code, got.Body)
	}
	if got := request(testServer(t, nil), "POST", "/hooks/verk", triggerPush, nil); got.Code != 503 {
		t.Fatal(got.Code, got.Body)
	}
}
func TestVerkWebhookScopeAndDurableDedup(t *testing.T) {
	s := triggerServer(t)
	workflow := triggerWorkflow(verk.Trigger{ID: "push", Type: "push", Scope: "specific", Repositories: []string{"studio/repo"}, Branches: []string{"main"}})
	saveTriggerWorkflow(t, s, workflow)
	foreign := strings.Replace(triggerPush, "studio/repo", "studio/other", 1)
	if got := request(s, "POST", "/hooks/verk", foreign, triggerHeaders(foreign, "foreign")); got.Code != 202 {
		t.Fatal(got.Code, got.Body)
	}
	if len(s.cfg.Store.ListTasks()) != 0 {
		t.Fatal("foreign repository started task")
	}
	for _, delivery := range []string{"delivery", "delivery", "altered-header"} {
		got := request(s, "POST", "/hooks/verk", triggerPush, triggerHeaders(triggerPush, delivery))
		if got.Code != 202 {
			t.Fatal(got.Code, got.Body)
		}
	}
	tasks := s.cfg.Store.ListTasks()
	if len(tasks) != 1 || tasks[0].InvocationKey == "" || tasks[0].OriginChatID != "" || len(s.cfg.Store.ListSessions()) != 0 {
		t.Fatal("replay duplicated task or created Smidr", tasks)
	}
	changed := strings.Replace(triggerPush, `"after":"new"`, `"after":"different"`, 1)
	if got := request(s, "POST", "/hooks/verk", changed, triggerHeaders(changed, "delivery")); got.Code != 409 {
		t.Fatal(got.Code, got.Body)
	}
	if len(s.cfg.Store.ListTasks()) != 1 {
		t.Fatal("conflicting delivery created task")
	}
	if _, err := s.cfg.Private.ApplyOwner(privateconfig.Change{Action: "archive", ID: workflow.ID, Kind: "workflow", BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if got := request(s, "POST", "/hooks/verk", changed, triggerHeaders(changed, "new")); got.Code != 202 {
		t.Fatal(got.Code, got.Body)
	}
	if len(s.cfg.Store.ListTasks()) != 1 {
		t.Fatal("archived graph ran")
	}
	tags := strings.Replace(triggerPush, "refs/heads/main", "refs/tags/v1", 1)
	if got := request(s, "POST", "/hooks/verk", tags, triggerHeaders(tags, "tag")); got.Code != 202 || !strings.Contains(got.Body.String(), "ignored") {
		t.Fatal(got.Code, got.Body)
	}
}
func TestVerkCustomSourceAuthorization(t *testing.T) {
	s := triggerServer(t)
	saveTriggerWorkflow(t, s, triggerWorkflow(verk.Trigger{Type: "webhook", SourceRef: "catalog:updated"}))
	body := `{"catalog":"changed"}`
	for _, source := range []string{"other", "catalog:updated"} {
		got := request(s, "POST", "/hooks/verk", body, customTriggerHeaders(body, source, "1"))
		if got.Code != 202 {
			t.Fatal(got.Code, got.Body)
		}
	}
	if len(s.cfg.Store.ListTasks()) != 1 {
		t.Fatal("source scope")
	}
	got := request(s, "POST", "/hooks/verk", body, customTriggerHeaders(body, "catalog:updated", "2"))
	if got.Code != 202 || len(s.cfg.Store.ListTasks()) != 2 {
		t.Fatal("distinct signed delivery not accepted", got.Code, got.Body)
	}
}
func TestVerkSchedulesCurrentMinuteAndDSTDedup(t *testing.T) {
	s := triggerServer(t)
	workflow := triggerWorkflow(verk.Trigger{Type: "schedule", Schedule: "30 2 * * *", Timezone: "Europe/Rome"})
	saveTriggerWorkflow(t, s, workflow)
	now, _ := time.Parse(time.RFC3339, "2026-10-25T00:30:00Z")
	for _, at := range []time.Time{now, now, now.Add(time.Hour), now.Add(48 * time.Hour)} {
		s.runVerkSchedules(context.Background(), at)
	}
	if len(s.cfg.Store.ListTasks()) != 1 {
		t.Fatal("duplicate DST slot or backlog replay", s.cfg.Store.ListTasks())
	}
	next, _ := time.Parse(time.RFC3339, "2026-10-26T01:30:00Z")
	s.runVerkSchedules(context.Background(), next)
	if len(s.cfg.Store.ListTasks()) != 2 {
		t.Fatal("next scheduled slot missing")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	s.runVerkSchedules(cancelled, next.Add(24*time.Hour))
	if len(s.cfg.Store.ListTasks()) != 2 {
		t.Fatal("cancelled scheduler started work")
	}
	if len(s.cfg.Store.ListSessions()) != 0 {
		t.Fatal("schedule created Smidr")
	}
}
func TestVerkInvalidAndDraftTriggersStayInactive(t *testing.T) {
	s := triggerServer(t)
	invalid := triggerWorkflow(verk.Trigger{Type: "schedule", Schedule: "bad", Timezone: "UTC"})
	saveTriggerWorkflow(t, s, invalid)
	draft := triggerWorkflow(verk.Trigger{Type: "push", Scope: "specific", Repositories: []string{"a/b"}})
	draft.ID = "private:draft"
	draft.Graph.Edges = nil
	saveTriggerWorkflow(t, s, draft)
	if len(s.verkTriggerWorkflows()) != 0 {
		t.Fatal("invalid or draft graph activated")
	}
	if len(testServer(t, nil).verkTriggerWorkflows()) != 0 {
		t.Fatal("missing catalog enumerated")
	}
}
func TestVerkSignedWaitSignals(t *testing.T) {
	s := triggerServer(t)
	workflow := reviewGraph("wait", map[string]any{"event": "catalog:updated"}, "resume", "timeout")
	task := reviewGraphTask(t, s, workflow)
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.GraphState.Status != "blocked" {
		t.Fatal(task.Status, task.Reason)
	}
	revision := task.GraphState.Revision
	body := `{"result":"ready"}`
	for _, headers := range []map[string]string{nil, customTriggerHeaders(body, "wrong", "one")} {
		request(s, "POST", "/hooks/verk", body, headers)
		if now := getWorkflowTask(t, s, task.ID); now.GraphState.Revision != revision {
			t.Fatal("unauthorized source woke gate")
		}
	}
	got := request(s, "POST", "/hooks/verk", body, customTriggerHeaders(body, "catalog:updated", "one"))
	if got.Code != 202 {
		t.Fatal(got.Code, got.Body)
	}
	current := getWorkflowTask(t, s, task.ID)
	if len(current.GraphState.Runs) < 2 || !current.GraphState.Runs[1].OutputRecorded || current.GraphState.Runs[1].Fields["trigger"] == nil {
		t.Fatal(current.GraphState)
	}
	for _, run := range current.GraphState.Runs {
		if run.NodeID == "action" && run.Port != "resume" {
			t.Fatal(run)
		}
	}
	got = request(s, "POST", "/hooks/verk", body, customTriggerHeaders(body, "catalog:updated", "one"))
	if got.Code != 202 {
		t.Fatal(got.Code, got.Body)
	}
	if len(s.cfg.Store.ListTasks()) != 1 {
		t.Fatal("signal created extra task")
	}
	event := verktriggers.Event{Source: "wrong", DeliveryID: "id", Payload: json.RawMessage(body)}
	if len(s.wakeVerkWaits(event, "")) != 0 {
		t.Fatal("wrong source")
	}
}
func TestVerkWebhookMethod(t *testing.T) {
	s := triggerServer(t)
	r := httptest.NewRequest("GET", "/hooks/verk", nil)
	w := httptest.NewRecorder()
	s.verkTriggerHTTP(w, r)
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
}

func TestVerkWebhookReplayAfterRestart(t *testing.T) {
	stateDir, privateDir := t.TempDir(), t.TempDir()
	open := func() *Server {
		store, err := managerstate.Open(stateDir)
		if err != nil {
			t.Fatal(err)
		}
		private, err := privateconfig.Open(privateDir)
		if err != nil {
			t.Fatal(err)
		}
		s, err := New(Config{Token: testToken, Store: store, Private: private, LookupEnv: func(string) string { return triggerSecret }})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	close := func(s *Server) { s.Close(); s.cfg.Store.Close(); s.cfg.Private.Close() }
	s := open()
	saveTriggerWorkflow(t, s, triggerWorkflow(verk.Trigger{Type: "push", Scope: "specific", Repositories: []string{"studio/repo"}}))
	if w := request(s, "POST", "/hooks/verk", triggerPush, triggerHeaders(triggerPush, "original")); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	task := s.cfg.Store.ListTasks()[0]
	close(s)
	s = open()
	defer close(s)
	for _, delivery := range []string{"original", "changed-unsigned-header"} {
		if w := request(s, "POST", "/hooks/verk", triggerPush, triggerHeaders(triggerPush, delivery)); w.Code != 202 {
			t.Fatal(w.Code, w.Body)
		}
	}
	tasks := s.cfg.Store.ListTasks()
	if len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatal("restart replay created task", tasks)
	}
}

func TestVerkWaitReplayCannotAdvanceNextGate(t *testing.T) {
	s := triggerServer(t)
	workflow := reviewGraph("wait", map[string]any{"event": "catalog:updated"}, "resume", "timeout")
	second := workflow.Graph.Nodes[1]
	second.ID = "second"
	workflow.Graph.Nodes = append(workflow.Graph.Nodes, second)
	workflow.Graph.Edges[1].To = "second"
	workflow.Graph.Edges = append(workflow.Graph.Edges, verk.Edge{ID: "second-resume", From: "second", Port: "resume", To: "end"}, verk.Edge{ID: "second-timeout", From: "second", Port: "timeout", To: "end"})
	task := reviewGraphTask(t, s, workflow)
	s.runGraphTask(context.Background(), task.ID)
	body := `{"ready":true}`
	if w := request(s, "POST", "/hooks/verk", body, customTriggerHeaders(body, "catalog:updated", "first")); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	deadline := time.Now().Add(time.Second)
	for {
		task = getWorkflowTask(t, s, task.ID)
		if task.GraphState.Status == "blocked" && task.GraphState.Cursor == "second" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("next wait did not activate", task.GraphState)
		}
		time.Sleep(time.Millisecond)
	}
	revision := task.GraphState.Revision
	if w := request(s, "POST", "/hooks/verk", body, customTriggerHeaders(body, "catalog:updated", "first")); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	if task = getWorkflowTask(t, s, task.ID); task.GraphState.Revision != revision {
		t.Fatal("delivery replay advanced another gate")
	}
}

func TestVerkTriggerDraftSaveAndRunValidation(t *testing.T) {
	s := triggerServer(t)
	workflow := triggerWorkflow(verk.Trigger{Type: "schedule", Schedule: "invalid", Timezone: "UTC"})
	saveTriggerWorkflow(t, s, workflow)
	body, _ := json.Marshal(map[string]any{"workflow": workflow})
	got := request(s, "POST", "/api/workflows/validate", string(body), ownerHeaders())
	if got.Code != 400 || !strings.Contains(got.Body.String(), "node entry trigger 1") {
		t.Fatal("missing actionable runnable validation", got.Code, got.Body)
	}
	if len(s.verkTriggerWorkflows()) != 0 {
		t.Fatal("invalid draft activated")
	}
}
