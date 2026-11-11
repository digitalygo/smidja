package manager

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/digitalygo/smidja/internal/envai"
)

type detachedToolRemote interface {
	toolRemote
	Task(context.Context, string, string) (envai.DetachedTask, error)
	VerifyDetachedToolRequest(context.Context, envai.Event, string, string) (envai.ToolRequest, error)
}

func (s *Server) handleDetachedTool(ctx context.Context, event envai.Event) error {
	if s.workflowToolCancelled(event.ChatID) {
		return errors.New("Verk cancellation forbids further tools")
	}
	var wrapped envai.DetachedEvent
	if err := json.Unmarshal(event.Data, &wrapped); err != nil {
		return err
	}
	if wrapped.EventType != "agent.tool.request" {
		return nil
	}
	if task, known := s.managedNativeTask(wrapped.TaskID); known && (task.CancelRequested || task.Status == "cancelled") {
		return errors.New("Verk cancellation forbids further detached tools")
	}
	remote, ok := s.cfg.Remote.(detachedToolRemote)
	if !ok {
		return errors.New("native detached tool verification unavailable")
	}
	task, err := remote.Task(ctx, event.ChatID, wrapped.TaskID)
	if err != nil {
		return err
	}
	if task.Status != "running" {
		return errors.New("detached tool task is no longer running")
	}
	request, err := remote.VerifyDetachedToolRequest(ctx, event, task.TaskID, task.OriginTurnID)
	if err != nil {
		return err
	}
	event.TurnID = "task:" + task.TaskID
	return s.executeVerifiedTool(ctx, event, request, task.OriginTurnID)
}
