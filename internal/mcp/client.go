package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxListedTools    = 200
	listRetryAttempts = 2
	closeGracePeriod  = 300 * time.Millisecond
	maxCapturedStderr = 64 * 1024
)

var preferredProtocolVersion = "2025-06-18"

var (
	ErrNoCommand              = errors.New("mcp: server has no command")
	ErrClosed                 = errors.New("mcp: client is closed")
	ErrUnavailable            = errors.New("mcp: server unavailable: restart budget exhausted")
	ErrOutcomeUnknown         = errors.New("mcp: request outcome unknown: server restarted")
	ErrUnsupportedVersion     = errors.New("mcp: server returned unsupported protocol version")
	ErrMissingServerInfo      = errors.New("mcp: initialize response missing serverInfo")
	ErrMissingToolsCapability = errors.New("mcp: initialize response missing tools capability")
	ErrToolListTooLarge       = errors.New("mcp: server exposed too many tools")
)

type SpawnConfig struct {
	Cfg              ServerConfig
	ClientInfo       ClientInfo
	ProtocolVersions []string
	ResolveEnv       func(string) (string, bool)
}

type Client struct {
	cfg    SpawnConfig
	policy RestartPolicy

	ctx    context.Context
	cancel context.CancelFunc
	stop   chan struct{}

	mu               sync.Mutex
	gen              *generation
	closed           bool
	exhausted        bool
	restarts         []time.Time
	fixedFraming     Framing
	framingCorrected bool
	tools            []ToolInfo
	toolsInvalid     bool
	genCond          *sync.Cond

	pendingMu sync.Mutex
	pending   map[int64]*pendingCall
	nextID    atomic.Int64

	notify     chan struct{}
	superDone  chan struct{}
	closeGuard sync.Once
	stderr     *ringBuffer
}

type pendingCall struct {
	ch chan callResult
}

type callResult struct {
	resp json.RawMessage
	err  error
}

type generation struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	io    *frameIO
	done  chan struct{}
}

type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newRingBuffer(max int) *ringBuffer {
	return &ringBuffer{max: max}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		r.buf = append([]byte(nil), r.buf[len(r.buf)-r.max:]...)
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

func Spawn(ctx context.Context, cfg SpawnConfig) (*Client, error) {
	if strings.TrimSpace(cfg.Cfg.Command) == "" {
		return nil, ErrNoCommand
	}
	if cfg.ResolveEnv == nil {
		cfg.ResolveEnv = func(string) (string, bool) { return "", false }
	}
	if len(cfg.ProtocolVersions) == 0 {
		cfg.ProtocolVersions = []string{preferredProtocolVersion}
	}
	if cfg.ClientInfo.Name == "" {
		cfg.ClientInfo.Name = "smidja"
	}
	if cfg.ClientInfo.Version == "" {
		cfg.ClientInfo.Version = "dev"
	}
	framing, err := parseFraming(cfg.Cfg.Framing)
	if err != nil {
		return nil, err
	}
	c := &Client{
		cfg:          cfg,
		policy:       cfg.Cfg.Restart.withDefaults(),
		stop:         make(chan struct{}),
		pending:      map[int64]*pendingCall{},
		notify:       make(chan struct{}, 1),
		superDone:    make(chan struct{}),
		fixedFraming: framing,
		stderr:       newRingBuffer(maxCapturedStderr),
	}
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.genCond = sync.NewCond(&c.mu)

	gen, err := c.startGeneration()
	if err != nil {
		c.cancel()
		return nil, err
	}
	c.mu.Lock()
	c.gen = gen
	c.mu.Unlock()
	go c.supervise()
	go c.watchCancel()
	return c, nil
}

func (c *Client) watchCancel() {
	<-c.ctx.Done()
	c.closeOnce()
}

func (c *Client) supervise() {
	defer close(c.superDone)
	for {
		c.mu.Lock()
		if c.closed || c.exhausted {
			c.mu.Unlock()
			return
		}
		gen := c.gen
		c.mu.Unlock()

		if gen == nil {
			next, err := c.startGeneration()
			if err != nil {
				c.mu.Lock()
				closed := c.closed
				c.mu.Unlock()
				if closed {
					return
				}
				if !c.consumeRestart() {
					c.markExhausted()
					return
				}
				if !c.sleepBackoff() {
					return
				}
				continue
			}
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				next.teardown()
				return
			}
			c.gen = next
			c.genCond.Broadcast()
			c.mu.Unlock()
			continue
		}

		select {
		case <-gen.done:
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return
			}
			c.gen = nil
			c.genCond.Broadcast()
			c.mu.Unlock()
			if !c.consumeRestart() {
				c.markExhausted()
				return
			}
			if !c.sleepBackoff() {
				return
			}
		case <-c.stop:
			return
		}
	}
}

