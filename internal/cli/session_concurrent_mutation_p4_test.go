package cli

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

type p4MutationEvent struct {
	kind   string
	reason string
	path   string
}

type p4Tracker struct {
	current atomic.Int32
	max     atomic.Int32
}

func (t *p4Tracker) enter() {
	c := t.current.Add(1)
	for {
		m := t.max.Load()
		if c <= m {
			break
		}
		if t.max.CompareAndSwap(m, c) {
			break
		}
	}
}

func (t *p4Tracker) exit() {
	t.current.Add(-1)
}

func (t *p4Tracker) maximum() int {
	return int(t.max.Load())
}

type p4MutationHooks struct {
	mu              sync.Mutex
	events          []p4MutationEvent
	shutdownEntered chan struct{}
	shutdownRelease chan struct{}
	startEntered    chan struct{}
	startRelease    chan struct{}
	shutdownOnce    sync.Once
	startOnce       sync.Once
	tracker         *p4Tracker
}

func (h *p4MutationHooks) record(kind, reason, path string) {
	h.mu.Lock()
	h.events = append(h.events, p4MutationEvent{kind: kind, reason: reason, path: path})
	h.mu.Unlock()
}

func (h *p4MutationHooks) snapshot() []p4MutationEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]p4MutationEvent(nil), h.events...)
}

func (h *p4MutationHooks) Context(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	return agent.ContextResult{Messages: req.Messages, System: req.System}, nil
}

func (h *p4MutationHooks) MessageEnd(ctx context.Context, m *agent.Message) (*agent.Message, error) {
	return m, nil
}

func (h *p4MutationHooks) AutoRetryStart(ctx context.Context, attempt int, maxAttempts int, delayMs int64, errorMessage string) error {
	return nil
}

func (h *p4MutationHooks) AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error {
	return nil
}

func (h *p4MutationHooks) ToolCall(ctx context.Context, name string, callID string, args json.RawMessage) (agent.ToolCallDecision, error) {
	return agent.ToolCallDecision{FinalArgs: args}, nil
}

func (h *p4MutationHooks) ToolResult(ctx context.Context, name string, callID string, args json.RawMessage, res agent.Result) (agent.Result, error) {
	return res, nil
}

func (h *p4MutationHooks) SessionStart(ctx context.Context, reason string) error {
	h.record("start", reason, "")
	return nil
}

func (h *p4MutationHooks) SessionShutdown(ctx context.Context, reason string) error {
	h.record("shutdown", reason, "")
	return nil
}

func (h *p4MutationHooks) SessionStartWithFiles(ctx context.Context, reason, previousPath string) error {
	if h.tracker != nil {
		h.tracker.enter()
		defer h.tracker.exit()
	}
	h.record("start", reason, previousPath)
	if h.startEntered != nil {
		h.startOnce.Do(func() { close(h.startEntered) })
	}
	if h.startRelease != nil {
		<-h.startRelease
	}
	return nil
}

func (h *p4MutationHooks) SessionShutdownWithFiles(ctx context.Context, reason, targetPath string) error {
	if h.tracker != nil {
		h.tracker.enter()
		defer h.tracker.exit()
	}
	h.record("shutdown", reason, targetPath)
	if h.shutdownEntered != nil {
		h.shutdownOnce.Do(func() { close(h.shutdownEntered) })
	}
	if h.shutdownRelease != nil {
		<-h.shutdownRelease
	}
	return nil
}

func p4InstallTrackingPreparer(fixture *bridgeFixture, tracker *p4Tracker) {
	fixture.bridge.sessions.SetPreparer(func(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
		if tracker != nil {
			tracker.enter()
			defer tracker.exit()
		}
		return testSessionPreparer(sess, mode)
	})
}

func p4WaitTurns(t *testing.T, turns chan struct{}, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case <-turns:
		case <-time.After(5 * time.Second):
			t.Fatal("queued turn did not finish")
		}
	}
}

func p4WaitError(t *testing.T, ch chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("mutation did not finish")
		return nil
	}
}

