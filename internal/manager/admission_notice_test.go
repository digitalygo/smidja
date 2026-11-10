package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/verk"
)

type noticeAdmissionRemote struct {
	admissionReviewRemote
	notices   chan envai.Event
	processed chan error
}

func (r *noticeAdmissionRemote) StreamEvents(ctx context.Context, chatID string, _ int64, handler envai.EventHandler) error {
	if chatID != "target-chat" {
		<-ctx.Done()
		return ctx.Err()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-r.notices:
			err := handler(event)
			r.processed <- err
			if err != nil {
				return err
			}
		}
	}
}

func TestWorkflowAdmissionNoticeAndRestart(t *testing.T) {
	for _, graph := range []bool{false, true} {
		for _, status := range []int{http.StatusPaymentRequired, http.StatusUnprocessableEntity, http.StatusServiceUnavailable} {
			t.Run(fmt.Sprintf("graph=%t/status=%d", graph, status), func(t *testing.T) {
				dir := t.TempDir()
				remote := &noticeAdmissionRemote{
					admissionReviewRemote: admissionReviewRemote{admissionErr: &envai.APIError{Status: status, Code: "billing_wallet_insufficient"}},
					notices:               make(chan envai.Event, 1), processed: make(chan error, 1),
				}
				open := func() *Server {
					store, err := managerstate.Open(dir)
					if err != nil {
						t.Fatal(err)
					}
					s, err := New(Config{Token: testToken, Store: store, Remote: remote, WorkspaceID: "workspace"})
					if err != nil {
						store.Close()
						t.Fatal(err)
					}
					t.Cleanup(func() { s.Close(); store.Close() })
					return s
				}
				s := open()
				var task managerstate.Task
				var run verk.Run
				var err error
				if graph {
					task, run = reviewGraphRun(t, s, reviewGraph("lid", map[string]any{"ref": "team"}, "done", "error"))
				} else {
					origin, err := s.cfg.Store.CreateSession("origin")
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.cfg.Store.StartSessionTurn(origin.ID, "origin-request", "work"); err != nil {
						t.Fatal(err)
					}
					if _, err = s.cfg.Store.AttachSessionTurn(origin.ID, "origin-request", "origin-chat", "origin-turn"); err != nil {
						t.Fatal(err)
					}
					input := workflowInput()
					input.OriginSessionID, input.OriginChatID, input.OriginTurnID = origin.ID, "origin-chat", "origin-turn"
					if task, err = s.cfg.Store.CreateTask(input); err != nil {
						t.Fatal(err)
					}
					if task, err = s.cfg.Store.StartPhase(task.ID, "phase-request"); err != nil {
						t.Fatal(err)
					}
				}
				call := func() error {
					if graph {
						_, _, err := s.graphLid(context.Background(), task, task.Workflow.Graph.Nodes[1], run)
						return err
					}
					phaseContext, err := s.cfg.Store.CurrentContext(task.ID)
					if err != nil {
						return err
					}
					_, err = (&nativeWorkflow{server: s, remote: remote}).StartPhase(context.Background(), task, phaseContext)
					return err
				}
				remote.before = func() {
					remote.notices <- envai.Event{ID: "billing-notice", ChatID: "target-chat", Sequence: 1, Type: "billing.credit_exhausted"}
					select {
					case err := <-remote.processed:
						if err != nil {
							t.Fatal(err)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("observer did not consume pre-admission notice")
					}
				}
				if err = call(); !errors.Is(err, remote.admissionErr) {
					t.Fatal("original admission failure lost", err)
				}
				rejected := status != http.StatusServiceUnavailable
				var sessionID string
				for _, session := range s.cfg.Store.ListSessions() {
					if session.WorkflowTaskID == task.ID {
						sessionID = session.ID
					}
				}
				assertState := func() {
					session, err := s.cfg.Store.GetSession(sessionID)
					if err != nil || session.AdmissionRejected != rejected || session.LastSequence != 1 || session.Turn != nil || session.ChatID != "target-chat" {
						t.Fatal("notice or admission state lost", session, err)
					}
					awaitWatch(t, func() bool {
						s.mu.Lock()
						defer s.mu.Unlock()
						_, active := s.active["watch:"+sessionID]
						return active != rejected
					})
				}
				assertState()
				s.Close()
				if err = s.cfg.Store.Close(); err != nil {
					t.Fatal(err)
				}
				s = open()
				assertState()
				if err = call(); err == nil || remote.admissions != 1 || remote.creates != 1 {
					t.Fatal("admission repeated after restart", err, remote.admissions, remote.creates)
				}
				assertState()
			})
		}
	}
}
