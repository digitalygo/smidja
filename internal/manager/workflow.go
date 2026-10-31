package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/digitalygo/smidja/internal/managerstate"
)

type PhaseExecution struct {
	SessionID    string
	Observed     bool
	ChatID       string
	TurnID       string
	NativeTaskID string
	OriginChatID string
}
type Classification struct {
	Decision managerstate.Decision
	Model    string
}
type WorkflowRuntime interface {
	Ready(context.Context) error
	StartPhase(context.Context, managerstate.Task, managerstate.PhaseContext) (PhaseExecution, error)
	FollowPhase(context.Context, PhaseExecution) (string, error)
	Classify(context.Context, managerstate.Task, managerstate.PhaseContext, string) (Classification, error)
	Notify(context.Context, managerstate.Task) error
}

func (s *Server) startTaskAPI(w http.ResponseWriter, r *http.Request) {
	var input managerstate.StartTask
	if decode(w, r, &input, 2<<20) != nil {
		return
	}
	if s.cfg.WorkflowRuntime == nil {
		writeError(w, 503, errors.New("native Envai Verk contracts are not configured"))
		return
	}
	if err := s.cfg.WorkflowRuntime.Ready(r.Context()); err != nil {
		writeError(w, 503, err)
		return
	}
	if input.OriginSessionID != "" {
		session, err := s.cfg.Store.GetSession(input.OriginSessionID)
		if err != nil || session.Turn == nil || session.Turn.TurnID == "" {
			writeError(w, 400, errors.New("origin session needs a remote Smidr turn"))
			return
		}
		input.OriginChatID = session.ChatID
		input.OriginTurnID = session.Turn.TurnID
	} else {
		input.OriginChatID = ""
		input.OriginTurnID = ""
	}
	workflow, resolveErr := s.resolveWorkflow(input.Workflow)
	if resolveErr != nil {
		writeError(w, 400, resolveErr)
		return
	}
	input.Workflow = workflow
	task, err := s.cfg.Store.CreateTask(input)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	s.scheduleTask(task.ID)
	writeJSON(w, 202, task)
}
func (s *Server) scheduleTask(id string) {
	if s.cfg.WorkflowRuntime == nil {
		return
	}
	s.launchMode("task:"+id, func(ctx context.Context) { s.runTask(ctx, id) }, true)
}
func (s *Server) runTask(ctx context.Context, id string) {
	runtime := s.cfg.WorkflowRuntime
	if err := runtime.Ready(ctx); err != nil {
		_, _ = s.cfg.Store.Pause(id, err.Error())
		return
	}
	for ctx.Err() == nil {
		task, err := s.cfg.Store.GetTask(id)
		if err != nil {
			return
		}
		if task.Status != managerstate.Queued && task.Status != managerstate.Running {
			return
		}
		if task.PhaseIndex >= len(task.Phases) {
			return
		}
		phase := task.Phases[task.PhaseIndex]
		requestID := ""
		output := ""
		hasOutput := false
		if len(phase.Attempts) > 0 {
			attempt := phase.Attempts[len(phase.Attempts)-1]
			if attempt.OutputRecorded && attempt.Decision == "" {
				requestID = attempt.RequestID
				output = attempt.Output
				hasOutput = true
			}
		}
		if !hasOutput {
			if task.Status == managerstate.Running {
				if len(phase.Attempts) == 0 {
					s.pauseTask(id, errors.New("running phase has no durable attempt"))
					return
				}
				requestID = phase.Attempts[len(phase.Attempts)-1].RequestID
				receipt, receiptErr := s.cfg.Store.GetTool(requestID)
				if receiptErr != nil || receipt.Status != managerstate.Completed || receipt.ToolName != "workflow.delegate" {
					s.pauseTask(id, errors.New("phase outcome is uncertain; inspect remote execution before resuming"))
					return
				}
			} else {
				requestID = randomID()
				task, err = s.cfg.Store.StartPhase(id, requestID)
				if err != nil {
					return
				}
			}
			phaseContext, e := s.cfg.Store.CurrentContext(id)
			if e != nil {
				s.pauseTask(id, e)
				return
			}
			execution, e := runtime.StartPhase(ctx, task, phaseContext)
			if e != nil {
				s.pauseTask(id, e)
				return
			}
			if _, e = s.cfg.Store.AttachRemote(id, requestID, execution.ChatID, execution.TurnID); e != nil {
				s.pauseTask(id, e)
				return
			}
			output, e = runtime.FollowPhase(ctx, execution)
			if e != nil {
				s.pauseTask(id, e)
				return
			}
			task, e = s.cfg.Store.RecordOutput(id, requestID, output)
			if e != nil {
				s.pauseTask(id, e)
				return
			}
		}
		phaseContext, err := s.cfg.Store.CurrentContext(id)
		if err != nil {
			s.pauseTask(id, err)
			return
		}
		classification, err := runtime.Classify(ctx, task, phaseContext, output)
		if err != nil {
			s.pauseTask(id, fmt.Errorf("classification failed: %w", err))
			return
		}
		task, err = s.cfg.Store.Classify(id, requestID, classification.Decision, classification.Model)
		if err != nil {
			s.pauseTask(id, err)
			return
		}
		if task.Status == managerstate.Blocked || task.Status == managerstate.Completed {
			if err = runtime.Notify(ctx, task); err != nil {
				s.recordNotificationFailure(task, err)
			}
			return
		}
	}
	s.pauseTask(id, ctx.Err())
}
func (s *Server) pauseTask(id string, err error) {
	if err != nil {
		_, _ = s.cfg.Store.Pause(id, err.Error())
	}
}
func (s *Server) recordNotificationFailure(task managerstate.Task, err error) {
	if task.OriginSessionID != "" {
		body, _ := json.Marshal(map[string]string{"task_id": task.ID, "error": err.Error(), "status": "notification_failed", "event_id": notificationEventID(task)})
		_, _ = s.cfg.Store.AppendMessage(task.OriginSessionID, managerstate.Message{ID: "notification-failure:" + notificationEventID(task), Role: "system", Content: string(body)})
	}
}
