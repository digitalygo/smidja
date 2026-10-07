package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/digitalygo/smidja/internal/session"
)

func (h *hostRuntime) commitSession(handle *hostSessionHandle, write func(*session.Session) error, after func(*hostSessionHandle)) error {
	if handle == nil || handle.sess == nil {
		return errHostClosed
	}
	h.sessionMu.Lock()
	defer h.sessionMu.Unlock()
	if h.closed.Load() {
		return errHostClosed
	}
	h.mu.Lock()
	current := h.handle
	h.mu.Unlock()
	if current == nil || current.generation != handle.generation || current.sess != handle.sess {
		return errHostStaleSession
	}
	if err := write(handle.sess); err != nil {
		return err
	}
	if after != nil {
		h.mu.Lock()
		if h.handle != nil && h.handle.generation == handle.generation {
			after(h.handle)
		}
		h.mu.Unlock()
	}
	return nil
}

func (h *hostRuntime) appendEntry(handle *hostSessionHandle, customType string, data any) error {
	if strings.TrimSpace(customType) == "" {
		return errHostCustomType
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("extensions: AppendEntry %q: %w", customType, err)
	}
	if err := h.commitSession(handle, func(sess *session.Session) error {
		return sess.AppendEntry(&session.CustomEntry{CustomType: customType, Data: raw})
	}, nil); err != nil {
		return err
	}
	h.deliverCurrent(handle, func() {
		h.lifecycleMu.Lock()
		fn := h.lifecycle.entry
		h.lifecycleMu.Unlock()
		if fn != nil {
			fn(customType, append(json.RawMessage(nil), raw...))
		}
	})
	return nil
}

func (h *hostRuntime) setSessionName(handle *hostSessionHandle, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errHostSessionName
	}
	if err := h.commitSession(handle, func(sess *session.Session) error {
		return sess.AppendEntry(&session.SessionInfoEntry{Name: &name})
	}, func(current *hostSessionHandle) {
		updated := *current
		updated.name = name
		h.handle = &updated
	}); err != nil {
		return err
	}
	h.deliverCurrent(handle, func() {
		h.lifecycleMu.Lock()
		fn := h.lifecycle.name
		h.lifecycleMu.Unlock()
		if fn != nil {
			fn(name)
		}
	})
	return nil
}

func (h *hostRuntime) labelEntry(handle *hostSessionHandle, entryID, label string) error {
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return errHostEntryID
	}
	value := label
	return h.commitSession(handle, func(sess *session.Session) error {
		return sess.AppendEntry(&session.LabelEntry{TargetID: entryID, Label: &value})
	}, nil)
}

func (h *hostRuntime) deliverCurrent(handle *hostSessionHandle, deliver func()) {
	if deliver == nil || handle == nil {
		return
	}
	generation := handle.generation
	h.dispatchCallback(func() {
		h.mu.Lock()
		current := h.handle
		closed := h.closed.Load()
		h.mu.Unlock()
		if closed || current == nil || current.generation != generation {
			return
		}
		deliver()
	})
}

func (h *hostRuntime) shutdown() {
	var (
		first    bool
		fn       func()
		failures []compactFailure
	)
	h.shutdownOnce.Do(func() {
		first = true
		h.sessionMu.Lock()
		h.closed.Store(true)
		h.sessionMu.Unlock()
		h.mailboxMu.Lock()
		h.mailbox.steer = nil
		h.mailbox.followUp = nil
		h.mailbox.deferred = nil
		h.mailbox.nextTurn = nil
		h.mailbox.scheduled = nil
		h.mailbox.continuationQueued = false
		h.mailboxMu.Unlock()
		h.closeCallbacks()
		h.lifecycleMu.Lock()
		cancel := h.cancelRun
		fn = h.lifecycle.shutdown
		h.lifecycleMu.Unlock()
		if cancel != nil {
			cancel()
		}
		for _, opts := range h.claimPendingCompact() {
			failures = append(failures, compactFailure{opts: opts, err: errHostCompactCanceled})
		}
		failures = append(failures, h.settleAllJobs(errHostClosed)...)
	})
	if !first {
		return
	}
	if fn != nil {
		h.guardedCallback(fn)
	}
	h.reportCompactFailures(failures)
}
