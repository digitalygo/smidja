package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

func (b *tuiBridge) newSession(args string) error {
	return b.newSessionWithSignal(b.ctx, args)
}

func (b *tuiBridge) newSessionWithSignal(signal context.Context, args string) error {
	if strings.TrimSpace(args) != "" {
		return errors.New("new: unexpected arguments")
	}
	if b.sessions == nil {
		return errors.New("new: session control is unavailable")
	}
	next, err := b.sessions.PrepareNew()
	if err != nil {
		return err
	}
	if signal != nil && signal.Err() != nil {
		if abortErr := b.sessions.Abort(next); abortErr != nil {
			return errors.Join(signal.Err(), abortErr)
		}
		return signal.Err()
	}
	return b.commitPrepared(signal, next, "started a new session")
}

func (b *tuiBridge) resumeSession(args string) error {
	target := strings.TrimSpace(args)
	if target == "" {
		return b.sessionsBrowser()
	}
	return b.resumeSessionWithSignal(b.ctx, target)
}

func (b *tuiBridge) resumeSessionWithSignal(signal context.Context, target string) error {
	if b.sessions == nil {
		return errors.New("resume: session control is unavailable")
	}
	resolved, err := b.resolveSessionTarget(target)
	if err != nil {
		return err
	}
	next, err := b.sessions.PrepareOpen(resolved)
	if err != nil {
		return err
	}
	if signal != nil && signal.Err() != nil {
		if abortErr := b.sessions.Abort(next); abortErr != nil {
			return errors.Join(signal.Err(), abortErr)
		}
		return signal.Err()
	}
	return b.commitPrepared(signal, next, "resumed session")
}

func (b *tuiBridge) forkSession(args string) error {
	return b.forkSessionWithSignal(b.ctx, args)
}

func (b *tuiBridge) forkSessionWithSignal(signal context.Context, args string) error {
	if b.sessions == nil {
		return errors.New("fork: session control is unavailable")
	}
	next, err := b.sessions.PrepareFork(strings.TrimSpace(args), sdk.ForkOptions{})
	if err != nil {
		return err
	}
	if signal != nil && signal.Err() != nil {
		if abortErr := b.sessions.Abort(next); abortErr != nil {
			return errors.Join(signal.Err(), abortErr)
		}
		return signal.Err()
	}
	return b.commitPrepared(signal, next, "forked session")
}

func sessionTransitionReason(notice string, next *activeSession) string {
	lowered := strings.ToLower(notice)
	if strings.Contains(lowered, "resum") {
		return string(sdk.SessionStartResume)
	}
	if strings.Contains(lowered, "fork") {
		return string(sdk.SessionStartFork)
	}
	if strings.Contains(lowered, "new") {
		return string(sdk.SessionStartNew)
	}
	if next != nil {
		switch next.mode {
		case sessionModeResume:
			return string(sdk.SessionStartResume)
		case sessionModeFork:
			return string(sdk.SessionStartFork)
		default:
			return string(sdk.SessionStartNew)
		}
	}
	return string(sdk.SessionStartNew)
}

