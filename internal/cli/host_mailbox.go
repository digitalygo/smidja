package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

var (
	errHostMessageType       = errors.New("extensions: SendMessage requires a non-empty custom message type")
	errHostUserText          = errors.New("extensions: SendUserMessage requires non-empty text")
	errHostActiveUserMode    = errors.New("extensions: SendUserMessage during an active turn requires an explicit steer, followUp, or nextTurn delivery mode")
	errHostMessageCanceled   = errors.New("extensions: message delivery canceled")
	errHostPromptUnavailable = errors.New("extensions: prompt template expansion is unavailable")
)

type hostDelivery struct {
	handle       *hostSessionHandle
	signal       context.Context
	custom       *session.CustomMessageEntry
	text         string
	mode         sdk.DeliveryMode
	continueTurn bool
}

type hostScheduledTurn struct {
	generation uint64
	token      uint64
	text       string
	external   bool
}

type hostMailbox struct {
	generation         uint64
	steer              []hostDelivery
	followUp           []hostDelivery
	deferred           []hostDelivery
	nextTurn           []hostDelivery
	scheduled          []hostScheduledTurn
	continuationQueued bool
	skipSteerOnce      bool
	continuationToken  uint64
	tokenSequence      uint64
	jobQueued          bool
}

func (h *hostRuntime) setPromptExpander(expand func(string) (string, error)) {
	h.mailboxMu.Lock()
	h.expander = expand
	h.mailboxMu.Unlock()
}

func (h *hostRuntime) sendMessage(handle *hostSessionHandle, signal context.Context, message sdk.CustomMessage, options sdk.SendOptions) error {
	if strings.TrimSpace(message.Type) == "" {
		return errHostMessageType
	}
	mode, err := normalizeDeliveryMode(options.DeliverAs, true)
	if err != nil {
		return err
	}
	content := message.Content
	var details json.RawMessage
	if message.Details != nil {
		details, err = json.Marshal(message.Details)
		if err != nil {
			return fmt.Errorf("extensions: SendMessage details must be valid JSON: %w", err)
		}
	}
	if options.ExpandPromptTemplates {
		content, err = h.expandPrompt(content)
		if err != nil {
			return err
		}
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("extensions: SendMessage content must be valid JSON: %w", err)
	}
	entry := &session.CustomMessageEntry{
		CustomType: message.Type,
		Content:    contentJSON,
		Display:    message.Display,
		Details:    details,
	}
	return h.enqueueDelivery(handle, signal, hostDelivery{custom: entry, mode: mode}, options.TriggerTurn, false)
}

func (h *hostRuntime) sendUserMessage(handle *hostSessionHandle, signal context.Context, text string, options sdk.SendOptions) error {
	if strings.TrimSpace(text) == "" {
		return errHostUserText
	}
	mode, err := normalizeDeliveryMode(options.DeliverAs, false)
	if err != nil {
		return err
	}
	if options.ExpandPromptTemplates {
		text, err = h.expandPrompt(text)
		if err != nil {
			return err
		}
	}
	return h.enqueueDelivery(handle, signal, hostDelivery{text: text, mode: mode}, options.TriggerTurn, true)
}

func normalizeDeliveryMode(mode sdk.DeliveryMode, custom bool) (sdk.DeliveryMode, error) {
	if mode == "" && custom {
		return sdk.DeliverySteer, nil
	}
	if mode == "" && !custom {
		return "", nil
	}
	switch mode {
	case sdk.DeliverySteer, sdk.DeliveryFollowUp, sdk.DeliveryNextTurn:
		return mode, nil
	default:
		return "", fmt.Errorf("extensions: unknown delivery mode %q", mode)
	}
}

func (h *hostRuntime) expandPrompt(input string) (string, error) {
	h.mailboxMu.Lock()
	expand := h.expander
	h.mailboxMu.Unlock()
	if expand == nil {
		return "", errHostPromptUnavailable
	}
	return expand(input)
}

