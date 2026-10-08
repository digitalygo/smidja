package ui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

type installGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newInstallGate() *installGate {
	return &installGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *installGate) enter() {
	g.once.Do(func() { close(g.entered) })
	<-g.release
}

func (g *installGate) wait(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("dialog install did not reach the gate")
	}
}

func (g *installGate) open() { close(g.release) }

type dialogInstallSnapshot struct {
	hasOverlay bool
	focused    tui.Component
}

type dialogOutcome struct {
	accepted bool
	err      error
}

func waitForInstallSnapshots(t *testing.T, observed <-chan struct{}, count int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for i := 0; i < count; i++ {
		select {
		case <-observed:
		case <-deadline:
			t.Fatalf("only %d of %d dialog installs were observed", i, count)
		}
	}
}

func dialogInstallSnapshotAt(t *testing.T, mu *sync.Mutex, snapshots []dialogInstallSnapshot, index int) dialogInstallSnapshot {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if index >= len(snapshots) {
		t.Fatalf("install snapshot %d missing, have %d", index, len(snapshots))
	}
	return snapshots[index]
}

func waitForWaiters(t *testing.T, runner *Runner, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runner.dialogs.waitingCount() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("dialog waiters = %d, want %d", runner.dialogs.waitingCount(), want)
}

func waitForOverlay(t *testing.T, runner *Runner) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runner.dialogs.overlayFocused() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("dialog overlay was not installed and focused")
}

func TestModalHandoffDiscardsInputAndKeepsCapture(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	first := make(chan bool, 1)
	go func() {
		ok, _ := runner.Confirm("first", "one")
		first <- ok
	}()
	waitForDialog(t, runner)

	gate := newInstallGate()
	runner.dialogs.setInstallHook(gate.enter)
	t.Cleanup(func() { runner.dialogs.setInstallHook(nil) })

	second := make(chan string, 1)
	go func() {
		value, _ := runner.Input("second", "")
		second <- value
	}()
	waitForWaiters(t, runner, 1)

	terminal.SendInput("y")
	select {
	case ok := <-first:
		if !ok {
			t.Fatal("first dialog was not accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first dialog did not resolve")
	}
	gate.wait(t)

	if !runner.view.ModalCapture() {
		t.Fatal("capture was disabled during handoff to the next dialog")
	}
	if runner.view.HasOverlay() {
		t.Fatal("a stale overlay was installed during handoff")
	}
	terminal.SendInput("zzz-handoff-secret")
	if got := runner.Surface().Editor().Text(); got != "" {
		t.Fatalf("input during handoff reached the chat editor: %q", got)
	}

	gate.open()
	waitForOverlay(t, runner)
	if got := runner.Surface().Editor().Text(); got != "" {
		t.Fatalf("handoff input reached the editor after install: %q", got)
	}
	terminal.SendInput("ok")
	terminal.SendInput("\r")
	select {
	case got := <-second:
		if got != "ok" {
			t.Fatalf("second dialog value = %q, want ok", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second dialog did not resolve")
	}
	if runner.view.ModalCapture() {
		t.Fatal("capture stayed enabled after the final dialog closed")
	}
	if runner.view.FocusedComponent() != runner.Surface().Editor() {
		t.Fatal("focus was not restored to the editor")
	}
}

func TestCanceledWaitingDialogCannotLeaveCaptureEnabled(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	first := make(chan bool, 1)
	go func() {
		ok, _ := runner.Confirm("first", "one")
		first <- ok
	}()
	waitForDialog(t, runner)

	ctx, cancel := context.WithCancel(context.Background())
	bound := runner.BoundUI(ctx)
	second := make(chan error, 1)
	go func() {
		_, err := bound.Input("second", "")
		second <- err
	}()
	waitForWaiters(t, runner, 1)

	cancel()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting dialog error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiting dialog did not cancel")
	}
	if !runner.view.ModalCapture() {
		t.Fatal("capture dropped while the admitted dialog still owned admission")
	}

	terminal.SendInput("n")
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("first dialog did not resolve")
	}
	if runner.view.ModalCapture() {
		t.Fatal("a canceled final waiter left modal capture enabled")
	}
	if runner.view.HasOverlay() {
		t.Fatal("overlay remained after the admitted dialog closed")
	}
}

func TestCanceledAdmittedDialogReleasesOwnership(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	first := make(chan bool, 1)
	go func() {
		ok, _ := runner.Confirm("first", "one")
		first <- ok
	}()
	waitForDialog(t, runner)

	gate := newInstallGate()
	runner.dialogs.setInstallHook(gate.enter)
	t.Cleanup(func() { runner.dialogs.setInstallHook(nil) })

	ctx, cancel := context.WithCancel(context.Background())
	bound := runner.BoundUI(ctx)
	second := make(chan error, 1)
	go func() {
		_, err := bound.Input("second", "")
		second <- err
	}()
	waitForWaiters(t, runner, 1)

	terminal.SendInput("n")
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("first dialog did not resolve")
	}
	gate.wait(t)
	if !runner.view.ModalCapture() {
		t.Fatal("capture was disabled after the next dialog took ownership")
	}

	cancel()
	gate.open()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("admitted dialog error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("admitted dialog did not cancel")
	}
	if runner.view.ModalCapture() {
		t.Fatal("canceled admitted dialog left capture enabled")
	}
	if runner.view.HasOverlay() {
		t.Fatal("canceled admitted dialog left its overlay installed")
	}
	if runner.view.FocusedComponent() != runner.Surface().Editor() {
		t.Fatal("focus was not restored after the admitted dialog canceled")
	}
}

