package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func spawnTestClient(t *testing.T, behavior, clientFraming, helperFraming string) (*Client, string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "marker.log")
	cfg := SpawnConfig{
		Cfg: ServerConfig{
			Enabled: true,
			Command: os.Args[0],
			Args:    []string{"-test.run=TestHelperProcess"},
			Framing: clientFraming,
			Env: map[string]string{
				helperEnv:         "1",
				helperEnvBehavior: behavior,
				helperEnvFraming:  helperFraming,
				helperEnvMarker:   marker,
			},
			Restart: &RestartPolicy{
				MaxRestarts: 3,
				Window:      Duration(time.Minute),
				Backoff:     []Duration{Duration(5 * time.Millisecond), Duration(5 * time.Millisecond), Duration(5 * time.Millisecond)},
			},
		},
		ClientInfo:       ClientInfo{Name: "smidja", Version: "test"},
		ProtocolVersions: []string{preferredProtocolVersion, "2024-11-05"},
		ResolveEnv:       func(string) (string, bool) { return "", false },
	}
	c, err := Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Spawn(%s): %v", behavior, err)
	}
	t.Cleanup(func() { c.Close() })
	return c, marker
}

func TestHandshakeNegotiation(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 6 {
		t.Fatalf("ListTools returned %d tools, want 6", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Fatalf("first tool = %q, want echo", tools[0].Name)
	}
	if !json.Valid(tools[0].InputSchema) {
		t.Fatalf("echo inputSchema missing")
	}
}

func TestHandshakeVersionMismatchRejected(t *testing.T) {
	_, err := spawnRaw(t, "version-fixed", "auto", "ndjson")
	if err == nil {
		t.Fatal("Spawn succeeded with unsupported protocol version")
	}
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("Spawn error = %v, want ErrUnsupportedVersion", err)
	}
}

func TestHandshakeAcceptedAlternativeVersion(t *testing.T) {
	cfg := testSpawnConfig("version-other", "auto", "ndjson", "")
	cfg.ProtocolVersions = []string{"2024-11-05"}
	c, err := Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Spawn with supported alternative version: %v", err)
	}
	defer c.Close()
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
}

func TestHandshakeMissingToolsCapabilityRejected(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker.log")
	cfg := testSpawnConfig("echo", "auto", "ndjson", marker)
	cfg.Cfg.Env[helperEnvBehavior] = "notools-cap"
	_, err := Spawn(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "tools capability") {
		t.Fatalf("Spawn error = %v, want missing tools capability", err)
	}
}

func TestCallToolSuccessAndIsError(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	args := json.RawMessage(`{"value":"hello"}`)
	res, err := c.CallTool(context.Background(), "echo", args)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatal("echo returned isError")
	}
	if len(res.Content) != 1 || res.Content[0].Type != "text" {
		t.Fatalf("echo content = %+v", res.Content)
	}
	if !strings.Contains(res.Content[0].Text, "hello") {
		t.Fatalf("echo text = %q", res.Content[0].Text)
	}

	failRes, err := c.CallTool(context.Background(), "fail", nil)
	if err != nil {
		t.Fatalf("CallTool fail: %v", err)
	}
	if !failRes.IsError {
		t.Fatal("fail returned isError=false")
	}
	if len(failRes.Content) != 1 || failRes.Content[0].Text != "boom" {
		t.Fatalf("fail content = %+v", failRes.Content)
	}
}

func TestCallToolRPCMethodError(t *testing.T) {
	c, _ := spawnTestClient(t, "call-error", "auto", "ndjson")
	_, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("CallTool succeeded despite rpc error")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("error = %v, want RPCError -32602", err)
	}
	if !strings.Contains(err.Error(), "rpc error -32602") {
		t.Fatalf("error string = %q", err.Error())
	}
}

func TestCallToolContextCancellation(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CallTool(ctx, "echo", json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("CallTool with canceled ctx = %v, want context.Canceled", err)
	}
}

func TestHandshakeBadInitResponse(t *testing.T) {
	if _, err := spawnRaw(t, "bad-init", "auto", "ndjson"); err == nil {
		t.Fatal("Spawn accepted malformed initialize result")
	}
}