func (h *hostRuntime) enqueueDelivery(handle *hostSessionHandle, signal context.Context, delivery hostDelivery, trigger, user bool) error {
	if h.closed.Load() {
		return errHostClosed
	}
	if handle == nil {
		handle = h.snapshot()
	}
	if handle == nil || handle.sess == nil {
		return errHostClosed
	}
	if !h.currentGeneration(handle.generation) {
		return errHostStaleSession
	}
	if signal != nil && signal.Err() != nil {
		return fmt.Errorf("%w: %w", errHostMessageCanceled, signal.Err())
	}
	if runErr := h.runContext().Err(); runErr != nil {
		return fmt.Errorf("%w: %w", errHostMessageCanceled, runErr)
	}
	delivery.handle = handle
	delivery.signal = signal

	h.turnMu.Lock()
	active := h.turnActive
	if user && active && delivery.mode == "" {
		h.turnMu.Unlock()
		return errHostActiveUserMode
	}
	h.mailboxMu.Lock()
	current := h.snapshot()
	if h.closed.Load() {
		h.mailboxMu.Unlock()
		h.turnMu.Unlock()
		return errHostClosed
	}
	if current == nil || current.generation != handle.generation || h.mailbox.generation != handle.generation {
		h.mailboxMu.Unlock()
		h.turnMu.Unlock()
		return errHostStaleSession
	}
	if signal != nil && signal.Err() != nil {
		h.mailboxMu.Unlock()
		h.turnMu.Unlock()
		return fmt.Errorf("%w: %w", errHostMessageCanceled, signal.Err())
	}
	if runErr := h.runContext().Err(); runErr != nil {
		h.mailboxMu.Unlock()
		h.turnMu.Unlock()
		return fmt.Errorf("%w: %w", errHostMessageCanceled, runErr)
	}
	if user && !active {
		h.mailbox.scheduled = append(h.mailbox.scheduled, hostScheduledTurn{generation: handle.generation, text: delivery.text, external: true})
		h.mailboxMu.Unlock()
		h.turnMu.Unlock()
		h.scheduleMailbox()
		return nil
	}
	if delivery.mode == sdk.DeliveryNextTurn {
		delivery.signal = nil
		h.mailbox.nextTurn = append(h.mailbox.nextTurn, delivery)
		h.mailboxMu.Unlock()
		h.turnMu.Unlock()
		return nil
	}
	if !active && !trigger {
		h.mailboxMu.Unlock()
		_, err := h.persistDelivery(delivery, false)
		h.turnMu.Unlock()
		return err
	}
	if !active {
		delivery.continueTurn = true
		h.mailbox.steer = append(h.mailbox.steer, delivery)
		h.queueContinuationLocked()
		h.mailboxMu.Unlock()
		h.turnMu.Unlock()
		h.scheduleMailbox()
		return nil
	}
	switch delivery.mode {
	case sdk.DeliveryFollowUp:
		delivery.continueTurn = true
		h.mailbox.followUp = append(h.mailbox.followUp, delivery)
	case sdk.DeliverySteer:
		if user || trigger {
			delivery.continueTurn = true
			h.mailbox.steer = append(h.mailbox.steer, delivery)
		} else {
			h.mailbox.deferred = append(h.mailbox.deferred, delivery)
		}
	}
	h.mailboxMu.Unlock()
	h.turnMu.Unlock()
	return nil
}

func (h *hostRuntime) scheduleMailbox() {
	h.lifecycleMu.Lock()
	run := h.lifecycle.runScheduled
	h.lifecycleMu.Unlock()
	if run == nil || h.closed.Load() {
		return
	}
	h.mailboxMu.Lock()
	if h.mailbox.jobQueued || len(h.mailbox.scheduled) == 0 {
		h.mailboxMu.Unlock()
		return
	}
	turn := h.mailbox.scheduled[0]
	h.mailbox.scheduled = h.mailbox.scheduled[1:]
	h.mailbox.jobQueued = true
	h.mailboxMu.Unlock()
	h.dispatchCallback(func() {
		h.mailboxMu.Lock()
		if h.mailbox.generation != turn.generation || h.closed.Load() || (!turn.external && h.mailbox.continuationToken != turn.token) {
			if h.mailbox.generation == turn.generation {
				h.mailbox.jobQueued = false
			}
			h.mailboxMu.Unlock()
			h.scheduleMailbox()
			return
		}
		h.mailbox.jobQueued = false
		if !turn.external {
			h.mailbox.continuationQueued = false
		}
		h.mailboxMu.Unlock()
		h.lifecycleMu.Lock()
		currentRun := h.lifecycle.runScheduled
		h.lifecycleMu.Unlock()
		if currentRun != nil && h.currentGeneration(turn.generation) {
			currentRun(turn)
		}
		h.scheduleMailbox()
	})
}

