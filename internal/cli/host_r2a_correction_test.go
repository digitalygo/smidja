package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/contextmanager"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/subagent"
	"github.com/digitalygo/smidja/sdk"
)

type gatedCompactSelector struct {
	entered      chan struct{}
	release      chan struct{}
	ignoreCancel bool

	mu    sync.Mutex
	calls int
}

func newGatedCompactSelector() *gatedCompactSelector {
	return &gatedCompactSelector{
		entered: make(chan struct{}, 16),
		release: make(chan struct{}),
	}
}

func (s *gatedCompactSelector) Select(ctx context.Context, req subagent.SelectionRequest) (subagent.Selection, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	s.entered <- struct{}{}
	if s.ignoreCancel {
		<-s.release
	} else {
		select {
		case <-s.release:
		case <-ctx.Done():
			return subagent.Selection{}, ctx.Err()
		}
	}
	if len(req.Candidates) == 0 {
		return subagent.Selection{}, nil
	}
	return subagent.Selection{KeptIDs: []string{req.Candidates[0].Ref}}, nil
}

func (s *gatedCompactSelector) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *gatedCompactSelector) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-s.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("selector was never called")
	}
}

func assertNoCompactionEntry(t *testing.T, sess *session.Session) {
	t.Helper()
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range loader.Entries() {
		if _, ok := entry.(*session.CompactionEntry); ok {
			t.Fatal("a settled compaction job persisted a late entry")
		}
	}
}

func awaitFailure(t *testing.T, failure <-chan error) error {
	t.Helper()
	select {
	case err := <-failure:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("expected OnError was not invoked")
		return nil
	}
}

func assertNoExtraFailure(t *testing.T, failure <-chan error, result <-chan sdk.CompactionResult) {
	t.Helper()
	select {
	case err := <-failure:
		t.Fatalf("duplicate OnError callback: %v", err)
	case res := <-result:
		t.Fatalf("unexpected OnComplete callback: %+v", res)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHostIdleCompactSettledOnShutdownDoesNotPersistLateResult(t *testing.T) {
	selector := newGatedCompactSelector()
	selector.ignoreCancel = true
	host, sess, _, _, _ := newCompactHostWithSelector(t, 1, selector)
	failure := make(chan error, 2)
	result := make(chan sdk.CompactionResult, 2)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnComplete: func(res sdk.CompactionResult) { result <- res },
		OnError:    func(err error) { failure <- err },
	})
	selector.waitEntered(t)
	host.shutdown()
	if err := awaitFailure(t, failure); !errors.Is(err, errHostClosed) {
		t.Fatalf("error = %v, want errHostClosed", err)
	}
	close(selector.release)
	assertNoExtraFailure(t, failure, result)
	host.waitCompacts()
	assertNoCompactionEntry(t, sess)
	if selector.callCount() != 1 {
		t.Fatalf("selector calls = %d, want 1", selector.callCount())
	}
}

func TestHostIdleCompactSelectorCanceledAndJoinedOnShutdown(t *testing.T) {
	selector := newGatedCompactSelector()
	host, sess, _, _, _ := newCompactHostWithSelector(t, 1, selector)
	failure := make(chan error, 2)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnError: func(err error) { failure <- err },
	})
	selector.waitEntered(t)
	host.shutdown()
	if err := awaitFailure(t, failure); !errors.Is(err, errHostClosed) {
		t.Fatalf("error = %v, want errHostClosed", err)
	}
	done := make(chan struct{})
	go func() {
		host.waitCompacts()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a canceled selector was not joined by waitCompacts")
	}
	assertNoCompactionEntry(t, sess)
}

func TestHostIdleCompactSettledOnSessionChangeDoesNotPersistLateResult(t *testing.T) {
	selector := newGatedCompactSelector()
	selector.ignoreCancel = true
	host, sess, _, _, _ := newCompactHostWithSelector(t, 1, selector)
	failure := make(chan error, 2)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnError: func(err error) { failure <- err },
	})
	selector.waitEntered(t)

	other := wiringStore(t)
	sessB, err := other.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()
	host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), t.TempDir(), "")
	if err := awaitFailure(t, failure); !errors.Is(err, errHostStaleSession) {
		t.Fatalf("error = %v, want errHostStaleSession", err)
	}
	close(selector.release)
	host.waitCompacts()
	assertNoCompactionEntry(t, sess)
	assertNoCompactionEntry(t, sessB)
}