func TestHandshakeMissingServerInfo(t *testing.T) {
	_, err := spawnRaw(t, "no-serverinfo", "auto", "ndjson")
	if !errors.Is(err, ErrMissingServerInfo) {
		t.Fatalf("Spawn error = %v, want ErrMissingServerInfo", err)
	}
}

func TestRespawnHandshakeFailureExhausts(t *testing.T) {
	c, _ := spawnTestClient(t, "fail-on-respawn", "auto", "ndjson")
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, lastErr = c.ListTools(ctx)
		if errors.Is(lastErr, ErrUnavailable) {
			break
		}
		if !errors.Is(lastErr, ErrOutcomeUnknown) && !errors.Is(lastErr, ErrUnavailable) {
			t.Fatalf("unexpected error: %v", lastErr)
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !errors.Is(lastErr, ErrUnavailable) {
		t.Fatalf("final error = %v, want ErrUnavailable", lastErr)
	}
}

func TestStderrCapture(t *testing.T) {
	c, marker := spawnTestClient(t, "stderr-log", "auto", "ndjson")
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	waitMarkerLine(t, marker, "initialized:1")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(c.Stderr(), "helper-stderr-line") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stderr capture missing helper output: %q", c.Stderr())
}

func TestParseRPCID(t *testing.T) {
	if n, err := parseRPCID(json.RawMessage(`42`)); err != nil || n != 42 {
		t.Fatalf("numeric id = %d, %v", n, err)
	}
	if n, err := parseRPCID(json.RawMessage(`"42"`)); err != nil || n != 42 {
		t.Fatalf("string id = %d, %v", n, err)
	}
	if _, err := parseRPCID(json.RawMessage(`{}`)); err == nil {
		t.Fatal("parseRPCID accepted object id")
	}
}

func TestListToolsPagination(t *testing.T) {
	c, _ := spawnTestClient(t, "paged", "auto", "ndjson")
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 6 {
		t.Fatalf("ListTools returned %d tools, want 6", len(tools))
	}
	names := make([]string, len(tools))
	for i, tl := range tools {
		names[i] = tl.Name
	}
	want := []string{"echo", "fail", "rich", "jsonify", "mixed", "slow"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestListToolsCapExceeded(t *testing.T) {
	c, _ := spawnTestClient(t, "toomany", "auto", "ndjson")
	_, err := c.ListTools(context.Background())
	if !errors.Is(err, ErrToolListTooLarge) {
		t.Fatalf("ListTools error = %v, want ErrToolListTooLarge", err)
	}
}

func TestPingAndUnknownRequestHandled(t *testing.T) {
	c, marker := spawnTestClient(t, "ping-request", "auto", "ndjson")
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	waitMarkerLine(t, marker, "ping:ok")
	waitMarkerLine(t, marker, "unknown:ok")
}

func TestListChangedCoalescingAndCacheInvalidation(t *testing.T) {
	c, marker := spawnTestClient(t, "notify-list-changed", "auto", "ndjson")
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	waitMarkerLine(t, marker, "notify:5")
	waitMarkerLine(t, marker, "list:1")
	signals := drainSignals(t, c.ListChanged(), 2*time.Second)
	if signals < 1 {
		t.Fatal("no list_changed signal delivered")
	}
	if _, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"value":"x"}`)); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if signals := drainSignals(t, c.ListChanged(), 2*time.Second); signals < 1 {
		t.Fatal("no list_changed signal after tool call")
	}
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools after invalidation: %v", err)
	}
	waitMarkerCount(t, marker, "list:", 2)
	lines := readMarkerLines(t, marker)
	if countPrefix(lines, "list:") != 2 {
		t.Fatalf("tools/list invoked %d times, want 2 (invalidation refetch); marker: %v", countPrefix(lines, "list:"), lines)
	}
}

func TestNoReplayAfterCrashAndOutcomeUnknown(t *testing.T) {
	c, marker := spawnTestClient(t, "crash-after-call", "auto", "ndjson")
	_, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"value":"first"}`))
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("first CallTool error = %v, want ErrOutcomeUnknown", err)
	}
	waitMarkerLine(t, marker, "start:2")
	res, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"value":"second"}`))
	if err != nil {
		t.Fatalf("second CallTool: %v", err)
	}
	if !strings.Contains(res.Content[0].Text, "second") {
		t.Fatalf("second call text = %q", res.Content[0].Text)
	}
	lines := readMarkerLines(t, marker)
	joined := strings.Join(lines, "\n")
	if strings.Count(joined, "crash:call:1") != 1 {
		t.Fatalf("crash call not recorded exactly once: %v", lines)
	}
	if strings.Count(joined, "crash:call:2") != 0 {
		t.Fatalf("first call replayed to restarted process: %v", lines)
	}
	c.mu.Lock()
	restarts := len(c.restarts)
	c.mu.Unlock()
	if restarts != 1 {
		t.Fatalf("restarts recorded = %d, want 1", restarts)
	}
}

func TestRestartBudgetExhausted(t *testing.T) {
	c, _ := spawnTestClient(t, "crash-always", "auto", "ndjson")
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, lastErr = c.ListTools(ctx)
		if errors.Is(lastErr, ErrUnavailable) {
			break
		}
		if !errors.Is(lastErr, ErrOutcomeUnknown) && !errors.Is(lastErr, ErrUnavailable) {
			t.Fatalf("unexpected error during exhaustion: %v", lastErr)
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !errors.Is(lastErr, ErrUnavailable) {
		t.Fatalf("final error = %v, want ErrUnavailable", lastErr)
	}
	c.mu.Lock()
	exhausted := c.exhausted
	restarts := len(c.restarts)
	c.mu.Unlock()
	if !exhausted {
		t.Fatal("client not marked exhausted")
	}
	if restarts != 3 {
		t.Fatalf("restarts recorded = %d, want 3", restarts)
	}
}

func TestListToolsRetryOnceAfterRestart(t *testing.T) {
	c, marker := spawnTestClient(t, "crash-on-list", "auto", "ndjson")
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 6 {
		t.Fatalf("tools = %d, want 6", len(tools))
	}
	waitMarkerLine(t, marker, "start:2")
	lines := readMarkerLines(t, marker)
	if countPrefix(lines, "list:") != 1 {
		t.Fatalf("tools/list invoked %d times across generations, want 1 (no replay); marker: %v", countPrefix(lines, "list:"), lines)
	}
}

func TestConcurrentCalls(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	const n = 24
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			value := fmt.Sprintf("v%d", i)
			args := json.RawMessage(fmt.Sprintf(`{"value":%q}`, value))
			res, err := c.CallTool(context.Background(), "slow", args)
			if err != nil {
				errs[i] = err
				return
			}
			if res.IsError || len(res.Content) != 1 || !strings.Contains(res.Content[0].Text, value) {
				errs[i] = fmt.Errorf("call %d: unexpected result %+v", i, res)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent call %d: %v", i, err)
		}
	}
}

func TestConcurrentCallsWithMidFlightCrash(t *testing.T) {
	c, _ := spawnTestClient(t, "crash-after-call", "auto", "ndjson")
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"value":"x"}`))
			errs[i] = err
		}(i)
	}
	wg.Wait()
	unknown := 0
	for _, err := range errs {
		if err == nil {
			t.Fatal("call succeeded while server crashed without responding")
		}
		if !errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("unexpected error kind: %v", err)
		}
		unknown++
	}
	if unknown == 0 {
		t.Fatal("no call observed the crash as outcome unknown")
	}
}

