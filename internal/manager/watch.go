package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
)

type chatStreamRemote interface {
	StreamEvents(context.Context, string, int64, envai.EventHandler) error
}

func (s *Server) observeSession(id string) bool {
	remote, ok := s.cfg.Remote.(chatStreamRemote)
	if !ok {
		return false
	}
	session, err := s.cfg.Store.GetSession(id)
	if err != nil || session.ChatID == "" || session.AdmissionRejected || s.workflowToolCancelled(session.ChatID) {
		return false
	}
	s.launch("watch:"+id, func(ctx context.Context) {
		backoff := time.Second
		for {
			current, err := s.cfg.Store.GetSession(id)
			if err != nil || s.workflowToolCancelled(current.ChatID) {
				return
			}
			err = remote.StreamEvents(ctx, current.ChatID, int64(current.LastSequence), func(event envai.Event) error { return s.observeEvent(ctx, id, event) })
			if ctx.Err() != nil {
				return
			}
			if err != nil && !errors.Is(err, io.EOF) {
				current, readErr := s.cfg.Store.GetSession(id)
				if readErr != nil {
					return
				}
				if current.Turn != nil && (current.Turn.Status == managerstate.Running || current.Turn.Status == "submitting") {
					_, _ = s.cfg.Store.PauseSessionTurn(id, current.Turn.RequestID, "Chat stream stopped: "+err.Error())
				}
				_, _ = s.cfg.Store.AppendMessage(id, managerstate.Message{ID: fmt.Sprintf("stream-error:%s:%d", current.ChatID, current.LastSequence), Role: "system", Content: "Chat stream stopped; explicit recovery required: " + err.Error(), ChatID: current.ChatID})
				return
			}
			latest, _ := s.cfg.Store.GetSession(id)
			if latest.LastSequence > current.LastSequence {
				backoff = time.Second
			}
			timer := time.NewTimer(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			}
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	})
	return true
}

func (s *Server) observeEvent(ctx context.Context, id string, event envai.Event) error {
	session, err := s.cfg.Store.GetSession(id)
	if err != nil {
		return err
	}
	if event.ChatID != session.ChatID || event.ID == "" || event.Sequence <= 0 {
		return errors.New("invalid observed chat event identity")
	}
	if uint64(event.Sequence) <= session.LastSequence {
		return nil
	}
	if uint64(event.Sequence) != session.LastSequence+1 {
		return envai.ErrReplayGap
	}
	for session.Turn != nil && session.Turn.Status == "submitting" {
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		session, err = s.cfg.Store.GetSession(id)
		if err != nil {
			return err
		}
	}
	known := session.Turn != nil && session.Turn.TurnID == event.TurnID && event.TurnID != ""
	if known && session.Turn.Status == managerstate.Paused {
		return errors.New("observed turn is paused; explicit recovery required")
	}
	active := known && session.Turn.Status == managerstate.Running
	root := event.Agent != nil && event.Agent.Role == "orchestrator" && (event.Agent.ParentID == nil || *event.Agent.ParentID == "")
	switch event.Type {
	case "task.event":
		if err = s.handleDetachedTool(ctx, event); err != nil {
			return err
		}
	case "agent.tool.request":
		if event.TurnID == "" {
			return errors.New("tool event missing native turn identity")
		}
		if known && session.Turn.Status == managerstate.Cancelled {
			break
		}
		if err = s.handleTool(ctx, event); err != nil {
			return err
		}
	case "agent.text.done":
		if root {
			var payload struct {
				Text string `json:"text"`
			}
			if err = json.Unmarshal(event.Data, &payload); err != nil {
				return err
			}
			if active {
				_, err = s.cfg.Store.RecordSessionOutput(id, session.Turn.RequestID, payload.Text)
			} else if !known {
				_, err = s.cfg.Store.AppendMessage(id, managerstate.Message{ID: event.ID, Role: "assistant", Content: payload.Text, ChatID: event.ChatID, TurnID: event.TurnID})
			}
			if err != nil {
				return err
			}
		}
	case "turn.completed":
		if active {
			if !session.Turn.OutputRecorded {
				return errors.New("native turn completed without durable root output")
			}
			_, err = s.cfg.Store.CompleteSessionTurn(id, session.Turn.RequestID, session.Turn.Output)
			if err != nil {
				return err
			}
		}
	case "turn.cancelled":
		if active {
			_, err = s.cfg.Store.CancelSessionTurn(id, session.Turn.RequestID)
			if err != nil {
				return err
			}
		}
	case "turn.failed":
		if active {
			_, err = s.cfg.Store.PauseSessionTurn(id, session.Turn.RequestID, "Native turn failed: "+string(event.Data))
			if err != nil {
				return err
			}
		}
	}
	_, err = s.cfg.Store.RecordChatSequence(id, uint64(event.Sequence))
	return err
}

func (s *Server) observeAdmission(sessionID string, admissionErr error) (bool, error) {
	if envai.AdmissionRejected(admissionErr) {
		_, err := s.cfg.Store.RejectSessionAdmission(sessionID)
		s.cancelJob("watch:" + sessionID)
		return false, errors.Join(admissionErr, err)
	}
	return s.observeSession(sessionID), admissionErr
}