func TestHostExplicitCompactStaleGenerationDoesNotPersist(t *testing.T) {
	selector := newGatedCompactSelector()
	selector.ignoreCancel = true
	host, sessA, adapter, history, entryIDs := newCompactHostWithSelector(t, 1, selector)
	failure := make(chan error, 2)
	host.beginTurn()
	defer host.endTurn()
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnError: func(err error) { failure <- err },
	})
	type prepareOutcome struct {
		result agent.ContextResult
		err    error
	}
	prepared := make(chan prepareOutcome, 1)
	go func() {
		res, err := adapter.Prepare(context.Background(), agent.ContextRequest{System: "system", Messages: history, EntryIDs: entryIDs})
		prepared <- prepareOutcome{result: res, err: err}
	}()
	selector.waitEntered(t)

	other := wiringStore(t)
	sessB, err := other.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()
	host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), t.TempDir(), "")
	close(selector.release)
	outcome := <-prepared
	if outcome.err != nil {
		t.Fatalf("Prepare: %v", outcome.err)
	}
	if outcome.result.Compacted {
		t.Fatal("explicit compaction persisted for a stale generation")
	}
	if err := awaitFailure(t, failure); !errors.Is(err, errHostStaleSession) {
		t.Fatalf("error = %v, want errHostStaleSession", err)
	}
	assertNoCompactionEntry(t, sessA)
	assertNoCompactionEntry(t, sessB)
}

func TestHostPendingCompactSettledByShutdown(t *testing.T) {
	host, sess, adapter, _, _ := newCompactHost(t, 1)
	failure := make(chan error, 2)
	host.beginTurn()
	if !adapter.requestCompact(sdk.CompactOptions{OnError: func(err error) { failure <- err }}) {
		t.Fatal("pending request was not accepted")
	}
	host.shutdown()
	if err := awaitFailure(t, failure); !errors.Is(err, errHostCompactCanceled) {
		t.Fatalf("error = %v, want errHostCompactCanceled", err)
	}
	host.endTurn()
	assertNoCompactionEntry(t, sess)
}

func TestHostConcurrentCompactRequestsSettleExactlyOnce(t *testing.T) {
	host, _, _, _, _ := newCompactHost(t, 1)
	var callbacks atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			host.beginTurn()
			host.endTurn()
		}
	}()
	const workers = 6
	const requests = 12
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requests; j++ {
				host.requestCompact(context.Background(), sdk.CompactOptions{
					OnComplete: func(sdk.CompactionResult) { callbacks.Add(1) },
					OnError:    func(error) { callbacks.Add(1) },
				})
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
	host.shutdown()
	host.waitCompacts()
	if got := callbacks.Load(); got != workers*requests {
		t.Fatalf("callbacks = %d, want exactly %d", got, workers*requests)
	}
}

func TestHostContextSnapshotStaysBoundToCapturedGeneration(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessA.Close()
	sessB, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()

	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(sessA, &sessionRecorder{sessA}, sessA.ID(), sessA.Path(), cwd, "alpha")
	host.setMessages([]*agent.Message{
		{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"message-a"`)}},
		{Assistant: &agent.AssistantMessage{Role: string(agent.RoleAssistant), Usage: agent.Usage{Input: 25}}},
	})
	host.setSystem("system-a")
	host.setWindow(100)
	host.setModel(models.NewRegistry(), "model-a", "model-a", "provider-a")
	ctxA := host.context()
	viewA := ctxA.SessionManager()

	host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), cwd, "beta")
	host.setMessages([]*agent.Message{
		{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"message-b"`)}},
	})
	host.setSystem("system-b")
	host.setWindow(200)
	host.setModel(models.NewRegistry(), "model-b", "model-b", "provider-b")

	if viewA.ID() != sessA.ID() || viewA.Path() != sessA.Path() || viewA.Name() != "alpha" {
		t.Fatalf("captured view = %q/%q/%q, want session A", viewA.ID(), viewA.Path(), viewA.Name())
	}
	messagesA := viewA.Messages()
	if len(messagesA) != 2 || !strings.Contains(messageText(messagesA[0]), "message-a") {
		t.Fatalf("captured messages = %+v, want session A history", messagesA)
	}
	for _, message := range messagesA {
		if strings.Contains(messageText(message), "message-b") {
			t.Fatal("captured session view returned content from the replacement session")
		}
	}
	if ctxA.Cwd() != cwd {
		t.Fatalf("cwd = %q, want %q", ctxA.Cwd(), cwd)
	}
	if ctxA.Model() == nil || ctxA.Model().ID != "model-a" {
		t.Fatalf("model = %+v, want model-a", ctxA.Model())
	}
	if registry := ctxA.ModelRegistry(); registry == nil || registry.Model() == nil || registry.Model().ID != "model-a" {
		t.Fatalf("registry model = %+v, want model-a", registry)
	}
	if ctxA.SystemPrompt() != "system-a" {
		t.Fatalf("system = %q, want system-a", ctxA.SystemPrompt())
	}
	usageA := ctxA.ContextUsage()
	if usageA == nil || usageA.ContextWindow != 100 || usageA.Tokens == nil || *usageA.Tokens != 25 {
		t.Fatalf("usage = %+v, want session A usage", usageA)
	}

	ctxB := host.context()
	if view := ctxB.SessionManager(); view == nil || view.ID() != sessB.ID() {
		t.Fatalf("new context view = %+v, want session B", view)
	}
	if ctxB.Model() == nil || ctxB.Model().ID != "model-b" || ctxB.SystemPrompt() != "system-b" {
		t.Fatalf("new context model/system = %+v/%q, want session B", ctxB.Model(), ctxB.SystemPrompt())
	}
	if usageB := ctxB.ContextUsage(); usageB == nil || usageB.ContextWindow != 200 || usageB.Tokens != nil {
		t.Fatalf("new usage = %+v, want session B usage", usageB)
	}
	bound, ok := ctxA.(extensions.SignalBoundContext)
	if !ok {
		t.Fatal("host context does not support signal binding")
	}
	withSignal := bound.WithSignal(context.Background())
	if withSignal.SessionManager() == nil || withSignal.SessionManager().ID() != sessB.ID() {
		t.Fatal("WithSignal must bind a fresh coherent snapshot")
	}
}