func TestP4ConcurrentNewSwitchSingleInvocation(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	origin := fixture.bridge.rd.sessionPath
	target := seedStoredSession(t, fixture, "p4 newswitch target")
	before := countJSONL(t, fixture.store.Root())
	tracker := &p4Tracker{}
	hooks := &p4MutationHooks{
		shutdownEntered: make(chan struct{}),
		shutdownRelease: make(chan struct{}),
		tracker:         tracker,
	}
	fixture.bridge.rd.hooks = hooks
	p4InstallTrackingPreparer(fixture, tracker)
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	fixture.bridge.ctx = parent
	newErrCh := make(chan error, 1)
	switchErrCh := make(chan error, 1)
	if _, err := fixture.commands.Register("xp4newswitch", sdk.Command{
		Description: "p4 newswitch race",
		Handler: func(ctx sdk.CommandContext, args string) error {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, err := ctx.NewSession(sdk.NewSessionOptions{})
				newErrCh <- err
			}()
			select {
			case <-hooks.shutdownEntered:
			case <-time.After(5 * time.Second):
				wg.Done()
				wg.Done()
				return errors.New("first new did not enter shutdown")
			}
			time.Sleep(100 * time.Millisecond)
			go func() {
				defer wg.Done()
				_, err := ctx.SwitchSession(target, sdk.SwitchOptions{})
				switchErrCh <- err
			}()
			time.Sleep(100 * time.Millisecond)
			wg.Wait()
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	markPathCh := make(chan string, 1)
	markDone := make(chan struct{}, 1)
	if _, err := fixture.commands.Register("p4marknewswitch", sdk.Command{
		Description: "p4 mark",
		Handler: func(ctx sdk.CommandContext, args string) error {
			markPathCh <- fixture.bridge.rd.sessionPath
			markDone <- struct{}{}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	turns := make(chan struct{}, 8)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("/xp4newswitch")
	select {
	case <-hooks.shutdownEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first new did not block in shutdown hook")
	}
	fixture.bridge.submit("/p4marknewswitch second")
	select {
	case <-markDone:
		t.Fatal("next turn overtook the blocked mutation")
	case <-time.After(200 * time.Millisecond):
	}
	cancelParent()
	close(hooks.shutdownRelease)
	newErr := p4WaitError(t, newErrCh)
	switchErr := p4WaitError(t, switchErrCh)
	p4WaitTurns(t, turns, 2)
	if newErr != nil {
		t.Fatalf("first new err = %v, want nil", newErr)
	}
	if !errors.Is(switchErr, context.Canceled) {
		t.Fatalf("second switch err = %v, want canceled", switchErr)
	}
	finalPath := fixture.bridge.rd.sessionPath
	if finalPath == origin || finalPath == target {
		t.Fatalf("final path %q must be the new session, not origin or switch target", finalPath)
	}
	if after := countJSONL(t, fixture.store.Root()); after != before+1 {
		t.Fatalf("session files = %d, want %d", after, before+1)
	}
	events := hooks.snapshot()
	if len(events) != 2 {
		t.Fatalf("hook events = %d, want shutdown plus start", len(events))
	}
	if events[0].kind != "shutdown" || events[1].kind != "start" {
		t.Fatalf("hook ordering = %+v, want shutdown before start", events)
	}
	if events[0].reason != "new" || events[1].reason != "new" {
		t.Fatalf("hook reasons = %q %q, want new new", events[0].reason, events[1].reason)
	}
	if events[0].path != finalPath {
		t.Fatalf("shutdown target = %q, want %q", events[0].path, finalPath)
	}
	if events[1].path != origin {
		t.Fatalf("start previous = %q, want %q", events[1].path, origin)
	}
	if tracker.maximum() != 1 {
		t.Fatalf("concurrent transactions = %d, want 1", tracker.maximum())
	}
	select {
	case got := <-markPathCh:
		if got != finalPath {
			t.Fatalf("next turn path = %q, want final %q", got, finalPath)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("next turn did not observe the final session")
	}
	current := fixture.bridge.sessions.Current()
	if current == nil || current.path != finalPath {
		t.Fatal("controller must publish the first committed session")
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestP4ConcurrentTwoForksSingleInvocation(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	seedTurn(t, fixture.sess, "p4 two forks origin")
	origin := fixture.bridge.rd.sessionPath
	before := countJSONL(t, fixture.store.Root())
	tracker := &p4Tracker{}
	hooks := &p4MutationHooks{
		shutdownEntered: make(chan struct{}),
		shutdownRelease: make(chan struct{}),
		tracker:         tracker,
	}
	fixture.bridge.rd.hooks = hooks
	p4InstallTrackingPreparer(fixture, tracker)
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	fixture.bridge.ctx = parent
	firstErrCh := make(chan error, 1)
	secondErrCh := make(chan error, 1)
	if _, err := fixture.commands.Register("xp4twoforks", sdk.Command{
		Description: "p4 two forks race",
		Handler: func(ctx sdk.CommandContext, args string) error {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, err := ctx.Fork("", sdk.ForkOptions{})
				firstErrCh <- err
			}()
			select {
			case <-hooks.shutdownEntered:
			case <-time.After(5 * time.Second):
				wg.Done()
				wg.Done()
				return errors.New("first fork did not enter shutdown")
			}
			time.Sleep(100 * time.Millisecond)
			go func() {
				defer wg.Done()
				_, err := ctx.Fork("", sdk.ForkOptions{})
				secondErrCh <- err
			}()
			time.Sleep(100 * time.Millisecond)
			wg.Wait()
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	markPathCh := make(chan string, 1)
	markDone := make(chan struct{}, 1)
	if _, err := fixture.commands.Register("p4marktwoforks", sdk.Command{
		Description: "p4 mark forks",
		Handler: func(ctx sdk.CommandContext, args string) error {
			markPathCh <- fixture.bridge.rd.sessionPath
			markDone <- struct{}{}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	turns := make(chan struct{}, 8)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("/xp4twoforks")
	select {
	case <-hooks.shutdownEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first fork did not block in shutdown hook")
	}
	fixture.bridge.submit("/p4marktwoforks second")
	select {
	case <-markDone:
		t.Fatal("next turn overtook the blocked fork")
	case <-time.After(200 * time.Millisecond):
	}
	cancelParent()
	close(hooks.shutdownRelease)
	firstErr := p4WaitError(t, firstErrCh)
	secondErr := p4WaitError(t, secondErrCh)
	p4WaitTurns(t, turns, 2)
	if firstErr != nil {
		t.Fatalf("first fork err = %v, want nil", firstErr)
	}
	if !errors.Is(secondErr, context.Canceled) {
		t.Fatalf("second fork err = %v, want canceled", secondErr)
	}
	finalPath := fixture.bridge.rd.sessionPath
	if finalPath == origin {
		t.Fatal("forked session must use a distinct path")
	}
	if after := countJSONL(t, fixture.store.Root()); after != before+1 {
		t.Fatalf("session files = %d, want %d", after, before+1)
	}
	assertFileContains(t, finalPath, "p4 two forks origin", true)
	events := hooks.snapshot()
	if len(events) != 2 {
		t.Fatalf("hook events = %d, want 2", len(events))
	}
	if events[0].reason != "fork" || events[1].reason != "fork" {
		t.Fatalf("hook reasons = %q %q, want fork fork", events[0].reason, events[1].reason)
	}
	if events[0].path != finalPath {
		t.Fatalf("shutdown target = %q, want %q", events[0].path, finalPath)
	}
	if events[1].path != origin {
		t.Fatalf("start previous = %q, want %q", events[1].path, origin)
	}
	if tracker.maximum() != 1 {
		t.Fatalf("concurrent transactions = %d, want 1", tracker.maximum())
	}
	select {
	case got := <-markPathCh:
		if got != finalPath {
			t.Fatalf("next turn path = %q, want final %q", got, finalPath)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("next turn did not observe the final fork")
	}
	current := fixture.bridge.sessions.Current()
	if current == nil || current.path != finalPath {
		t.Fatal("controller must publish the first fork")
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestP4ConcurrentCancelDuringCommitBlocked(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	seedTurn(t, fixture.sess, "p4 commit blocked origin")
	origin := fixture.bridge.rd.sessionPath
	target := seedStoredSession(t, fixture, "p4 commit blocked switch target")
	before := countJSONL(t, fixture.store.Root())
	tracker := &p4Tracker{}
	hooks := &p4MutationHooks{tracker: tracker}
	fixture.bridge.rd.hooks = hooks
	p4InstallTrackingPreparer(fixture, tracker)
	commitEntered := make(chan struct{})
	commitRelease := make(chan struct{})
	var commitOnce sync.Once
	fixture.bridge.applyStep = func(next *activeSession) error {
		if tracker != nil {
			tracker.enter()
			defer tracker.exit()
		}
		commitOnce.Do(func() { close(commitEntered) })
		<-commitRelease
		return nil
	}
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	fixture.bridge.ctx = parent
	forkErrCh := make(chan error, 1)
	switchErrCh := make(chan error, 1)
	if _, err := fixture.commands.Register("xp4commitblocked", sdk.Command{
		Description: "p4 commit blocked race",
		Handler: func(ctx sdk.CommandContext, args string) error {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, err := ctx.Fork("", sdk.ForkOptions{})
				forkErrCh <- err
			}()
			select {
			case <-commitEntered:
			case <-time.After(5 * time.Second):
				wg.Done()
				wg.Done()
				return errors.New("first fork did not enter commit")
			}
			time.Sleep(100 * time.Millisecond)
			go func() {
				defer wg.Done()
				_, err := ctx.SwitchSession(target, sdk.SwitchOptions{})
				switchErrCh <- err
			}()
			time.Sleep(100 * time.Millisecond)
			wg.Wait()
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	markPathCh := make(chan string, 1)
	markDone := make(chan struct{}, 1)
	if _, err := fixture.commands.Register("p4markcommitblocked", sdk.Command{
		Description: "p4 mark commit",
		Handler: func(ctx sdk.CommandContext, args string) error {
			markPathCh <- fixture.bridge.rd.sessionPath
			markDone <- struct{}{}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	turns := make(chan struct{}, 8)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("/xp4commitblocked")
	select {
	case <-commitEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first fork did not block in commit")
	}
	fixture.bridge.submit("/p4markcommitblocked second")
	select {
	case <-markDone:
		t.Fatal("next turn overtook the blocked commit")
	case <-time.After(200 * time.Millisecond):
	}
	cancelParent()
	close(commitRelease)
	forkErr := p4WaitError(t, forkErrCh)
	switchErr := p4WaitError(t, switchErrCh)
	p4WaitTurns(t, turns, 2)
	if forkErr != nil {
		t.Fatalf("first fork err = %v, want nil", forkErr)
	}
	if !errors.Is(switchErr, context.Canceled) {
		t.Fatalf("second switch err = %v, want canceled", switchErr)
	}
	finalPath := fixture.bridge.rd.sessionPath
	if finalPath == origin || finalPath == target {
		t.Fatalf("final path %q must be the fork, not origin or switch target", finalPath)
	}
	if after := countJSONL(t, fixture.store.Root()); after != before+1 {
		t.Fatalf("session files = %d, want %d", after, before+1)
	}
	events := hooks.snapshot()
	if len(events) != 2 {
		t.Fatalf("hook events = %d, want 2", len(events))
	}
	if events[0].reason != "fork" || events[1].reason != "fork" {
		t.Fatalf("hook reasons = %q %q, want fork fork", events[0].reason, events[1].reason)
	}
	if events[0].path != finalPath {
		t.Fatalf("shutdown target = %q, want %q", events[0].path, finalPath)
	}
	if events[1].path != origin {
		t.Fatalf("start previous = %q, want %q", events[1].path, origin)
	}
	if tracker.maximum() != 1 {
		t.Fatalf("concurrent transactions = %d, want 1", tracker.maximum())
	}
	select {
	case got := <-markPathCh:
		if got != finalPath {
			t.Fatalf("next turn path = %q, want final %q", got, finalPath)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("next turn did not observe the final fork")
	}
	current := fixture.bridge.sessions.Current()
	if current == nil || current.path != finalPath {
		t.Fatal("controller must publish the committed fork")
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestP4ControllerCommitChecksOperationContext(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	seedTurn(t, source, "p4 commit context source")
	controller := testController(t, store, cwd)
	defer controller.Close()
	activeSource, err := controller.Adopt(source, sessionModeResume)
	if err != nil {
		t.Fatal(err)
	}
	before := countJSONL(t, store.Root())
	candidate, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := controller.CommitWithContext(canceled, candidate); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled CommitWithContext = %v, want canceled", err)
	}
	if controller.Current() != activeSource {
		t.Fatal("canceled commit must leave the current session intact")
	}
	if after := countJSONL(t, store.Root()); after != before {
		t.Fatalf("canceled commit files = %d, want %d", after, before)
	}
	fresh, err := controller.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.CommitWithContext(context.Background(), fresh); err != nil {
		t.Fatalf("live CommitWithContext = %v", err)
	}
	if controller.Current() != fresh {
		t.Fatal("live commit must publish the candidate")
	}
	if err := controller.CommitWithContext(context.Background(), nil); err == nil {
		t.Fatal("nil commit must fail")
	}
	closedController := newSessionController(store, t.TempDir())
	if err := closedController.Close(); err != nil {
		t.Fatal(err)
	}
	stale, err := testSessionPreparer(source, sessionModeResume)
	if err != nil {
		t.Fatal(err)
	}
	stale.origin = candidateOpened
	if err := closedController.CommitWithContext(context.Background(), stale); err == nil {
		t.Fatal("commit after close must fail")
	}
	if err := closedController.CommitWithContext(canceled, stale); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled commit after close = %v, want canceled", err)
	}
}

func TestP4BeginMutationFIFOThenSucceeds(t *testing.T) {
	invocation := newCommandInvocation(context.Background())
	defer invocation.invalidate()
	opFirst, doneFirst, err := invocation.beginMutation(context.Background())
	if err != nil {
		t.Fatalf("first beginMutation = %v", err)
	}
	if opFirst == nil {
		t.Fatal("first operation context is nil")
	}
	secondCh := make(chan error, 1)
	var opSecond context.Context
	go func() {
		op, done, err := invocation.beginMutation(context.Background())
		if err != nil {
			secondCh <- err
			return
		}
		opSecond = op
		done()
		secondCh <- nil
	}()
	select {
	case err := <-secondCh:
		t.Fatalf("second overtook the first mutation: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	doneFirst()
	select {
	case err := <-secondCh:
		if err != nil {
			t.Fatalf("second beginMutation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second did not acquire after first released")
	}
	if opSecond == nil {
		t.Fatal("second operation context is nil")
	}
}

func TestP4BeginMutationCancelSignalWhileWaiting(t *testing.T) {
	invocation := newCommandInvocation(context.Background())
	defer invocation.invalidate()
	_, doneFirst, err := invocation.beginMutation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	signal, cancelSignal := context.WithCancel(context.Background())
	waitCh := make(chan error, 1)
	go func() {
		_, _, err := invocation.beginMutation(signal)
		waitCh <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancelSignal()
	select {
	case err := <-waitCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting mutation err = %v, want canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiting mutation did not observe signal cancel")
	}
	doneFirst()
}

func TestP4BeginMutationNilMutChFallback(t *testing.T) {
	invocation := &commandInvocation{mutCh: nil}
	invocation.active.Store(true)
	invocation.opCtx = context.Background()
	op, done, err := invocation.beginMutation(context.Background())
	if err != nil {
		t.Fatalf("nil channel beginMutation = %v", err)
	}
	if op == nil {
		t.Fatal("nil channel operation context is nil")
	}
	done()
	invocation.invalidate()
	if _, _, err := invocation.beginMutation(context.Background()); err != sdk.ErrModeUnsupported {
		t.Fatalf("post-invalidate beginMutation = %v, want unsupported", err)
	}
}

func TestP4ActivateSessionWithContextGuards(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	if err := fixture.bridge.activateSessionWithContext(context.Background(), nil, "notice"); err == nil {
		t.Fatal("nil activate must fail")
	}
	bare := newBridgeFixture(t, nil, nil)
	if err := bare.bridge.activateSessionWithContext(context.Background(), &activeSession{path: "next"}, "notice"); err == nil {
		t.Fatal("activate without controller must fail")
	}
	candidate, err := fixture.bridge.sessions.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	origin := fixture.bridge.rd.sessionPath
	if err := fixture.bridge.activateSessionWithContext(canceled, candidate, "started a new session"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled activate = %v, want canceled", err)
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("canceled activate changed path to %s", fixture.bridge.rd.sessionPath)
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestP4BeginMutationPostAcquireRechecks(t *testing.T) {
	for i := 0; i < 100; i++ {
		invocation := newCommandInvocation(context.Background())
		_, doneFirst, err := invocation.beginMutation(context.Background())
		if err != nil {
			t.Fatalf("iteration %d first = %v", i, err)
		}
		signal, cancelSignal := context.WithCancel(context.Background())
		resultCh := make(chan error, 1)
		doneCh := make(chan func(), 1)
		go func() {
			_, done, err := invocation.beginMutation(signal)
			if err != nil {
				resultCh <- err
				return
			}
			doneCh <- done
			resultCh <- nil
		}()
		time.Sleep(5 * time.Millisecond)
		if i%3 == 0 {
			cancelSignal()
			doneFirst()
		} else if i%3 == 1 {
			invocation.active.Store(false)
			if invocation.opCancel != nil {
				invocation.opCancel()
			}
			doneFirst()
		} else {
			doneFirst()
			cancelSignal()
		}
		select {
		case err := <-resultCh:
			if err == nil {
				select {
				case done := <-doneCh:
					done()
				default:
				}
			} else if !errors.Is(err, context.Canceled) && err != sdk.ErrModeUnsupported {
				t.Fatalf("iteration %d err = %v", i, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d did not finish", i)
		}
		cancelSignal()
		select {
		case done := <-doneCh:
			done()
		default:
		}
		invocation.active.Store(false)
		if invocation.opCancel != nil {
			invocation.opCancel()
		}
		invocation.wg.Wait()
	}
}
