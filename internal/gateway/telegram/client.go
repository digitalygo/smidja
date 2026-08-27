package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const tokenRedacted = "[redacted]"

type client struct {
	base  string
	token string
	http  *http.Client
}

type APIError struct {
	Code        int
	Description string
	RetryAfter  int
	token       string
}

func (e *APIError) Error() string {
	desc := e.Description
	if e.token != "" {
		desc = strings.ReplaceAll(desc, e.token, tokenRedacted)
	}
	return fmt.Sprintf("telegram api error %d: %s", e.Code, desc)
}

type apiEnvelope struct {
	OK          bool            `json:"ok"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *apiParameters  `json:"parameters"`
	Result      json.RawMessage `json:"result"`
}

type apiParameters struct {
	RetryAfter int `json:"retry_after"`
}

func (c *client) call(ctx context.Context, method string, params url.Values, out any) error {
	endpoint := c.base + "/bot" + c.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return fmt.Errorf("telegram: %s", c.redact(err.Error()))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: %s", c.redact(err.Error()))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("telegram: %s", c.redact(err.Error()))
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("telegram: decode response: %s", c.redact(err.Error()))
	}
	if !envelope.OK {
		apiErr := &APIError{Code: envelope.ErrorCode, Description: envelope.Description, token: c.token}
		if envelope.Parameters != nil {
			apiErr.RetryAfter = envelope.Parameters.RetryAfter
		}
		if apiErr.Code == 0 && apiErr.Description == "" {
			apiErr.Description = c.redact(string(body))
		}
		return apiErr
	}
	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("telegram: decode result: %s", c.redact(err.Error()))
		}
	}
	return nil
}

func (c *client) getMe(ctx context.Context) (User, error) {
	var out User
	err := c.call(ctx, "getMe", nil, &out)
	return out, err
}

func (c *client) deleteWebhook(ctx context.Context) error {
	return c.call(ctx, "deleteWebhook", url.Values{}, nil)
}

func (c *client) getUpdates(ctx context.Context, offset int64, timeoutSecs int) ([]Update, error) {
	params := url.Values{}
	params.Set("offset", strconv.FormatInt(offset, 10))
	params.Set("limit", strconv.Itoa(maxUpdates))
	params.Set("timeout", strconv.Itoa(timeoutSecs))
	params.Set("allowed_updates", `["message"]`)
	var out []Update
	if err := c.call(ctx, "getUpdates", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *client) sendChatAction(ctx context.Context, chatID, threadID int64, action string) error {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("action", action)
	if threadID != 0 {
		params.Set("message_thread_id", strconv.FormatInt(threadID, 10))
	}
	return c.call(ctx, "sendChatAction", params, nil)
}

func (c *client) sendRichMessage(ctx context.Context, chatID, threadID int64, rich InputRichMessage, reply *ReplyParameters) (ResponseMessage, error) {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	if threadID != 0 {
		params.Set("message_thread_id", strconv.FormatInt(threadID, 10))
	}
	data, err := json.Marshal(rich)
	if err != nil {
		return ResponseMessage{}, err
	}
	params.Set("rich_message", string(data))
	setReplyParams(params, reply)
	var out ResponseMessage
	if err := c.call(ctx, "sendRichMessage", params, &out); err != nil {
		return ResponseMessage{}, err
	}
	return out, nil
}

func (c *client) sendRichMessageDraft(ctx context.Context, chatID, threadID int64, draftID int, rich InputRichMessage) error {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("draft_id", strconv.Itoa(draftID))
	if threadID != 0 {
		params.Set("message_thread_id", strconv.FormatInt(threadID, 10))
	}
	data, err := json.Marshal(rich)
	if err != nil {
		return err
	}
	params.Set("rich_message", string(data))
	return c.call(ctx, "sendRichMessageDraft", params, nil)
}

func (c *client) sendMessage(ctx context.Context, chatID, threadID int64, text string, reply *ReplyParameters) (ResponseMessage, error) {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("text", text)
	if threadID != 0 {
		params.Set("message_thread_id", strconv.FormatInt(threadID, 10))
	}
	setReplyParams(params, reply)
	var out ResponseMessage
	if err := c.call(ctx, "sendMessage", params, &out); err != nil {
		return ResponseMessage{}, err
	}
	return out, nil
}

func setReplyParams(params url.Values, reply *ReplyParameters) {
	if reply == nil {
		return
	}
	data, err := json.Marshal(reply)
	if err != nil {
		return
	}
	params.Set("reply_parameters", string(data))
}

func (c *client) redact(s string) string {
	if c.token == "" || s == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, tokenRedacted)
}
