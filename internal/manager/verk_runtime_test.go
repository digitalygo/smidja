package manager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/extensiontools"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
	"github.com/digitalygo/smidja/internal/verk"
)

func reviewGraph(kind string, params map[string]any, ports ...string) managerstate.Workflow {
	outputs := []verk.Port{}
	edges := []verk.Edge{{ID: "entry-edge", From: "entry", Port: "next", To: "action"}}
	for _, port := range ports {
		outputs = append(outputs, verk.Port{ID: port, Label: port})
		edges = append(edges, verk.Edge{ID: port + "-edge", From: "action", Port: port, To: "end"})
	}
	return managerstate.Workflow{ID: "review-graph", Version: "1", SchemaVersion: 2, Graph: &verk.Graph{SchemaVersion: 1, Nodes: []verk.Node{{ID: "entry", Type: "entry", Outputs: []verk.Port{{ID: "next", Label: "Next"}}}, {ID: "action", Type: kind, Params: params, Outputs: outputs}, {ID: "end", Type: "end", ExitActions: &verk.ExitActions{SetState: true, State: "completed", ReturnData: true}}}, Edges: edges}}
}
func reviewGraphTask(t *testing.T, s *Server, w managerstate.Workflow) managerstate.Task {
	t.Helper()
	task, err := s.cfg.Store.CreateTask(managerstate.StartTask{Workflow: w, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func reviewGraphRun(t *testing.T, s *Server, w managerstate.Workflow) (managerstate.Task, verk.Run) {
	t.Helper()
	task := reviewGraphTask(t, s, w)
	for _, event := range []verk.Event{{Type: "start", RunID: "entry-run", RequestID: "entry-request"}, {Type: "complete", Port: "next"}, {Type: "start", RunID: "action-run", RequestID: "action-request"}} {
		var err error
		task, err = s.graphEvent(task, event)
		if err != nil {
			t.Fatal(err)
		}
	}
	run, _ := verk.CurrentRun(*task.GraphState)
	return task, run
}
func TestGraphRuntimeToolRoutesAndFullOutput(t *testing.T) {
	for _, ref := range []string{"read", "unknown-tool"} {
		t.Run(ref, func(t *testing.T) {
			s := testServer(t, nil)
			path := filepath.Join(t.TempDir(), "input")
			if err := os.WriteFile(path, []byte("full\noutput\n"), 0600); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]string{"path": path})
			task := reviewGraphTask(t, s, reviewGraph("tool", map[string]any{"ref": ref, "input": string(args)}, "done", "error"))
			s.runGraphTask(context.Background(), task.ID)
			task = getWorkflowTask(t, s, task.ID)
			if task.Status != managerstate.Completed || len(task.GraphState.Runs) != 3 {
				t.Fatal(task.Status, task.Reason, task.GraphState)
			}
			run := task.GraphState.Runs[1]
			want := "done"
			if ref == "unknown-tool" {
				want = "error"
			}
			if run.Port != want || !run.OutputRecorded || run.Output == "" || run.Fields["final_message"] != run.Output {
				t.Fatal(run)
			}
			if ref == "read" && !strings.Contains(run.Output, `full\noutput\n`) {
				t.Fatal(run.Output)
			}
		})
	}
}
func TestGraphToolReceiptPreventsEffectReplay(t *testing.T) {
	s := testServer(t, nil)
	path := filepath.Join(t.TempDir(), "once")
	args, _ := json.Marshal(map[string]string{"path": path, "content": "first"})
	w := reviewGraph("tool", map[string]any{"ref": "write", "input": string(args)}, "done", "error")
	task, run := reviewGraphRun(t, s, w)
	node := w.Graph.Nodes[1]
	output, port, err := s.executeGraphNode(context.Background(), task, node, run)
	if err != nil || port != "done" {
		t.Fatal(output, port, err)
	}
	if err = os.WriteFile(path, []byte("changed externally"), 0600); err != nil {
		t.Fatal(err)
	}
	again, againPort, err := s.executeGraphNode(context.Background(), task, node, run)
	if err != nil || again != output || againPort != port {
		t.Fatal(again, againPort, err)
	}
	task, err = s.graphEvent(task, verk.Event{Type: "output", Output: output, Fields: graphFields(output)})
	if err != nil {
		t.Fatal(err)
	}
	run, _ = verk.CurrentRun(*task.GraphState)
	again, _, err = s.executeGraphNode(context.Background(), task, node, run)
	if err != nil || again != output {
		t.Fatal(again, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "changed externally" {
		t.Fatal("effect repeated", string(data))
	}
}
func TestGraphUncertainToolPausesWithoutExecution(t *testing.T) {
	s := testServer(t, nil)
	path := filepath.Join(t.TempDir(), "never")
	args, _ := json.Marshal(map[string]string{"path": path, "content": "bad"})
	task, run := reviewGraphRun(t, s, reviewGraph("tool", map[string]any{"ref": "write", "input": string(args)}, "done", "error"))
	if _, _, err := s.cfg.Store.BeginTool(run.RequestID, "graph", task.ID, "verk.tool:write", args, "manager"); err != nil {
		t.Fatal(err)
	}
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Paused || !strings.Contains(task.Reason, "uncertain") {
		t.Fatal(task.Status, task.Reason)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("uncertain effect repeated", err)
	}
	if _, err := s.cfg.Store.Resume(task.ID, "inspect saved effect"); err != nil {
		t.Fatal(err)
	}
	s.runGraphTask(context.Background(), task.ID)
	if task = getWorkflowTask(t, s, task.ID); task.Status != managerstate.Paused {
		t.Fatal(task.Status)
	}
}
func TestGraphApprovalRequiresExactOwnerSignal(t *testing.T) {
	s := testServer(t, nil)
	task := reviewGraphTask(t, s, reviewGraph("approve", map[string]any{"proposal": "Exact proposal"}, "approved", "rejected", "timeout"))
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Blocked || task.GraphState.Gate == nil {
		t.Fatal(task)
	}
	gate := task.GraphState.Gate
	run, _ := verk.CurrentRun(*task.GraphState)
	body := map[string]any{"request_id": run.RequestID, "gate_id": gate.ID, "port": "approved", "proposal_digest": gate.ProposalDigest}
	encode := func() string { raw, _ := json.Marshal(body); return string(raw) }
	path := "/api/tasks/" + task.ID + "/signal"
	if w := request(s, "POST", path, encode(), nil); w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
	body["proposal_digest"] = "wrong"
	if w := request(s, "POST", path, encode(), ownerHeaders()); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	body["proposal_digest"] = gate.ProposalDigest
	body["request_id"] = "stale"
	if w := request(s, "POST", path, encode(), ownerHeaders()); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	body["request_id"] = run.RequestID
	if w := request(s, "POST", path, encode(), ownerHeaders()); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(s, "POST", path, encode(), ownerHeaders()); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
}
func TestGraphCancelledAndBadTimeoutDoNotRunEffects(t *testing.T) {
	s := testServer(t, nil)
	task := reviewGraphTask(t, s, reviewGraph("tool", map[string]any{"ref": "unknown"}, "done", "error"))
	if _, err := s.cfg.Store.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	s.runGraphTask(context.Background(), task.ID)
	if got := getWorkflowTask(t, s, task.ID); got.Status != managerstate.Cancelled || len(got.GraphState.Runs) != 0 {
		t.Fatal(got)
	}
	task = reviewGraphTask(t, s, reviewGraph("wait", map[string]any{"event": "external", "timeout": "bad"}, "resume", "timeout"))
	s.runGraphTask(context.Background(), task.ID)
	if got := getWorkflowTask(t, s, task.ID); got.Status != managerstate.Paused || !strings.Contains(got.Reason, "timeout") {
		t.Fatal(got)
	}
}

type graphReviewRemote struct {
	nativeFake
	delivered chan struct{}
}

func (f *graphReviewRemote) Deliver(ctx context.Context, chat string, input envai.DeliveryInput) (envai.DeliveryReceipt, error) {
	receipt, err := f.nativeFake.Deliver(ctx, chat, input)
	if f.delivered != nil {
		select {
		case f.delivered <- struct{}{}:
		default:
		}
	}
	return receipt, err
}
func (f *graphReviewRemote) StartJob(context.Context, string, envai.JobInput) (envai.Job, error) {
	return envai.Job{TaskID: "native-task", TurnID: "turn"}, nil
}
func (f *graphReviewRemote) ClassifyChoice(_ context.Context, _ string, input envai.ChoiceInput) (envai.ChoiceResult, error) {
	return envai.ChoiceResult{Choice: "yes", Model: "model", RequestID: input.RequestID, InvocationID: "invocation"}, nil
}
func TestGraphNotificationRetainsIdentityAcrossRetry(t *testing.T) {
	remote := &graphReviewRemote{}
	s := testServer(t, func(c *Config) { c.Remote = remote; c.WorkspaceID = "workspace"; c.CEOTeamID = "ceo" })
	task := reviewGraphTask(t, s, reviewGraph("approve", map[string]any{"proposal": "Review"}, "approved", "rejected", "timeout"))
	remote.deliverErr = errors.New("delivery unavailable")
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if len(remote.deliveries) != 1 || remote.creates != 1 {
		t.Fatal(remote.deliveries, remote.creates)
	}
	original := remote.deliveries[0]
	remote.deliverErr = nil
	if err := s.notifyGraph(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if len(remote.deliveries) != 2 || remote.creates != 1 || remote.deliveries[1].EventID != original.EventID || remote.deliveries[1].Output != original.Output {
		t.Fatal(remote.deliveries)
	}
	if err := s.notifyGraph(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if len(remote.deliveries) != 2 {
		t.Fatal("duplicate notification")
	}
}
func TestGraphGateDoesNotExpireEarly(t *testing.T) {
	s := testServer(t, nil)
	task := reviewGraphTask(t, s, reviewGraph("wait", map[string]any{"event": "test", "timeout": "3600"}, "resume", "timeout"))
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	before := task.GraphState.Revision
	s.expireGraphGates(context.Background(), time.Now())
	task = getWorkflowTask(t, s, task.ID)
	if task.GraphState.Revision != before {
		t.Fatal("gate expired early")
	}
}

func TestGraphRestartReusesCompletedEffectReceipt(t *testing.T) {
	dir := t.TempDir()
	store, err := managerstate.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Token: testToken, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "output")
	args, _ := json.Marshal(map[string]string{"path": path, "content": "once"})
	w := reviewGraph("tool", map[string]any{"ref": "write", "input": string(args)}, "done", "error")
	task, run := reviewGraphRun(t, s, w)
	if _, _, err = s.executeGraphNode(context.Background(), task, w.Graph.Nodes[1], run); err != nil {
		t.Fatal(err)
	}
	s.Close()
	store.Close()
	if err = os.WriteFile(path, []byte("external change"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err = managerstate.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s, err = New(Config{Token: testToken, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restored := getWorkflowTask(t, s, task.ID)
	if restored.Status != managerstate.Paused {
		t.Fatal(restored.Status)
	}
	if _, err = store.Resume(task.ID, "recover recorded tool result"); err != nil {
		t.Fatal(err)
	}
	s.runGraphTask(context.Background(), task.ID)
	restored = getWorkflowTask(t, s, task.ID)
	if restored.Status != managerstate.Completed {
		t.Fatal(restored.Status, restored.Reason)
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != "external change" {
		t.Fatal("effect repeated after restart", string(actual))
	}
}

func (f *graphReviewRemote) Task(_ context.Context, chat, id string) (envai.DetachedTask, error) {
	return envai.DetachedTask{TaskID: id, TargetChatID: chat, Status: "completed", Output: "complete native output"}, nil
}
func TestGraphNativeJobAndChoice(t *testing.T) {
	for _, kind := range []string{"lid", "ai"} {
		t.Run(kind, func(t *testing.T) {
			remote := &graphReviewRemote{}
			s := testServer(t, func(c *Config) { c.Remote = remote; c.CEOTeamID = "ceo"; c.WorkspaceID = "workspace" })
			params := map[string]any{"ref": "remote-team", "instruction": "Full instruction", "question": "Which route?"}
			ports := []string{"done", "error"}
			if kind == "ai" {
				params["choices"] = []any{map[string]any{"id": "yes", "value": "yes"}, map[string]any{"id": "no", "value": "no"}}
				ports = []string{"yes", "no", "invalid"}
			}
			task := reviewGraphTask(t, s, reviewGraph(kind, params, ports...))
			s.runGraphTask(context.Background(), task.ID)
			task = getWorkflowTask(t, s, task.ID)
			if task.Status != managerstate.Completed {
				t.Fatal(task.Status, task.Reason)
			}
			if !task.GraphState.Runs[1].OutputRecorded {
				t.Fatal("native output not saved")
			}
		})
	}
}

func TestGraphRecordedOutputNeedsRoutingReceipt(t *testing.T) {
	s := testServer(t, nil)
	w := reviewGraph("tool", map[string]any{"ref": "read"}, "done", "error")
	task, run := reviewGraphRun(t, s, w)
	task, err := s.graphEvent(task, verk.Event{Type: "output", Output: "recorded"})
	if err != nil {
		t.Fatal(err)
	}
	run, _ = verk.CurrentRun(*task.GraphState)
	if _, _, err = s.executeGraphNode(context.Background(), task, w.Graph.Nodes[1], run); err == nil || !strings.Contains(err.Error(), "routing receipt") {
		t.Fatal(err)
	}
	if _, _, err = s.cfg.Store.BeginTool(run.RequestID+":result", "graph", task.ID, "verk.result", json.RawMessage(`{}`), "manager"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.executeGraphNode(context.Background(), task, w.Graph.Nodes[1], run); err == nil {
		t.Fatal("incomplete receipt accepted")
	}
}

func TestGraphCompletedUncertainToolReceiptStaysPaused(t *testing.T) {
	s := testServer(t, nil)
	w := reviewGraph("tool", map[string]any{"ref": "read", "input": "{}"}, "done", "error")
	task, run := reviewGraphRun(t, s, w)
	if _, _, err := s.cfg.Store.BeginTool(run.RequestID, "graph", task.ID, "verk.tool:read", json.RawMessage(`{}`), "manager"); err != nil {
		t.Fatal(err)
	}
	if err := s.cfg.Store.FinishTool(run.RequestID, `{"ok":false,"code":"outcome_unknown","error":"connection lost after execution"}`, true); err != nil {
		t.Fatal(err)
	}
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Paused || !strings.Contains(task.Reason, "uncertain") {
		t.Fatal("uncertain effect routed as final error", task.Status, task.Reason)
	}
}

func TestGraphNestedSnapshotSurvivesDefinitionUpdate(t *testing.T) {
	private := privateCatalog(t)
	child := reviewGraph("tool", map[string]any{"ref": "unknown-tool"}, "done", "error")
	child.ID = "private:child"
	child.Version = "1"
	savePrivate(t, private, child.ID, "workflow", 0, child)
	s := testServer(t, func(c *Config) { c.Private = private })
	parent := reviewGraph("verk", map[string]any{"ref": child.ID, "version": "1", "input": "{}"}, "done", "error")
	pinned, err := s.resolveWorkflow(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned.WorkflowDefinitions["action"]) == 0 {
		t.Fatal("missing child snapshot")
	}
	child.Version = "2"
	savePrivate(t, private, child.ID, "workflow", 1, child)
	if _, err = s.resolveWorkflow(parent); err == nil {
		t.Fatal("requested old child version silently changed")
	}
	task := reviewGraphTask(t, s, pinned)
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Completed {
		t.Fatal(task.Status, task.Reason)
	}
	children := 0
	for _, candidate := range s.cfg.Store.ListTasks() {
		if candidate.ParentTaskID == task.ID {
			children++
			if !strings.HasPrefix(candidate.Workflow.Version, "1+") || candidate.Status != managerstate.Completed {
				t.Fatal(candidate)
			}
		}
	}
	if children != 1 {
		t.Fatal("wrong child count", children)
	}
}

func TestGraphNestedSnapshotRejectsCycles(t *testing.T) {
	private := privateCatalog(t)
	child := reviewGraph("verk", map[string]any{"ref": "private:recursive", "version": "1"}, "done", "error")
	child.ID = "private:recursive"
	savePrivate(t, private, child.ID, "workflow", 0, child)
	s := testServer(t, func(c *Config) { c.Private = private })
	if _, err := s.resolveWorkflow(managerstate.Workflow{ID: child.ID}); err == nil {
		t.Fatal("recursive snapshot accepted")
	}
}

func (f *graphReviewRemote) CancelTask(_ context.Context, chat, id string, subtree bool) (envai.TaskCancellation, error) {
	f.cancels++
	return envai.TaskCancellation{TaskID: id, Status: "cancelled", Subtree: subtree}, f.cancelErr
}
func TestGraphCancellationRetainedAdmissions(t *testing.T) {
	for _, mode := range []string{"none", "retained", "uncertain", "invalid", "remote-error"} {
		t.Run(mode, func(t *testing.T) {
			remote := &graphReviewRemote{}
			s := testServer(t, func(c *Config) { c.Remote = remote })
			task, run := reviewGraphRun(t, s, reviewGraph("lid", map[string]any{"ref": "team"}, "done", "error"))
			if mode != "none" {
				if _, _, err := s.cfg.Store.BeginTool(run.RequestID, "chat", task.ID, "workflow.delegate", json.RawMessage(`{}`), "envai"); err != nil {
					t.Fatal(err)
				}
				if mode != "uncertain" {
					raw, _ := json.Marshal(PhaseExecution{OriginChatID: "chat", NativeTaskID: "job"})
					if mode == "invalid" {
						raw = []byte("bad")
					}
					if err := s.cfg.Store.FinishTool(run.RequestID, string(raw), false); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "remote-error" {
				remote.cancelErr = errors.New("offline")
			}
			updated, err := s.cancelTask(context.Background(), task.ID)
			if mode == "none" || mode == "retained" {
				if err != nil || updated.Status != managerstate.Cancelled {
					t.Fatal(updated, err)
				}
			} else {
				if err == nil || !updated.CancelRequested || updated.CancelError == "" {
					t.Fatal(updated, err)
				}
			}
			if mode == "retained" && remote.cancels != 1 {
				t.Fatal("native job not cancelled")
			}
		})
	}
}

func TestGraphEffectErrorsAndReplay(t *testing.T) {
	s := testServer(t, nil)
	w := reviewGraph("tool", map[string]any{"ref": "read"}, "done", "error")
	task, run := reviewGraphRun(t, s, w)
	for _, kind := range []string{"lid", "ai", "verk", "unknown"} {
		node := w.Graph.Nodes[1]
		node.Type = kind
		if _, _, err := s.executeGraphNode(context.Background(), task, node, run); err == nil {
			t.Fatal("unavailable effect accepted", kind)
		}
	}
	node := w.Graph.Nodes[1]
	node.Params["input"] = "invalid JSON"
	if _, _, err := s.graphTool(context.Background(), task, node, run); err == nil {
		t.Fatal("bad arguments accepted")
	}
	remote := &graphReviewRemote{}
	s2 := testServer(t, func(c *Config) { c.Remote = remote; c.WorkspaceID = "workspace"; c.CEOTeamID = "ceo" })
	task2, run2 := reviewGraphRun(t, s2, w)
	first, err := s2.graphExecutionChat(context.Background(), task2, run2, "team")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s2.graphExecutionChat(context.Background(), task2, run2, "team")
	if err != nil || first.ChatID != second.ChatID || first.SessionID != second.SessionID || remote.creates != 1 {
		t.Fatal(first, second, err)
	}
	next := run2
	next.RequestID = "uncertain-chat"
	if _, _, err = s2.cfg.Store.BeginTool(next.RequestID+":chat", "graph", task2.ID, "verk.chat", json.RawMessage(`{"team_id":"team"}`), "envai"); err != nil {
		t.Fatal(err)
	}
	if _, err = s2.graphExecutionChat(context.Background(), task2, next, "team"); err == nil {
		t.Fatal("uncertain chat recreated")
	}
}

func TestGraphTypedToolBindings(t *testing.T) {
	s := testServer(t, nil)
	path := filepath.Join(t.TempDir(), "bound")
	w := reviewGraph("tool", map[string]any{"ref": "write", "input": map[string]any{"path": map[string]any{"mode": "literal", "type": "string", "value": path}, "content": map[string]any{"mode": "ref", "type": "string", "value": "inputs.message"}}}, "done", "error")
	task, err := s.cfg.Store.CreateTask(managerstate.StartTask{Workflow: w, Inputs: json.RawMessage(`{"message":"full input"}`)})
	if err != nil {
		t.Fatal(err)
	}
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Completed {
		t.Fatal(task.Status, task.Reason)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "full input" {
		t.Fatal(string(data), err)
	}
}

func TestGraphPrivateToolPinnedEnvironment(t *testing.T) {
	for _, mode := range []string{"success", "missing", "protected", "unpinned", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			private := privateCatalog(t)
			s := testServer(t, func(c *Config) {
				c.Private = private
				c.Extensions = extensiontools.New(nil, []string{"PATH=/usr/bin:/bin"})
				c.LookupEnv = func(string) string { return "configured" }
				c.ProtectedEnvironment = []string{"MANAGER_SECRET"}
			})
			definition := privateconfig.Extension{Name: "echo", Description: "test", Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/sh", "-c", "printf '%s' \"$DECLARED\""}, Environment: map[string]string{"DECLARED": "USER_SETTING"}}
			if mode == "protected" {
				definition.Environment["DECLARED"] = "MANAGER_SECRET"
			}
			if mode == "missing" {
				s.cfg.LookupEnv = nil
			}
			raw, _ := json.Marshal(definition)
			if mode == "invalid" {
				raw = []byte(`{`)
			}
			w := reviewGraph("tool", map[string]any{"ref": "private:echo"}, "done", "error")
			savePrivate(t, private, "private:echo", "extension", 0, definition)
			if err := s.pinGraphTools(&w); err != nil {
				t.Fatal(err)
			}
			if mode == "unpinned" || mode == "invalid" {
				w.ToolDefinitions = nil
			}
			task, run := reviewGraphRun(t, s, w)
			if mode == "invalid" {
				task.Workflow.ToolDefinitions = map[string]json.RawMessage{"private:echo": raw}
			}
			output, port, err := s.graphTool(context.Background(), task, w.Graph.Nodes[1], run)
			if mode == "success" {
				if err != nil || port != "done" || !strings.Contains(output, "configured") {
					t.Fatal(output, port, err)
				}
			} else if err == nil {
				t.Fatal("invalid environment accepted", mode, output)
			}
		})
	}
}

func TestGraphChatAndNativeAdmissionFailures(t *testing.T) {
	for _, mode := range []string{"feature", "chat", "job-replay", "choice-replay"} {
		t.Run(mode, func(t *testing.T) {
			remote := &graphReviewRemote{}
			s := testServer(t, func(c *Config) { c.Remote = remote; c.CEOTeamID = "ceo"; c.WorkspaceID = "workspace" })
			w := reviewGraph("lid", map[string]any{"ref": "team"}, "done", "error")
			task, run := reviewGraphRun(t, s, w)
			node := w.Graph.Nodes[1]
			if mode == "feature" {
				remote.readyErr = errors.New("unsupported")
			}
			if mode == "chat" {
				remote.createErr = errors.New("unavailable")
			}
			if strings.HasSuffix(mode, "replay") {
				teamID := "team"
				if mode == "choice-replay" {
					teamID = "ceo"
				}
				execution, err := s.graphExecutionChat(context.Background(), task, run, teamID)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "job-replay" {
					body, _ := graphPayload(task, node)
					if _, _, err = s.cfg.Store.BeginTool(run.RequestID, execution.ChatID, task.ID, "workflow.delegate", body, "envai"); err != nil {
						t.Fatal(err)
					}
				} else {
					node.Type = "ai"
					node.Params["question"] = "Choose"
					node.Outputs = []verk.Port{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}}
					body, _ := graphPayload(task, node)
					raw, _ := json.Marshal(envai.ChoiceInput{RequestID: run.RequestID, Context: body, Question: "Choose", Choices: []envai.Choice{{Label: "yes", Description: "Yes"}, {Label: "no", Description: "No"}}})
					if _, _, err = s.cfg.Store.BeginTool(run.RequestID, execution.ChatID, task.ID, "verk.choice", raw, "envai"); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, _, err := s.executeGraphNode(context.Background(), task, node, run)
			if err == nil {
				t.Fatal("uncertain/failed native admission accepted")
			}
		})
	}
}

func TestGraphValidationAndNotificationAPI(t *testing.T) {
	s := testServer(t, nil)
	w := reviewGraph("tool", map[string]any{"ref": "read"}, "done", "error")
	raw, _ := json.Marshal(map[string]any{"workflow": w})
	if response := request(s, "POST", "/api/workflows/validate", string(raw), ownerHeaders()); response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	for _, body := range []string{`{`, `{"workflow":{}}`} {
		if response := request(s, "POST", "/api/workflows/validate", body, ownerHeaders()); response.Code != 400 {
			t.Fatal(response.Code, response.Body)
		}
	}
	if response := request(s, "POST", "/api/tasks/missing/signal", `{}`, ownerHeaders()); response.Code != 404 {
		t.Fatal(response.Code, response.Body)
	}
	task := reviewGraphTask(t, s, reviewGraph("approve", map[string]any{"proposal": "Exact"}, "approved", "rejected", "timeout"))
	s.runGraphTask(context.Background(), task.ID)
	if response := request(s, "POST", "/api/tasks/"+task.ID+"/retry-notification", `{}`, ownerHeaders()); response.Code != 502 {
		t.Fatal(response.Code, response.Body)
	}
}

func TestGraphExpiredGateAdvancesOnce(t *testing.T) {
	s := testServer(t, nil)
	w := reviewGraph("wait", map[string]any{"event": "source"}, "resume", "timeout")
	task, run := reviewGraphRun(t, s, w)
	past := time.Now().Add(-time.Second)
	var err error
	task, err = s.graphEvent(task, verk.Event{Type: "wait", Gate: &verk.Gate{ID: "gate", RunID: run.ID, Kind: "wait", SourceRef: "source", Deadline: &past}})
	if err != nil {
		t.Fatal(err)
	}
	s.expireGraphGates(context.Background(), time.Now())
	task = getWorkflowTask(t, s, task.ID)
	if task.GraphState.Gate.Status != "timeout" {
		t.Fatal(task.GraphState.Gate)
	}
	s.expireGraphGates(context.Background(), time.Now())
}

func TestGraphLoopExhaustionRequiresExplicitBudget(t *testing.T) {
	s := testServer(t, nil)
	w := reviewGraph("repeat", map[string]any{"limit": 1}, "again", "done", "exhausted")
	w.Graph.Nodes = append(w.Graph.Nodes, verk.Node{ID: "effect", Type: "tool", Params: map[string]any{"ref": "unknown-tool"}, Outputs: []verk.Port{{ID: "done", Label: "Done"}, {ID: "error", Label: "Error"}}})
	for i := range w.Graph.Edges {
		if w.Graph.Edges[i].Port == "again" {
			w.Graph.Edges[i].To = "effect"
		}
	}
	w.Graph.Edges = append(w.Graph.Edges, verk.Edge{ID: "back-done", From: "effect", Port: "done", To: "action"}, verk.Edge{ID: "back-error", From: "effect", Port: "error", To: "action"})
	task := reviewGraphTask(t, s, w)
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Blocked || task.GraphState.Loops["action"].Used != 1 {
		t.Fatal(task.Status, task.Reason)
	}
	if _, err := s.cfg.Store.Resume(task.ID, "try again"); err == nil {
		t.Fatal("resume bypassed budget")
	}
	run, _ := verk.CurrentRun(*task.GraphState)
	raw, _ := json.Marshal(map[string]any{"request_id": run.RequestID, "budget": 1})
	if response := request(s, "POST", "/api/tasks/"+task.ID+"/budget", string(raw), ownerHeaders()); response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
}

func TestGraphOutputSchemaFailureKeepsFullOutput(t *testing.T) {
	s := testServer(t, nil)
	w := reviewGraph("tool", map[string]any{"ref": "unknown-tool"}, "done", "error")
	w.Graph.Nodes[1].OutputSchema = json.RawMessage(`{"type":"object","required":["mandatory"]}`)
	task := reviewGraphTask(t, s, w)
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Paused || !task.GraphState.Runs[1].OutputRecorded || task.GraphState.Runs[1].Output == "" {
		t.Fatal(task.Status, task.Reason)
	}
}

func TestGraphCancellationIncludesNestedChild(t *testing.T) {
	s := testServer(t, nil)
	parent := reviewGraphTask(t, s, reviewGraph("wait", map[string]any{"event": "external"}, "resume", "timeout"))
	child, err := s.cfg.Store.CreateTask(managerstate.StartTask{Workflow: reviewGraph("wait", map[string]any{"event": "external"}, "resume", "timeout"), Inputs: json.RawMessage(`{}`), ParentTaskID: parent.ID, Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cancelTask(context.Background(), parent.ID); err != nil {
		t.Fatal(err)
	}
	if got := getWorkflowTask(t, s, child.ID); got.Status != managerstate.Cancelled {
		t.Fatal("child left active", got.Status)
	}
	if _, err = s.cancelTask(context.Background(), parent.ID); err == nil {
		t.Fatal("terminal cancellation accepted")
	}
}

func TestGraphCompletionNotificationReturnsToOrigin(t *testing.T) {
	remote := &graphReviewRemote{}
	s := testServer(t, func(c *Config) { c.Remote = remote; c.WorkspaceID = "workspace"; c.CEOTeamID = "ceo" })
	origin, err := s.cfg.Store.CreateSession("Origin")
	if err != nil {
		t.Fatal(err)
	}
	origin, err = s.cfg.Store.AttachSessionIdentity(origin.ID, "origin-chat", "ceo", "ceo")
	if err != nil {
		t.Fatal(err)
	}
	w := reviewGraph("tool", map[string]any{"ref": "unknown-tool"}, "done", "error")
	w.Graph.Nodes[2].ExitActions.NotifySmidr = true
	task, err := s.cfg.Store.CreateTask(managerstate.StartTask{Workflow: w, Inputs: json.RawMessage(`{}`), OriginSessionID: origin.ID, OriginChatID: origin.ChatID, OriginTurnID: "original-turn"})
	if err != nil {
		t.Fatal(err)
	}
	s.runGraphTask(context.Background(), task.ID)
	task = getWorkflowTask(t, s, task.ID)
	if task.Status != managerstate.Completed || remote.creates != 0 || len(remote.deliveries) != 1 {
		t.Fatal(task.Status, remote.creates, remote.deliveries)
	}
	delivery := remote.deliveries[0]
	if delivery.OriginTurnID != "original-turn" || delivery.TaskID != task.ID || delivery.StrictModel == nil || !*delivery.StrictModel {
		t.Fatal(delivery)
	}
	if response := request(s, "POST", "/api/tasks/"+task.ID+"/retry-notification", `{}`, ownerHeaders()); response.Code != 200 {
		t.Fatal(response.Code, response.Body)
	}
	if len(remote.deliveries) != 1 {
		t.Fatal("completed delivery repeated")
	}
}

func TestGraphNestedGateNotifiesSmidr(t *testing.T) {
	private := privateCatalog(t)
	child := reviewGraph("approve", map[string]any{"proposal": "Exact nested proposal"}, "approved", "rejected", "timeout")
	child.ID = "private:nested-gate"
	savePrivate(t, private, child.ID, "workflow", 0, child)
	remote := &graphReviewRemote{delivered: make(chan struct{}, 1)}
	s := testServer(t, func(c *Config) {
		c.Private = private
		c.Remote = remote
		c.CEOTeamID = "ceo"
		c.WorkspaceID = "workspace"
	})
	parent, err := s.resolveWorkflow(reviewGraph("verk", map[string]any{"ref": child.ID, "version": "1"}, "done", "error"))
	if err != nil {
		t.Fatal(err)
	}
	task := reviewGraphTask(t, s, parent)
	s.runGraphTask(context.Background(), task.ID)
	select {
	case <-remote.delivered:
	case <-time.After(time.Second):
		t.Fatal("nested gate notification missing")
	}
	s.Close()
	if len(remote.deliveries) != 1 || remote.deliveries[0].Status != managerstate.Blocked || !strings.Contains(remote.deliveries[0].Output, "Exact nested proposal") {
		t.Fatal("nested gate did not reach Smidr", remote.deliveries)
	}
}