func (b *tuiBridge) dispatchSessionLifecycle(signal context.Context, reason, previousPath, targetPath string) {
	if b.rd == nil || b.rd.hooks == nil {
		return
	}
	ctx := signal
	if ctx == nil {
		ctx = b.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	type starter interface {
		SessionStartWithFiles(context.Context, string, string) error
	}
	type stopper interface {
		SessionShutdownWithFiles(context.Context, string, string) error
	}
	if stopper, ok := b.rd.hooks.(stopper); ok {
		_ = stopper.SessionShutdownWithFiles(ctx, reason, targetPath)
	} else {
		_ = b.rd.hooks.SessionShutdown(ctx, reason)
	}
	if starter, ok := b.rd.hooks.(starter); ok {
		_ = starter.SessionStartWithFiles(ctx, reason, previousPath)
	} else {
		_ = b.rd.hooks.SessionStart(ctx, reason)
	}
}

func (b *tuiBridge) commitPrepared(signal context.Context, next *activeSession, notice string) error {
	if next == nil {
		return errors.New("session: nothing to activate")
	}
	if b.sessions == nil {
		return errors.New("session: session control is unavailable")
	}
	if signal != nil && signal.Err() != nil {
		if abortErr := b.sessions.Abort(next); abortErr != nil {
			return errors.Join(signal.Err(), abortErr)
		}
		return signal.Err()
	}
	previousPath := ""
	if current := b.sessions.Current(); current != nil {
		previousPath = current.path
	} else if b.rd != nil {
		previousPath = b.rd.sessionPath
	}
	targetPath := next.path
	reason := sessionTransitionReason(notice, next)
	if signal != nil && signal.Err() != nil {
		if abortErr := b.sessions.Abort(next); abortErr != nil {
			return errors.Join(signal.Err(), abortErr)
		}
		return signal.Err()
	}
	if err := b.activateSessionWithContext(signal, next, notice); err != nil {
		return err
	}
	hookSignal := signal
	if hookSignal == nil {
		hookSignal = b.ctx
	}
	b.dispatchSessionLifecycle(hookSignal, reason, previousPath, targetPath)
	b.syncCommandInventory()
	return nil
}

func (b *tuiBridge) activateSession(next *activeSession, notice string) error {
	return b.activateSessionWithContext(context.Background(), next, notice)
}

func (b *tuiBridge) activateSessionWithContext(signal context.Context, next *activeSession, notice string) error {
	if next == nil {
		return errors.New("session: nothing to activate")
	}
	if b.sessions == nil {
		return errors.New("session: session control is unavailable")
	}
	b.sessions.SetApplier(b.applyActiveSession)
	b.pendingNotice = notice
	return b.sessions.CommitWithContext(signal, next)
}

type sessionDisplayState struct {
	recorder    agent.Recorder
	sess        *session.Session
	sessionPath string
	preparer    *contextPreparerAdapter
	detector    agent.LoopDetector
	model       string
	wireModel   string
	history     []*agent.Message
	entryIDs    []string
}

func (b *tuiBridge) captureSessionDisplayState() sessionDisplayState {
	state := sessionDisplayState{history: b.history, entryIDs: b.entryIDs}
	if b.rd != nil {
		state.recorder = b.rd.recorder
		state.sess = b.rd.sess
		state.sessionPath = b.rd.sessionPath
		state.preparer = b.rd.preparer
		state.detector = b.rd.detector
		state.model = b.rd.model
		state.wireModel = b.rd.wireModel
	}
	return state
}

func (b *tuiBridge) installSessionDisplayState(next *activeSession) {
	if b.rd != nil {
		b.rd.recorder = next.recorder
		b.rd.sess = next.sess
		b.rd.sessionPath = next.path
		b.rd.preparer = next.preparer
		b.rd.detector = next.detector
		b.rd.model = next.model
		b.rd.wireModel = next.wireModel
	}
	b.history = next.history
	b.entryIDs = next.entryIDs
}

func (b *tuiBridge) restoreSessionDisplayState(state sessionDisplayState) {
	if b.rd != nil {
		b.rd.recorder = state.recorder
		b.rd.sess = state.sess
		b.rd.sessionPath = state.sessionPath
		b.rd.preparer = state.preparer
		b.rd.detector = state.detector
		b.rd.model = state.model
		b.rd.wireModel = state.wireModel
	}
	b.history = state.history
	b.entryIDs = state.entryIDs
}

func (b *tuiBridge) applyActiveSession(previous, next *activeSession) error {
	if b.runner == nil || b.runner.Surface() == nil {
		return errors.New("session: no display is available for the session")
	}
	notice := b.pendingNotice
	b.pendingNotice = ""
	state := b.captureSessionDisplayState()
	b.installSessionDisplayState(next)
	if b.applyStep != nil {
		if err := b.applyStep(next); err != nil {
			b.restoreSessionDisplayState(state)
			return err
		}
	}
	dropped := b.runner.Surface().Editor().ClearQueued()
	b.replaySession(next)
	if dropped > 0 {
		b.runner.Surface().AddNotice(interactive.NoticeWarning, fmt.Sprintf("dropped %d queued follow-up message(s) on session switch", dropped))
	}
	if notice != "" {
		b.runner.Surface().AddNotice(interactive.NoticeInfo, notice)
	}
	return nil
}

func (b *tuiBridge) replaySession(active *activeSession) {
	if active == nil || b.runner == nil {
		return
	}
	surface := b.runner.Surface()
	surface.ReplaceTranscript(active.transcript)
	for _, warning := range active.warnings {
		surface.AddNotice(interactive.NoticeWarning, warning)
	}
	if strings.TrimSpace(active.name) != "" {
		surface.SetSessionName(active.name)
	} else if strings.TrimSpace(active.path) != "" {
		surface.SetSessionName(active.path)
	}
	b.syncCommandInventory()
}

func (b *tuiBridge) showTree() error {
	if b.sessions == nil {
		return errors.New("tree: session control is unavailable")
	}
	current := b.sessions.Current()
	if current == nil {
		return errors.New("tree: no active session")
	}
	loader := current.sessLoader()
	if loader == nil {
		b.runner.Surface().AddNotice(interactive.NoticeInfo, "the session has no entries yet")
		return nil
	}
	nodes, warnings := buildTreeNodes(loader, false, false)
	for _, warning := range warnings {
		b.runner.Surface().AddNotice(interactive.NoticeWarning, warning)
	}
	activeEntryID := ""
	if leaf := loader.Leaf(); leaf != nil {
		activeEntryID = session.EntryID(leaf)
	}
	filter := interactive.TreeFilterDefault
	hideTimestamps := false
	selected := activeEntryID
	for {
		fresh := current.sessLoader()
		if fresh != nil {
			loader = fresh
			rebuilt, _ := buildTreeNodes(loader, false, false)
			nodes = rebuilt
		}
		result, err := b.runner.ShowTreeBrowser(b.ctx, interactive.TreeBrowserOptions{
			Title:          "Session tree",
			Nodes:          nodes,
			ActiveEntryID:  selected,
			Filter:         filter,
			HideTimestamps: hideTimestamps,
		})
		if err != nil {
			return err
		}
		if result.Filter != "" {
			filter = result.Filter
		}
		hideTimestamps = result.HideTimestamps
		if result.EntryID != "" {
			selected = result.EntryID
		}
		switch result.Action {
		case interactive.TreeActionClose, "":
			return nil
		case interactive.TreeActionLabel:
			if err := b.editTreeLabel(current, result.EntryID, result.Node.Label); err != nil {
				b.warn(err)
			}
			continue
		default:
			return nil
		}
	}
}

func (b *tuiBridge) editTreeLabel(current *activeSession, targetID, currentLabel string) error {
	if strings.TrimSpace(targetID) == "" {
		return errors.New("label: no entry selected")
	}
	if current == nil || current.sess == nil {
		return errors.New("label: no active session")
	}
	value, err := b.runner.Input("Label entry", currentLabel)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == strings.TrimSpace(currentLabel) && trimmed != "" {
		return nil
	}
	if trimmed == "" {
		return current.sess.AppendEntry(&session.LabelEntry{TargetID: targetID})
	}
	label := trimmed
	return current.sess.AppendEntry(&session.LabelEntry{TargetID: targetID, Label: &label})
}

func (b *tuiBridge) sessionsBrowser() error {
	if b.sessions == nil || b.rd == nil || b.rd.store == nil {
		return errors.New("sessions: session control is unavailable")
	}
	for {
		nodes, err := b.sessionBrowserNodes()
		if err != nil {
			return err
		}
		if len(nodes) == 0 {
			b.runner.Surface().AddNotice(interactive.NoticeInfo, "no sessions for this project")
			return nil
		}
		activePath := b.rd.sessionPath
		result, err := b.runner.ShowTreeBrowser(b.ctx, interactive.TreeBrowserOptions{
			Title:  "Sessions",
			Nodes:  nodes,
			Filter: interactive.TreeFilterAll,
		})
		if err != nil {
			return err
		}
		switch result.Action {
		case interactive.TreeActionClose, "":
			return nil
		case interactive.TreeActionDelete:
			if err := b.deleteSessionInteractive(result.EntryID); err != nil {
				b.warn(err)
			}
		case interactive.TreeActionRename:
			if err := b.renameSessionInteractive(result.EntryID); err != nil {
				b.warn(err)
			}
		case interactive.TreeActionSelect:
			outcome, err := b.resumeBrowserSelection(result.EntryID, activePath)
			if outcome == browserResumeActivated {
				return err
			}
			if err != nil {
				b.warn(err)
				continue
			}
			if outcome == browserResumeAlreadyActive {
				b.runner.Surface().AddNotice(interactive.NoticeInfo, "that session is already active")
			}
			continue
		}
	}
}

type browserResumeOutcome int

const (
	browserResumeRefused browserResumeOutcome = iota
	browserResumeAlreadyActive
	browserResumeActivated
)

func (b *tuiBridge) resumeBrowserSelection(entryID, activePath string) (browserResumeOutcome, error) {
	resolved, err := b.resolveSessionTarget(entryID)
	if err != nil {
		return browserResumeRefused, err
	}
	if sameFilePath(resolved, activePath) {
		return browserResumeAlreadyActive, nil
	}
	next, err := b.sessions.PrepareOpen(resolved)
	if err != nil {
		return browserResumeRefused, err
	}
	return browserResumeActivated, b.commitPrepared(b.ctx, next, "resumed "+filepath.Base(next.path))
}

func (b *tuiBridge) sessionBrowserNodes() ([]interactive.TreeBrowserNode, error) {
	candidates, err := validatedSessionCandidates(b.rd.store, b.rd.cwd)
	if err != nil {
		return nil, err
	}
	activePath := b.rd.sessionPath
	nodes := make([]interactive.TreeBrowserNode, 0, len(candidates))
	for _, candidate := range candidates {
		label := candidate.name
		if label == "" {
			label = filepath.Base(candidate.path)
		}
		isActive := sameFilePath(candidate.path, activePath)
		nodes = append(nodes, interactive.TreeBrowserNode{
			ID:        candidate.path,
			Kind:      "session",
			Label:     label,
			Timestamp: candidate.timestamp,
			Preview:   candidate.path,
			Leaf:      isActive,
			Deletable: !isActive,
			Renamable: true,
		})
	}
	return nodes, nil
}

func (b *tuiBridge) renameSessionInteractive(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("rename: no session selected")
	}
	current := b.sessions.Current()
	if current != nil && sameFilePath(path, current.path) {
		name, err := b.runner.Input("Rename session", "")
		if err != nil {
			return err
		}
		return b.renameActiveSession(strings.TrimSpace(name))
	}
	if _, err := b.resolveSessionTarget(path); err != nil {
		return err
	}
	name, err := b.runner.Input("Rename session", "")
	if err != nil {
		return err
	}
	resolved, err := b.resolveSessionTarget(path)
	if err != nil {
		return err
	}
	return b.renameStoredSession(resolved, strings.TrimSpace(name))
}

