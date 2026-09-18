package cli

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

func TestInvocationBeginMutationGuards(t *testing.T) {
	var nilInvocation *commandInvocation
	nilInvocation.invalidate()
	if _, _, err := nilInvocation.beginMutation(context.Background()); err != sdk.ErrModeUnsupported {
		t.Fatalf("nil beginMutation = %v, want unsupported", err)
	}
	invocation := newCommandInvocation(context.Background())
	opCtx, done, err := invocation.beginMutation(context.Background())
	if err != nil {
		t.Fatalf("beginMutation = %v", err)
	}
	if opCtx == nil {
		t.Fatal("beginMutation returned no operation context")
	}
	done()
	invocation.invalidate()
	if _, _, err := invocation.beginMutation(context.Background()); err != sdk.ErrModeUnsupported {
		t.Fatalf("post-invalidate beginMutation = %v, want unsupported", err)
	}
	canceledParent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	canceledInvocation := newCommandInvocation(canceledParent)
	if _, _, err := canceledInvocation.beginMutation(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled parent beginMutation = %v, want canceled", err)
	}
	canceledInvocation.invalidate()
	live := newCommandInvocation(context.Background())
	canceledSignal, cancelSignal := context.WithCancel(context.Background())
	cancelSignal()
	if _, _, err := live.beginMutation(canceledSignal); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled signal beginMutation = %v, want canceled", err)
	}
	live.invalidate()
	nilParent := newCommandInvocation(nil)
	opCtx, done, err = nilParent.beginMutation(nil)
	if err != nil {
		t.Fatalf("nil parent beginMutation = %v", err)
	}
	if opCtx == nil {
		t.Fatal("nil parent operation context is nil")
	}
	done()
	nilParent.invalidate()
	if err := nilParent.check(context.Background()); err != sdk.ErrModeUnsupported {
		t.Fatalf("post-invalidate check = %v, want unsupported", err)
	}
}

