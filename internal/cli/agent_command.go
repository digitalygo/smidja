package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/agents"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

const (
	agentCommandName         = "agent"
	subagentResultCustomType = "smidja.subagent.result"
)

type agentCommandSlot struct {
	mu         sync.Mutex
	registered string
	handler    func(ctx sdk.CommandContext, args string) error
}

func newAgentCommandSlot() *agentCommandSlot {
	return &agentCommandSlot{}
}

func (s *agentCommandSlot) bind(commands *extensions.CommandCatalog) string {
	if s == nil || commands == nil {
		return ""
	}
	s.mu.Lock()
	if s.registered != "" {
		registered := s.registered
		s.mu.Unlock()
		return registered
	}
	s.mu.Unlock()
	registered, _ := commands.Register(agentCommandName, sdk.Command{
		Description: "run a named agent definition in an isolated child session; /agent lists the available names",
		Handler: func(ctx sdk.CommandContext, args string) error {
			return s.dispatch(ctx, args)
		},
	})
	s.mu.Lock()
	s.registered = registered
	s.mu.Unlock()
	return registered
}

func (s *agentCommandSlot) set(handler func(ctx sdk.CommandContext, args string) error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.handler = handler
	s.mu.Unlock()
}

func (s *agentCommandSlot) dispatch(ctx sdk.CommandContext, args string) error {
	if s == nil {
		return errors.New("agent: execution is not available in this context")
	}
	s.mu.Lock()
	handler := s.handler
	s.mu.Unlock()
	if handler == nil {
		return errors.New("agent: execution is not available yet")
	}
	return handler(ctx, args)
}

type subagentToolSlot struct {
	mu   sync.Mutex
	tool *agents.Tool
}

func newSubagentToolSlot() *subagentToolSlot {
	return &subagentToolSlot{tool: agents.NewTool(nil)}
}

func (s *subagentToolSlot) bind(catalog *extensions.ToolCatalog) {
	if s == nil || catalog == nil || s.tool == nil {
		return
	}
	if _, exists := catalog.Get(agents.SubagentToolName); exists {
		return
	}
	_ = catalog.Register(s.tool)
}

func (s *subagentToolSlot) toolRef() *agents.Tool {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tool
}

func (s *subagentToolSlot) set(invoke func(ctx context.Context, name, task string) agent.Result) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.tool.SetInvoke(invoke)
	s.mu.Unlock()
}

type subagentProgress interface {
	Start(name, identity string)

	Event(text string)

	Finish(res agents.Result, err error)
}

type subagentCommandRunner interface {
	runSubagentCommand(name string, run func(context.Context, subagentProgress) error) error
}

func handleAgentCommand(ctx sdk.CommandContext, catalog agents.Catalog, exec *agents.Executor, host *hostRuntime, out io.Writer, args string) error {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return listAgentCatalog(out, catalog)
	}
	name, task := splitFirstToken(trimmed)
	definition, ok := catalog.Lookup(name)
	if !ok {
		if parseErr := catalog.ParseError(name); parseErr != nil {
			return parseErr
		}
		return fmt.Errorf("no agent named %q", sanitizeTerm(name))
	}
	if strings.TrimSpace(task) == "" {
		return fmt.Errorf("agent %q: a task is required", sanitizeTerm(definition.Name))
	}
	if exec == nil || host == nil {
		return errors.New("agent: execution is not available in this context")
	}
	run := func(runCtx context.Context, progress subagentProgress) error {
		if runCtx == nil {
			runCtx = context.Background()
		}
		if err := runCtx.Err(); err != nil {
			return err
		}
		return host.runOwnedAgentTurn(runCtx, func(turnCtx context.Context) error {
			parent, handle := host.agentParentSnapshot()
			if handle == nil {
				return errors.New("agent: no active session")
			}
			if err := turnCtx.Err(); err != nil {
				return err
			}
			if progress != nil {
				progress.Start(definition.Name, agentProgressIdentity(definition, parent))
			}
			var onEvent func(agents.Event)
			if progress != nil {
				onEvent = func(event agents.Event) { emitAgentProgress(progress, event) }
			}
			res, runErr := exec.Run(turnCtx, agents.Request{Name: name, Task: task, Parent: parent, OnEvent: onEvent})
			if ctxErr := turnCtx.Err(); ctxErr != nil {
				if runErr == nil {
					runErr = ctxErr
				}
				finishAgentProgress(host, handle, progress, res, runErr)
				return runErr
			}
			if persistErr := persistAgentResult(host, handle, res, runErr); persistErr != nil {
				if !errors.Is(persistErr, errHostStaleSession) {
					finishAgentProgress(host, handle, progress, res, persistErr)
				}
				if runErr != nil {
					return errors.Join(runErr, persistErr)
				}
				return persistErr
			}
			finishAgentProgress(host, handle, progress, res, runErr)
			return runErr
		})
	}
	if runner, ok := ctx.(subagentCommandRunner); ok {
		return runner.runSubagentCommand(definition.Name, run)
	}
	if ctx != nil {
		if signal := ctx.Signal(); signal != nil {
			return run(signal, &writerSubagentProgress{w: out})
		}
	}
	return run(context.Background(), &writerSubagentProgress{w: out})
}

