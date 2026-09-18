package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
)

type cancelBlockedClient struct {
	entered chan struct{}
	stream  string
	once    sync.Once
}

func (c *cancelBlockedClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	if c.stream != "" && onText != nil {
		onText(c.stream)
	}
	c.once.Do(func() { close(c.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func runCancelWithPersistFailure(t *testing.T, stream string) (fixture *bridgeFixture, text string) {
	t.Helper()
	fixture = newBridgeFixture(t, nil, nil)
	client := &cancelBlockedClient{entered: make(chan struct{}), stream: stream}
	fixture.bridge.rd.client = client
	queueCompactionEntry(t, fixture, errors.New("disk on fire"))
	turns := make(chan struct{}, 4)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	fixture.bridge.submit("cancel me")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not start")
	}
	fixture.bridge.interrupt()
	select {
	case <-turns:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled turn did not finish")
	}
	return fixture, bridgeFrameText(t, fixture)
}

func TestBridgeCancelWithPersistFailureWarnsAfterStream(t *testing.T) {
	_, text := runCancelWithPersistFailure(t, "partial answer")
	if !strings.Contains(text, "partial answer") {
		t.Fatalf("frame lost the streamed content:\n%s", text)
	}
	if !strings.Contains(text, "Operation aborted") {
		t.Fatalf("frame missing the abort marker:\n%s", text)
	}
	if got := strings.Count(text, "persist compaction entry: disk on fire"); got != 1 {
		t.Fatalf("persist failure rendered %d times, want 1:\n%s", got, text)
	}
	if strings.Contains(text, "interrupted") {
		t.Fatalf("opened scope must not render the interrupted notice:\n%s", text)
	}
}

func TestBridgeCancelWithPersistFailureWarnsWithoutStream(t *testing.T) {
	_, text := runCancelWithPersistFailure(t, "")
	if !strings.Contains(text, "interrupted") {
		t.Fatalf("frame missing the interrupted notice:\n%s", text)
	}
	if got := strings.Count(text, "persist compaction entry: disk on fire"); got != 1 {
		t.Fatalf("persist failure rendered %d times, want 1:\n%s", got, text)
	}
	if strings.Contains(text, "Operation aborted") {
		t.Fatalf("unopened scope must not render an aborted block:\n%s", text)
	}
}
