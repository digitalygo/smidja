package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/verk"
)

func (s *Server) notifyGraph(ctx context.Context, task managerstate.Task) error {
	if task.ParentTaskID != "" && task.Status != managerstate.Blocked {
		return nil
	}
	if task.GraphState == nil {
		return errors.New("not a graph task")
	}
	node, err := verk.CurrentNode(*task.Workflow.Graph, *task.GraphState)
	if err != nil {
		return err
	}
	notify := task.Status == managerstate.Blocked
	if task.Status == managerstate.Completed {
		notify = node.ExitActions != nil && (node.ExitActions.NotifySmidr || node.ExitActions.Intervention)
	}
	if !notify {
		return nil
	}
	remote, ok := s.cfg.Remote.(graphRemote)
	if !ok {
		return errors.New("native notification transport unavailable")
	}
	rootID := task.ID
	ancestor := task
	for ancestor.ParentTaskID != "" {
		ancestor, err = s.cfg.Store.GetTask(ancestor.ParentTaskID)
		if err != nil {
			return err
		}
		rootID = ancestor.ID
	}
	var session managerstate.Session
	if task.OriginSessionID != "" {
		session, err = s.cfg.Store.GetSession(task.OriginSessionID)
		if err != nil {
			return err
		}
	} else {
		key := "verk-notification-session:" + rootID
		record, created, e := s.cfg.Store.BeginTool(key, "graph", task.ID, "verk.notification-session", json.RawMessage(`{}`), "envai")
		if e != nil {
			return e
		}
		if !created {
			if record.Status != managerstate.Completed {
				return errors.New("notification chat creation uncertain; inspect before retry")
			}
			if e = json.Unmarshal([]byte(record.Output), &session); e != nil {
				return e
			}
		} else {
			if e = remote.RequireFeatures(ctx, "originless_deliveries", "strict_model"); e != nil {
				return e
			}
			chat, e := remote.CreateChat(ctx, s.cfg.WorkspaceID, s.cfg.CEOTeamID)
			if e != nil {
				return e
			}
			session, e = s.cfg.Store.CreateSession("Verk " + task.Workflow.ID + " / " + task.ID)
			if e != nil {
				return e
			}
			session, e = s.cfg.Store.AttachSessionIdentity(session.ID, chat.ID, "ceo", s.cfg.CEOTeamID)
			if e != nil {
				return e
			}
			raw, _ := json.Marshal(session)
			if e = s.cfg.Store.FinishTool(key, string(raw), false); e != nil {
				return e
			}
		}
	}
	s.observeSession(session.ID)
	eventID := fmt.Sprintf("verk:%s:%d:%s", task.ID, task.GraphState.Revision, task.Status)
	output, err := json.Marshal(task)
	if err != nil {
		return err
	}
	strict := true
	input := envai.DeliveryInput{WorkspaceID: s.cfg.WorkspaceID, EventID: eventID, OriginTurnID: task.OriginTurnID, TaskID: task.ID, Status: task.Status, Output: string(output), StrictModel: &strict}
	raw, _ := json.Marshal(input)
	record, created, err := s.cfg.Store.BeginTool(eventID, session.ChatID, task.ID, "verk.notify", raw, "envai")
	if err != nil {
		return err
	}
	if !created {
		if record.Status == managerstate.Completed {
			return nil
		}
		if err = json.Unmarshal(record.Args, &input); err != nil {
			return err
		}
	}
	receipt, err := remote.Deliver(ctx, session.ChatID, input)
	if err != nil {
		return err
	}
	if receipt.Status != "delivered" && receipt.Status != "queued" {
		return fmt.Errorf("native notification %s", receipt.Status)
	}
	raw, _ = json.Marshal(receipt)
	return s.cfg.Store.FinishTool(eventID, string(raw), false)
}
func (s *Server) expireGraphGates(ctx context.Context, now time.Time) {
	for _, task := range s.cfg.Store.ListTasks() {
		if task.GraphState == nil || task.Status != managerstate.Blocked || task.CancelRequested {
			continue
		}
		gate := task.GraphState.Gate
		if gate == nil || gate.Status != "pending" || gate.Deadline == nil || now.Before(*gate.Deadline) {
			continue
		}
		updated, err := s.graphEvent(task, verk.Event{Type: "signal", RunID: gate.RunID, GateID: gate.ID, Port: "timeout"})
		if err == nil {
			s.scheduleTask(updated.ID)
		}
	}
}