func TestCloseIsClean(t *testing.T) {
	c, marker := spawnTestClient(t, "echo", "auto", "ndjson")
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	lines := readMarkerLines(t, marker)
	if countPrefix(lines, "start:") != 1 {
		t.Fatalf("process restarted after Close: %v", lines)
	}
	if _, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{}`)); !errors.Is(err, ErrClosed) {
		t.Fatalf("CallTool after Close = %v, want ErrClosed", err)
	}
}

func TestCloseIdempotent(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestClientAutoDetectNDJSON(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	c.mu.Lock()
	framing := c.fixedFraming
	corrected := c.framingCorrected
	restarts := len(c.restarts)
	c.mu.Unlock()
	if framing != FramingAuto || corrected {
		t.Fatalf("unexpected framing state: fixed=%v corrected=%v", framing, corrected)
	}
	if restarts != 0 {
		t.Fatalf("restarts = %d, want 0", restarts)
	}
}

func TestContentLengthFallbackRespawnOnce(t *testing.T) {
	c, marker := spawnTestClient(t, "echo", "auto", "auto-cl")
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	waitMarkerLine(t, marker, "start:2")
	c.mu.Lock()
	framing := c.fixedFraming
	corrected := c.framingCorrected
	restarts := len(c.restarts)
	c.mu.Unlock()
	if framing != FramingContentLength || !corrected {
		t.Fatalf("framing not corrected: fixed=%v corrected=%v", framing, corrected)
	}
	if restarts != 0 {
		t.Fatalf("framing correction consumed restart budget: restarts=%d", restarts)
	}
	if res, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"value":"cl"}`)); err != nil {
		t.Fatalf("CallTool over content-length: %v", err)
	} else if !strings.Contains(res.Content[0].Text, "cl") {
		t.Fatalf("call text = %q", res.Content[0].Text)
	}
}