func finishAgentProgress(host *hostRuntime, handle *hostSessionHandle, progress subagentProgress, res agents.Result, err error) {
	if progress == nil {
		return
	}
	if host == nil || handle == nil {
		progress.Finish(res, err)
		return
	}
	host.deliverCurrent(handle, func() { progress.Finish(res, err) })
}

func listAgentCatalog(out io.Writer, catalog agents.Catalog) error {
	if out == nil {
		return nil
	}
	for _, info := range catalog.Infos() {
		description := sanitizeTerm(info.Description)
		if description == "" {
			if _, err := fmt.Fprintln(out, sanitizeTerm(info.Name)); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(out, "%s\t%s\n", sanitizeTerm(info.Name), description); err != nil {
			return err
		}
	}
	return nil
}

func agentProgressIdentity(definition agents.Definition, parent agents.Parent) string {
	model := definition.Model
	if model == "" {
		model = parent.Model
	}
	return fmt.Sprintf("tier=%s origin=%s model=%s", definition.Tier, definition.Origin, model)
}

func emitAgentProgress(progress subagentProgress, event agents.Event) {
	if progress == nil {
		return
	}
	switch event.Kind {
	case agents.EventToolCall:
		progress.Event("tool: " + sanitizeTerm(event.Name))
	}
}

type writerSubagentProgress struct {
	mu sync.Mutex
	w  io.Writer
}

func (p *writerSubagentProgress) Start(name, identity string) {
	p.printf("agent %s: %s\n", sanitizeTerm(name), sanitizeTerm(identity))
}

func (p *writerSubagentProgress) Event(text string) {
	p.printf("agent: %s\n", sanitizeTerm(text))
}

func (p *writerSubagentProgress) Finish(res agents.Result, err error) {
	if err != nil {
		p.printf("agent %s failed: %s\n", sanitizeTerm(res.Name), clampText(err.Error()))
		return
	}
	answer := strings.TrimSpace(res.Answer)
	if answer == "" {
		answer = "(no output)"
	}
	p.printf("agent %s:\n%s\n", sanitizeTerm(res.Name), answer)
}

func (p *writerSubagentProgress) printf(format string, args ...any) {
	if p == nil || p.w == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.w, format, args...)
}

type tuiSubagentProgress struct {
	mu    sync.Mutex
	block *interactive.SubagentBlock
	lines []string
}

func (p *tuiSubagentProgress) Start(name, identity string) {
	p.add(identity)
}

func (p *tuiSubagentProgress) Event(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	p.add(text)
}

func (p *tuiSubagentProgress) Finish(res agents.Result, err error) {
	if err != nil {
		p.add("failed: " + clampText(err.Error()))
		p.setStatus(interactive.ToolError)
		return
	}
	answer := strings.TrimSpace(res.Answer)
	if answer == "" {
		answer = "(no output)"
	}
	p.add(answer)
	p.setStatus(interactive.ToolSuccess)
}

func (p *tuiSubagentProgress) add(line string) {
	p.mu.Lock()
	if len(p.lines) < 200 {
		p.lines = append(p.lines, line)
	}
	text := strings.Join(p.lines, "\n")
	block := p.block
	p.mu.Unlock()
	if block != nil {
		block.SetOutput(text)
	}
}

