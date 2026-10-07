package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

const (
	DefaultDepthLimit = 4

	childMaxLines = 2000
	childMaxBytes = 50 * 1024
)

type Parent struct {
	Generation    uint64
	SessionID     string
	Model         string
	WireModel     string
	Provider      string
	System        string
	Thinking      sdk.ThinkingLevel
	ThinkingSet   bool
	ReasoningSeam bool
	Depth         int
	Ancestry      []string
	Tools         agent.ToolCatalog
}

type Client struct {
	Client        agent.Client
	Model         string
	Wire          string
	Provider      string
	Thinking      sdk.ThinkingLevel
	ThinkingSet   bool
	ReasoningSeam bool
}

type Preparer interface {
	agent.ContextPreparer

	ForceSafety()

	DrainCompactions() []*agent.CompactionEntry
}

type RetryFunc func(ctx context.Context, produce func(context.Context) (*agent.AssistantMessage, error), policy agent.RetryPolicy, callbacks *agent.RetryCallbacks) (*agent.AssistantMessage, error)

type Dependencies struct {
	Catalog           func() Catalog
	ParentTools       func() agent.ToolCatalog
	Client            func(def Definition, parent Parent) (Client, error)
	Preparer          func(model, wire string, client agent.Client) (Preparer, error)
	Detector          func() agent.LoopDetector
	Retry             RetryFunc
	RetryPolicy       agent.RetryPolicy
	IsContextOverflow func(string) bool
	SessionsRoot      string
	Cwd               string
	DepthLimit        int
	TempDir           string
}

type Event struct {
	Kind string
	Name string
	Text string
}

const (
	EventStart      = "start"
	EventToolCall   = "tool_call"
	EventToolResult = "tool_result"
	EventDone       = "done"
)

type Request struct {
	Name    string
	Task    string
	Parent  Parent
	OnEvent func(Event)
}

type Result struct {
	Name           string
	DisplayName    string
	Description    string
	Package        string
	Path           string
	Tier           string
	Origin         string
	Model          string
	Wire           string
	Provider       string
	Thinking       sdk.ThinkingLevel
	ThinkingSet    bool
	Depth          int
	SessionPath    string
	SessionID      string
	Answer         string
	FullOutputPath string
	Truncated      bool
	IsError        bool
}

type Executor struct {
	deps Dependencies
}

func NewExecutor(deps Dependencies) (*Executor, error) {
	if deps.Catalog == nil {
		return nil, errors.New("agents: a catalog provider is required")
	}
	if deps.ParentTools == nil {
		return nil, errors.New("agents: a parent tool catalog provider is required")
	}
	if deps.Client == nil {
		return nil, errors.New("agents: a client factory is required")
	}
	if deps.Preparer == nil {
		return nil, errors.New("agents: a preparer factory is required")
	}
	if strings.TrimSpace(deps.SessionsRoot) == "" {
		return nil, errors.New("agents: a session root is required")
	}
	if strings.TrimSpace(deps.Cwd) == "" {
		return nil, errors.New("agents: a working directory is required")
	}
	if deps.DepthLimit <= 0 {
		deps.DepthLimit = DefaultDepthLimit
	}
	return &Executor{deps: deps}, nil
}

