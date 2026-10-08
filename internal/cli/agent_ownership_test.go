package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/openrouter"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

type ownershipTextClient struct {
	mu      sync.Mutex
	calls   int
	cancels int
	once    sync.Once
	started chan struct{}
}

func (c *ownershipTextClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	c.once.Do(func() { close(c.started) })
	<-ctx.Done()
	c.mu.Lock()
	c.cancels++
	c.mu.Unlock()
	return nil, ctx.Err()
}

func (c *ownershipTextClient) snapshot() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.cancels
}

func newOwnedTestHost(t *testing.T) (*hostRuntime, *session.Session, *session.Store) {
	t.Helper()
	store := wiringStore(t)
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	if err := sess.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"seed"`)}); err != nil {
		t.Fatal(err)
	}
	catalog := extensions.NewToolCatalog()
	host := newHostRuntime(context.Background(), t.TempDir(), nil, catalog)
	host.setModel(models.NewRegistry(), "test/model", "test/model", "openrouter")
	host.setSystem("be terse")
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), t.TempDir(), "")
	return host, sess, store
}

func TestSessionGenerationDoneHandlesNilHandle(t *testing.T) {
	unbound := newHostRuntime(context.Background(), t.TempDir(), nil, nil)
	select {
	case <-unbound.sessionGenerationDone(nil):
	default:
		t.Fatal("an unbound host did not close the generation channel for a nil handle")
	}
	bound, _, _ := newOwnedTestHost(t)
	select {
	case <-bound.sessionGenerationDone(nil):
	default:
		t.Fatal("a bound host did not close the generation channel for a nil handle")
	}
}

func goroutineStacksContain(marker string) bool {
	buffer := make([]byte, 1<<20)
	n := runtime.Stack(buffer, true)
	return strings.Contains(string(buffer[:n]), marker)
}

func TestWatchSessionGenerationStopJoinsWatcher(t *testing.T) {
	done := make(chan struct{})
	var canceled atomic.Bool
	stop := watchSessionGeneration(done, func() { canceled.Store(true) })
	if !goroutineStacksContain("cli.watchSessionGeneration") {
		t.Fatal("the watcher goroutine never started")
	}
	stop()
	if goroutineStacksContain("cli.watchSessionGeneration") {
		t.Fatal("stop returned with the watcher goroutine still alive")
	}
	close(done)
	time.Sleep(20 * time.Millisecond)
	if canceled.Load() {
		t.Fatal("the watcher canceled after stop")
	}
}

func TestRunOwnedAgentTurnHoldsTurnOwnership(t *testing.T) {
	host, _, _ := newOwnedTestHost(t)
	if err := host.runOwnedAgentTurn(context.Background(), func(turnCtx context.Context) error {
		host.turnMu.Lock()
		active := host.turnActive
		host.turnMu.Unlock()
		if !active {
			t.Fatal("turnActive must be set for the whole owned run")
		}
		select {
		case <-turnCtx.Done():
			t.Fatal("owned turn context canceled before the run finished")
		default:
		}
		return nil
	}); err != nil {
		t.Fatalf("runOwnedAgentTurn: %v", err)
	}
	host.turnMu.Lock()
	active := host.turnActive
	host.turnMu.Unlock()
	if active {
		t.Fatal("turnActive must be cleared after the owned run")
	}
}

func TestRunOwnedAgentTurnUnavailableHost(t *testing.T) {
	closed, _, _ := newOwnedTestHost(t)
	closed.shutdown()
	called := false
	if err := closed.runOwnedAgentTurn(context.Background(), func(context.Context) error {
		called = true
		return nil
	}); !errors.Is(err, errHostClosed) {
		t.Fatalf("closed host error = %v, want errHostClosed", err)
	}
	if called {
		t.Fatal("closed host executed the owned run")
	}
	unbound := newHostRuntime(context.Background(), t.TempDir(), nil, nil)
	if err := unbound.runOwnedAgentTurn(context.Background(), func(context.Context) error {
		called = true
		return nil
	}); !errors.Is(err, errHostClosed) {
		t.Fatalf("unbound host error = %v, want errHostClosed", err)
	}
	if called {
		t.Fatal("unbound host executed the owned run")
	}
}

func TestRunOwnedAgentTurnPreCanceledSkipsRun(t *testing.T) {
	host, _, _ := newOwnedTestHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := host.runOwnedAgentTurn(ctx, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("pre-canceled owned run executed")
	}
	host.turnMu.Lock()
	active := host.turnActive
	host.turnMu.Unlock()
	if active {
		t.Fatal("pre-canceled owned run left turnActive set")
	}
}

func TestRunOwnedAgentTurnAbortCancelsChildStream(t *testing.T) {
	host, _, _ := newOwnedTestHost(t)
	client := &ownershipTextClient{started: make(chan struct{})}
	var turnSignal context.Context
	errs := make(chan error, 1)
	go func() {
		errs <- host.runOwnedAgentTurn(context.Background(), func(turnCtx context.Context) error {
			turnSignal = turnCtx
			_, err := client.StreamTurn(turnCtx, &agent.TurnRequest{Model: "test/model"}, nil, nil)
			return err
		})
	}()
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("child stream never started")
	}
	host.abort(turnSignal)
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("aborted run error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("aborted owned run did not return")
	}
	if calls, cancels := client.snapshot(); calls != 1 || cancels != 1 {
		t.Fatalf("client calls/cancels = %d/%d, want 1/1", calls, cancels)
	}
	host.turnMu.Lock()
	active := host.turnActive
	host.turnMu.Unlock()
	if active {
		t.Fatal("aborted owned run left turnActive set")
	}
}

func TestRunOwnedAgentTurnCancelsOnSessionRebind(t *testing.T) {
	host, _, store := newOwnedTestHost(t)
	client := &ownershipTextClient{started: make(chan struct{})}
	errs := make(chan error, 1)
	go func() {
		errs <- host.runOwnedAgentTurn(context.Background(), func(turnCtx context.Context) error {
			_, err := client.StreamTurn(turnCtx, &agent.TurnRequest{Model: "test/model"}, nil, nil)
			return err
		})
	}()
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("child stream never started")
	}
	next, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	host.bindSession(next, &sessionRecorder{next}, next.ID(), next.Path(), t.TempDir(), "")
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("rebind run error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session rebind did not cancel the owned child run")
	}
	if calls, cancels := client.snapshot(); calls != 1 || cancels != 1 {
		t.Fatalf("client calls/cancels = %d/%d, want 1/1", calls, cancels)
	}
}

func TestRunOwnedAgentTurnShutdownCancelsChild(t *testing.T) {
	host, _, _ := newOwnedTestHost(t)
	client := &ownershipTextClient{started: make(chan struct{})}
	errs := make(chan error, 1)
	go func() {
		errs <- host.runOwnedAgentTurn(host.runContext(), func(turnCtx context.Context) error {
			_, err := client.StreamTurn(turnCtx, &agent.TurnRequest{Model: "test/model"}, nil, nil)
			return err
		})
	}()
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("child stream never started")
	}
	host.shutdown()
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("shutdown did not cancel the owned child run")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not return the owned child run")
	}
	if calls, cancels := client.snapshot(); calls != 1 || cancels != 1 {
		t.Fatalf("client calls/cancels = %d/%d, want 1/1", calls, cancels)
	}
}

func TestOwnedAgentTurnDefersIdleCompaction(t *testing.T) {
	host, _, _, _, _ := newCompactHost(t, 1)
	result := make(chan sdk.CompactionResult, 1)
	failure := make(chan error, 1)
	if err := host.runOwnedAgentTurn(context.Background(), func(ctx context.Context) error {
		host.requestCompact(ctx, sdk.CompactOptions{
			OnComplete: func(res sdk.CompactionResult) { result <- res },
			OnError:    func(err error) { failure <- err },
		})
		select {
		case res := <-result:
			t.Fatalf("idle compaction completed during the owned agent turn: %+v", res)
		case err := <-failure:
			t.Fatalf("idle compaction failed during the owned agent turn: %v", err)
		case <-time.After(200 * time.Millisecond):
		}
		return nil
	}); err != nil {
		t.Fatalf("runOwnedAgentTurn: %v", err)
	}
	select {
	case <-result:
	case err := <-failure:
		t.Fatalf("deferred compaction failed after the owned turn: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("deferred compaction never settled after the owned turn")
	}
}

func TestPreCanceledAgentCommandCreatesNoArtifacts(t *testing.T) {
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"id\":\"gen_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"should not run\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	cwd := t.TempDir()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	deps := wiringTestDeps(t.TempDir())
	deps.Context = canceled
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Stdin = strings.NewReader("/agent reader hello\n/quit\n")
	deps.Client = openrouter.New(srv.URL, "sk-test", srv.Client())
	deps.Config = testConfig(t, cwd)
	deps.Store = wiringStore(t)
	deps.Bundle = agentBundle(map[string]string{"reader": "---\ndescription: reader\n---\nchild body"})
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("pre-canceled command produced %d network requests", got)
	}
	if paths := childSessionPaths(t, deps.Store.Root()); len(paths) != 0 {
		t.Fatalf("pre-canceled command created child sessions: %v", paths)
	}
	parent := parentSessionPath(t, deps.Store.Root())
	loader, err := session.LoadWithOptions(parent, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range loader.Entries() {
		if custom, ok := entry.(*session.CustomEntry); ok && custom.CustomType == subagentResultCustomType {
			t.Fatalf("pre-canceled command persisted a subagent result: %s", custom.Data)
		}
	}
}

type ownedSignalCommandContext struct {
	fakeCommandContext
	signal context.Context
}

func (c *ownedSignalCommandContext) Signal() context.Context { return c.signal }

func TestAgentDirectCommandOwnsTurnOwnership(t *testing.T) {
	host, _, _ := newOwnedTestHost(t)
	catalog := testDefinitionCatalog()
	client := &ownershipTextClient{started: make(chan struct{})}
	exec, _ := newTestAgentExecutor(t, catalog, client)
	signal, cancelSignal := context.WithCancel(context.Background())
	defer cancelSignal()
	signal = withHostTurnCancel(signal, cancelSignal)
	command := &ownedSignalCommandContext{signal: signal}
	var out bytes.Buffer
	errs := make(chan error, 1)
	go func() {
		errs <- handleAgentCommand(command, catalog, exec, host, &out, "reader hello")
	}()
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("direct /agent never reached the child model")
	}
	host.turnMu.Lock()
	active := host.turnActive
	host.turnMu.Unlock()
	if !active {
		t.Fatal("direct /agent ran without owning the parent turn")
	}
	host.abort(signal)
	var err error
	select {
	case err = <-errs:
	case <-time.After(5 * time.Second):
		t.Fatal("direct /agent did not return after abort")
	}
	if err == nil {
		t.Fatal("aborted direct /agent reported success")
	}
	loader, err := session.LoadWithOptions(host.snapshot().path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range loader.Entries() {
		if custom, ok := entry.(*session.CustomEntry); ok && custom.CustomType == subagentResultCustomType {
			t.Fatalf("aborted direct /agent persisted a subagent result: %s", custom.Data)
		}
	}
}

func TestAgentDirectCommandPreCanceledSignalSkipsChild(t *testing.T) {
	host, _, _ := newOwnedTestHost(t)
	catalog := testDefinitionCatalog()
	client := &ownershipTextClient{started: make(chan struct{})}
	exec, _ := newTestAgentExecutor(t, catalog, client)
	signal, cancelSignal := context.WithCancel(context.Background())
	cancelSignal()
	command := &ownedSignalCommandContext{signal: signal}
	var out bytes.Buffer
	err := handleAgentCommand(command, catalog, exec, host, &out, "reader hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if calls, _ := client.snapshot(); calls != 0 {
		t.Fatalf("pre-canceled direct /agent called the model %d times", calls)
	}
}