func (b *tuiBridge) renameActiveSession(name string) error {
	if name == "" {
		return nil
	}
	current := b.sessions.Current()
	if current == nil || current.sess == nil {
		return errors.New("rename: no active session")
	}
	if err := current.sess.AppendEntry(&session.SessionInfoEntry{Name: &name}); err != nil {
		return err
	}
	current.name = name
	b.runner.Surface().SetSessionName(name)
	b.runner.Surface().AddNotice(interactive.NoticeInfo, "session renamed to "+name)
	return nil
}

func (b *tuiBridge) renameStoredSession(path, name string) error {
	if name == "" {
		return nil
	}
	if sameFilePath(path, b.rd.sessionPath) {
		return b.renameActiveSession(name)
	}
	held, err := b.rd.store.Open(path, session.OpenOptions{Strict: true})
	if err != nil {
		return err
	}
	defer held.Close()
	if err := held.AppendEntry(&session.SessionInfoEntry{Name: &name}); err != nil {
		return err
	}
	b.runner.Surface().AddNotice(interactive.NoticeInfo, "session renamed to "+name)
	return nil
}

func (b *tuiBridge) deleteSessionInteractive(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("delete: no session selected")
	}
	if sameFilePath(path, b.rd.sessionPath) {
		return errDeleteActive
	}
	resolved, err := b.resolveSessionTarget(path)
	if err != nil {
		return err
	}
	candidate, err := validateDeleteCandidate(b.rd.store, b.rd.cwd, resolved, b.rd.sessionPath)
	if err != nil {
		return err
	}
	defer candidate.Close()
	ok, err := b.runner.Confirm("Delete session", "Permanently delete "+filepath.Base(candidate.path)+"? This cannot be undone.")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := deleteSessionFile(b.rd.store, candidate); err != nil {
		return err
	}
	b.runner.Surface().AddNotice(interactive.NoticeInfo, "deleted session "+filepath.Base(candidate.path))
	return nil
}