func (e *Executor) Run(ctx context.Context, req Request) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{Name: req.Name, Depth: req.Parent.Depth + 1}, fmt.Errorf("agent %q: %w", bounded(req.Name), err)
	}
	if req.Parent.Depth >= e.deps.DepthLimit {
		return Result{Name: req.Name, Depth: req.Parent.Depth + 1}, fmt.Errorf("agent %q: maximum nesting depth %d reached", bounded(req.Name), e.deps.DepthLimit)
	}
	if nameInAncestry(req.Parent.Ancestry, req.Name) {
		return Result{Name: req.Name, Depth: req.Parent.Depth + 1}, fmt.Errorf("agent %q: delegation cycle detected: %s", bounded(req.Name), bounded(ancestryText(req.Parent.Ancestry, req.Name)))
	}
	catalog := e.deps.Catalog()
	definition, ok := catalog.Lookup(req.Name)
	if !ok {
		if parseErr := catalog.ParseError(req.Name); parseErr != nil {
			return Result{}, parseErr
		}
		return Result{}, fmt.Errorf("agents: no agent named %q", bounded(req.Name))
	}
	res := Result{
		Name:        definition.Name,
		DisplayName: definition.DisplayName,
		Description: definition.Description,
		Package:     definition.Package,
		Path:        definition.Path,
		Tier:        string(definition.Tier),
		Origin:      definition.Origin,
		Depth:       req.Parent.Depth + 1,
	}
	if strings.TrimSpace(req.Task) == "" {
		return res, fmt.Errorf("agent %q: a task is required", bounded(definition.Name))
	}
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("agent %q: %w", bounded(definition.Name), err)
	}
	client, err := e.deps.Client(definition, req.Parent)
	if err != nil {
		return res, fmt.Errorf("agent %q: %w", bounded(definition.Name), err)
	}
	res.Model = client.Model
	res.Wire = client.Wire
	res.Provider = client.Provider
	res.Thinking = client.Thinking
	res.ThinkingSet = client.ThinkingSet
	if client.Client == nil {
		return res, fmt.Errorf("agent %q: the client factory returned no client", bounded(definition.Name))
	}
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("agent %q: %w", bounded(definition.Name), err)
	}
	tools, err := e.buildTools(definition, req.Parent, client)
	if err != nil {
		return res, err
	}
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("agent %q: %w", bounded(definition.Name), err)
	}
	preparer, err := e.deps.Preparer(client.Model, clientWire(client), client.Client)
	if err != nil {
		return res, fmt.Errorf("agent %q: %w", bounded(definition.Name), err)
	}
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("agent %q: %w", bounded(definition.Name), err)
	}
	child, err := e.newChildSession(definition, req.Parent)
	if err != nil {
		return res, fmt.Errorf("agent %q: %w", bounded(definition.Name), err)
	}
	res.SessionPath = child.path
	res.SessionID = child.id
	defer child.close()
	system := childSystemPrompt(definition, req.Parent)
	if req.OnEvent != nil {
		req.OnEvent(Event{Kind: EventStart, Name: definition.Name, Text: resultIdentity(res)})
	}
	history, runErr := e.runChild(ctx, req, definition, client, tools, child, preparer, system)
	for _, compaction := range preparer.DrainCompactions() {
		if appendErr := child.appendCompaction(compaction); appendErr != nil && runErr == nil {
			runErr = appendErr
		}
	}
	res.Answer = finalAnswer(history, runErr)
	res.Answer, res.FullOutputPath, res.Truncated = e.boundAnswer(res.Answer)
	if req.OnEvent != nil {
		req.OnEvent(Event{Kind: EventDone, Name: definition.Name, Text: res.Answer})
	}
	if runErr != nil {
		res.IsError = true
		return res, fmt.Errorf("agent %q: %w", bounded(definition.Name), runErr)
	}
	return res, nil
}

func (e *Executor) runChild(ctx context.Context, req Request, definition Definition, client Client, tools *childTools, child *childSession, preparer Preparer, system string) ([]*agent.Message, error) {
	deps := &agent.LoopDeps{
		Client:                 client.Client,
		Catalog:                tools,
		Recorder:               child,
		Stdout:                 io.Discard,
		Preparer:               preparer,
		Hooks:                  e.childHooks(req.OnEvent),
		Detector:               e.detector(),
		Retry:                  e.deps.Retry,
		RetryPolicy:            e.deps.RetryPolicy,
		RetryPolicySet:         true,
		IsContextOverflow:      e.deps.IsContextOverflow,
		SessionEntryIDs:        []string{},
		RefreshSessionEntryIDs: child.refreshEntryIDs,
	}
	wire := clientWire(client)
	history, err := agent.RunTurn(ctx, deps, wire, system, nil, req.Task)
	var overflow *agent.ContextOverflowError
	if errors.As(err, &overflow) {
		preparer.ForceSafety()
		contHistory, contIDs, projectionErr := projectChild(child.path)
		if projectionErr != nil {
			return history, projectionErr
		}
		contDeps := *deps
		contDeps.SessionEntryIDs = contIDs
		history, err = agent.ContinueTurn(ctx, &contDeps, wire, system, contHistory)
		var again *agent.ContextOverflowError
		if errors.As(err, &again) {
			return history, fmt.Errorf("context still overflows the model window after compaction: %w", err)
		}
	}
	return history, err
}

func clientWire(client Client) string {
	wire := strings.TrimSpace(client.Wire)
	if wire == "" {
		return client.Model
	}
	return wire
}

func (e *Executor) detector() agent.LoopDetector {
	if e.deps.Detector == nil {
		return nil
	}
	return e.deps.Detector()
}

func (e *Executor) childHooks(onEvent func(Event)) agent.HookDispatcher {
	if onEvent == nil {
		return nil
	}
	return &progressHooks{onEvent: onEvent}
}

func childSystemPrompt(definition Definition, parent Parent) string {
	base := strings.TrimSpace(parent.System)
	if base == "" {
		return definition.Body
	}
	return definition.Body + "\n\n" + base
}

func finalAnswer(history []*agent.Message, runErr error) string {
	for i := len(history) - 1; i >= 0; i-- {
		message := history[i]
		if message == nil || message.Assistant == nil {
			continue
		}
		if text := strings.TrimSpace(assistantText(message.Assistant)); text != "" {
			return text
		}
		if message.Assistant.StopReason == "error" && strings.TrimSpace(message.Assistant.ErrorMessage) != "" {
			return message.Assistant.ErrorMessage
		}
	}
	if runErr != nil {
		return runErr.Error()
	}
	return ""
}

func assistantText(message *agent.AssistantMessage) string {
	var out strings.Builder
	for _, block := range message.Content {
		if block.Type == agent.BlockTypeText {
			out.WriteString(block.Text)
		}
	}
	return out.String()
}

