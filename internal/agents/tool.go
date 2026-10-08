package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/digitalygo/smidja/internal/agent"
)

const SubagentToolName = "subagent"

type Tool struct {
	mu     sync.Mutex
	invoke func(ctx context.Context, name, task string) agent.Result
}

func NewTool(invoke func(ctx context.Context, name, task string) agent.Result) *Tool {
	return &Tool{invoke: invoke}
}

func (t *Tool) SetInvoke(invoke func(ctx context.Context, name, task string) agent.Result) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.invoke = invoke
	t.mu.Unlock()
}

func (t *Tool) Name() string { return SubagentToolName }

func (t *Tool) Description() string {
	return "Runs a named agent definition in an isolated child session and returns the bounded child answer. The child inherits the parent model unless the definition sets one, and can use only the parent-active tools the definition allows."
}

func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"Agent definition name to run."},"task":{"type":"string","description":"Plain-text task for the subagent; it is never interpreted as a shell command."}},"required":["name","task"]}`)
}

func (t *Tool) Exec(ctx context.Context, args json.RawMessage) agent.Result {
	var request struct {
		Name *string `json:"name"`
		Task *string `json:"task"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return agent.ErrorResult("subagent: invalid arguments: " + bounded(err.Error()))
	}
	if request.Name == nil || strings.TrimSpace(*request.Name) == "" {
		return agent.ErrorResult("subagent: missing required argument 'name'")
	}
	if request.Task == nil || strings.TrimSpace(*request.Task) == "" {
		return agent.ErrorResult("subagent: missing required argument 'task'")
	}
	if t == nil {
		return agent.ErrorResult("subagent: delegation is not available in this context")
	}
	t.mu.Lock()
	invoke := t.invoke
	t.mu.Unlock()
	if invoke == nil {
		return agent.ErrorResult("subagent: delegation is not available in this context")
	}
	return invoke(ctx, *request.Name, *request.Task)
}

func ToolResult(res Result, err error) agent.Result {
	var out strings.Builder
	fmt.Fprintf(&out, "[subagent %s tier=%s origin=%s depth=%d model=%s]\n", res.Name, res.Tier, res.Origin, res.Depth, res.Model)
	text := strings.TrimSpace(res.Answer)
	switch {
	case err != nil:
		message := bounded(err.Error())
		if text != "" && text != message {
			text = "error: " + message + "\n\n" + text
		} else {
			text = "error: " + message
		}
	case text == "":
		text = "(no output)"
	}
	out.WriteString(text)
	if res.Truncated && res.FullOutputPath == "" {
		out.WriteString("\n[subagent output truncated]")
	}
	if strings.TrimSpace(res.SessionPath) != "" {
		fmt.Fprintf(&out, "\n[subagent session: %s]", res.SessionPath)
	}
	result := agent.TextResult(out.String())
	if err != nil || res.IsError {
		result.IsError = true
	}
	return result
}

type childTools struct {
	parent      agent.ToolCatalog
	allowed     map[string]struct{}
	nested      *Tool
	buildNested func() *Tool
}

func (e *Executor) buildTools(definition Definition, parent Parent, client Client) (*childTools, error) {
	parentCatalog := parent.Tools
	if parentCatalog == nil {
		if parent.Depth > 0 {
			return nil, fmt.Errorf("agent %q: a nested child requires the immediate parent tool view", bounded(definition.Name))
		}
		parentCatalog = e.deps.ParentTools()
	}
	if definition.ToolsSet {
		for _, name := range definition.Tools {
			if _, ok := activeTool(parentCatalog, name); !ok {
				return nil, fmt.Errorf("agent %q: tool %q is not active for the parent", bounded(definition.Name), bounded(name))
			}
		}
	}
	tools := &childTools{parent: parentCatalog}
	if definition.ToolsSet {
		tools.allowed = make(map[string]struct{}, len(definition.Tools))
		for _, name := range definition.Tools {
			tools.allowed[name] = struct{}{}
		}
	}
	if tools.allows(SubagentToolName) {
		tools.buildNested = func() *Tool {
			return e.newNestedTool(definition, parent, client, tools)
		}
		if active, ok := activeTool(parentCatalog, SubagentToolName); ok {
			if _, ours := active.(*Tool); ours {
				tools.nested = tools.buildNested()
			}
		}
	}
	return tools, nil
}

func (e *Executor) newNestedTool(definition Definition, parent Parent, client Client, tools *childTools) *Tool {
	nestedParent := Parent{
		Generation:    parent.Generation,
		SessionID:     parent.SessionID,
		Model:         client.Model,
		WireModel:     client.Wire,
		Provider:      client.Provider,
		System:        childSystemPrompt(definition, parent),
		Thinking:      client.Thinking,
		ThinkingSet:   client.ThinkingSet,
		ReasoningSeam: client.ReasoningSeam,
		Depth:         parent.Depth + 1,
		Ancestry:      append(append([]string(nil), parent.Ancestry...), definition.Name),
		Tools:         tools,
	}
	return NewTool(func(ctx context.Context, name, task string) agent.Result {
		res, err := e.Run(ctx, Request{Name: name, Task: task, Parent: nestedParent})
		return ToolResult(res, err)
	})
}

func (t *childTools) allows(name string) bool {
	if t == nil {
		return false
	}
	if t.allowed == nil {
		return true
	}
	_, ok := t.allowed[name]
	return ok
}

func (t *childTools) subagentView(stored agent.Tool) (agent.Tool, bool) {
	if _, ours := stored.(*Tool); !ours {
		return stored, true
	}
	if t.nested != nil {
		return t.nested, true
	}
	if t.buildNested == nil {
		return nil, false
	}
	return t.buildNested(), true
}

func (t *childTools) Tools() []agent.Tool {
	if t == nil || t.parent == nil {
		return nil
	}
	out := make([]agent.Tool, 0, len(t.parent.Tools()))
	for _, tool := range t.parent.Tools() {
		if tool == nil {
			continue
		}
		name := tool.Name()
		if !t.allows(name) {
			continue
		}
		if name == SubagentToolName {
			view, ok := t.subagentView(tool)
			if !ok {
				continue
			}
			out = append(out, view)
			continue
		}
		out = append(out, tool)
	}
	return out
}

func (t *childTools) Get(name string) (agent.Tool, bool) {
	if t == nil || t.parent == nil || !t.allows(name) {
		return nil, false
	}
	stored, ok := t.parent.Get(name)
	if !ok {
		return nil, false
	}
	if name == SubagentToolName {
		return t.subagentView(stored)
	}
	return stored, true
}

func (t *childTools) GetActive(name string) (agent.Tool, bool) {
	if t == nil || t.parent == nil || !t.allows(name) {
		return nil, false
	}
	active, ok := activeTool(t.parent, name)
	if !ok {
		return nil, false
	}
	if name == SubagentToolName {
		return t.subagentView(active)
	}
	return active, true
}

func activeTool(catalog agent.ToolCatalog, name string) (agent.Tool, bool) {
	if catalog == nil {
		return nil, false
	}
	if active, ok := catalog.(agent.ActiveToolCatalog); ok {
		return active.GetActive(name)
	}
	return catalog.Get(name)
}
