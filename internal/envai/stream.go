package envai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var ErrReplayGap = errors.New("envai replay gap or reset; explicit recovery required")
var ErrStreamInterrupted = errors.New("envai stream interrupted before terminal event")
var ErrTurnCancelled = errors.New("envai turn cancelled")
var errTerminal = errors.New("turn terminal received")

type Agent struct {
	InstanceID string  `json:"instance_id"`
	Name       string  `json:"name"`
	Role       string  `json:"role"`
	ParentID   *string `json:"parent_id"`
}

type Event struct {
	Version   int             `json:"v"`
	ID        string          `json:"id"`
	Sequence  int64           `json:"seq"`
	Timestamp string          `json:"ts"`
	ChatID    string          `json:"chat_id"`
	TurnID    string          `json:"turn_id"`
	Agent     *Agent          `json:"agent"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
}

type EventHandler func(Event) error

type TurnResult struct {
	ChatID       string `json:"chat_id"`
	TurnID       string `json:"turn_id"`
	Output       string `json:"output"`
	LastSequence int64  `json:"last_sequence"`
	Terminal     Event  `json:"terminal"`
}

func (c *Client) StreamEvents(ctx context.Context, chatID string, after int64, handler EventHandler) error {
	chat, err := identifier(chatID)
	if err != nil {
		return err
	}
	if after < 0 || handler == nil {
		return errors.New("valid replay cursor and event handler required")
	}
	resp, err := c.request(ctx, http.MethodGet, "/api/v1/atp/v3/chats/"+chat+"/events?after="+strconv.FormatInt(after, 10), nil, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return errors.New("envai response is not an event stream")
	}
	return decodeEvents(resp.Body, chatID, after, handler)
}

func decodeEvents(reader io.Reader, chatID string, after int64, handler EventHandler) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maximumResponseBytes)
	var data []string
	dataBytes := 0
	last := after
	dispatch := func() error {
		if len(data) == 0 {
			return nil
		}
		raw := strings.Join(data, "\n")
		data = nil
		dataBytes = 0
		var event Event
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return fmt.Errorf("invalid envai SSE JSON: %w", err)
		}
		if event.Version != 3 {
			return ErrContractMismatch
		}
		if event.Type == "ping" && event.Sequence == 0 {
			return nil
		}
		if event.ChatID != chatID || event.Sequence <= 0 || event.ID == "" || event.Type == "" {
			return errors.New("invalid envai event identity")
		}
		if event.Sequence <= after {
			return ErrReplayGap
		}
		if event.Sequence != last+1 {
			return ErrReplayGap
		}
		if err := handler(event); err != nil {
			return err
		}
		last = event.Sequence
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			dataBytes += len(value) + 1
			if dataBytes > maximumResponseBytes {
				return errors.New("envai SSE event exceeds supported size")
			}
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(data) > 0 {
		return ErrStreamInterrupted
	}
	return io.EOF
}

func (c *Client) FollowTurn(ctx context.Context, chatID, turnID string, after int64, handler EventHandler) (TurnResult, error) {
	result := TurnResult{ChatID: chatID, TurnID: turnID, LastSequence: after}
	if _, err := identifier(turnID); err != nil {
		return result, err
	}
	rootID := ""
	foundFinal := false
	var terminalError error
	for reconnect := 0; reconnect < 3; reconnect++ {
		var callbackError error
		err := c.StreamEvents(ctx, chatID, result.LastSequence, func(event Event) error {
			if handler != nil {
				if err := handler(event); err != nil {
					callbackError = err
					return err
				}
			}
			result.LastSequence = event.Sequence
			if event.TurnID != turnID {
				return nil
			}
			if event.Type == "agent.started" && event.Agent != nil && event.Agent.Role == "orchestrator" && (event.Agent.ParentID == nil || *event.Agent.ParentID == "") && rootID == "" {
				rootID = event.Agent.InstanceID
			}
			if event.Type == "agent.text.done" && event.Agent != nil && ((rootID != "" && event.Agent.InstanceID == rootID) || (rootID == "" && event.Agent.Role == "orchestrator" && (event.Agent.ParentID == nil || *event.Agent.ParentID == ""))) {
				var payload struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(event.Data, &payload); err != nil {
					return err
				}
				result.Output = payload.Text
				foundFinal = true
			}
			switch event.Type {
			case "turn.completed":
				result.Terminal = event
				if !foundFinal {
					terminalError = errors.New("completed turn has no recoverable orchestrator final output")
				}
				return errTerminal
			case "turn.failed":
				result.Terminal = event
				var failure struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				}
				_ = json.Unmarshal(event.Data, &failure)
				terminalError = fmt.Errorf("envai turn failed (%s): %s", failure.Code, failure.Message)
				return errTerminal
			case "turn.cancelled":
				result.Terminal = event
				terminalError = ErrTurnCancelled
				return errTerminal
			}
			return nil
		})
		if callbackError != nil {
			return result, callbackError
		}
		if errors.Is(err, errTerminal) {
			return result, terminalError
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if !errors.Is(err, io.EOF) && !errors.Is(err, ErrStreamInterrupted) {
			return result, err
		}
		if reconnect < 2 {
			timer := time.NewTimer(time.Duration(reconnect+1) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return result, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return result, ErrStreamInterrupted
}
