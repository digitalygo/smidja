package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/prreview"
)

type PRReviewInput struct {
	originTurnID    string
	Repository      string `json:"repository"`
	Number          int    `json:"number"`
	Focus           string `json:"focus,omitempty"`
	OriginSessionID string `json:"origin_session_id"`
	RequestID       string `json:"request_id,omitempty"`
}

func (s *Server) startPRReview(ctx context.Context, input PRReviewInput, automatic bool) (managerstate.Task, error) {
	s.prReviewMu.Lock()
	defer s.prReviewMu.Unlock()
	var empty managerstate.Task
	if s.cfg.PRReview == nil {
		return empty, errors.New("PR review is not configured")
	}
	if s.cfg.WorkflowRuntime == nil {
		return empty, errors.New("native Verk runtime unavailable")
	}
	session, err := s.cfg.Store.GetSession(input.OriginSessionID)
	if err != nil || session.WorkflowTaskID != "" || session.ChatID == "" || session.Turn == nil || session.Turn.TurnID == "" {
		return empty, errors.New("PR review requires an originating Smidr session with a remote turn")
	}
	if err = s.cfg.WorkflowRuntime.Ready(ctx); err != nil {
		return empty, err
	}
	ref := prreview.Reference{Repository: input.Repository, Number: input.Number}
	pull, err := s.cfg.PRReview.Pull(ctx, ref)
	if err != nil {
		return empty, err
	}
	key := "pr-manual:" + input.RequestID
	if input.RequestID == "" {
		key = "pr-manual:" + randomID()
	}
	if automatic {
		key = fmt.Sprintf("pr-poll:%s:%d:%s:%s:%s", ref.Repository, ref.Number, pull.Head.SHA, prreview.WorkflowID, prreview.Version)
	}
	for _, task := range s.cfg.Store.ListTasks() {
		if task.InvocationKey == key {
			var snapshot prreview.Snapshot
			if json.Unmarshal(task.Inputs, &snapshot) != nil || snapshot.Reference != ref || task.Focus != input.Focus || task.OriginSessionID != input.OriginSessionID {
				return empty, errors.New("PR review request ID conflicts with an existing task")
			}
			return task, nil
		}
	}
	snapshot, err := s.cfg.PRReview.Snapshot(ctx, ref, pull)
	if err != nil {
		return empty, err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return empty, err
	}
	originTurnID := session.Turn.TurnID
	if input.originTurnID != "" {
		originTurnID = input.originTurnID
	}
	task, err := s.cfg.Store.CreateTask(managerstate.StartTask{Workflow: prreview.Workflow(s.cfg.PRReview.Config.Model), Inputs: data, Focus: input.Focus, OriginSessionID: session.ID, OriginChatID: session.ChatID, OriginTurnID: originTurnID, InvocationKey: key})
	if err == nil {
		s.scheduleTask(task.ID)
	}
	return task, err
}
func (s *Server) prReviewAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" && r.URL.Path == "/api/pr-review/status" {
		s.mu.Lock()
		status := s.prReviewStatus
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"configured": s.cfg.PRReview != nil, "poll": status})
		return
	}
	if r.Method != "POST" || r.URL.Path != "/api/pr-review" {
		http.NotFound(w, r)
		return
	}
	var input PRReviewInput
	if decode(w, r, &input, 8192) != nil {
		return
	}
	task, err := s.startPRReview(r.Context(), input, false)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 202, task)
}
func (s *Server) prReviewTool(ctx context.Context, event envai.Event, args json.RawMessage) machinetools.Result {
	if err := s.authorizeWorkflow(event, managerstate.Workflow{AllowedInvokers: []string{"ceo"}}); err != nil {
		return toolResult(nil, err)
	}
	var input PRReviewInput
	if err := json.Unmarshal(args, &input); err != nil {
		return toolResult(nil, err)
	}
	input.OriginSessionID = ""
	for _, session := range s.cfg.Store.ListSessions() {
		if session.ChatID == event.ChatID && session.WorkflowTaskID == "" {
			input.OriginSessionID = session.ID
			break
		}
	}
	request, err := event.ToolRequest()
	if err != nil {
		return toolResult(nil, err)
	}
	digest := sha256.Sum256([]byte(event.ChatID + "\x00" + request.CallID))
	input.RequestID = "tool-" + hex.EncodeToString(digest[:])
	input.originTurnID = event.TurnID
	task, err := s.startPRReview(ctx, input, false)
	return toolResult(task, err)
}
func (s *Server) pollPRReviews(ctx context.Context) error {
	refs, err := s.cfg.PRReview.References(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, ref := range refs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, err = s.startPRReview(ctx, PRReviewInput{Repository: ref.Repository, Number: ref.Number, Focus: s.cfg.PRReview.Config.Focus, OriginSessionID: s.cfg.PRReview.Config.OriginSessionID}, true)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s#%d: %w", ref.Repository, ref.Number, err))
		}
	}
	return errors.Join(failures...)
}
func (s *Server) startPRReviewPolling() {
	service := s.cfg.PRReview
	if service == nil || (len(service.Config.Repositories) == 0 && len(service.Config.Organizations) == 0) {
		return
	}
	s.launch("pr-review:poll", func(ctx context.Context) {
		ticker := time.NewTicker(time.Duration(service.Config.IntervalSeconds) * time.Second)
		defer ticker.Stop()
		for {
			err := s.pollPRReviews(ctx)
			s.mu.Lock()
			s.prReviewStatus = map[string]any{"checked_at": time.Now().UTC(), "ok": err == nil}
			if err != nil {
				s.prReviewStatus["error"] = err.Error()
			}
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}