func TestShutdownWaitsForGatedInstallAndDetachesOverlay(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	first := make(chan bool, 1)
	go func() {
		ok, _ := runner.Confirm("first", "one")
		first <- ok
	}()
	waitForDialog(t, runner)

	gate := newInstallGate()
	runner.dialogs.setInstallHook(gate.enter)
	t.Cleanup(func() { runner.dialogs.setInstallHook(nil) })

	second := make(chan error, 1)
	go func() {
		_, err := runner.Input("second", "")
		second <- err
	}()
	waitForWaiters(t, runner, 1)

	terminal.SendInput("y")
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("first dialog did not resolve")
	}
	gate.wait(t)

	shutdownDone := make(chan struct{})
	go func() {
		runner.CancelDialogs()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned before the admitted dialog drained")
	case <-time.After(150 * time.Millisecond):
	}

	gate.open()
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not return after teardown")
	}
	select {
	case err := <-second:
		if !errors.Is(err, errDialogsClosed) {
			t.Fatalf("install-after-close dialog error = %v, want errDialogsClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second dialog did not resolve")
	}
	if runner.view.HasOverlay() {
		t.Fatal("overlay was installed after dialogs closed")
	}
	if runner.dialogs.Active() {
		t.Fatal("dialog stayed active after shutdown")
	}
	if runner.view.ModalCapture() {
		t.Fatal("capture stayed enabled after shutdown")
	}
	if runner.view.FocusedComponent() != runner.Surface().Editor() {
		t.Fatal("focus was not restored after shutdown")
	}
}