func (p *tuiSubagentProgress) setStatus(status interactive.ToolStatus) {
	p.mu.Lock()
	block := p.block
	p.mu.Unlock()
	if block != nil {
		block.SetStatus(status)
	}
}

type subagentResultEntry struct {
	Agent       string `json:"agent"`
	DisplayName string `json:"displayName,omitempty"`
	Tier        string `json:"tier"`
	Origin      string `json:"origin"`
	Path        string `json:"path"`
	Model       string `json:"model"`
	Depth       int    `json:"depth"`
	Session     string `json:"session"`
	SessionID   string `json:"sessionId"`
	Status      string `json:"status"`
	Result      string `json:"result"`
	FullOutput  string `json:"fullOutput,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

func persistAgentResult(host *hostRuntime, handle *hostSessionHandle, res agents.Result, runErr error) error {
	payload := subagentResultEntry{
		Agent:       res.Name,
		DisplayName: res.DisplayName,
		Tier:        res.Tier,
		Origin:      res.Origin,
		Path:        res.Path,
		Model:       res.Model,
		Depth:       res.Depth,
		Session:     res.SessionPath,
		SessionID:   res.SessionID,
		Status:      "ok",
		Result:      res.Answer,
		FullOutput:  res.FullOutputPath,
		Truncated:   res.Truncated,
	}
	if runErr != nil {
		payload.Status = "error"
		if strings.TrimSpace(payload.Result) == "" {
			payload.Result = clampText(runErr.Error())
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return host.commitSession(handle, func(sess *session.Session) error {
		return sess.AppendEntry(&session.CustomEntry{CustomType: subagentResultCustomType, Data: raw})
	}, nil)
}

func (h *hostRuntime) agentParentSnapshot() (agents.Parent, *hostSessionHandle) {
	h.mu.Lock()
	defer h.mu.Unlock()
	handle := copyHostHandle(h.handle)
	parent := agents.Parent{
		Model:         h.modelID,
		WireModel:     h.wireModel,
		Provider:      h.provider,
		System:        h.system,
		Thinking:      h.thinking,
		ThinkingSet:   h.thinkingSet,
		ReasoningSeam: h.reasoningSeam,
	}
	if handle != nil {
		parent.Generation = handle.generation
		parent.SessionID = handle.id
	}
	return parent, handle
}

func runHostSubagent(ctx context.Context, exec *agents.Executor, host *hostRuntime, name, task string) agent.Result {
	if exec == nil || host == nil {
		return agent.ErrorResult("subagent: delegation is not available in this context")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return agent.ErrorResult("subagent: " + clampText(err.Error()))
		}
	}
	parent, handle := host.agentParentSnapshot()
	if handle == nil {
		return agent.ErrorResult("subagent: no active parent session")
	}
	res, err := exec.Run(ctx, agents.Request{Name: name, Task: task, Parent: parent})
	return agents.ToolResult(res, err)
}

func (c *commandContext) runSubagentCommand(name string, run func(context.Context, subagentProgress) error) error {
	signal := c.ctx
	if signal == nil {
		signal = context.Background()
	}
	return run(signal, &writerSubagentProgress{w: c.d.stdout})
}

func (c *tuiCommandContext) runSubagentCommand(name string, run func(context.Context, subagentProgress) error) error {
	bridge := c.bridge
	if bridge == nil || bridge.runner == nil {
		return errors.New("agent: the interactive runner is unavailable")
	}
	turnCtx, cancel := context.WithCancel(bridge.ctx)
	turnCtx = withHostTurnCancel(turnCtx, cancel)
	handle := &tuiTurnHandle{cancel: cancel}
	bridge.lifecycle.trackTurn(handle)
	defer bridge.lifecycle.completeTurn(handle)
	bridge.runner.SetWorking(true)
	defer bridge.runner.SetWorking(false)
	progress := &tuiSubagentProgress{}
	if surface := bridge.runner.Surface(); surface != nil {
		progress.block = surface.AddSubagent(name)
	}
	return run(turnCtx, progress)
}

func clampText(text string) string {
	clean := sanitizeTerm(text)
	runes := []rune(clean)
	if len(runes) <= 200 {
		return clean
	}
	return string(runes[:200]) + "..."
}
