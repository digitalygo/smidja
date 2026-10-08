//go:build linux

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui"
	"github.com/digitalygo/smidja/internal/ui"
	"github.com/digitalygo/smidja/sdk"
)

type ptyTextClient struct {
	mu      sync.Mutex
	calls   int
	cancels int
}

func (c *ptyTextClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	<-ctx.Done()
	c.mu.Lock()
	c.cancels++
	c.mu.Unlock()
	return nil, ctx.Err()
}

func (c *ptyTextClient) snapshot() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.cancels
}

func newPTYCorrectionHost(t *testing.T, workspace string, sess *session.Session, preparer *contextPreparerAdapter) *hostRuntime {
	t.Helper()
	catalog := extensions.NewToolCatalog()
	host := newHostRuntime(context.Background(), workspace, nil, catalog)
	host.attachPreparer(preparer)
	host.setModel(models.NewRegistry(), "test/model", "test/model", "openrouter")
	host.setSystem("be terse")
	host.setWindow(128000)
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), workspace, "")
	return host
}

func startPTYCorrectionRun(t *testing.T, master, slave *os.File, client agent.Client, preparer *contextPreparerAdapter, extension sdk.Extension) (*hostRuntime, chan error, func()) {
	t.Helper()
	workspace := t.TempDir()
	home := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	history, entryIDs := seedCompactableSession(t, sess, 4)
	registry := extensions.NewRegistry()
	if extension != nil {
		if err := registry.Register(extension); err != nil {
			t.Fatal(err)
		}
	}
	hookRuntime := extensions.NewRuntime(registry)
	host := newPTYCorrectionHost(t, workspace, sess, preparer)
	host.setMessages(history)
	host.setEntryIDs(entryIDs)
	api := extensions.NewAPI(extensions.APIOptions{Catalog: host.catalog, Host: host.hostOptions()})
	host.bindAPI(api)
	hookRuntime.SetAPI(func() sdk.API { return api })
	hookRuntime.SetContext(func() sdk.HandlerContext { return host.context() })
	if err := hookRuntime.Start(); err != nil {
		t.Fatal(err)
	}
	var depsStderr bytes.Buffer
	var rdStdout bytes.Buffer
	var rdStderr bytes.Buffer
	deps := &Deps{
		Env:    envFrom(nil),
		Getwd:  func() (string, error) { return workspace, nil },
		Home:   func() string { return home },
		Stdin:  slave,
		Stdout: slave,
		Stderr: &depsStderr,
	}
	rd := &runDeps{
		model:       "test/model",
		system:      "be terse",
		sessionPath: sess.Path(),
		client:      client,
		recorder:    &sessionRecorder{sess},
		stdout:      &rdStdout,
		stderr:      &rdStderr,
		hooks:       hookRuntime.Dispatcher(),
		retry:       retryAdapter,
		retryPolicy: agent.RetryPolicy{Enabled: false},
		catalog:     host.catalog,
		commands:    extensions.NewCommandCatalog(),
		host:        host,
		preparer:    preparer,
		handlerContext: func(signal context.Context) sdk.HandlerContext {
			return hookRuntime.HandlerContext(signal)
		},
	}
	lineUI := ui.New(deps.Stdin, deps.Stdout, deps.Stderr, sdk.ModeInteractive)
	factory := func(io.Reader, io.Writer) tui.Terminal {
		return tui.NewProcessTerminal(slave, slave)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runTUI(ctx, deps, rd, lineUI, ui.TUIModeFullscreen, workspace, workspace, nil, factory, hookRuntime, nil)
	}()
	return host, done, cancel
}

func waitHostLifecycle(t *testing.T, host *hostRuntime) {
	t.Helper()
	waitUntil(t, 10*time.Second, "host lifecycle was not installed", func() bool {
		host.lifecycleMu.Lock()
		defer host.lifecycleMu.Unlock()
		return host.lifecycle.dispatch != nil
	})
}

func assertPTYTerminalRestored(t *testing.T, capture *smokePTYCapture, slave *os.File, before syscall.Termios) {
	t.Helper()
	capture.drain(t, 500*time.Millisecond)
	full := capture.snapshot()
	if !strings.Contains(full, tui.AltScreenEnter) || !strings.Contains(full, tui.AltScreenExit) {
		t.Fatalf("alt screen lifecycle was not clean:\n%q", full)
	}
	if after := smokePTYTermios(t, slave); after != before {
		t.Fatal("terminal attributes were not restored after quit")
	}
}

