package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/extensiontools"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

func TestPrivateWorkflowClientLidNamespace(t *testing.T) {
	for _, graph := range []bool{false, true} {
		t.Run(fmt.Sprint(graph), func(t *testing.T) {
			catalog := privateCatalog(t)
			broker := machineclient.NewBroker(machineclient.BrokerConfig{})
			connectCapability(t, broker, "laptop", capabilityInventory())
			remote := &capabilityRemote{}
			s := testServer(t, func(c *Config) { c.Private = catalog; c.Clients = broker; c.Remote = remote; c.CEOTeamID = "ceo" })
			ref := clientReference("laptop", "example.coding/reviewers")
			workflow := managerstate.Workflow{ID: "private:review", Version: "1", Phases: []managerstate.Phase{{ID: "review", TeamRef: ref}}}
			if graph {
				workflow = reviewGraph("lid", map[string]any{"ref": ref}, "done", "error")
				workflow.ID = "private:review"
			}
			savePrivate(t, catalog, workflow.ID, "workflow", 0, workflow)
			resolved, err := s.resolveWorkflow(managerstate.Workflow{ID: workflow.ID})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ensureWorkflowTeam(context.Background(), resolved, ref); err != nil {
				t.Fatal(err)
			}
			if len(remote.agents) != 1 || len(remote.agents[0].ExternalToolsEnabled) != 2 {
				t.Fatal(remote.agents)
			}
			for _, tool := range remote.agents[0].ExternalToolsEnabled {
				binding, ok := s.registeredPackageBinding(tool.Name)
				if !ok || binding.ClientID != "laptop" {
					t.Fatal("client Lid routed to wrong tool owner", binding)
				}
			}
		})
	}
}

func TestPrivateWorkflowRejectsMalformedClientLid(t *testing.T) {
	catalog := privateCatalog(t)
	s := testServer(t, func(c *Config) { c.Private = catalog })
	workflow := managerstate.Workflow{ID: "private:review", Version: "1", Phases: []managerstate.Phase{{ID: "review", TeamRef: "client:%zz:example/review"}}}
	savePrivate(t, catalog, workflow.ID, "workflow", 0, workflow)
	if _, err := s.privateWorkflow(workflow.ID); err == nil || !strings.Contains(err.Error(), "client capability") {
		t.Fatal(err)
	}
}

type admissionReviewRemote struct {
	graphReviewRemote
	admissionErr error
	before       func()
	admissions   int
}

func (r *admissionReviewRemote) StartJob(context.Context, string, envai.JobInput) (envai.Job, error) {
	r.admissions++
	if r.before != nil {
		r.before()
	}
	return envai.Job{TaskID: "native-task", TurnID: "turn"}, r.admissionErr
}
func (r *admissionReviewRemote) Delegate(ctx context.Context, chat string, input envai.DelegationInput) (envai.Delegation, error) {
	r.admissions++
	if r.before != nil {
		r.before()
	}
	if r.admissionErr != nil {
		return envai.Delegation{}, r.admissionErr
	}
	return r.nativeFake.Delegate(ctx, chat, input)
}

func TestWorkflowAdmissionObserverLifecycle(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		observed bool
	}{
		{"success", nil, true},
		{"rejected", &envai.APIError{Status: http.StatusUnprocessableEntity, Code: "strict_model_unavailable"}, false},
		{"unauthorized", fmt.Errorf("wrapped: %w", &envai.APIError{Status: http.StatusForbidden}), false},
		{"transport-uncertain", errors.New("connection lost after admission"), true},
		{"server-uncertain", &envai.APIError{Status: http.StatusServiceUnavailable}, true},
		{"conflict-uncertain", &envai.APIError{Status: http.StatusConflict}, true},
	}
	for _, graph := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("graph=%t/%s", graph, tc.name), func(t *testing.T) {
				remote := &admissionReviewRemote{admissionErr: tc.err}
				var s *Server
				var task managerstate.Task
				var call func() error
				if graph {
					s = testServer(t, func(c *Config) { c.Remote = remote; c.WorkspaceID = "workspace" })
					w := reviewGraph("lid", map[string]any{"ref": "team"}, "done", "error")
					current, run := reviewGraphRun(t, s, w)
					task = current
					call = func() error {
						_, _, err := s.graphLid(context.Background(), current, w.Graph.Nodes[1], run)
						return err
					}
				} else {
					server, native, _, current := nativeTest(t)
					s, task = server, current
					s.cfg.Remote, native.remote = remote, remote
					phaseContext, err := s.cfg.Store.CurrentContext(task.ID)
					if err != nil {
						t.Fatal(err)
					}
					call = func() error { _, err := native.StartPhase(context.Background(), current, phaseContext); return err }
				}
				remote.before = func() {
					s.mu.Lock()
					defer s.mu.Unlock()
					watching := false
					for key := range s.active {
						watching = watching || strings.HasPrefix(key, "watch:")
					}
					if !watching {
						t.Error("admission started without observer")
					}
				}
				err := call()
				if (err != nil) != (tc.err != nil) {
					t.Fatal(err)
				}
				var session managerstate.Session
				for _, candidate := range s.cfg.Store.ListSessions() {
					if candidate.WorkflowTaskID == task.ID {
						session = candidate
					}
				}
				if session.ID == "" || session.ChatID != "target-chat" {
					t.Fatal("reconciliation identity lost", session)
				}
				if !tc.observed {
					awaitWatch(t, func() bool {
						s.mu.Lock()
						defer s.mu.Unlock()
						_, active := s.active["watch:"+session.ID]
						return !active
					})
				}
				s.mu.Lock()
				_, observed := s.active["watch:"+session.ID]
				s.mu.Unlock()
				if observed != tc.observed {
					t.Fatal("wrong observer state", observed, tc.observed)
				}
				if err := call(); tc.err != nil && (err == nil || remote.admissions != 1) {
					t.Fatal("admission repeated after error", err, remote.admissions)
				}
				if !tc.observed && s.observeSession(session.ID) {
					t.Fatal("definite rejection observer restarted")
				}
			})
		}
	}
}