func TestExplicitContentLengthFraming(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "content-length", "content-length")
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if res, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"value":"explicit"}`)); err != nil {
		t.Fatalf("CallTool: %v", err)
	} else if !strings.Contains(res.Content[0].Text, "explicit") {
		t.Fatalf("call text = %q", res.Content[0].Text)
	}
}

func TestExplicitNDJSONFraming(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "ndjson", "ndjson")
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
}

func TestFragmentedFrames(t *testing.T) {
	c, _ := spawnTestClient(t, "fragmented", "auto", "ndjson")
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools with fragmented frames: %v", err)
	}
	if len(tools) != 6 {
		t.Fatalf("tools = %d, want 6", len(tools))
	}
	if _, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"value":"frag"}`)); err != nil {
		t.Fatalf("CallTool with fragmented frames: %v", err)
	}
}

func TestFragmentedContentLengthFrames(t *testing.T) {
	c, _ := spawnTestClient(t, "fragmented", "content-length", "content-length")
	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools with fragmented content-length frames: %v", err)
	}
}

func TestSpawnWithoutCommand(t *testing.T) {
	_, err := Spawn(context.Background(), SpawnConfig{})
	if !errors.Is(err, ErrNoCommand) {
		t.Fatalf("Spawn error = %v, want ErrNoCommand", err)
	}
}

func TestSpawnInvalidFraming(t *testing.T) {
	_, err := Spawn(context.Background(), SpawnConfig{Cfg: ServerConfig{Command: "true", Framing: "bogus"}})
	if err == nil {
		t.Fatal("Spawn accepted invalid framing")
	}
}

func drainSignals(t *testing.T, ch <-chan struct{}, window time.Duration) int {
	t.Helper()
	count := 0
	deadline := time.After(window)
	for {
		select {
		case <-ch:
			count++
			continue
		case <-deadline:
			return count
		}
	}
}

func countPrefix(lines []string, prefix string) int {
	count := 0
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}

func testSpawnConfig(behavior, clientFraming, helperFraming, marker string) SpawnConfig {
	if marker == "" {
		marker = os.DevNull
	}
	return SpawnConfig{
		Cfg: ServerConfig{
			Enabled: true,
			Command: os.Args[0],
			Args:    []string{"-test.run=TestHelperProcess"},
			Framing: clientFraming,
			Env: map[string]string{
				helperEnv:         "1",
				helperEnvBehavior: behavior,
				helperEnvFraming:  helperFraming,
				helperEnvMarker:   marker,
			},
			Restart: &RestartPolicy{
				MaxRestarts: 3,
				Window:      Duration(time.Minute),
				Backoff:     []Duration{Duration(5 * time.Millisecond), Duration(5 * time.Millisecond), Duration(5 * time.Millisecond)},
			},
		},
		ClientInfo:       ClientInfo{Name: "smidja", Version: "test"},
		ProtocolVersions: []string{preferredProtocolVersion, "2024-11-05"},
		ResolveEnv:       func(string) (string, bool) { return "", false },
	}
}

func spawnRaw(t *testing.T, behavior, clientFraming, helperFraming string) (*Client, error) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "marker.log")
	return Spawn(context.Background(), testSpawnConfig(behavior, clientFraming, helperFraming, marker))
}