func TestTUIRealPTYHostIdleCompactCanceledOnQuit(t *testing.T) {
	master, slave := smokePTYOpen(t)
	before := smokePTYTermios(t, slave)
	capture := &smokePTYCapture{master: master}
	selector := newGatedCompactSelector()
	cmCfg := contextmanager.Config{
		Enabled:             true,
		ContextWindowTokens: 2000,
		CompactTarget:       0.1,
		KeepRecentMessages:  1,
	}
	live, err := contextmanager.New(cmCfg, selector)
	if err != nil {
		t.Fatal(err)
	}
	preparer := newContextPreparerAdapter(live, cmCfg)
	client := &ptyTextClient{}
	host, done, cancel := startPTYCorrectionRun(t, master, slave, client, preparer, nil)
	defer cancel()
	capture.waitFor(t, tui.AltScreenEnter, 10*time.Second)
	waitHostLifecycle(t, host)
	failures := make(chan error, 4)
	results := make(chan sdk.CompactionResult, 4)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnComplete: func(res sdk.CompactionResult) { results <- res },
		OnError:    func(err error) { failures <- err },
	})
	entered := false
	enteredDeadline := time.Now().Add(10 * time.Second)
	for !entered && time.Now().Before(enteredDeadline) {
		select {
		case <-selector.entered:
			entered = true
		case err := <-failures:
			t.Fatalf("compaction failed before reaching the selector: %v", err)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	if !entered {
		host.lifecycleMu.Lock()
		dispatch := host.lifecycle.dispatch
		host.lifecycleMu.Unlock()
		t.Fatalf("selector was never called; dispatch=%v output=%q", dispatch != nil, capture.snapshot())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("quitting did not cancel and join the blocked selector")
	}
	select {
	case err := <-failures:
		if !errors.Is(err, errHostClosed) && !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want the quit cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the settled compaction produced no OnError")
	}
	select {
	case res := <-results:
		t.Fatalf("a quit compaction reported OnComplete: %+v", res)
	case err := <-failures:
		t.Fatalf("duplicate OnError: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if calls, _ := client.snapshot(); calls != 0 {
		t.Fatalf("client calls = %d, want no phantom model calls", calls)
	}
	if host.callbackPanics.Load() != 0 {
		t.Fatalf("unexpected callback panics = %d", host.callbackPanics.Load())
	}
	host.waitCompacts()
	assertPTYTerminalRestored(t, capture, slave, before)
}

func TestTUIRealPTYPendingCompactPanicCallbackKeepsTerminalClean(t *testing.T) {
	master, slave := smokePTYOpen(t)
	before := smokePTYTermios(t, slave)
	capture := &smokePTYCapture{master: master}
	cmCfg := contextmanager.Config{Enabled: true, ContextWindowTokens: 2000, CompactTarget: 0.1, KeepRecentMessages: 1}
	live, err := contextmanager.New(cmCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	preparer := newContextPreparerAdapter(live, cmCfg)
	client := &ptyTextClient{}
	host, done, cancel := startPTYCorrectionRun(t, master, slave, client, preparer, nil)
	defer cancel()
	capture.waitFor(t, tui.AltScreenEnter, 10*time.Second)
	waitHostLifecycle(t, host)
	panicked := make(chan struct{}, 2)
	if !preparer.requestCompact(sdk.CompactOptions{
		OnError: func(error) {
			panicked <- struct{}{}
			panic("pending compact boom")
		},
	}) {
		t.Fatal("pending request was not accepted")
	}
	cancel()
	select {
	case <-panicked:
	case <-time.After(20 * time.Second):
		t.Fatal("the pending compaction callback was never invoked")
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("runTUI: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a panicking quit callback stranded the run")
	}
	if host.callbackPanics.Load() == 0 {
		t.Fatal("a panicking callback was not reported")
	}
	select {
	case <-panicked:
		t.Fatal("the panicking callback ran twice")
	case <-time.After(100 * time.Millisecond):
	}
	if calls, _ := client.snapshot(); calls != 0 {
		t.Fatalf("client calls = %d, want no phantom model calls", calls)
	}
	assertPTYTerminalRestored(t, capture, slave, before)
}
