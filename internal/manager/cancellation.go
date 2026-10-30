package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/digitalygo/smidja/internal/managerstate"
)

type phaseCanceller interface {
	CancelPhase(context.Context, managerstate.Task) error
}

func (s *Server) cancelTask(ctx context.Context, id string) (managerstate.Task, error) {
	canceller, ok := s.cfg.WorkflowRuntime.(phaseCanceller)
	if !ok {
		s.cancelJob("task:" + id)
		return s.cfg.Store.Cancel(id)
	}
	task, err := s.cfg.Store.RequestCancellation(id)
	if err != nil {
		return task, err
	}
	s.cancelJob("task:" + id)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = canceller.CancelPhase(ctx, task); err != nil {
		task, saveErr := s.cfg.Store.CancellationFailed(id, "Remote cancellation unconfirmed: "+err.Error())
		if saveErr != nil {
			return task, saveErr
		}
		return task, fmt.Errorf("local execution stopped; remote cancellation unconfirmed: %w", err)
	}
	return s.cfg.Store.ConfirmCancellation(id)
}
func (n *nativeWorkflow) CancelPhase(ctx context.Context, task managerstate.Task) error {
	if task.PhaseIndex >= len(task.Phases) {
		return nil
	}
	phase := task.Phases[task.PhaseIndex]
	if len(phase.Attempts) == 0 {
		return nil
	}
	attempt := phase.Attempts[len(phase.Attempts)-1]
	receipt, err := n.server.cfg.Store.GetTool(attempt.RequestID)
	if errors.Is(err, managerstate.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if receipt.Status != managerstate.Completed {
		return errors.New("delegation admission outcome unknown; no confirmed native task ID")
	}
	var execution PhaseExecution
	if err = json.Unmarshal([]byte(receipt.Output), &execution); err != nil {
		return err
	}
	if execution.NativeTaskID == "" || execution.OriginChatID == "" {
		return errors.New("native cancellation handle missing")
	}
	_, err = n.remote.CancelTask(ctx, execution.OriginChatID, execution.NativeTaskID, true)
	return err
}
func (s *Server) workflowToolCancelled(chatID string) bool {
	for _, task := range s.cfg.Store.ListTasks() {
		if task.Status != managerstate.Cancelled && !task.CancelRequested {
			continue
		}
		for _, phase := range task.Phases {
			for _, attempt := range phase.Attempts {
				if attempt.ChatID == chatID {
					for _, session := range s.cfg.Store.ListSessions() {
						if session.ChatID == chatID && session.WorkflowTaskID == task.ID {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func (s *Server) managedNativeTask(nativeID string) (managerstate.Task, bool) {
	for _, task := range s.cfg.Store.ListTasks() {
		for _, phase := range task.Phases {
			for _, attempt := range phase.Attempts {
				receipt, err := s.cfg.Store.GetTool(attempt.RequestID)
				if err != nil || receipt.Status != managerstate.Completed {
					continue
				}
				var execution PhaseExecution
				if json.Unmarshal([]byte(receipt.Output), &execution) == nil && execution.NativeTaskID == nativeID {
					return task, true
				}
			}
		}
	}
	return managerstate.Task{}, false
}