func (h *hostRuntime) currentGeneration(generation uint64) bool {
	if h.closed.Load() {
		return false
	}
	current := h.snapshot()
	return current != nil && current.generation == generation
}

func (h *hostRuntime) waitMailbox() {
	h.waitCallbacks()
}

func (h *hostRuntime) scheduledTurnCurrent(turn hostScheduledTurn) bool {
	if !h.currentGeneration(turn.generation) {
		return false
	}
	if turn.external {
		return true
	}
	h.mailboxMu.Lock()
	defer h.mailboxMu.Unlock()
	return h.mailbox.generation == turn.generation && h.mailbox.continuationToken == turn.token
}

func (h *hostRuntime) mailboxBoundary(ctx context.Context, history []*agent.Message, stopping, external bool) (agent.MailboxResult, error) {
	if err := ctx.Err(); err != nil {
		return agent.MailboxResult{}, err
	}
	handle := h.snapshot()
	if handle == nil {
		return agent.MailboxResult{}, errHostClosed
	}
	h.mailboxMu.Lock()
	if h.mailbox.generation != handle.generation {
		h.mailboxMu.Unlock()
		return agent.MailboxResult{}, errHostStaleSession
	}
	var delivery hostDelivery
	var found bool
	var pollAgain bool
	if stopping {
		switch {
		case len(h.mailbox.steer) > 0:
			delivery, h.mailbox.steer, found = popHostDelivery(h.mailbox.steer)
		case len(h.mailbox.followUp) > 0:
			delivery, h.mailbox.followUp, found = popHostDelivery(h.mailbox.followUp)
		case len(h.mailbox.deferred) > 0:
			delivery, h.mailbox.deferred, found = popHostDelivery(h.mailbox.deferred)
			pollAgain = len(h.mailbox.deferred) > 0
		}
		if found && delivery.continueTurn {
			h.mailbox.skipSteerOnce = true
			h.mailbox.continuationQueued = false
			h.mailbox.tokenSequence++
			h.mailbox.continuationToken = h.mailbox.tokenSequence
		}
	} else if external && len(h.mailbox.nextTurn) > 0 {
		delivery, h.mailbox.nextTurn, found = popHostDelivery(h.mailbox.nextTurn)
		pollAgain = len(h.mailbox.nextTurn) > 0
	} else if h.mailbox.skipSteerOnce {
		h.mailbox.skipSteerOnce = false
	} else if len(h.mailbox.steer) > 0 {
		delivery, h.mailbox.steer, found = popHostDelivery(h.mailbox.steer)
		if delivery.continueTurn {
			h.mailbox.continuationQueued = false
			h.mailbox.tokenSequence++
			h.mailbox.continuationToken = h.mailbox.tokenSequence
		}
	}
	h.mailboxMu.Unlock()
	if !found {
		return agent.MailboxResult{}, nil
	}
	messages, err := h.persistDelivery(delivery, true)
	if err != nil {
		return agent.MailboxResult{}, err
	}
	return agent.MailboxResult{
		Messages:  messages,
		Delivered: true,
		Continue:  stopping && delivery.continueTurn,
		PollAgain: pollAgain,
	}, nil
}

func (h *hostRuntime) queueContinuationLocked() {
	if h.mailbox.continuationQueued {
		return
	}
	h.mailbox.tokenSequence++
	h.mailbox.continuationToken = h.mailbox.tokenSequence
	h.mailbox.scheduled = append(h.mailbox.scheduled, hostScheduledTurn{
		generation: h.mailbox.generation,
		token:      h.mailbox.continuationToken,
	})
	h.mailbox.continuationQueued = true
}