func TestRequestExitDrainsDialogAndRestoresFocus(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	done := make(chan error, 1)
	go func() {
		_, err := runner.Confirm("t", "m")
		done <- err
	}()
	waitForDialog(t, runner)

	runner.RequestExit()
	select {
	case err := <-done:
		if !errors.Is(err, errDialogsClosed) {
			t.Fatalf("Confirm error = %v, want errDialogsClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dialog did not resolve on exit")
	}
	select {
	case <-runner.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not close after RequestExit")
	}
	if runner.view.HasOverlay() {
		t.Fatal("overlay remained after RequestExit")
	}
	if runner.view.ModalCapture() {
		t.Fatal("capture remained after RequestExit")
	}
	if runner.view.FocusedComponent() != runner.Surface().Editor() {
		t.Fatal("focus was not restored after RequestExit")
	}
}

func assertLoginPublicationTeardown(t *testing.T, runner *Runner) {
	t.Helper()
	if runner.view.HasOverlay() {
		t.Fatal("settlement during publication left an orphan overlay")
	}
	if runner.view.ModalCapture() {
		t.Fatal("settlement during publication left modal capture enabled")
	}
	if runner.dialogs.Active() {
		t.Fatal("dialog stayed active after settlement during publication")
	}
	if runner.dialogs.sessions != 0 {
		t.Fatalf("admitted sessions = %d, want 0", runner.dialogs.sessions)
	}
	if runner.view.FocusedComponent() != runner.Surface().Editor() {
		t.Fatal("focus was not restored after settlement during publication")
	}
}

func assertNextDialogWorks(t *testing.T, runner *Runner, terminal *fakeUITerminal) {
	t.Helper()
	next := make(chan bool, 1)
	go func() {
		ok, _ := runner.Confirm("next", "works")
		next <- ok
	}()
	waitForDialog(t, runner)
	terminal.SendInput("y")
	select {
	case ok := <-next:
		if !ok {
			t.Fatal("next dialog after the publication race was not accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("next dialog after the publication race did not resolve")
	}
	if runner.view.HasOverlay() {
		t.Fatal("next dialog left an overlay installed")
	}
	if runner.view.ModalCapture() {
		t.Fatal("next dialog left modal capture enabled")
	}
}

func TestLoginEnterSettlesDuringOverlayPublication(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)

	var publishOnce sync.Once
	runner.dialogs.setPublishHook(func() {
		publishOnce.Do(func() { terminal.SendInput("\r") })
	})
	t.Cleanup(func() { runner.dialogs.setPublishHook(nil) })

	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter", ContinueOnEnter: true})
	if err != nil {
		t.Fatalf("StartLogin during Enter publication = %v, want the settled operation", err)
	}
	assertLoginPublicationTeardown(t, runner)
	select {
	case <-op.Done():
	default:
		t.Fatal("operation Done channel was not closed")
	}
	if op.State() != LoginSucceeded {
		t.Fatalf("state = %q, want %q", op.State(), LoginSucceeded)
	}
	if op.Err() != nil {
		t.Fatalf("error = %v, want nil", op.Err())
	}
	if result := op.Wait(); result.State != LoginSucceeded || result.Err != nil {
		t.Fatalf("result = %+v, want succeeded", result)
	}
	assertNextDialogWorks(t, runner, terminal)
}

func TestLoginProviderFailureSettlesDuringOverlayPublication(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	boom := errors.New("provider boom")

	var captured *LoginOperation
	runner.loginStartHook = func(op *LoginOperation) { captured = op }
	t.Cleanup(func() { runner.loginStartHook = nil })

	var publishOnce sync.Once
	runner.dialogs.setPublishHook(func() {
		publishOnce.Do(func() { captured.Fail(boom) })
	})
	t.Cleanup(func() { runner.dialogs.setPublishHook(nil) })

	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin during provider failure = %v, want the settled operation", err)
	}
	if op != captured {
		t.Fatal("StartLogin returned a different operation than the one that settled")
	}
	assertLoginPublicationTeardown(t, runner)
	select {
	case <-op.Done():
	default:
		t.Fatal("operation Done channel was not closed")
	}
	if op.State() != LoginFailed {
		t.Fatalf("state = %q, want %q", op.State(), LoginFailed)
	}
	if !errors.Is(op.Err(), boom) {
		t.Fatalf("error = %v, want %v", op.Err(), boom)
	}
	if result := op.Wait(); result.State != LoginFailed || !errors.Is(result.Err, boom) {
		t.Fatalf("result = %+v, want provider failure", result)
	}
	assertNextDialogWorks(t, runner, terminal)
}

func TestLoginSettlementBetweenAdmissionAndPublicationReturnsSettled(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)

	var captured *LoginOperation
	runner.loginStartHook = func(op *LoginOperation) {
		captured = op
		op.Succeed()
	}
	t.Cleanup(func() { runner.loginStartHook = nil })

	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	if err != nil {
		t.Fatalf("StartLogin after pre-publication settlement = %v, want the settled operation", err)
	}
	if op != captured {
		t.Fatal("StartLogin returned a different operation than the one that settled")
	}
	if op.State() != LoginSucceeded || op.Err() != nil {
		t.Fatalf("state = %q error = %v, want succeeded", op.State(), op.Err())
	}
	select {
	case <-op.Done():
	default:
		t.Fatal("operation Done channel was not closed")
	}
	assertLoginPublicationTeardown(t, runner)
}