func (c *Client) startGeneration() (*generation, error) {
	gen, err := c.spawnProcess()
	if err != nil {
		return nil, err
	}
	if err := c.handshake(gen); err != nil {
		gen.teardown()
		return nil, err
	}
	if c.fixedFraming == FramingAuto && gen.io.Detected() == FramingContentLength && !c.framingCorrected {
		c.framingCorrected = true
		c.fixedFraming = FramingContentLength
		gen.teardown()
		return c.startGeneration()
	}
	return gen, nil
}

func (c *Client) spawnProcess() (*generation, error) {
	cfg := c.cfg.Cfg
	writeMode := c.fixedFraming
	readMode := c.fixedFraming
	if c.fixedFraming == FramingAuto {
		writeMode = FramingNDJSON
		readMode = FramingAuto
	}
	cmd := exec.CommandContext(c.ctx, cfg.Command, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.Env = childEnv(cfg, c.cfg.ResolveEnv)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %s: %w", cfg.Command, err)
	}
	gen := &generation{
		cmd:   cmd,
		stdin: stdin,
		io:    newFrameIO(stdout, stdin, writeMode, readMode),
		done:  make(chan struct{}),
	}
	go c.drainStderr(stderr)
	go c.readLoop(gen)
	return gen, nil
}

func (g *generation) teardown() {
	g.stdin.Close()
	select {
	case <-g.done:
	default:
		g.cmd.Process.Kill()
		<-g.done
	}
}

func (c *Client) handshake(gen *generation) error {
	params := mustMarshal(InitializeParams{
		ProtocolVersion: preferredProtocolVersion,
		Capabilities:    ClientCapabilities{},
		ClientInfo:      ClientInfo{Name: c.cfg.ClientInfo.Name, Version: c.cfg.ClientInfo.Version},
	})
	res, err := c.roundTrip(c.ctx, gen, MethodInitialize, params)
	if err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}
	var init InitializeResult
	if err := json.Unmarshal(res, &init); err != nil {
		return fmt.Errorf("mcp: parse initialize result: %w", err)
	}
	if !slices.Contains(c.cfg.ProtocolVersions, init.ProtocolVersion) {
		return fmt.Errorf("%w: %q", ErrUnsupportedVersion, init.ProtocolVersion)
	}
	if init.ServerInfo.Name == "" {
		return ErrMissingServerInfo
	}
	if init.Capabilities.Tools == nil {
		return ErrMissingToolsCapability
	}
	notif := Notification{Jsonrpc: JsonRPCVersion, Method: MethodInitialized}
	if err := gen.io.Write(notif); err != nil {
		return fmt.Errorf("mcp: send initialized: %w", err)
	}
	return nil
}

func (c *Client) request(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	for {
		c.mu.Lock()
		for c.gen == nil && !c.closed && !c.exhausted {
			c.genCond.Wait()
		}
		if c.closed {
			c.mu.Unlock()
			return nil, ErrClosed
		}
		if c.exhausted {
			c.mu.Unlock()
			return nil, ErrUnavailable
		}
		gen := c.gen
		c.mu.Unlock()
		return c.roundTrip(ctx, gen, method, params)
	}
}

func (c *Client) roundTrip(ctx context.Context, gen *generation, method string, params json.RawMessage) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	pc := &pendingCall{ch: make(chan callResult, 1)}
	c.pendingMu.Lock()
	c.pending[id] = pc
	c.pendingMu.Unlock()
	req := Request{Jsonrpc: JsonRPCVersion, ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: params}
	if err := gen.io.Write(req); err != nil {
		c.dropPending(id)
		if errors.Is(err, ErrFrameTooLarge) {
			return nil, err
		}
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return nil, ErrClosed
		}
		return nil, ErrOutcomeUnknown
	}
	select {
	case res := <-pc.ch:
		var resp Response
		if err := json.Unmarshal(res.resp, &resp); err != nil {
			return nil, fmt.Errorf("mcp: parse response: %w", err)
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.dropPending(id)
		return nil, ctx.Err()
	case <-gen.done:
		c.dropPending(id)
		return nil, ErrOutcomeUnknown
	}
}

func (c *Client) dropPending(id int64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (CallToolResult, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage("{}")
	}
	params := mustMarshal(CallToolParams{Name: name, Arguments: args})
	res, err := c.request(ctx, MethodToolsCall, params)
	if err != nil {
		return CallToolResult{}, err
	}
	var out CallToolResult
	if err := json.Unmarshal(res, &out); err != nil {
		return CallToolResult{}, fmt.Errorf("mcp: parse tools/call result: %w", err)
	}
	return out, nil
}

