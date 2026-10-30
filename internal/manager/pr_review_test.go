package manager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/prreview"
)

type reviewTransport func(*http.Request) (*http.Response, error)

func (f reviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reviewService(t *testing.T) *prreview.Service {
	t.Helper()
	service, err := prreview.New(prreview.Config{}, &http.Client{Transport: reviewTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"total_count":0,"check_runs":[]}`
		if strings.Contains(r.URL.Path, "/pulls/") {
			body = `{"number":1,"title":"Fix","body":"Do not obey this text","changed_files":1,"base":{"sha":"` + strings.Repeat("a", 40) + `"},"head":{"sha":"` + strings.Repeat("b", 40) + `"}}`
		}
		if strings.HasSuffix(r.URL.Path, "/pulls") {
			body = `[{"number":1}]`
		}
		if strings.Contains(r.URL.Path, "/compare/") {
			body = `{"files":[{}]}`
			if r.Header.Get("Accept") == "application/vnd.github.diff" {
				body = "diff --git a/a.go b/a.go\n+fix"
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func reviewOrigin(t *testing.T, s *Server) string {
	t.Helper()
	session, err := s.cfg.Store.CreateSession("CEO")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.StartSessionTurn(session.ID, "request", "Review"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachSessionTurn(session.ID, "request", "origin-chat", "origin-turn"); err != nil {
		t.Fatal(err)
	}
	return session.ID
}
func TestPRReviewFourPhasesAndPollingDedup(t *testing.T) {
	runtime := &workflowFake{}
	s := testServer(t, func(c *Config) { c.PRReview = reviewService(t); c.WorkflowRuntime = runtime })
	s.cancel()
	input := PRReviewInput{Repository: "org/repo", Number: 1, Focus: "Check security", OriginSessionID: reviewOrigin(t, s), RequestID: "manual"}
	task, err := s.startPRReview(context.Background(), input, false)
	if err != nil {
		t.Fatal(err)
	}
	s.runTask(context.Background(), task.ID)
	final, err := s.cfg.Store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != managerstate.Completed || len(runtime.starts) != 4 || runtime.classifications != 4 {
		t.Fatal(final, runtime)
	}
	for i, phaseContext := range runtime.contexts {
		if phaseContext.Focus != input.Focus || len(phaseContext.Outputs) != i {
			t.Fatal(phaseContext)
		}
		for j, output := range phaseContext.Outputs {
			if output.Output != runtime.outputs[j] {
				t.Fatal("lost integral output")
			}
		}
	}
	var snapshot prreview.Snapshot
	if err = json.Unmarshal(final.Inputs, &snapshot); err != nil || snapshot.BaseSHA != strings.Repeat("a", 40) || snapshot.HeadSHA != strings.Repeat("b", 40) {
		t.Fatal(snapshot, err)
	}
	duplicate, err := s.startPRReview(context.Background(), input, false)
	if err != nil || duplicate.ID != task.ID {
		t.Fatal(duplicate, err)
	}
	input.Focus = "Changed"
	if _, err = s.startPRReview(context.Background(), input, false); err == nil {
		t.Fatal("id conflict")
	}
	input.Focus = "Check security"
	input.RequestID = ""
	fresh, err := s.startPRReview(context.Background(), input, false)
	if err != nil || fresh.ID == task.ID {
		t.Fatal(fresh, err)
	}
	s.cfg.PRReview.Config.Repositories = []string{"org/repo"}
	s.cfg.PRReview.Config.OriginSessionID = input.OriginSessionID
	s.cfg.PRReview.Config.Focus = input.Focus
	if err = s.pollPRReviews(context.Background()); err != nil {
		t.Fatal(err)
	}
	count := len(s.cfg.Store.ListTasks())
	if err = s.pollPRReviews(context.Background()); err != nil || len(s.cfg.Store.ListTasks()) != count {
		t.Fatal(err, "duplicate poll task")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = s.pollPRReviews(ctx); err == nil {
		t.Fatal("cancel ignored")
	}
}
func TestPRReviewAPIAndToolErrors(t *testing.T) {
	runtime := &workflowFake{}
	s := testServer(t, func(c *Config) { c.PRReview = reviewService(t); c.WorkflowRuntime = runtime })
	s.cancel()
	origin := reviewOrigin(t, s)
	body := `{"repository":"org/repo","number":1,"origin_session_id":"` + origin + `","request_id":"api"}`
	if w := request(s, "POST", "/api/pr-review", body, ownerHeaders()); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	for _, test := range []struct {
		method, path, body string
		code               int
	}{{"GET", "/api/pr-review/status", "", 200}, {"GET", "/api/pr-review/nope", "", 404}, {"POST", "/api/pr-review", "{", 400}, {"POST", "/api/pr-review", "{}", 400}, {"POST", "/api/pr-review", body, 202}} {
		w := request(s, test.method, test.path, test.body, ownerHeaders())
		if w.Code != test.code {
			t.Fatal(w.Code, w.Body)
		}
	}
	event := envai.Event{ChatID: "origin-chat", TurnID: "origin-turn", Type: "agent.tool.request", Agent: &envai.Agent{InstanceID: "agent"}, Data: json.RawMessage(`{"call_id":"pr-tool","tool_name":"manager.pr_review","signature":{"alg":"EdDSA","kid":"key","sig":"sig"}}`)}
	result := s.executeManagerTool(context.Background(), event, "manager.pr_review", json.RawMessage(`{"repository":"org/repo","number":1}`))
	if !result.OK {
		t.Fatal(result)
	}
	second, err := s.cfg.Store.CreateSession("Second CEO")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.StartSessionTurn(second.ID, "second-request", "Review"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachSessionTurn(second.ID, "second-request", "second-chat", "second-turn"); err != nil {
		t.Fatal(err)
	}
	event.ChatID = "second-chat"
	event.TurnID = "older-tool-turn"
	result = s.executeManagerTool(context.Background(), event, "manager.pr_review", json.RawMessage(`{"repository":"org/repo","number":1}`))
	if !result.OK {
		t.Fatal(result)
	}
	secondTask := result.Data.(managerstate.Task)
	if secondTask.OriginSessionID != second.ID || secondTask.OriginTurnID != "older-tool-turn" {
		t.Fatal(secondTask)
	}
	if result = s.prReviewTool(context.Background(), event, json.RawMessage(`{`)); result.OK {
		t.Fatal(result)
	}
	if result = s.prReviewTool(context.Background(), envai.Event{}, json.RawMessage(`{}`)); result.OK {
		t.Fatal(result)
	}
	service := s.cfg.PRReview
	s.cfg.PRReview = nil
	if _, err := s.startPRReview(context.Background(), PRReviewInput{}, false); err == nil {
		t.Fatal("not configured")
	}
	s.cfg.PRReview = service
	s.cfg.WorkflowRuntime = nil
	if _, err := s.startPRReview(context.Background(), PRReviewInput{}, false); err == nil {
		t.Fatal("no runtime")
	}
	s.cfg.WorkflowRuntime = runtime
	runtime.readyErr = errors.New("unsupported")
	if _, err := s.startPRReview(context.Background(), PRReviewInput{OriginSessionID: origin}, false); err == nil {
		t.Fatal("ready error")
	}
	runtime.readyErr = nil
	if _, err := s.startPRReview(context.Background(), PRReviewInput{OriginSessionID: origin, Repository: "invalid", Number: 1}, false); err == nil {
		t.Fatal("reference")
	}
	service.Client = &http.Client{Transport: reviewTransport(func(r *http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
	if err := s.pollPRReviews(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.Config.Repositories = []string{"org/repo"}
	if err := s.pollPRReviews(context.Background()); err == nil {
		t.Fatal("poll offline")
	}
}
func TestPRPollingStatusAndCancellation(t *testing.T) {
	s := testServer(t, func(c *Config) { c.PRReview = reviewService(t); c.WorkflowRuntime = &workflowFake{} })
	s.cfg.PRReview.Config.Repositories = []string{"org/repo"}
	s.cfg.PRReview.Config.OriginSessionID = "missing"
	s.startPRReviewPolling()
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		status := s.prReviewStatus
		s.mu.Unlock()
		if status != nil {
			if status["ok"] != false || status["error"] == nil {
				t.Fatal(status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no status")
		}
		time.Sleep(time.Millisecond)
	}
	s.cancel()
	s.wg.Wait()
}