func messageText(message sdk.Message) string {
	var builder strings.Builder
	for _, block := range message.Content {
		builder.WriteString(block.Text)
	}
	return builder.String()
}

func TestHostMutationsRejectStaleClosedAndLateWrites(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessA.Close()
	sessB, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()

	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	delivered := make(chan string, 8)
	host.setLifecycle(hostLifecycle{
		entry: func(customType string, _ json.RawMessage) { delivered <- customType },
	})
	host.bindSession(sessA, &sessionRecorder{sessA}, sessA.ID(), sessA.Path(), cwd, "alpha")
	stale := host.snapshot()
	host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), cwd, "beta")

	if err := host.appendEntry(stale, "note", map[string]int{"a": 1}); !errors.Is(err, errHostStaleSession) {
		t.Fatalf("stale AppendEntry = %v, want errHostStaleSession", err)
	}
	if err := host.setSessionName(stale, "stale-name"); !errors.Is(err, errHostStaleSession) {
		t.Fatalf("stale SetSessionName = %v, want errHostStaleSession", err)
	}
	if err := host.labelEntry(stale, "entry", "tag"); !errors.Is(err, errHostStaleSession) {
		t.Fatalf("stale LabelEntry = %v, want errHostStaleSession", err)
	}
	select {
	case name := <-delivered:
		t.Fatalf("stale mutation delivered a live UI update: %q", name)
	case <-time.After(100 * time.Millisecond):
	}

	current := host.snapshot()
	if err := host.appendEntry(current, "live", map[string]int{"b": 2}); err != nil {
		t.Fatalf("live AppendEntry: %v", err)
	}
	select {
	case name := <-delivered:
		if name != "live" {
			t.Fatalf("delivered %q, want live", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("live AppendEntry did not deliver a UI update")
	}

	host.shutdown()
	if err := host.appendEntry(current, "late", nil); !errors.Is(err, errHostClosed) {
		t.Fatalf("late AppendEntry = %v, want errHostClosed", err)
	}
	if err := host.setSessionName(current, "late-name"); !errors.Is(err, errHostClosed) {
		t.Fatalf("late SetSessionName = %v, want errHostClosed", err)
	}
	if err := host.labelEntry(current, "entry", "tag"); !errors.Is(err, errHostClosed) {
		t.Fatalf("late LabelEntry = %v, want errHostClosed", err)
	}
	select {
	case name := <-delivered:
		t.Fatalf("late mutation delivered a live UI update: %q", name)
	case <-time.After(100 * time.Millisecond):
	}

	loader, err := session.LoadWithOptions(sessB.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	liveCount := 0
	for _, entry := range loader.Entries() {
		custom, ok := entry.(*session.CustomEntry)
		if !ok {
			continue
		}
		if custom.CustomType == "late" {
			t.Fatal("a late mutation reached the session file")
		}
		if custom.CustomType == "live" {
			liveCount++
		}
	}
	if liveCount != 1 {
		t.Fatalf("live custom entries = %d, want 1", liveCount)
	}
}

func TestHostConcurrentMutationsStayOnCapturedSession(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessA.Close()
	sessB, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()

	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	type delivery struct {
		customType string
		sessionID  string
	}
	deliveries := make(chan delivery, 512)
	jobs := make(chan func(), 4096)
	workerDone := make(chan struct{})
	host.setLifecycle(hostLifecycle{
		entry: func(customType string, _ json.RawMessage) {
			current := host.snapshot()
			sessionID := ""
			if current != nil {
				sessionID = current.id
			}
			deliveries <- delivery{customType: customType, sessionID: sessionID}
		},
		dispatch: func(job func()) bool {
			jobs <- job
			return true
		},
	})
	go func() {
		defer close(workerDone)
		for job := range jobs {
			job()
		}
	}()
	host.bindSession(sessA, &sessionRecorder{sessA}, sessA.ID(), sessA.Path(), cwd, "alpha")
	handleA := host.snapshot()

	const workers = 4
	const writes = 20
	var successes atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for j := 0; j < writes; j++ {
				err := host.appendEntry(handleA, fmt.Sprintf("note-%d", index), map[string]int{"n": j})
				if err == nil {
					successes.Add(1)
					continue
				}
				if !errors.Is(err, errHostStaleSession) && !errors.Is(err, errHostClosed) {
					t.Errorf("append error = %v, want stale or closed", err)
					return
				}
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		recorderA := &sessionRecorder{sessA}
		recorderB := &sessionRecorder{sessB}
		for i := 0; i < 40; i++ {
			jobs <- func() { host.bindSession(sessA, recorderA, sessA.ID(), sessA.Path(), cwd, "alpha") }
			jobs <- func() { host.bindSession(sessB, recorderB, sessB.ID(), sessB.Path(), cwd, "beta") }
		}
	}()
	wg.Wait()
	close(jobs)
	<-workerDone
	host.shutdown()
	host.waitCompacts()
	close(deliveries)

	var got []delivery
	for entry := range deliveries {
		got = append(got, entry)
	}
	for _, entry := range got {
		if entry.sessionID != sessA.ID() {
			t.Fatalf("delivery %q was applied to session %q, want session A %q", entry.customType, entry.sessionID, sessA.ID())
		}
	}
	if len(got) > int(successes.Load()) {
		t.Fatalf("deliveries = %d, want at most %d successful appends", len(got), successes.Load())
	}
	loaderB, err := session.LoadWithOptions(sessB.Path(), session.LoadOptions{Strict: true})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err == nil {
		for _, entry := range loaderB.Entries() {
			if custom, ok := entry.(*session.CustomEntry); ok && strings.HasPrefix(custom.CustomType, "note-") {
				t.Fatal("an append intended for session A reached session B")
			}
		}
	}
}

func TestHostCompactCallbackPanicIsolation(t *testing.T) {
	host, _, _, _, _ := newCompactHost(t, 1)
	result := make(chan struct{}, 2)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnComplete: func(sdk.CompactionResult) {
			result <- struct{}{}
			panic("complete boom")
		},
	})
	select {
	case <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("OnComplete was not invoked")
	}
	host.waitCompacts()
	if host.callbackPanics.Load() == 0 {
		t.Fatal("a panicking compaction callback was not reported")
	}
	select {
	case <-result:
		t.Fatal("OnComplete ran twice")
	case <-time.After(100 * time.Millisecond):
	}

	failure := make(chan struct{}, 2)
	emptyStore := wiringStore(t)
	emptySess, err := emptyStore.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer emptySess.Close()
	cmCfg := contextmanager.Config{Enabled: true, ContextWindowTokens: 1000, KeepRecentMessages: 1}
	live, err := contextmanager.New(cmCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	host.attachPreparer(newContextPreparerAdapter(live, cmCfg))
	host.setMessages(nil)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnError: func(error) {
			failure <- struct{}{}
			panic("error boom")
		},
	})
	select {
	case <-failure:
	case <-time.After(5 * time.Second):
		t.Fatal("OnError was not invoked")
	}
	host.waitCompacts()
	select {
	case <-failure:
		t.Fatal("OnError ran twice")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHostCompactCallbackReentrancyCompletes(t *testing.T) {
	selector := newGatedCompactSelector()
	host, _, adapter, _, _ := newCompactHostWithSelector(t, 1, selector)
	selector.ignoreCancel = true
	var callbacks atomic.Int64
	other := wiringStore(t)
	sessB, err := other.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()
	feedback := make(chan error, 2)
	host.beginTurn()
	if !adapter.requestCompact(sdk.CompactOptions{
		OnError: func(err error) {
			callbacks.Add(1)
			_ = host.context().Model()
			if view := host.context().SessionManager(); view != nil {
				_ = view.Messages()
			}
			_ = host.context().SystemPrompt()
			host.requestCompact(context.Background(), sdk.CompactOptions{})
			host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), t.TempDir(), "beta")
			host.shutdown()
			feedback <- err
		},
	}) {
		t.Fatal("pending request was not accepted")
	}
	host.shutdown()
	if err := awaitFailure(t, feedback); !errors.Is(err, errHostCompactCanceled) {
		t.Fatalf("error = %v, want errHostCompactCanceled", err)
	}
	host.endTurn()
	if got := callbacks.Load(); got != 1 {
		t.Fatalf("callbacks = %d, want exactly 1", got)
	}
	assertNoExtraFailure(t, feedback, make(chan sdk.CompactionResult))
}

