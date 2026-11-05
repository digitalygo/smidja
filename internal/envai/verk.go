package envai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type JobInput struct {
	WorkspaceID string `json:"workspace_id"`
	RequestID   string `json:"request_id"`
	Prompt      string `json:"prompt"`
	StrictModel *bool  `json:"strict_model,omitempty"`
}

type Job struct {
	TaskID string `json:"task_id"`
	TurnID string `json:"turn_id"`
}

type Choice struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type ChoiceInput struct {
	RequestID string          `json:"request_id"`
	Context   json.RawMessage `json:"context"`
	Question  string          `json:"question"`
	Choices   []Choice        `json:"choices"`
}

type ChoiceResult struct {
	Choice       string `json:"choice"`
	Model        string `json:"model"`
	RequestID    string `json:"request_id"`
	InvocationID string `json:"invocation_id"`
}

func (c *Client) StartJob(ctx context.Context, chatID string, input JobInput) (Job, error) {
	var result Job
	if input.WorkspaceID == "" || input.RequestID == "" || strings.TrimSpace(input.Prompt) == "" {
		return result, errors.New("job workspace, request ID and prompt required")
	}
	path, err := c.nativePath(ctx, chatID, "independent_jobs")
	if err != nil {
		return result, err
	}
	if input.StrictModel != nil && *input.StrictModel {
		if err = c.RequireFeatures(ctx, "strict_model"); err != nil {
			return result, err
		}
	}
	err = c.json(ctx, http.MethodPost, path+"/jobs", input, &result, true)
	if err == nil && (result.TaskID == "" || result.TurnID == "") {
		err = errors.New("invalid native job receipt; outcome uncertain")
	}
	return result, err
}

func (c *Client) ClassifyChoice(ctx context.Context, chatID string, input ChoiceInput) (ChoiceResult, error) {
	var result ChoiceResult
	if input.RequestID == "" || strings.TrimSpace(input.Question) == "" || len(input.Choices) < 2 || len(input.Choices) > 32 || !json.Valid(input.Context) || string(input.Context) == "null" {
		return result, errors.New("choice classification requires identity, context, question and two to 32 choices")
	}
	labels := map[string]bool{}
	for _, choice := range input.Choices {
		if strings.TrimSpace(choice.Label) == "" || labels[choice.Label] {
			return result, errors.New("choice labels must be nonempty and unique")
		}
		labels[choice.Label] = true
	}
	path, err := c.nativePath(ctx, chatID, "finite_choice_classification")
	if err != nil {
		return result, err
	}
	err = c.json(ctx, http.MethodPost, path+"/classify-choice", input, &result, true)
	if err == nil && (!labels[result.Choice] || result.Model == "" || result.RequestID != input.RequestID || result.InvocationID == "") {
		err = errors.New("invalid native choice classification; outcome uncertain")
	}
	return result, err
}
