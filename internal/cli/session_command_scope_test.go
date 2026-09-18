package cli

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

func TestBridgeCommandContextRetainedInvocationIsRejected(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	path := fixture.bridge.rd.sessionPath
	before := countJSONL(t, fixture.store.Root())
	var retained sdk.CommandContext
	if _, err := fixture.commands.Register("capture", sdk.Command{
		Description: "capture the context",
		Handler: func(ctx sdk.CommandContext, args string) error {
			retained = ctx
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/capture")
	if retained == nil {
		t.Fatal("the capture command did not run")
	}
	if _, err := retained.NewSession(sdk.NewSessionOptions{}); err != sdk.ErrModeUnsupported {
		t.Errorf("retained NewSession = %v, want unsupported", err)
	}
	if _, err := retained.Fork("", sdk.ForkOptions{}); err != sdk.ErrModeUnsupported {
		t.Errorf("retained Fork = %v, want unsupported", err)
	}
	if _, err := retained.SwitchSession(path, sdk.SwitchOptions{}); err != sdk.ErrModeUnsupported {
		t.Errorf("retained SwitchSession = %v, want unsupported", err)
	}
	if fixture.bridge.rd.sessionPath != path {
		t.Errorf("a retained context changed the session path to %s", fixture.bridge.rd.sessionPath)
	}
	if after := countJSONL(t, fixture.store.Root()); after != before {
		t.Errorf("a retained context created %d session files", after-before)
	}
}

func TestBridgeCommandContextCanceledSignalIsRejected(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	before := countJSONL(t, fixture.store.Root())
	ctx, cancel := context.WithCancel(context.Background())
	fixture.bridge.ctx = ctx
	var mutationErr error
	if _, err := fixture.commands.Register("cancelnow", sdk.Command{
		Description: "cancel then mutate",
		Handler: func(commandCtx sdk.CommandContext, args string) error {
			cancel()
			_, mutationErr = commandCtx.NewSession(sdk.NewSessionOptions{})
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/cancelnow")
	if !errors.Is(mutationErr, context.Canceled) {
		t.Errorf("canceled NewSession = %v, want context.Canceled", mutationErr)
	}
	if after := countJSONL(t, fixture.store.Root()); after != before {
		t.Errorf("a canceled mutation created %d session files", after-before)
	}
}

func TestBridgeCommandContextRetainedDuringActiveTurnIsRejected(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	fixture.bridge.rd.client = &partialBlockingClient{entered: make(chan struct{})}
	var retained sdk.CommandContext
	if _, err := fixture.commands.Register("capture", sdk.Command{
		Description: "capture the context",
		Handler: func(ctx sdk.CommandContext, args string) error {
			retained = ctx
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/capture")
	turns := make(chan struct{}, 1)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("blocking turn")
	select {
	case <-fixture.bridge.rd.client.(*partialBlockingClient).entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn did not start")
	}
	if _, err := retained.NewSession(sdk.NewSessionOptions{}); err != sdk.ErrModeUnsupported {
		t.Errorf("retained NewSession during a turn = %v, want unsupported", err)
	}
	fixture.bridge.interrupt()
	select {
	case <-turns:
	case <-time.After(5 * time.Second):
		t.Fatal("the interrupted turn did not finish")
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
}

func TestBridgeCommandContextPostShutdownIsRejected(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	before := countJSONL(t, fixture.store.Root())
	var retained sdk.CommandContext
	if _, err := fixture.commands.Register("capture", sdk.Command{
		Description: "capture the context",
		Handler: func(ctx sdk.CommandContext, args string) error {
			retained = ctx
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/capture")
	fixture.bridge.shutdown()
	fixture.bridge.wait()
	if _, err := retained.NewSession(sdk.NewSessionOptions{}); err != sdk.ErrModeUnsupported {
		t.Errorf("post-shutdown NewSession = %v, want unsupported", err)
	}
	if after := countJSONL(t, fixture.store.Root()); after != before {
		t.Errorf("a post-shutdown mutation created %d session files", after-before)
	}
	if err := retained.WaitForIdle(); err != sdk.ErrModeUnsupported {
		t.Errorf("WaitForIdle = %v, want unsupported", err)
	}
}

func TestBridgeCommandContextInCommandMutationsWork(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	seedTurn(t, fixture.sess, "scope origin")
	origin := fixture.bridge.rd.sessionPath

	if _, err := fixture.commands.Register("xnew", sdk.Command{
		Description: "new session",
		Handler: func(ctx sdk.CommandContext, args string) error {
			_, err := ctx.NewSession(sdk.NewSessionOptions{})
			return err
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/xnew")
	newPath := fixture.bridge.rd.sessionPath
	if newPath == origin {
		t.Fatal("/xnew must activate a distinct session file")
	}

	target := seedStoredSession(t, fixture, "scope target")
	if _, err := fixture.commands.Register("xswitch", sdk.Command{
		Description: "switch session",
		Handler: func(ctx sdk.CommandContext, args string) error {
			_, err := ctx.SwitchSession(target, sdk.SwitchOptions{})
			return err
		},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.handle("/xswitch")
	if fixture.bridge.rd.sessionPath != target {
		t.Fatalf("switch path = %s, want %s", fixture.bridge.rd.sessionPath, target)
	}

	if _, err := fixture.commands.Register("xfork", sdk.Command{
		Description: "fork session",
		Handler: func(ctx sdk.CommandContext, args string) error {
			_, err := ctx.Fork("", sdk.ForkOptions{})
			return err
		},
	}); err != nil {
		t.Fatal(err)
	}
	beforeFork := fixture.bridge.rd.sessionPath
	fixture.bridge.handle("/xfork")
	if fixture.bridge.rd.sessionPath == beforeFork {
		t.Fatal("/xfork must activate a distinct session file")
	}
	assertFileContains(t, fixture.bridge.rd.sessionPath, "scope target", true)
}

func TestBridgeCommandContextCancelDuringPrepareAbortsCandidate(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	origin := fixture.bridge.rd.sessionPath
	target := seedStoredSession(t, fixture, "cancel target")

	entered := make(chan struct{})
	release := make(chan struct{})
	fixture.bridge.sessions.SetPreparer(func(sess *session.Session, mode sessionLoadMode) (*activeSession, error) {
		close(entered)
		<-release
		return testSessionPreparer(sess, mode)
	})

	ctx, cancel := context.WithCancel(context.Background())
	fixture.bridge.ctx = ctx

	var switchErr error
	if _, err := fixture.commands.Register("xslow", sdk.Command{
		Description: "slow switch",
		Handler: func(commandCtx sdk.CommandContext, args string) error {
			_, switchErr = commandCtx.SwitchSession(target, sdk.SwitchOptions{})
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		fixture.bridge.handle("/xslow")
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the switch prepare did not start")
	}
	cancel()
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the switch command did not finish")
	}
	if !errors.Is(switchErr, context.Canceled) {
		t.Errorf("switch err = %v, want context.Canceled", switchErr)
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Errorf("a canceled switch changed the session path to %s", fixture.bridge.rd.sessionPath)
	}
	current := fixture.bridge.sessions.Current()
	if current == nil || current.path != origin {
		t.Error("a canceled switch must leave the current session intact")
	}
	held, err := fixture.store.Open(target, session.OpenOptions{})
	if err != nil {
		t.Fatalf("a canceled switch leaked the candidate lock: %v", err)
	}
	held.Close()
}

func TestBridgeApplyFailureRestoresDisplayState(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	seedTurn(t, fixture.sess, "apply origin")
	origin := fixture.bridge.rd.sessionPath
	originRecorder := fixture.bridge.rd.recorder
	originHistory := fixture.bridge.history

	candidate, err := fixture.bridge.sessions.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	fixture.bridge.applyStep = func(*activeSession) error { return errors.New("display boom") }
	err = fixture.bridge.activateSession(candidate, "started a new session")
	if err == nil || err.Error() != "display boom" {
		t.Fatalf("activateSession = %v, want display boom", err)
	}
	if fixture.bridge.rd.sessionPath != origin {
		t.Errorf("failed apply changed the session path to %s", fixture.bridge.rd.sessionPath)
	}
	if fixture.bridge.rd.recorder != originRecorder {
		t.Error("failed apply changed the recorder")
	}
	if len(fixture.bridge.history) != len(originHistory) {
		t.Error("failed apply changed the history")
	}
	current := fixture.bridge.sessions.Current()
	if current == nil || current.path != origin {
		t.Error("failed apply changed the current session")
	}
	if _, err := os.Stat(candidate.path); !os.IsNotExist(err) {
		t.Fatalf("failed apply did not remove the created candidate: %v", err)
	}
}

func TestBridgeApplyInstallsStateBeforePublishingOwner(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	origin := fixture.bridge.rd.sessionPath
	candidate, err := fixture.bridge.sessions.PrepareNew()
	if err != nil {
		t.Fatal(err)
	}
	var installedDuringApply bool
	fixture.bridge.applyStep = func(next *activeSession) error {
		installedDuringApply = fixture.bridge.rd.sessionPath == next.path
		return nil
	}
	if err := fixture.bridge.activateSession(candidate, "started a new session"); err != nil {
		t.Fatalf("activateSession = %v", err)
	}
	if !installedDuringApply {
		t.Error("the bridge state was not installed before the owner was published")
	}
	if fixture.bridge.sessions.Current() != candidate {
		t.Error("the commit did not publish the candidate as the owner")
	}
	if fixture.bridge.rd.sessionPath != candidate.path {
		t.Error("the bridge path was not installed")
	}
	if candidate.path == origin {
		t.Fatal("the new session must use a distinct path")
	}
}

func TestBridgeSessionBrowserCancelLeavesSessionIntact(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	before := fixture.bridge.rd.sessionPath
	seedStoredSession(t, fixture, "cancel browser")
	done := make(chan error, 1)
	go func() { done <- fixture.bridge.sessionsBrowser() }()
	waitForOutputSettled(t, fixture.terminal, "Sessions", 3*time.Second)
	fixture.terminal.SendInput("\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sessionsBrowser = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sessions browser dialog did not close")
	}
	if fixture.bridge.rd.sessionPath != before {
		t.Errorf("a canceled dialog changed the session path to %s", fixture.bridge.rd.sessionPath)
	}
	current := fixture.bridge.sessions.Current()
	if current == nil || current.path != before {
		t.Error("a canceled dialog changed the current session")
	}
}

func TestBridgeCommitPreparedGuards(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	if err := fixture.bridge.commitPrepared(context.Background(), nil, ""); err == nil {
		t.Error("commitPrepared without a candidate must fail")
	}
	bare := newBridgeFixture(t, nil, nil)
	if err := bare.bridge.commitPrepared(context.Background(), &activeSession{path: "next"}, ""); err == nil {
		t.Error("commitPrepared without session control must fail")
	}
}

func TestCommandInvocationValidityChecks(t *testing.T) {
	invocation := newCommandInvocation(context.Background())
	if err := invocation.check(context.Background()); err != nil {
		t.Errorf("active invocation check = %v", err)
	}
	invocation.invalidate()
	if err := invocation.check(context.Background()); err != sdk.ErrModeUnsupported {
		t.Errorf("invalidated invocation check = %v, want unsupported", err)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := newCommandInvocation(cancelCtx)
	if !errors.Is(canceled.check(context.Background()), context.Canceled) {
		t.Errorf("canceled invocation check = %v, want canceled", canceled.check(context.Background()))
	}
	var nilInvocation *commandInvocation
	if nilInvocation.check(context.Background()) != sdk.ErrModeUnsupported {
		t.Error("a nil invocation must be unsupported")
	}
	if signal := handlerSignal(nil, context.Background()); signal == nil {
		t.Error("handlerSignal must fall back to the bridge signal")
	}
	if signal := (&tuiCommandContext{}).commandSignal(); signal != nil {
		t.Error("a context without a handler must report no signal")
	}
	if err := (&tuiCommandContext{}).mutationError(); err != sdk.ErrModeUnsupported {
		t.Errorf("mutation without a handler = %v, want unsupported", err)
	}
}
