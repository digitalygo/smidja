package telegram

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/gateway"
)

const errorUserMessage = "An error occurred while processing your request."

func (t *Telegram) Deliver(ctx context.Context, d gateway.Delivery) error {
	c := t.client.Load()
	if c == nil {
		return errors.New("telegram: transport not started")
	}
	chatID, threadID, _, err := parseChatKey(d.ExternalChatKey)
	if err != nil {
		return err
	}
	reply := replyParamsFor(d.ID)
	if d.Err != nil {
		_, err := c.sendMessage(ctx, chatID, threadID, errorUserMessage, reply)
		return err
	}
	if d.Result.Text == "" {
		return nil
	}
	return t.sendResponse(ctx, c, chatID, threadID, d.Result.Text, reply, d.ID)
}

func (t *Telegram) sendResponse(ctx context.Context, c *client, chatID, threadID int64, text string, reply *ReplyParameters, deliveryID string) error {
	if t.opts.Streaming {
		return t.sendStreamed(ctx, c, chatID, threadID, text, reply, deliveryID)
	}
	return t.sendRich(ctx, c, chatID, threadID, text, reply)
}

func (t *Telegram) sendStreamed(ctx context.Context, c *client, chatID, threadID int64, text string, reply *ReplyParameters, deliveryID string) error {
	parts := draftParts(text, 4)
	if len(parts) == 0 {
		return t.sendRich(ctx, c, chatID, threadID, text, reply)
	}
	draftID := draftIDFor(chatID, deliveryID)
	interval := t.streamingInterval()
	for _, part := range parts {
		if err := c.sendRichMessageDraft(ctx, chatID, threadID, draftID, InputRichMessage{Markdown: part}); err != nil {
			break
		}
		if err := t.sleep(ctx, interval); err != nil {
			return ctx.Err()
		}
	}
	return t.sendRich(ctx, c, chatID, threadID, text, reply)
}

func (t *Telegram) sendRich(ctx context.Context, c *client, chatID, threadID int64, text string, reply *ReplyParameters) error {
	_, err := c.sendRichMessage(ctx, chatID, threadID, InputRichMessage{Markdown: text}, reply)
	if err == nil {
		return nil
	}
	if !isFallbackError(err) {
		return err
	}
	chunks := chunkText(text, legacyChunkMax)
	for i, chunk := range chunks {
		var chunkReply *ReplyParameters
		if i == 0 {
			chunkReply = reply
		}
		if _, err := c.sendMessage(ctx, chatID, threadID, chunk, chunkReply); err != nil {
			return err
		}
	}
	return nil
}

func isFallbackError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == 400 || apiErr.Code == 404
}

func (t *Telegram) streamingInterval() time.Duration {
	if t.opts.StreamingInterval > 0 {
		return t.opts.StreamingInterval
	}
	return defaultStreamingInterval
}

func replyParamsFor(deliveryID string) *ReplyParameters {
	_, after, ok := strings.Cut(deliveryID, ":")
	if !ok {
		return nil
	}
	messageID, err := strconv.ParseInt(after, 10, 64)
	if err != nil {
		return nil
	}
	return &ReplyParameters{MessageID: messageID}
}
