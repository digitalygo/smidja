package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

// TestConcurrentDispatchAndRegister runs many dispatches in parallel
// while new extensions register concurrently, then verifies per-dispatch
// handler ordering from the shared log. Run with -race; the assertions
// only rely on the snapshot semantics (each dispatch sees a consistent
// snapshot) and on per-handler ordering within one dispatch.
func TestConcurrentDispatchAndRegister(t *testing.T) {
	reg := NewRegistry()

	var logMu sync.Mutex
	var log []string
	record := func(line string) {
		logMu.Lock()
		log = append(log, line)
		logMu.Unlock()
	}

	// markerHandler returns a context handler that logs
	// "<marker>:<name>" where the marker travels inside the request
	// message, so the test can group log lines per dispatch.
	markerHandler := func(name string) sdk.ContextHandler {
		return func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			marker := ""
			if len(e.Messages) > 0 && len(e.Messages[0].Content) > 0 {
				marker = e.Messages[0].Content[0].Text
			}
			record(marker + ":" + name)
			return nil, nil
		}
	}
	toolHandler := func(name string) sdk.ToolCallHandler {
		return func(ctx sdk.HandlerContext, e sdk.ToolCallEvent) (*sdk.ToolCallDecision, error) {
			record("tool:" + name)
			return nil, nil
		}
	}

	reg.Register(ext("a").
		context(markerHandler("a1")).
		context(markerHandler("a2")).
		toolCall(toolHandler("aT")).
		build())
	reg.Register(ext("b").
		context(markerHandler("b1")).
		build())

	rt := NewRuntime(reg).SetAPI(func() sdk.API { return &stubAPI{} }).SetLogger(&recLogger{})
	d := rt.Dispatcher()

	const goroutines = 8
	const dispatchesPerGoroutine = 40
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			marker := fmt.Sprintf("g%d", g)
			req := agent.ContextRequest{Messages: []*agent.Message{{
				User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"` + marker + `"`)},
			}}}
			for i := 0; i < dispatchesPerGoroutine; i++ {
				if _, err := d.Context(context.Background(), req); err != nil {
					t.Errorf("goroutine %d context: %v", g, err)
					return
				}
				if _, err := d.ToolCall(context.Background(), "read", "call_1", json.RawMessage(`{}`)); err != nil {
					t.Errorf("goroutine %d tool call: %v", g, err)
					return
				}
			}
		}(g)
	}

	// Register new extensions while dispatches are in flight. They must
	// never corrupt a dispatch's snapshot, only affect later events.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := reg.Register(ext(fmt.Sprintf("late%d", i)).
				context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
					return nil, nil
				}).
				build())
			if err != nil {
				t.Errorf("register late%d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// Every dispatch of one goroutine must observe the handlers of a and
	// b in the exact registration order; late registrations add no
	// logging handlers. Tool calls must have run once per dispatch.
	byMarker := make(map[string][]string)
	toolCount := 0
	for _, line := range log {
		if strings.HasPrefix(line, "tool:") {
			toolCount++
			continue
		}
		marker, name, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("malformed log line %q", line)
		}
		byMarker[marker] = append(byMarker[marker], name)
	}

	wantSeq := []string{"a1", "a2", "b1"}
	for g := 0; g < goroutines; g++ {
		marker := fmt.Sprintf("g%d", g)
		seq := byMarker[marker]
		want := make([]string, 0, dispatchesPerGoroutine*len(wantSeq))
		for i := 0; i < dispatchesPerGoroutine; i++ {
			want = append(want, wantSeq...)
		}
		if !reflect.DeepEqual(seq, want) {
			t.Fatalf("goroutine %s order: got %d entries, want %d; first mismatch region: %v",
				marker, len(seq), len(want), seq)
		}
	}
	if toolCount != goroutines*dispatchesPerGoroutine {
		t.Fatalf("tool call dispatches = %d, want %d", toolCount, goroutines*dispatchesPerGoroutine)
	}
}

// TestConcurrentSetupAndDispatch runs the setup phase while dispatches
// are in flight, which must be race-free: Setup snapshots the extensions
// under the lock and runs extension code outside it, exactly like
// dispatch.
func TestConcurrentSetupAndDispatch(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("a").
		setup(func(sdk.API) error { return nil }).
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			return nil, nil
		}).
		build())
	reg.Register(ext("b").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			return nil, nil
		}).
		build())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		rt := NewRuntime(reg).SetAPI(func() sdk.API { return &stubAPI{} })
		if err := rt.Start(); err != nil {
			t.Errorf("start: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		d := NewRuntime(reg).Dispatcher()
		for i := 0; i < 200; i++ {
			if _, err := d.Context(context.Background(), agent.ContextRequest{}); err != nil {
				t.Errorf("context: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}