func TestLoginSettlementDuringOverlayPublicationLeavesNoLeak(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)

	var (
		snapshotMu      sync.Mutex
		snapshots       []dialogInstallSnapshot
		installObserved = make(chan struct{}, 8)
	)
	runner.dialogs.setInstallHook(func() {
		snapshotMu.Lock()
		snapshots = append(snapshots, dialogInstallSnapshot{
			hasOverlay: runner.view.HasOverlay(),
			focused:    runner.view.FocusedComponent(),
		})
		snapshotMu.Unlock()
		installObserved <- struct{}{}
	})
	t.Cleanup(func() { runner.dialogs.setInstallHook(nil) })

	var (
		publishOnce     sync.Once
		publishFired    = make(chan struct{})
		successorQueued = make(chan struct{})
		successorResult = make(chan dialogOutcome, 1)
	)
	runner.dialogs.setPublishHook(func() {
		publishOnce.Do(func() {
			go func() {
				ok, err := runner.Confirm("queued successor", "already waiting")
				successorResult <- dialogOutcome{accepted: ok, err: err}
			}()
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if runner.dialogs.waitingCount() == 1 {
					close(successorQueued)
					break
				}
				time.Sleep(time.Millisecond)
			}
			terminal.SendInput("\x1b")
			close(publishFired)
		})
	})
	t.Cleanup(func() { runner.dialogs.setPublishHook(nil) })

	op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
	select {
	case <-successorQueued:
	case <-time.After(2 * time.Second):
		t.Fatal("successor was not queued while the login publication was unsettled")
	}
	select {
	case <-publishFired:
	case <-time.After(2 * time.Second):
		t.Fatal("overlay publication hook did not fire")
	}
	if err != nil {
		t.Fatalf("StartLogin during publication race = %v, want the settled operation", err)
	}
	if op.State() != LoginCanceled {
		t.Fatalf("state = %q, want %q", op.State(), LoginCanceled)
	}
	if !errors.Is(op.Err(), errLoginCanceled) {
		t.Fatalf("error = %v, want %v", op.Err(), errLoginCanceled)
	}
	select {
	case <-op.Done():
	default:
		t.Fatal("operation Done channel was not closed")
	}
	runner.dialogs.setPublishHook(nil)

	waitForInstallSnapshots(t, installObserved, 2)
	snapshot := dialogInstallSnapshotAt(t, &snapshotMu, snapshots, 1)
	if snapshot.hasOverlay {
		t.Fatal("queued successor installed while the canceled login overlay was still attached")
	}
	if snapshot.focused != runner.Surface().Editor() {
		t.Fatal("queued successor did not observe the chat focus as its preFocus")
	}
	waitForOverlay(t, runner)
	terminal.SendInput("y")
	select {
	case outcome := <-successorResult:
		if outcome.err != nil {
			t.Fatalf("queued successor dialog failed: %v", outcome.err)
		}
		if !outcome.accepted {
			t.Fatal("queued successor dialog was not accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued successor did not resolve")
	}
	assertLoginPublicationTeardown(t, runner)
	assertNextDialogWorks(t, runner, terminal)
}

func TestLoginSettlementBeforeShutdownDuringPublicationReturnsSettled(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)

	var (
		releasePublicationOnce sync.Once
		releasePublication     = make(chan struct{})
		publicationBlocked     = make(chan struct{})
		publishOnce            sync.Once
	)
	releasePublicationNow := func() {
		releasePublicationOnce.Do(func() { close(releasePublication) })
	}
	t.Cleanup(releasePublicationNow)
	runner.dialogs.setPublishHook(func() {
		publishOnce.Do(func() {
			terminal.SendInput("\x1b")
			close(publicationBlocked)
			<-releasePublication
		})
	})
	t.Cleanup(func() { runner.dialogs.setPublishHook(nil) })

	started := make(chan error, 1)
	var startedOp *LoginOperation
	go func() {
		op, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
		startedOp = op
		started <- err
	}()
	select {
	case <-publicationBlocked:
	case <-time.After(2 * time.Second):
		t.Fatal("settlement during shutdown publication did not block")
	}

	shutdownDone := make(chan struct{})
	go func() {
		runner.CancelDialogs()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned before the deferred publication release completed")
	case <-time.After(150 * time.Millisecond):
	}
	releasePublicationNow()

	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("StartLogin after settlement during publication = %v, want the settled operation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StartLogin did not resolve after settlement during publication")
	}
	if startedOp == nil {
		t.Fatal("StartLogin returned no operation after settlement during publication")
	}
	if startedOp.State() != LoginCanceled || !errors.Is(startedOp.Err(), errLoginCanceled) {
		t.Fatalf("state = %q error = %v, want canceled", startedOp.State(), startedOp.Err())
	}
	select {
	case <-startedOp.Done():
	default:
		t.Fatal("operation Done channel was not closed")
	}
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not return after the publication release")
	}
	assertLoginPublicationTeardown(t, runner)
	if runner.view.FocusedComponent() != runner.Surface().Editor() {
		t.Fatal("focus was not restored after shutdown")
	}
}