func TestHostIdleCompactPersistFailureReportsOnce(t *testing.T) {
	host, _, _, _, _ := newCompactHost(t, 1)
	host.mu.Lock()
	broken := *host.handle
	broken.recorder = nil
	host.handle = &broken
	host.mu.Unlock()
	failure := make(chan error, 2)
	result := make(chan sdk.CompactionResult, 2)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnComplete: func(res sdk.CompactionResult) { result <- res },
		OnError:    func(err error) { failure <- err },
	})
	host.waitCompacts()
	if err := awaitFailure(t, failure); !errors.Is(err, errHostClosed) {
		t.Fatalf("error = %v, want errHostClosed", err)
	}
	assertNoExtraFailure(t, failure, result)
}

func TestHostExecHonorsOwnedRunCancellation(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	host := newHostRuntime(context.Background(), dir, nil, extensions.NewToolCatalog())
	runCtx, cancelRun := context.WithCancel(context.Background())
	host.bindRunContext(runCtx, cancelRun)
	options := host.hostOptions()
	api := extensions.NewAPI(extensions.APIOptions{Host: options})
	cancelRun()
	res, err := options.Exec(context.Background(), "/bin/sh", []string{"-c", "touch " + marker}, sdk.ExecOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("direct Exec error = %v, want context.Canceled", err)
	}
	if res != nil {
		t.Fatalf("direct Exec result = %+v, want nil", res)
	}
	if res, err = api.Exec("/bin/sh", []string{"-c", "touch " + marker}, sdk.ExecOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("api Exec error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("a canceled owned context started the command: stat(%s) = %v", marker, statErr)
	}

	second := newHostRuntime(context.Background(), dir, nil, extensions.NewToolCatalog())
	secondCtx, secondCancel := context.WithCancel(context.Background())
	second.bindRunContext(secondCtx, secondCancel)
	done := make(chan *sdk.ExecResult, 1)
	failures := make(chan error, 1)
	go func() {
		result, execErr := second.hostOptions().Exec(context.Background(), "/bin/sh", []string{"-c", "sleep 30"}, sdk.ExecOptions{Timeout: 20 * time.Second})
		if execErr != nil {
			failures <- execErr
			return
		}
		done <- result
	}()
	time.Sleep(100 * time.Millisecond)
	second.shutdown()
	select {
	case err := <-failures:
		t.Fatalf("running Exec failed: %v", err)
	case result := <-done:
		if !result.Killed {
			t.Fatalf("running Exec result = %+v, want killed by shutdown", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host shutdown did not cancel and join the running Exec")
	}
}

func TestContextPreparerExplicitHookSerialized(t *testing.T) {
	cmCfg := contextmanager.Config{Enabled: true, ContextWindowTokens: 1000, KeepRecentMessages: 1}
	live, err := contextmanager.New(cmCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter := newContextPreparerAdapter(live, cmCfg)
	hook := func(context.Context, agent.ContextRequest, sdk.CompactOptions) agent.ContextResult {
		return agent.ContextResult{}
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				adapter.setExplicit(hook)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = adapter.explicitHook()
			}
		}()
	}
	wg.Wait()
}

func assertSessionCustomTypes(t *testing.T, sess *session.Session, want map[string]int) {
	t.Helper()
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if errors.Is(err, os.ErrNotExist) {
		if len(want) != 0 {
			t.Fatalf("session %s has no file for custom entries %v", sess.ID(), want)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, entry := range loader.Entries() {
		if custom, ok := entry.(*session.CustomEntry); ok {
			got[custom.CustomType]++
		}
	}
	for customType, count := range want {
		if got[customType] != count {
			t.Fatalf("session %s custom %q = %d, want %d", sess.ID(), customType, got[customType], count)
		}
	}
	for customType, count := range got {
		if want[customType] != count {
			t.Fatalf("session %s unexpected custom %q = %d", sess.ID(), customType, count)
		}
	}
}

func TestHostStaleDeliveryDroppedAtSerialBoundary(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessA.Close()
	sessB, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()

	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	queued := make(chan func(), 8)
	delivered := make(chan string, 8)
	host.setLifecycle(hostLifecycle{
		dispatch: func(job func()) bool {
			queued <- job
			return true
		},
		entry: func(customType string, _ json.RawMessage) { delivered <- customType },
		name:  func(name string) { delivered <- "name:" + name },
	})
	host.bindSession(sessA, &sessionRecorder{sessA}, sessA.ID(), sessA.Path(), cwd, "alpha")
	handleA := host.snapshot()
	if err := host.appendEntry(handleA, "late-entry", nil); err != nil {
		t.Fatal(err)
	}
	if err := host.setSessionName(handleA, "renamed-a"); err != nil {
		t.Fatal(err)
	}
	host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), cwd, "beta")

	for i := 0; i < 2; i++ {
		select {
		case job := <-queued:
			job()
		case <-time.After(5 * time.Second):
			t.Fatal("a delivery was not serialized onto the lifecycle dispatcher")
		}
	}
	select {
	case name := <-delivered:
		t.Fatalf("a stale delivery reached the replaced session: %q", name)
	case <-time.After(100 * time.Millisecond):
	}

	handleB := host.snapshot()
	if err := host.appendEntry(handleB, "live-b", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-queued:
		job()
	case <-time.After(5 * time.Second):
		t.Fatal("a live delivery was not serialized onto the lifecycle dispatcher")
	}
	select {
	case customType := <-delivered:
		if customType != "live-b" {
			t.Fatalf("delivered %q, want live-b", customType)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a live delivery did not reach the current session")
	}
	assertSessionCustomTypes(t, sessA, map[string]int{"late-entry": 1})
	assertSessionCustomTypes(t, sessB, map[string]int{"live-b": 1})
}

func TestComposedContextGettersIsolateCoHandlers(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "alpha")
	host.setSystem("system-a")
	host.setWindow(100)
	host.setModel(models.NewRegistry(), "model-a", "model-a", "provider-a")
	host.setMessages([]*agent.Message{
		{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hello"`)}},
		{Assistant: &agent.AssistantMessage{
			Role: string(agent.RoleAssistant),
			Content: []agent.ContentBlock{{
				Type:      agent.BlockTypeToolCall,
				ID:        "call-1",
				Name:      "probe",
				Arguments: json.RawMessage(`{"x":1}`),
			}},
			Usage: agent.Usage{Input: 25, TotalTokens: 40, Cost: agent.Cost{Total: 1.5}},
		}},
	})

	type signalKey struct{}
	signal := context.WithValue(context.Background(), signalKey{}, "event-signal")
	mutator := &hostHookExtension{
		id: "cohandler-mutator",
		contextFn: func(_ int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			messages := ctx.SessionManager().Messages()
			messages[0].Content[0].Text = "mutated"
			messages[1].Content[0].Name = "mutated-call"
			messages[1].Content[0].Arguments[0] = '['
			messages[1].Usage.Input = 999
			messages[1].Usage.Cost.Total = 99
			sort.Slice(messages, func(i, j int) bool { return messages[i].Role > messages[j].Role })
			messages = append(messages, sdk.Message{Role: string(agent.RoleUser), Content: []sdk.Block{{Type: agent.BlockTypeText, Text: "injected"}}})
			if model := ctx.Model(); model != nil {
				model.ID = "mutated"
			}
			if usage := ctx.ContextUsage(); usage != nil {
				if usage.Tokens != nil {
					*usage.Tokens = 999
				}
				if usage.Percent != nil {
					*usage.Percent = 99
				}
			}
			return nil, nil
		},
	}
	var (
		observed       []sdk.Message
		observedModel  *sdk.Model
		observedUsage  *sdk.ContextUsage
		observedSignal context.Context
	)
	observer := &hostHookExtension{
		id: "cohandler-observer",
		contextFn: func(_ int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			observed = ctx.SessionManager().Messages()
			observedModel = ctx.Model()
			observedUsage = ctx.ContextUsage()
			observedSignal = ctx.Signal()
			return nil, nil
		},
	}
	registry := extensions.NewRegistry()
	if err := registry.Register(mutator); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(observer); err != nil {
		t.Fatal(err)
	}
	hookRuntime := extensions.NewRuntime(registry)
	hookRuntime.SetContext(func() sdk.HandlerContext { return host.context() })
	if _, err := hookRuntime.Dispatcher().Context(signal, agent.ContextRequest{
		Messages: []*agent.Message{{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"hello"`)}}},
	}); err != nil {
		t.Fatal(err)
	}

	if len(observed) != 2 {
		t.Fatalf("observed messages = %d, want 2", len(observed))
	}
	if observed[0].Content[0].Text != "hello" {
		t.Fatalf("co-handler message text = %q, want hello", observed[0].Content[0].Text)
	}
	if observed[1].Content[0].Name != "probe" {
		t.Fatalf("co-handler tool name = %q, want probe", observed[1].Content[0].Name)
	}
	if string(observed[1].Content[0].Arguments) != `{"x":1}` {
		t.Fatalf("co-handler arguments = %s, want the original JSON", observed[1].Content[0].Arguments)
	}
	if observed[1].Usage == nil || observed[1].Usage.Input != 25 || observed[1].Usage.Cost.Total != 1.5 {
		t.Fatalf("co-handler usage = %+v, want the original usage", observed[1].Usage)
	}
	if observedModel == nil || observedModel.ID != "model-a" {
		t.Fatalf("co-handler model = %+v, want model-a", observedModel)
	}
	if observedUsage == nil || observedUsage.Tokens == nil || *observedUsage.Tokens != 25 || observedUsage.Percent == nil || *observedUsage.Percent != 25 {
		t.Fatalf("co-handler usage = %+v, want the original tokens and percent", observedUsage)
	}
	if observedSignal != signal {
		t.Fatal("co-handlers must observe the per-event signal")
	}

	fresh := host.context()
	freshMessages := fresh.SessionManager().Messages()
	if len(freshMessages) != 2 || freshMessages[0].Content[0].Text != "hello" || freshMessages[1].Content[0].Name != "probe" {
		t.Fatalf("fresh context messages = %+v, want the unmutated state", freshMessages)
	}
	if string(freshMessages[1].Content[0].Arguments) != `{"x":1}` {
		t.Fatalf("fresh arguments = %s, want the original JSON", freshMessages[1].Content[0].Arguments)
	}
	if model := fresh.Model(); model == nil || model.ID != "model-a" {
		t.Fatalf("fresh model = %+v, want model-a", model)
	}
	freshUsage := fresh.ContextUsage()
	if freshUsage == nil || freshUsage.Tokens == nil || *freshUsage.Tokens != 25 || freshUsage.Percent == nil || *freshUsage.Percent != 25 {
		t.Fatalf("fresh usage = %+v, want the original tokens and percent", freshUsage)
	}
}

func TestHostIdleCompactCanceledSelectorDoesNotPersist(t *testing.T) {
	selector := newGatedCompactSelector()
	selector.ignoreCancel = true
	host, sess, _, _, _ := newCompactHostWithSelector(t, 1, selector)
	signal, cancel := context.WithCancel(context.Background())
	failure := make(chan error, 4)
	result := make(chan sdk.CompactionResult, 4)
	host.requestCompact(signal, sdk.CompactOptions{
		OnComplete: func(res sdk.CompactionResult) { result <- res },
		OnError:    func(err error) { failure <- err },
	})
	selector.waitEntered(t)
	cancel()
	close(selector.release)
	if err := awaitFailure(t, failure); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	host.waitCompacts()
	assertNoExtraFailure(t, failure, result)
	assertNoCompactionEntry(t, sess)
	if selector.callCount() != 1 {
		t.Fatalf("selector calls = %d, want 1", selector.callCount())
	}
	if err := sess.AppendEntry(&session.CustomEntry{CustomType: "proof", Data: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("the session was closed to force the cancellation: %v", err)
	}
}

func TestHostExplicitCompactCanceledContextDoesNotPersist(t *testing.T) {
	selector := newGatedCompactSelector()
	selector.ignoreCancel = true
	host, sessA, adapter, history, entryIDs := newCompactHostWithSelector(t, 1, selector)
	host.beginTurn()
	defer host.endTurn()
	failure := make(chan error, 4)
	if !adapter.requestCompact(sdk.CompactOptions{OnError: func(err error) { failure <- err }}) {
		t.Fatal("pending request was not accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	type prepareOutcome struct {
		result agent.ContextResult
		err    error
	}
	prepared := make(chan prepareOutcome, 1)
	go func() {
		res, err := adapter.Prepare(ctx, agent.ContextRequest{System: "system", Messages: history, EntryIDs: entryIDs})
		prepared <- prepareOutcome{result: res, err: err}
	}()
	selector.waitEntered(t)
	cancel()
	close(selector.release)
	outcome := <-prepared
	if outcome.err != nil {
		t.Fatalf("Prepare: %v", outcome.err)
	}
	if outcome.result.Compacted {
		t.Fatal("a canceled explicit compaction persisted an entry")
	}
	if err := awaitFailure(t, failure); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	assertNoCompactionEntry(t, sessA)
	if selector.callCount() != 1 {
		t.Fatalf("selector calls = %d, want 1", selector.callCount())
	}
}

func TestHostCompactDispatchAfterTeardownFailsTruthfully(t *testing.T) {
	host, _, _, _, _ := newCompactHost(t, 1)
	host.waitCompacts()
	failure := make(chan error, 2)
	result := make(chan sdk.CompactionResult, 2)
	host.requestCompact(context.Background(), sdk.CompactOptions{
		OnComplete: func(res sdk.CompactionResult) { result <- res },
		OnError:    func(err error) { failure <- err },
	})
	if err := awaitFailure(t, failure); !errors.Is(err, errHostClosed) {
		t.Fatalf("error = %v, want errHostClosed", err)
	}
	assertNoExtraFailure(t, failure, result)
}

func TestHostFallbackCallbacksAreOwnedAndJoined(t *testing.T) {
	host := newHostRuntime(context.Background(), t.TempDir(), nil, extensions.NewToolCatalog())
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	host.dispatchCallback(func() {
		close(started)
		<-release
		close(finished)
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the fallback callback did not start")
	}

	shutdownDone := make(chan struct{})
	go func() {
		host.shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited for a blocked fallback callback")
	}
	select {
	case <-finished:
		t.Fatal("a blocked fallback callback outlived shutdown")
	default:
	}

	joined := make(chan struct{})
	go func() {
		host.waitCallbacks()
		close(joined)
	}()
	select {
	case <-joined:
		t.Fatal("waitCallbacks returned while the fallback callback was blocked")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("waitCallbacks did not join the fallback callback")
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the released fallback callback did not finish")
	}
}

func TestHostCallbackCallingShutdownDoesNotSelfWait(t *testing.T) {
	host := newHostRuntime(context.Background(), t.TempDir(), nil, extensions.NewToolCatalog())
	done := make(chan struct{})
	host.dispatchCallback(func() {
		host.shutdown()
		close(done)
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a fallback callback calling Shutdown deadlocked")
	}
	host.waitCallbacks()
}

func TestHostCallbackDispatchRejectionFallsBack(t *testing.T) {
	host := newHostRuntime(context.Background(), t.TempDir(), nil, extensions.NewToolCatalog())
	var attempts atomic.Int64
	host.setLifecycle(hostLifecycle{dispatch: func(func()) bool {
		attempts.Add(1)
		return false
	}})
	executed := make(chan struct{}, 2)
	host.dispatchCallback(func() { executed <- struct{}{} })
	select {
	case <-executed:
	case <-time.After(5 * time.Second):
		t.Fatal("a rejected dispatch silently dropped the callback")
	}
	if attempts.Load() == 0 {
		t.Fatal("the lifecycle dispatcher was never offered the callback")
	}
	host.shutdown()
	host.waitCallbacks()
	select {
	case <-executed:
		t.Fatal("the fallback callback ran twice")
	case <-time.After(100 * time.Millisecond):
	}

	late := make(chan struct{}, 2)
	host.dispatchCallback(func() { late <- struct{}{} })
	select {
	case <-late:
	case <-time.After(5 * time.Second):
		t.Fatal("a callback requested after close was silently dropped")
	}
	select {
	case <-late:
		t.Fatal("a callback requested after close ran twice")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHostCallbackPanicsAreContainedAndCounted(t *testing.T) {
	host := newHostRuntime(context.Background(), t.TempDir(), nil, extensions.NewToolCatalog())
	queued := make(chan func(), 4)
	host.setLifecycle(hostLifecycle{dispatch: func(job func()) bool {
		queued <- job
		return true
	}})
	executed := make(chan struct{}, 2)
	host.dispatchCallback(func() {
		executed <- struct{}{}
		panic("dispatched boom")
	})
	select {
	case job := <-queued:
		job()
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatched callback was not handed to the lifecycle")
	}
	select {
	case <-executed:
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatched callback did not run")
	}
	if got := host.callbackPanics.Load(); got != 1 {
		t.Fatalf("panic counter = %d, want 1 for the dispatched callback", got)
	}

	host.setLifecycle(hostLifecycle{})
	fallback := make(chan struct{}, 2)
	host.dispatchCallback(func() {
		fallback <- struct{}{}
		panic("fallback boom")
	})
	select {
	case <-fallback:
	case <-time.After(5 * time.Second):
		t.Fatal("the fallback callback did not run")
	}
	host.shutdown()
	host.waitCallbacks()
	if got := host.callbackPanics.Load(); got != 2 {
		t.Fatalf("panic counter = %d, want 2", got)
	}
	select {
	case <-fallback:
		t.Fatal("a panicking callback ran twice")
	case <-time.After(100 * time.Millisecond):
	}
}