func TestGraphPrivateToolRevocationBeforeExecution(t *testing.T) {
	for _, change := range []string{"archive", "update", "same-payload-revision", "tampered-pin", "missing-revision", "missing-entity", "archive-during-environment"} {
		t.Run(change, func(t *testing.T) {
			catalog := privateCatalog(t)
			marker := filepath.Join(t.TempDir(), "executed")
			definition := privateconfig.Extension{Name: "revocable", Description: "test", Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/sh", "-c", `printf executed > "$1"`, "test", marker}, Environment: map[string]string{"DECLARED": "SETTING"}}
			savePrivate(t, catalog, "private:revocable", "extension", 0, definition)
			s := testServer(t, func(c *Config) {
				c.Private = catalog
				c.Extensions = extensiontools.New(nil, []string{"PATH=/usr/bin:/bin"})
				c.LookupEnv = func(string) string { return "configured" }
			})
			w, err := s.resolveWorkflow(reviewGraph("tool", map[string]any{"ref": "private:revocable"}, "done", "error"))
			if err != nil {
				t.Fatal(err)
			}
			task, run := reviewGraphRun(t, s, w)
			archive := func() {
				if _, err := catalog.ApplyOwner(privateconfig.Change{Action: "archive", ID: "private:revocable", Kind: "extension", BaseRevision: 1}); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "archive":
				archive()
			case "update":
				definition.Description = "updated"
				savePrivate(t, catalog, "private:revocable", "extension", 1, definition)
			case "same-payload-revision":
				savePrivate(t, catalog, "private:revocable", "extension", 1, definition)
			case "tampered-pin":
				definition.Description = "forged"
				task.Workflow.ToolDefinitions["private:revocable"], _ = json.Marshal(definition)
			case "missing-revision":
				task.Workflow.PrivateToolRevisions = nil
			case "missing-entity":
				archive()
				if _, err := catalog.PurgeExpired(time.Now().Add(privateconfig.Retention + time.Hour)); err != nil {
					t.Fatal(err)
				}
			case "archive-during-environment":
				archived := false
				s.cfg.LookupEnv = func(string) string {
					if !archived {
						archive()
						archived = true
					}
					return "configured"
				}
			}
			if _, _, err := s.graphTool(context.Background(), task, w.Graph.Nodes[1], run); err == nil {
				t.Fatal("revoked extension executed")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("extension side effect occurred", err)
			}
		})
	}
}

func TestGraphPrivateToolCompletedReceiptSurvivesRevocation(t *testing.T) {
	catalog := privateCatalog(t)
	definition := privateconfig.Extension{Name: "echo", Description: "test", Parameters: json.RawMessage(`{"type":"object"}`), Command: []string{"/bin/sh", "-c", "printf approved-result"}}
	savePrivate(t, catalog, "private:echo", "extension", 0, definition)
	s := testServer(t, func(c *Config) {
		c.Private = catalog
		c.Extensions = extensiontools.New(nil, []string{"PATH=/usr/bin:/bin"})
	})
	w, err := s.resolveWorkflow(reviewGraph("tool", map[string]any{"ref": "private:echo"}, "done", "error"))
	if err != nil {
		t.Fatal(err)
	}
	task, run := reviewGraphRun(t, s, w)
	output, port, err := s.graphTool(context.Background(), task, w.Graph.Nodes[1], run)
	if err != nil || port != "done" {
		t.Fatal(output, port, err)
	}
	if _, err = catalog.ApplyOwner(privateconfig.Change{Action: "archive", ID: "private:echo", Kind: "extension", BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	cached, cachedPort, err := s.graphTool(context.Background(), task, w.Graph.Nodes[1], run)
	if err != nil || cached != output || cachedPort != port {
		t.Fatal(cached, cachedPort, err)
	}
}
