package envai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type DeliveryInput struct {
	WorkspaceID  string `json:"workspace_id"`
	EventID      string `json:"event_id"`
	OriginTurnID string `json:"origin_turn_id"`
	TaskID       string `json:"task_id"`
	Status       string `json:"status"`
	Output       string `json:"output"`
}
type DeliveryReceipt struct {
	EventID   string          `json:"event_id"`
	Status    string          `json:"status"`
	TurnID    string          `json:"turn_id,omitempty"`
	Duplicate bool            `json:"duplicate"`
	Error     json.RawMessage `json:"error,omitempty"`
}
type DelegationInput struct {
	StrictModel  *bool  `json:"strict_model,omitempty"`
	NotifyOrigin *bool  `json:"notify_origin,omitempty"`
	WorkspaceID  string `json:"workspace_id"`
	EventID      string `json:"event_id"`
	OriginTurnID string `json:"origin_turn_id"`
	TargetChatID string `json:"target_chat_id"`
	Prompt       string `json:"prompt"`
}
type Delegation struct {
	TaskID       string `json:"task_id"`
	TargetChatID string `json:"target_chat_id"`
	TurnID       string `json:"turn_id"`
}
type DetachedTask struct {
	ResultTurnID  string          `json:"result_turn_id,omitempty"`
	TaskID        string          `json:"task_id"`
	OriginTurnID  string          `json:"origin_turn_id"`
	Status        string          `json:"status"`
	ParentTaskID  string          `json:"parent_task_id,omitempty"`
	TargetChatID  string          `json:"target_chat_id,omitempty"`
	Output        string          `json:"output,omitempty"`
	Error         json.RawMessage `json:"error,omitempty"`
	DeliveryError json.RawMessage `json:"delivery_error,omitempty"`
}
type TaskCancellation struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Subtree bool   `json:"subtree"`
}
type ClassificationInput struct {
	RequestID   string          `json:"request_id"`
	TaskContext json.RawMessage `json:"task_context"`
	PhaseID     string          `json:"phase_id"`
	Attempt     int             `json:"attempt"`
	FinalOutput string          `json:"final_output"`
}
type Classification struct {
	Decision     string `json:"decision"`
	Model        string `json:"model"`
	RequestID    string `json:"request_id"`
	InvocationID string `json:"invocation_id"`
}

func (c *Client) nativePath(ctx context.Context, chatID, feature string) (string, error) {
	chat, err := identifier(chatID)
	if err != nil {
		return "", err
	}
	if err = c.RequireFeatures(ctx, feature); err != nil {
		return "", err
	}
	return "/api/v1/atp/v3/chats/" + chat, nil
}
func (c *Client) Deliver(ctx context.Context, chatID string, input DeliveryInput) (DeliveryReceipt, error) {
	var result DeliveryReceipt
	if input.WorkspaceID == "" || input.EventID == "" || input.OriginTurnID == "" || input.TaskID == "" {
		return result, errors.New("delivery identity fields are required")
	}
	switch input.Status {
	case "completed", "blocked", "failed", "cancelled":
	default:
		return result, errors.New("invalid delivery status")
	}
	path, err := c.nativePath(ctx, chatID, "native_deliveries")
	if err != nil {
		return result, err
	}
	err = c.json(ctx, http.MethodPost, path+"/deliveries", input, &result, true)
	if err == nil && (result.EventID != input.EventID || (result.Status != "queued" && result.Status != "delivered" && result.Status != "cancelled" && result.Status != "failed")) {
		err = errors.New("invalid native delivery receipt; outcome uncertain")
	}
	return result, err
}
func (c *Client) Delegate(ctx context.Context, chatID string, input DelegationInput) (Delegation, error) {
	var result Delegation
	if input.WorkspaceID == "" || input.EventID == "" || input.OriginTurnID == "" || input.TargetChatID == "" || input.Prompt == "" {
		return result, errors.New("delegation identity and prompt required")
	}
	path, err := c.nativePath(ctx, chatID, "cross_team_delegation")
	if err != nil {
		return result, err
	}
	if input.NotifyOrigin != nil && !*input.NotifyOrigin {
		if err = c.RequireFeatures(ctx, "delegation_notify_origin"); err != nil {
			return result, err
		}
	}
	if input.StrictModel != nil && *input.StrictModel {
		if err = c.RequireFeatures(ctx, "strict_model"); err != nil {
			return result, err
		}
	}
	err = c.json(ctx, http.MethodPost, path+"/delegations", input, &result, true)
	if err == nil && (result.TaskID == "" || result.TurnID == "" || result.TargetChatID != input.TargetChatID) {
		err = errors.New("invalid native delegation receipt; outcome uncertain")
	}
	return result, err
}
func (c *Client) Task(ctx context.Context, chatID, taskID string) (DetachedTask, error) {
	var result DetachedTask
	task, err := identifier(taskID)
	if err != nil {
		return result, err
	}
	path, err := c.nativePath(ctx, chatID, "detached_background")
	if err != nil {
		return result, err
	}
	err = c.json(ctx, http.MethodGet, path+"/tasks/"+task, nil, &result, true)
	if err == nil && (result.TaskID != taskID || result.OriginTurnID == "" || (result.Status != "running" && result.Status != "completed" && result.Status != "failed" && result.Status != "cancelled")) {
		err = errors.New("invalid native task result")
	}
	return result, err
}
func (c *Client) CancelTask(ctx context.Context, chatID, taskID string, subtree bool) (TaskCancellation, error) {
	var result TaskCancellation
	task, err := identifier(taskID)
	if err != nil {
		return result, err
	}
	path, err := c.nativePath(ctx, chatID, "detached_background")
	if err != nil {
		return result, err
	}
	err = c.json(ctx, http.MethodPost, path+"/tasks/"+task+"/cancel", struct {
		Subtree bool `json:"subtree"`
	}{subtree}, &result, true)
	if err == nil && (result.TaskID != taskID || result.Status != "cancelled" || result.Subtree != subtree) {
		err = errors.New("invalid native cancellation receipt; outcome uncertain")
	}
	return result, err
}
func (c *Client) ClassifyPhase(ctx context.Context, chatID string, input ClassificationInput) (Classification, error) {
	var result Classification
	if input.RequestID == "" || len(input.RequestID) > 128 || input.PhaseID == "" || len(input.PhaseID) > 128 || input.Attempt < 1 || input.FinalOutput == "" || !json.Valid(input.TaskContext) || strings.TrimSpace(string(input.TaskContext)) == "null" {
		return result, errors.New("classification identity and valid context required")
	}
	body, err := json.Marshal(input)
	if err != nil || len(body) > 1<<20 {
		return result, errors.New("phase classification exceeds 1 MiB request limit")
	}
	path, err := c.nativePath(ctx, chatID, "phase_classification")
	if err != nil {
		return result, err
	}
	err = c.json(ctx, http.MethodPost, path+"/classify-phase", input, &result, true)
	if err == nil && (result.RequestID != input.RequestID || result.InvocationID == "" || result.Model == "" || (result.Decision != "BLOCKED" && result.Decision != "NOT BLOCKED")) {
		err = fmt.Errorf("invalid phase classification result; request %s outcome uncertain", input.RequestID)
	}
	return result, err
}