func (h *hostRuntime) invalidateContinuationLocked() {
	invalidatedToken := h.mailbox.continuationToken
	h.mailbox.tokenSequence++
	h.mailbox.continuationToken = h.mailbox.tokenSequence
	scheduled := h.mailbox.scheduled[:0]
	for _, turn := range h.mailbox.scheduled {
		if turn.generation == h.mailbox.generation && !turn.external && turn.token <= invalidatedToken {
			continue
		}
		scheduled = append(scheduled, turn)
	}
	h.mailbox.scheduled = scheduled
}

func popHostDelivery(queue []hostDelivery) (hostDelivery, []hostDelivery, bool) {
	if len(queue) == 0 {
		return hostDelivery{}, queue, false
	}
	return queue[0], queue[1:], true
}

func (h *hostRuntime) persistDelivery(delivery hostDelivery, activeBoundary bool) ([]*agent.Message, error) {
	h.deliveryMu.Lock()
	defer h.deliveryMu.Unlock()
	if delivery.signal != nil && delivery.signal.Err() != nil {
		return nil, fmt.Errorf("%w: %w", errHostMessageCanceled, delivery.signal.Err())
	}
	if h.closed.Load() {
		return nil, errHostClosed
	}
	if delivery.handle == nil {
		return nil, errHostClosed
	}
	if !h.currentGeneration(delivery.handle.generation) {
		return nil, errHostStaleSession
	}
	var customEntry *session.CustomMessageEntry
	err := h.commitSession(delivery.handle, func(sess *session.Session) error {
		if delivery.custom != nil {
			customEntry = &session.CustomMessageEntry{
				CustomType: delivery.custom.CustomType,
				Content:    append(json.RawMessage(nil), delivery.custom.Content...),
				Display:    delivery.custom.Display,
				Details:    append(json.RawMessage(nil), delivery.custom.Details...),
			}
			return sess.AppendEntry(customEntry)
		}
		content, marshalErr := json.Marshal(delivery.text)
		if marshalErr != nil {
			return fmt.Errorf("extensions: SendUserMessage content must be valid JSON: %w", marshalErr)
		}
		return sess.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: content, Timestamp: agent.NowMillis()})
	}, nil)
	if err != nil {
		return nil, err
	}
	loader, err := session.LoadWithOptions(delivery.handle.path, session.LoadOptions{Strict: true})
	if err != nil {
		return nil, fmt.Errorf("extensions: reload delivered message: %w", err)
	}
	messages, entryIDs, _, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		return nil, fmt.Errorf("extensions: project delivered message: %w", err)
	}
	h.mu.Lock()
	current := h.handle
	if current == nil || current.generation != delivery.handle.generation {
		h.mu.Unlock()
		return nil, errHostStaleSession
	}
	h.messages = append([]*agent.Message(nil), messages...)
	h.entryIDs = append([]string(nil), entryIDs...)
	h.mu.Unlock()
	if customEntry != nil && customEntry.Display {
		if activeBoundary {
			h.deliverMessageNow(delivery.handle, customEntry)
		} else {
			h.deliverCurrent(delivery.handle, func() { h.deliverMessageNow(delivery.handle, customEntry) })
		}
	}
	if delivery.custom == nil && activeBoundary {
		h.deliverUserMessageNow(delivery.handle, delivery.text)
	}
	return messages, nil
}

func (h *hostRuntime) deliverMessageNow(handle *hostSessionHandle, entry *session.CustomMessageEntry) {
	h.lifecycleMu.Lock()
	fn := h.lifecycle.message
	h.lifecycleMu.Unlock()
	if fn != nil && h.currentGeneration(handle.generation) {
		h.guardedCallback(func() { fn(entry) })
	}
}

func (h *hostRuntime) deliverUserMessageNow(handle *hostSessionHandle, text string) {
	h.lifecycleMu.Lock()
	fn := h.lifecycle.userMessage
	h.lifecycleMu.Unlock()
	if fn != nil && h.currentGeneration(handle.generation) {
		h.guardedCallback(func() { fn(text) })
	}
}