func TestInvocationLifetimeSyncSuccess(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	seedTurn(t, fixture.sess, "lifetime sync origin")
	origin := fixture.bridge.rd.sessionPath
	before := countJSONL(t, fixture.store.Root())
	if _, err := fixture.commands.Register("xsyncnew", sdk.Command{
		Description: "sync new",
		Handler: func(ctx sdk.CommandContext, args string) error {
			_, err := ctx.NewSession(sdk.NewSessionOptions{})
			return err
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/xsyncnew")
	afterNew := fixture.bridge.rd.sessionPath
	if afterNew == origin {
		t.Fatal("sync new must activate a distinct session")
	}
	if got := countJSONL(t, fixture.store.Root()); got != before+1 {
		t.Fatalf("session files after sync new = %d, want %d", got, before+1)
	}
	target := seedStoredSession(t, fixture, "lifetime sync target")
	if _, err := fixture.commands.Register("xsyncswitch", sdk.Command{
		Description: "sync switch",
		Handler: func(ctx sdk.CommandContext, args string) error {
			_, err := ctx.SwitchSession(target, sdk.SwitchOptions{})
			return err
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/xsyncswitch")
	if fixture.bridge.rd.sessionPath != target {
		t.Fatalf("sync switch path = %s, want %s", fixture.bridge.rd.sessionPath, target)
	}
	if _, err := fixture.commands.Register("xsyncfork", sdk.Command{
		Description: "sync fork",
		Handler: func(ctx sdk.CommandContext, args string) error {
			_, err := ctx.Fork("", sdk.ForkOptions{})
			return err
		},
	}); err != nil {
		t.Fatal(err)
	}
	forkedBefore := fixture.bridge.rd.sessionPath
	fixture.bridge.handle("/xsyncfork")
	if fixture.bridge.rd.sessionPath == forkedBefore {
		t.Fatal("sync fork must activate a distinct session")
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func lifetimeBlockingPreparer(fixture *bridgeFixture, entered, release chan struct{}) {
	var once sync.Once
	fixture.bridge.sessions.SetPreparer(func(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
		once.Do(func() { close(entered) })
		<-release
		return testSessionPreparer(sess, mode)
	})
}

func TestInvocationLifetimeNewAbortsPendingMutation(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	origin := fixture.bridge.rd.sessionPath
	originHistory := len(fixture.bridge.history)
	before := countJSONL(t, fixture.store.Root())
	entered := make(chan struct{})
	release := make(chan struct{})
	lifetimeBlockingPreparer(fixture, entered, release)
	mutationDone := make(chan struct{})
	var mutationErr error
	if _, err := fixture.commands.Register("xracenew", sdk.Command{
		Description: "race new",
		Handler: func(ctx sdk.CommandContext, args string) error {
			go func() {
				defer close(mutationDone)
				_, mutationErr = ctx.NewSession(sdk.NewSessionOptions{})
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Error("mutation did not enter prepare")
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	nextStarted := make(chan struct{})
	var nextOnce sync.Once
	if _, err := fixture.commands.Register("marknew", sdk.Command{
		Description: "mark new",
		Handler: func(ctx sdk.CommandContext, args string) error {
			nextOnce.Do(func() { close(nextStarted) })
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	turns := make(chan struct{}, 8)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("/xracenew")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("prepare gate did not open")
	}
	fixture.bridge.submit("/marknew second")
	select {
	case <-nextStarted:
		t.Fatal("next prompt overtook the pending mutation")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case <-mutationDone:
	case <-time.After(5 * time.Second):
		t.Fatal("mutation did not exit after release")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-turns:
		case <-time.After(5 * time.Second):
			t.Fatal("queued turn did not finish")
		}
	}
	if mutationErr == nil {
		t.Fatal("expired new mutation must fail")
	}
	if !errors.Is(mutationErr, context.Canceled) {
		t.Fatalf("new mutation err = %v, want canceled", mutationErr)
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("expired new changed path to %s", fixture.bridge.rd.sessionPath)
	}
	if len(fixture.bridge.history) != originHistory {
		t.Fatal("expired new changed the bridge history")
	}
	current := fixture.bridge.sessions.Current()
	if current == nil || current.path != origin {
		t.Fatal("expired new must leave the current session intact")
	}
	if after := countJSONL(t, fixture.store.Root()); after != before {
		t.Fatalf("expired new left %d files, want %d", after, before)
	}
	select {
	case <-nextStarted:
	default:
		t.Fatal("next prompt did not execute after the mutation exited")
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestInvocationLifetimeSwitchAbortsPendingMutation(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	origin := fixture.bridge.rd.sessionPath
	before := countJSONL(t, fixture.store.Root())
	target := seedStoredSession(t, fixture, "lifetime switch target")
	entered := make(chan struct{})
	release := make(chan struct{})
	lifetimeBlockingPreparer(fixture, entered, release)
	mutationDone := make(chan struct{})
	var mutationErr error
	if _, err := fixture.commands.Register("xraceswitch", sdk.Command{
		Description: "race switch",
		Handler: func(ctx sdk.CommandContext, args string) error {
			go func() {
				defer close(mutationDone)
				_, mutationErr = ctx.SwitchSession(target, sdk.SwitchOptions{})
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Error("mutation did not enter prepare")
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	nextStarted := make(chan struct{})
	var nextOnce sync.Once
	if _, err := fixture.commands.Register("markswitch", sdk.Command{
		Description: "mark switch",
		Handler: func(ctx sdk.CommandContext, args string) error {
			nextOnce.Do(func() { close(nextStarted) })
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	turns := make(chan struct{}, 8)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("/xraceswitch")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("prepare gate did not open")
	}
	fixture.bridge.submit("/markswitch second")
	select {
	case <-nextStarted:
		t.Fatal("next prompt overtook the pending switch")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case <-mutationDone:
	case <-time.After(5 * time.Second):
		t.Fatal("switch mutation did not exit after release")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-turns:
		case <-time.After(5 * time.Second):
			t.Fatal("queued turn did not finish")
		}
	}
	if mutationErr == nil {
		t.Fatal("expired switch must fail")
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("expired switch changed path to %s", fixture.bridge.rd.sessionPath)
	}
	current := fixture.bridge.sessions.Current()
	if current == nil || current.path != origin {
		t.Fatal("expired switch must leave the current session intact")
	}
	if after := countJSONL(t, fixture.store.Root()); after != before+1 {
		t.Fatalf("expired switch files = %d, want %d", after, before+1)
	}
	held, err := fixture.store.Open(target, session.OpenOptions{})
	if err != nil {
		t.Fatalf("expired switch leaked the candidate lock: %v", err)
	}
	held.Close()
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestInvocationLifetimeForkAbortsPendingMutation(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	seedTurn(t, fixture.sess, "lifetime fork origin")
	origin := fixture.bridge.rd.sessionPath
	before := countJSONL(t, fixture.store.Root())
	entered := make(chan struct{})
	release := make(chan struct{})
	lifetimeBlockingPreparer(fixture, entered, release)
	mutationDone := make(chan struct{})
	var mutationErr error
	if _, err := fixture.commands.Register("xracefork", sdk.Command{
		Description: "race fork",
		Handler: func(ctx sdk.CommandContext, args string) error {
			go func() {
				defer close(mutationDone)
				_, mutationErr = ctx.Fork("", sdk.ForkOptions{})
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Error("mutation did not enter prepare")
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	nextStarted := make(chan struct{})
	var nextOnce sync.Once
	if _, err := fixture.commands.Register("markfork", sdk.Command{
		Description: "mark fork",
		Handler: func(ctx sdk.CommandContext, args string) error {
			nextOnce.Do(func() { close(nextStarted) })
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	turns := make(chan struct{}, 8)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("/xracefork")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("prepare gate did not open")
	}
	fixture.bridge.submit("/markfork second")
	select {
	case <-nextStarted:
		t.Fatal("next prompt overtook the pending fork")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case <-mutationDone:
	case <-time.After(5 * time.Second):
		t.Fatal("fork mutation did not exit after release")
	}
	for i := 0; i < 2; i++ {
		select {
		case <-turns:
		case <-time.After(5 * time.Second):
			t.Fatal("queued turn did not finish")
		}
	}
	if mutationErr == nil {
		t.Fatal("expired fork must fail")
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("expired fork changed path to %s", fixture.bridge.rd.sessionPath)
	}
	current := fixture.bridge.sessions.Current()
	if current == nil || current.path != origin {
		t.Fatal("expired fork must leave the current session intact")
	}
	if after := countJSONL(t, fixture.store.Root()); after != before {
		t.Fatalf("expired fork left %d files, want %d", after, before)
	}
	select {
	case <-nextStarted:
	default:
		t.Fatal("next prompt did not execute after the fork exited")
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestInvocationLifetimeParentCancelAborts(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	origin := fixture.bridge.rd.sessionPath
	before := countJSONL(t, fixture.store.Root())
	entered := make(chan struct{})
	release := make(chan struct{})
	fixture.bridge.sessions.SetPreparer(func(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
		close(entered)
		<-release
		return testSessionPreparer(sess, mode)
	})
	parent, cancelParent := context.WithCancel(context.Background())
	fixture.bridge.ctx = parent
	var mutationErr error
	if _, err := fixture.commands.Register("xparent", sdk.Command{
		Description: "parent cancel",
		Handler: func(ctx sdk.CommandContext, args string) error {
			_, mutationErr = ctx.NewSession(sdk.NewSessionOptions{})
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		fixture.bridge.handle("/xparent")
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("prepare did not start")
	}
	cancelParent()
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("parent-cancel command did not finish")
	}
	if !errors.Is(mutationErr, context.Canceled) {
		t.Fatalf("parent-cancel err = %v, want canceled", mutationErr)
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("parent-cancel changed path to %s", fixture.bridge.rd.sessionPath)
	}
	if after := countJSONL(t, fixture.store.Root()); after != before {
		t.Fatalf("parent-cancel left %d files, want %d", after, before)
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestInvocationLifetimePostShutdownRejected(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	before := countJSONL(t, fixture.store.Root())
	var retained sdk.CommandContext
	if _, err := fixture.commands.Register("capturelife", sdk.Command{
		Description: "capture life",
		Handler: func(ctx sdk.CommandContext, args string) error {
			retained = ctx
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/capturelife")
	if retained == nil {
		t.Fatal("capture did not run")
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
	if _, _, err := retained.(*tuiCommandContext).invocation.beginMutation(context.Background()); err != sdk.ErrModeUnsupported {
		t.Fatalf("post-shutdown beginMutation = %v, want unsupported", err)
	}
	if _, err := retained.NewSession(sdk.NewSessionOptions{}); err != sdk.ErrModeUnsupported {
		t.Fatalf("post-shutdown NewSession = %v, want unsupported", err)
	}
	if _, err := retained.Fork("", sdk.ForkOptions{}); err != sdk.ErrModeUnsupported {
		t.Fatalf("post-shutdown Fork = %v, want unsupported", err)
	}
	if _, err := retained.SwitchSession("any", sdk.SwitchOptions{}); err != sdk.ErrModeUnsupported {
		t.Fatalf("post-shutdown Switch = %v, want unsupported", err)
	}
	if after := countJSONL(t, fixture.store.Root()); after != before {
		t.Fatalf("post-shutdown created %d files", after-before)
	}
}

type flapSignal struct {
	context.Context
	mu    sync.Mutex
	calls int
}

func (f *flapSignal) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return nil
	}
	return context.Canceled
}

func TestInvocationLifetimeCommitPreparedExpiry(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	origin := fixture.bridge.rd.sessionPath
	before := countJSONL(t, fixture.store.Root())
	candidate, err := fixture.bridge.sessions.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fixture.bridge.commitPrepared(canceled, candidate, "started a new session"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled commitPrepared = %v, want canceled", err)
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Fatalf("canceled commit changed path to %s", fixture.bridge.rd.sessionPath)
	}
	if after := countJSONL(t, fixture.store.Root()); after != before {
		t.Fatalf("canceled commit left %d files, want %d", after, before)
	}
	flapCandidate, err := fixture.bridge.sessions.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	flap := &flapSignal{Context: context.Background()}
	if err := fixture.bridge.commitPrepared(flap, flapCandidate, "started a new session"); !errors.Is(err, context.Canceled) {
		t.Fatalf("flapping commitPrepared = %v, want canceled", err)
	}
	if fixture.bridge.sessions.Current() == nil || fixture.bridge.sessions.Current().path != origin {
		t.Fatal("flapping commit must leave the current session intact")
	}
	if after := countJSONL(t, fixture.store.Root()); after != before {
		t.Fatalf("flapping commit left %d files, want %d", after, before)
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}
