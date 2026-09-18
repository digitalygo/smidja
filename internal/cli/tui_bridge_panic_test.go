package cli

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

const panicTestTimeout = 5 * time.Second

type panickingTurnClient struct {
	entered chan struct{}
	once    sync.Once
}

func (c *panickingTurnClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.once.Do(func() { close(c.entered) })
	panic("turn exploded")
}

type panickingTool struct{}

func (p *panickingTool) Name() string        { return "explode" }
func (p *panickingTool) Description() string { return "panics on exec" }
func (p *panickingTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}

func (p *panickingTool) Exec(ctx context.Context, args json.RawMessage) agent.Result {
	panic("tool exploded")
}

func waitRunnerExitRequested(t *testing.T, fixture *bridgeFixture) {
	t.Helper()
	select {
	case <-fixture.runner.Done():
	case <-time.After(panicTestTimeout):
		t.Fatal("worker panic did not request the runner exit")
	}
}

func waitBridgeJoined(t *testing.T, fixture *bridgeFixture) {
	t.Helper()
	joined := make(chan struct{})
	go func() {
		fixture.bridge.shutdown()
		fixture.bridge.wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(panicTestTimeout):
		t.Fatal("bridge did not join its worker after the panic")
	}
}

func assertPanicNotice(t *testing.T, fixture *bridgeFixture, leaked string) {
	t.Helper()
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, workerPanicNotice) {
		t.Fatalf("frame missing the sanitized panic notice:\n%s", text)
	}
	if strings.Contains(text, leaked) {
		t.Fatalf("raw panic value %q leaked into the surface:\n%s", leaked, text)
	}
}

func TestTuiLifecycleRecoversJobPanic(t *testing.T) {
	lifecycle := newTuiLifecycle()
	var failures atomic.Int32
	lifecycle.onPanic = func() { failures.Add(1) }
	lifecycle.enqueue(func() { panic("lifecycle boom") })
	deadline := time.Now().Add(panicTestTimeout)
	for failures.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	lifecycle.beginShutdown()
	joined := make(chan struct{})
	go func() {
		lifecycle.wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(panicTestTimeout):
		t.Fatal("worker did not close workerDone after a recovered panic")
	}
	if got := failures.Load(); got != 1 {
		t.Fatalf("onPanic calls = %d, want 1", got)
	}
	lifecycle.beginShutdown()
	lifecycle.wait()
}

func TestTUIBridgeCommandPanicExitsCleanly(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.commands.Register("boom", sdk.Command{
		Description: "panic on purpose",
		Handler: func(ctx sdk.CommandContext, args string) error {
			panic("command exploded")
		},
	})
	fixture.bridge.submit("/boom now")
	waitRunnerExitRequested(t, fixture)
	assertPanicNotice(t, fixture, "command exploded")
	waitBridgeJoined(t, fixture)
	if fixture.runner.Working() {
		t.Fatal("runner must be idle after the panic shutdown")
	}
}

func TestTUIBridgeTurnPanicExitsCleanly(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	client := &panickingTurnClient{entered: make(chan struct{})}
	fixture.bridge.rd.client = client
	fixture.bridge.submit("explode the turn")
	select {
	case <-client.entered:
	case <-time.After(panicTestTimeout):
		t.Fatal("turn did not start")
	}
	waitRunnerExitRequested(t, fixture)
	assertPanicNotice(t, fixture, "turn exploded")
	waitBridgeJoined(t, fixture)
	if len(fixture.bridge.history) != 0 {
		t.Fatalf("history length = %d, want 0 after the panicked turn", len(fixture.bridge.history))
	}
	if fixture.runner.Working() {
		t.Fatal("runner must be idle after the panic shutdown")
	}
}

func TestTUIBridgeToolPanicExitsCleanly(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{
		toolUseWithText("call_1", "explode", `{}`, "working on it"),
	}, []agent.Tool{&panickingTool{}})
	fixture.bridge.submit("use the exploding tool")
	waitRunnerExitRequested(t, fixture)
	assertPanicNotice(t, fixture, "tool exploded")
	waitBridgeJoined(t, fixture)
	if fixture.runner.Working() {
		t.Fatal("runner must be idle after the panic shutdown")
	}
}