func TestLoginShutdownDuringOverlayPublicationKeepsClosedSemantics(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)

	var captured *LoginOperation
	runner.loginStartHook = func(op *LoginOperation) { captured = op }
	t.Cleanup(func() { runner.loginStartHook = nil })

	var (
		releasePublicationOnce sync.Once
		releasePublication     = make(chan struct{})
		publicationBlocked     = make(chan struct{})
		publishOnce            sync.Once
	)
	releasePublicationNow := func() {
		releasePublicationOnce.Do(func() { close(releasePublication) })
	}
	t.Cleanup(releasePublicationNow)
	runner.dialogs.setPublishHook(func() {
		publishOnce.Do(func() {
			close(publicationBlocked)
			<-releasePublication
		})
	})
	t.Cleanup(func() { runner.dialogs.setPublishHook(nil) })

	started := make(chan error, 1)
	go func() {
		_, err := runner.StartLogin(context.Background(), LoginRequest{Provider: "openrouter"})
		started <- err
	}()
	select {
	case <-publicationBlocked:
	case <-time.After(2 * time.Second):
		t.Fatal("login publication did not block")
	}

	shutdownDone := make(chan struct{})
	go func() {
		runner.CancelDialogs()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned before the deferred publication release completed")
	case <-time.After(150 * time.Millisecond):
	}
	releasePublicationNow()

	select {
	case err := <-started:
		if !errors.Is(err, errDialogsClosed) {
			t.Fatalf("StartLogin during shutdown = %v, want errDialogsClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StartLogin did not resolve during shutdown")
	}
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not return after the publication release")
	}
	if captured == nil {
		t.Fatal("StartLogin did not create an operation before shutdown")
	}
	if captured.State() != LoginStarting {
		t.Fatalf("state after closure before settlement = %q, want %q", captured.State(), LoginStarting)
	}
	select {
	case <-captured.Done():
		t.Fatal("service closure before settlement must not settle the operation")
	default:
	}
	assertLoginPublicationTeardown(t, runner)
	if runner.view.FocusedComponent() != runner.Surface().Editor() {
		t.Fatal("focus was not restored after shutdown")
	}
}

func TestModalAcquireNormalizesNilContextAndRejectsClosed(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	session, err := runner.dialogs.acquire(nil)
	if err != nil {
		t.Fatalf("acquire = %v, want nil", err)
	}
	if !runner.view.ModalCapture() {
		t.Fatal("capture was not enabled for the admitted session")
	}
	session.release()
	if runner.view.ModalCapture() {
		t.Fatal("capture stayed enabled after release")
	}

	runner.CancelDialogs()
	if _, err := runner.dialogs.acquire(nil); !errors.Is(err, errDialogsClosed) {
		t.Fatalf("acquire after shutdown = %v, want errDialogsClosed", err)
	}
}

func TestShutdownCancelsWaitingDialog(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	first := make(chan error, 1)
	go func() {
		_, err := runner.Confirm("first", "one")
		first <- err
	}()
	waitForDialog(t, runner)
	second := make(chan error, 1)
	go func() {
		_, err := runner.Input("second", "")
		second <- err
	}()
	waitForWaiters(t, runner, 1)

	runner.CancelDialogs()
	for name, channel := range map[string]chan error{"admitted": first, "waiting": second} {
		select {
		case err := <-channel:
			if !errors.Is(err, errDialogsClosed) {
				t.Fatalf("%s dialog error = %v, want errDialogsClosed", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s dialog did not resolve on shutdown", name)
		}
	}
	if runner.view.ModalCapture() {
		t.Fatal("capture remained after shutdown drained the waiters")
	}
	if runner.dialogs.sessions != 0 {
		t.Fatalf("admitted sessions = %d, want 0", runner.dialogs.sessions)
	}
}

func TestAdmittedWaiterObservesShutdown(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	dialogs := runner.dialogs
	dialogs.mu.Lock()
	dialogs.owner = true
	dialogs.beginSessionLocked()
	dialogs.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := dialogs.acquire(context.Background())
		done <- err
	}()
	waitForWaiters(t, runner, 1)

	dialogs.mu.Lock()
	waiter := dialogs.waiting[0]
	dialogs.waiting = nil
	waiter.admitted = true
	close(waiter.ready)
	dialogs.closed = true
	dialogs.mu.Unlock()

	select {
	case err := <-done:
		if !errors.Is(err, errDialogsClosed) {
			t.Fatalf("acquire = %v, want errDialogsClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("admitted waiter did not observe shutdown")
	}
	if dialogs.sessions != 0 {
		t.Fatalf("admitted sessions = %d, want 0", dialogs.sessions)
	}
	runner.CancelDialogs()
}

func TestModalShutdownIsIdempotentAndTerminalStable(t *testing.T) {
	runner, terminal := startTestRunner(t, TUIModeRegular, nil)
	runner.CancelDialogs()
	runner.CancelDialogs()
	writes := terminal.WriteCount()
	runner.CancelDialogs()
	if got := terminal.WriteCount(); got != writes {
		t.Fatalf("repeated CancelDialogs wrote to the terminal: %d, want %d", got, writes)
	}
	runner.Stop()
	runner.Stop()
	if got := terminal.StopCalls(); got != 1 {
		t.Fatalf("terminal stop count = %d, want 1", got)
	}
	if runner.view.ModalCapture() {
		t.Fatal("capture remained after Stop")
	}
}
