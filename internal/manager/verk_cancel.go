package manager

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/digitalygo/smidja/internal/managerstate"
)

func (s *Server) cancelGraphTask(ctx context.Context, task managerstate.Task) (managerstate.Task, error) {
	if task.Status == managerstate.Completed || task.Status == managerstate.Cancelled {
		return task, errors.New("task is terminal")
	}
	task, err := s.cfg.Store.RequestCancellation(task.ID)
	if err != nil {
		return task, err
	}
	s.stopWorkflowExecution(task.ID)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	failures := []error{}
	for _, run := range task.GraphState.Runs {
		record, e := s.cfg.Store.GetTool(run.RequestID)
		if errors.Is(e, managerstate.ErrNotFound) {
			continue
		}
		if e != nil {
			failures = append(failures, e)
			continue
		}
		if record.ToolName != "workflow.delegate" {
			continue
		}
		if record.Status != managerstate.Completed {
			failures = append(failures, errors.New("native graph admission outcome unknown"))
			continue
		}
		var execution PhaseExecution
		if e = json.Unmarshal([]byte(record.Output), &execution); e != nil {
			failures = append(failures, e)
			continue
		}
		remote, ok := s.cfg.Remote.(NativeRemote)
		if !ok {
			failures = append(failures, errors.New("native cancellation unavailable"))
			continue
		}
		_, e = remote.CancelTask(ctx, execution.OriginChatID, execution.NativeTaskID, true)
		if e != nil {
			failures = append(failures, e)
		}
	}
	for _, child := range s.cfg.Store.ListTasks() {
		if child.ParentTaskID == task.ID && child.Status != managerstate.Completed && child.Status != managerstate.Cancelled {
			if _, e := s.cancelTask(ctx, child.ID); e != nil {
				failures = append(failures, e)
			}
		}
	}
	if err = errors.Join(failures...); err != nil {
		updated, saveErr := s.cfg.Store.CancellationFailed(task.ID, err.Error())
		return updated, errors.Join(err, saveErr)
	}
	return s.cfg.Store.ConfirmCancellation(task.ID)
}