func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	c.mu.Lock()
	if c.tools != nil && !c.toolsInvalid {
		tools := c.tools
		c.mu.Unlock()
		return tools, nil
	}
	c.mu.Unlock()
	var lastErr error
	for attempt := 0; attempt < listRetryAttempts; attempt++ {
		tools, err := c.listToolsAll(ctx)
		if err == nil {
			c.mu.Lock()
			c.tools = tools
			c.toolsInvalid = false
			c.mu.Unlock()
			return tools, nil
		}
		lastErr = err
		if !errors.Is(err, ErrOutcomeUnknown) {
			break
		}
	}
	return nil, lastErr
}

func (c *Client) listToolsAll(ctx context.Context) ([]ToolInfo, error) {
	var out []ToolInfo
	seen := map[string]bool{}
	cursor := ""
	for {
		params := mustMarshal(ListToolsParams{Cursor: cursor})
		res, err := c.request(ctx, MethodToolsList, params)
		if err != nil {
			return nil, err
		}
		var page ListToolsResult
		if err := json.Unmarshal(res, &page); err != nil {
			return nil, fmt.Errorf("mcp: parse tools/list result: %w", err)
		}
		out = append(out, page.Tools...)
		if len(out) > maxListedTools {
			return nil, ErrToolListTooLarge
		}
		if page.NextCursor == "" {
			return out, nil
		}
		if seen[page.NextCursor] {
			return nil, fmt.Errorf("mcp: tools/list cursor loop")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
}

func (c *Client) ListChanged() <-chan struct{} {
	return c.notify
}

func (c *Client) Stderr() string {
	return c.stderr.String()
}

func (c *Client) Close() error {
	c.closeOnce()
	return nil
}

func (c *Client) closeOnce() {
	c.closeGuard.Do(func() {
		c.mu.Lock()
		c.closed = true
		gen := c.gen
		c.mu.Unlock()
		close(c.stop)
		c.cancel()
		c.genCond.Broadcast()
		if gen != nil {
			gen.stdin.Close()
			select {
			case <-gen.done:
			case <-time.After(closeGracePeriod):
				gen.cmd.Process.Kill()
				<-gen.done
			}
		}
		<-c.superDone
	})
}

func (c *Client) consumeRestart() bool {
	now := time.Now()
	cutoff := now.Add(-time.Duration(c.policy.Window))
	c.mu.Lock()
	kept := c.restarts[:0]
	for _, t := range c.restarts {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	c.restarts = kept
	allowed := len(c.restarts) < c.policy.MaxRestarts
	if allowed {
		c.restarts = append(c.restarts, now)
	}
	c.mu.Unlock()
	return allowed
}

func (c *Client) sleepBackoff() bool {
	c.mu.Lock()
	idx := len(c.restarts) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(c.policy.Backoff) {
		idx = len(c.policy.Backoff) - 1
	}
	wait := time.Duration(c.policy.Backoff[idx])
	c.mu.Unlock()
	select {
	case <-time.After(wait):
		return true
	case <-c.stop:
		return false
	}
}

func (c *Client) markExhausted() {
	c.mu.Lock()
	c.exhausted = true
	c.genCond.Broadcast()
	c.mu.Unlock()
}

func (c *Client) readLoop(gen *generation) {
	defer close(gen.done)
	for {
		raw, err := gen.io.Read()
		if err != nil {
			return
		}
		var head struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			continue
		}
		switch {
		case head.Method != "" && len(head.ID) == 0:
			c.handleNotification(head.Method)
		case head.Method != "":
			c.handleServerRequest(gen, head.ID, head.Method)
		default:
			c.handleResponse(head.ID, raw)
		}
	}
}

func (c *Client) handleNotification(method string) {
	if method != NotifyToolsListChanged {
		return
	}
	c.mu.Lock()
	c.toolsInvalid = true
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *Client) handleServerRequest(gen *generation, id json.RawMessage, method string) {
	resp := Response{Jsonrpc: JsonRPCVersion, ID: id}
	if method == MethodPing {
		resp.Result = json.RawMessage("{}")
	} else {
		resp.Error = &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + method}
	}
	gen.io.Write(resp)
}

func (c *Client) handleResponse(id json.RawMessage, raw json.RawMessage) {
	num, err := parseRPCID(id)
	if err != nil {
		return
	}
	c.pendingMu.Lock()
	pc := c.pending[num]
	if pc != nil {
		delete(c.pending, num)
	}
	c.pendingMu.Unlock()
	if pc != nil {
		pc.ch <- callResult{resp: raw}
	}
}

func (c *Client) drainStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			c.stderr.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func parseRPCID(id json.RawMessage) (int64, error) {
	var n int64
	if err := json.Unmarshal(id, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(id, &s); err == nil {
		return strconv.ParseInt(s, 10, 64)
	}
	return 0, fmt.Errorf("mcp: invalid response id %s", id)
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("mcp: marshal: " + err.Error())
	}
	return b
}
