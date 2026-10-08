package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
)

type failingCompactionRecorder struct {
	agent.Recorder
	err error
}

func (r *failingCompactionRecorder) appendCompaction(e *agent.CompactionEntry) error {
	return r.err
}

func queueCompactionEntry(t *testing.T, fixture *bridgeFixture, failure error) {
	t.Helper()
	preparer, _ := forcedCompactFixture(t)
	preparer.entries = []*agent.CompactionEntry{{Summary: []byte("queued summary"), TokensBefore: 100}}
	fixture.bridge.rd.preparer = preparer
	fixture.bridge.rd.recorder = &failingCompactionRecorder{
		Recorder: fixture.bridge.rd.recorder,
		err:      failure,
	}
}

func TestPersistErrorUnwrapsSinkFailure(t *testing.T) {
	inner := errors.New("disk on fire")
	err := error(&persistError{err: inner})
	if !errors.Is(err, inner) {
		t.Fatal("persistError must unwrap to the sink failure")
	}
	var pe *persistError
	if !errors.As(err, &pe) {
		t.Fatal("errors.As must detect persistError")
	}
}

type emptyErrorStopClient struct {
	message string
}

func (c *emptyErrorStopClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	return &agent.AssistantMessage{
		Role:         string(agent.RoleAssistant),
		StopReason:   "error",
		ErrorMessage: c.message,
	}, nil
}

func TestBridgeEmptyErrorStopWithoutStreamShowsNotice(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.bridge.rd.client = &emptyErrorStopClient{message: "provider exploded"}
	fixture.bridge.handle("quiet error")
	text := bridgeFrameText(t, fixture)
	if strings.Contains(text, "Error:") {
		t.Fatalf("unopened scope must not render an error block:\n%s", text)
	}
	if strings.Count(text, "provider exploded") != 1 {
		t.Fatalf("notice must carry the assistant error once:\n%s", text)
	}
}

func TestBridgeEmptyErrorMessageShowsReturnedError(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	fixture.bridge.rd.client = &emptyErrorStopClient{}
	fixture.bridge.handle("nameless error")
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "agent: provider error") {
		t.Fatalf("empty assistant message must fall back to the returned error:\n%s", text)
	}
}

func TestBridgePersistFailureAfterEmptyStopShowsReturnedError(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{emptyStopAssistant()}, nil)
	queueCompactionEntry(t, fixture, errors.New("persist compaction entry: disk on fire"))
	fixture.bridge.handle("empty stop persist failure")
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "persist compaction entry: disk on fire") {
		t.Fatalf("persist failure after an empty stop must surface the returned error:\n%s", text)
	}
}

func TestBridgePersistCompactionFailurePreservesAnswerAndWarns(t *testing.T) {
	answer := textStop("persisted answer")
	answer.Usage = agent.Usage{Input: 999, Output: 7}
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{answer}, nil)
	queueCompactionEntry(t, fixture, errors.New("persist compaction entry: disk on fire"))
	fixture.bridge.handle("persist me")
	text := bridgeFrameText(t, fixture)
	if got := strings.Count(text, "persisted answer"); got != 1 {
		t.Fatalf("authoritative answer rendered %d times, want 1:\n%s", got, text)
	}
	if !strings.Contains(text, "persist compaction entry: disk on fire") {
		t.Fatalf("persist failure was silently discarded:\n%s", text)
	}
	if strings.Contains(text, "Error:") {
		t.Fatalf("valid content must not be relabeled as an error block:\n%s", text)
	}
	if !strings.Contains(text, "↑999") {
		t.Fatalf("usage summary missing from the footer:\n%s", text)
	}
	asst, ok := authoritativeSince(fixture.bridge.history, 0)
	if !ok || asst.StopReason != "stop" {
		t.Fatalf("authoritative = %+v %v, want stop", asst, ok)
	}
	if len(fixture.bridge.history) != 2 {
		t.Fatalf("history length = %d, want user plus assistant", len(fixture.bridge.history))
	}
	transcript, err := os.ReadFile(fixture.bridge.rd.sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(transcript), `"type":"compaction"`) {
		t.Fatalf("failed compaction entry must not be recorded:\n%s", transcript)
	}
	if !strings.Contains(string(transcript), "persisted answer") {
		t.Fatalf("assistant answer must stay recorded:\n%s", transcript)
	}
}

func TestBridgePostResponseErrorWarnsAfterErrorStop(t *testing.T) {
	authoritative := errorAssistantWithUsage("partial answer text", "error", "provider exploded", 12)
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{authoritative}, nil)
	queueCompactionEntry(t, fixture, errors.New("persist compaction entry: disk on fire"))
	fixture.bridge.handle("double failure")
	text := bridgeFrameText(t, fixture)
	if !strings.Contains(text, "provider exploded") {
		t.Fatalf("assistant error message missing from the frame:\n%s", text)
	}
	if !strings.Contains(text, "persist compaction entry: disk on fire") {
		t.Fatalf("post-response error was silently discarded:\n%s", text)
	}
}

func TestBridgeEmptyErrorWithPersistFailureShowsBothOnce(t *testing.T) {
	emptyErr := errorAssistantWithUsage("", "error", "provider exploded", 12)
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{emptyErr}, nil)
	queueCompactionEntry(t, fixture, errors.New("persist compaction entry: disk on fire"))
	fixture.bridge.handle("empty error plus persist failure")
	text := bridgeFrameText(t, fixture)
	if got := strings.Count(text, "provider exploded"); got != 1 {
		t.Fatalf("assistant error rendered %d times, want 1:\n%s", got, text)
	}
	if got := strings.Count(text, "persist compaction entry: disk on fire"); got != 1 {
		t.Fatalf("persist failure rendered %d times, want 1:\n%s", got, text)
	}
	if strings.Contains(text, "Error:") {
		t.Fatalf("unopened scope must not render an error block:\n%s", text)
	}
}