func resultIdentity(res Result) string {
	model := res.Model
	if model == "" {
		model = res.Wire
	}
	return fmt.Sprintf("%s tier=%s origin=%s depth=%d model=%s", res.Name, res.Tier, res.Origin, res.Depth, model)
}

func nameInAncestry(ancestry []string, name string) bool {
	for _, existing := range ancestry {
		if existing == name {
			return true
		}
	}
	return false
}

func ancestryText(ancestry []string, name string) string {
	chain := append(append([]string(nil), ancestry...), name)
	return strings.Join(chain, " -> ")
}

func (e *Executor) boundAnswer(text string) (string, string, bool) {
	if text == "" {
		return "", "", false
	}
	lines := strings.Count(text, "\n") + 1
	if lines <= childMaxLines && len(text) <= childMaxBytes {
		return text, "", false
	}
	display := headBound(text)
	label := fmt.Sprintf("%d lines, %d bytes", lines, len(text))
	file, err := os.CreateTemp(e.deps.TempDir, "smidja-subagent-*.log")
	if err != nil {
		return display + truncatedNote(label, err), "", true
	}
	path := file.Name()
	if chmodErr := file.Chmod(0o600); chmodErr != nil {
		file.Close()
		os.Remove(path)
		return display + truncatedNote(label, chmodErr), "", true
	}
	if _, writeErr := io.WriteString(file, text); writeErr != nil {
		file.Close()
		os.Remove(path)
		return display + truncatedNote(label, writeErr), "", true
	}
	if closeErr := file.Close(); closeErr != nil {
		os.Remove(path)
		return display + truncatedNote(label, closeErr), "", true
	}
	return display + fmt.Sprintf("\n[subagent output truncated: %s. Full output: %s]", label, path), path, true
}

func truncatedNote(label string, err error) string {
	return fmt.Sprintf("\n[subagent output truncated: %s; full output could not be saved: %s]", label, bounded(err.Error()))
}

func headBound(text string) string {
	var out strings.Builder
	lines := 0
	start := 0
	for start < len(text) && lines < childMaxLines {
		line := text[start:]
		next := len(text)
		if index := strings.IndexByte(text[start:], '\n'); index >= 0 {
			line = text[start : start+index]
			next = start + index + 1
		}
		if out.Len() > 0 {
			if out.Len() >= childMaxBytes {
				break
			}
			out.WriteByte('\n')
		}
		remaining := childMaxBytes - out.Len()
		if remaining <= 0 {
			break
		}
		if len(line) > remaining {
			out.WriteString(clampUTF8(line, remaining))
			break
		}
		out.WriteString(line)
		lines++
		start = next
	}
	return out.String()
}

func clampUTF8(text string, limit int) string {
	if limit >= len(text) {
		return text
	}
	end := limit
	for end > 0 && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

type progressHooks struct {
	inner   agent.HookDispatcher
	onEvent func(Event)
}

func (h *progressHooks) Context(ctx context.Context, req agent.ContextRequest) (agent.ContextResult, error) {
	if h.inner == nil {
		return agent.ContextResult{}, nil
	}
	return h.inner.Context(ctx, req)
}

func (h *progressHooks) MessageEnd(ctx context.Context, message *agent.Message) (*agent.Message, error) {
	if h.inner == nil {
		return nil, nil
	}
	return h.inner.MessageEnd(ctx, message)
}

func (h *progressHooks) AutoRetryStart(ctx context.Context, attempt int, maxAttempts int, delayMs int64, errorMessage string) error {
	if h.inner == nil {
		return nil
	}
	return h.inner.AutoRetryStart(ctx, attempt, maxAttempts, delayMs, errorMessage)
}

func (h *progressHooks) AutoRetryEnd(ctx context.Context, success bool, attempt int, finalError string) error {
	if h.inner == nil {
		return nil
	}
	return h.inner.AutoRetryEnd(ctx, success, attempt, finalError)
}

func (h *progressHooks) ToolCall(ctx context.Context, name string, callID string, args json.RawMessage) (agent.ToolCallDecision, error) {
	if h.onEvent != nil {
		h.onEvent(Event{Kind: EventToolCall, Name: name})
	}
	if h.inner == nil {
		return agent.ToolCallDecision{}, nil
	}
	return h.inner.ToolCall(ctx, name, callID, args)
}

func (h *progressHooks) ToolResult(ctx context.Context, name string, callID string, args json.RawMessage, res agent.Result) (agent.Result, error) {
	if h.onEvent != nil {
		h.onEvent(Event{Kind: EventToolResult, Name: name})
	}
	if h.inner == nil {
		return res, nil
	}
	return h.inner.ToolResult(ctx, name, callID, args, res)
}

func (h *progressHooks) SessionStart(ctx context.Context, reason string) error {
	if h.inner == nil {
		return nil
	}
	return h.inner.SessionStart(ctx, reason)
}

func (h *progressHooks) SessionShutdown(ctx context.Context, reason string) error {
	if h.inner == nil {
		return nil
	}
	return h.inner.SessionShutdown(ctx, reason)
}
