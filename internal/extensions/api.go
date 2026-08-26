package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

var (
	ErrUnavailable = errors.New("extensions: API method not available in this release")

	ErrToolNotFound = errors.New("extensions: tool not registered")

	ErrNilTool = errors.New("extensions: nil tool")
)

func unavailable(method string) error {
	return fmt.Errorf("%w: %s", ErrUnavailable, method)
}

type ToolCatalog struct {
	mu     sync.RWMutex
	tools  map[string]agent.Tool
	source map[string]string
	order  []string
}

var _ agent.ToolCatalog = (*ToolCatalog)(nil)

func NewToolCatalog() *ToolCatalog {
	return &ToolCatalog{
		tools:  make(map[string]agent.Tool),
		source: make(map[string]string),
	}
}

func (c *ToolCatalog) Register(t agent.Tool) error {
	return c.RegisterSource(t, "")
}

func (c *ToolCatalog) RegisterSource(t agent.Tool, source string) error {
	if t == nil {
		return ErrNilTool
	}
	name := t.Name()
	if name == "" {
		return errors.New("extensions: register tool with empty name")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.tools[name]; !ok {
		c.order = append(c.order, name)
	}
	c.tools[name] = t
	c.source[name] = source
	return nil
}

func (c *ToolCatalog) Unregister(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.tools[name]; !ok {
		return fmt.Errorf("%w: %s", ErrToolNotFound, name)
	}
	delete(c.tools, name)
	delete(c.source, name)
	for i, n := range c.order {
		if n == name {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	return nil
}

func (c *ToolCatalog) Tools() []agent.Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]agent.Tool, 0, len(c.order))
	for _, name := range c.order {
		if t, ok := c.tools[name]; ok {
			out = append(out, t)
		}
	}
	return out
}

func (c *ToolCatalog) Get(name string) (agent.Tool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.tools[name]
	return t, ok
}

func (c *ToolCatalog) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.order))
	for _, name := range c.order {
		if _, ok := c.tools[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

func (c *ToolCatalog) AllInfo() []sdk.ToolInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]sdk.ToolInfo, 0, len(c.order))
	for _, name := range c.order {
		t, ok := c.tools[name]
		if !ok {
			continue
		}
		out = append(out, sdk.ToolInfo{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      cloneRaw(t.Schema()),
			Source:      c.source[name],
		})
	}
	return out
}

type CommandCatalog struct {
	mu       sync.RWMutex
	commands map[string]sdk.Command
	order    []string
}

func NewCommandCatalog() *CommandCatalog {
	return &CommandCatalog{commands: make(map[string]sdk.Command)}
}

func (c *CommandCatalog) Register(name string, cmd sdk.Command) (string, error) {
	if name == "" {
		return "", errors.New("extensions: register command with empty name")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	registered := name
	if _, taken := c.commands[registered]; taken {
		for i := 2; ; i++ {
			candidate := fmt.Sprintf("%s%d", name, i)
			if _, ok := c.commands[candidate]; !ok {
				registered = candidate
				break
			}
		}
	}
	c.commands[registered] = cmd
	c.order = append(c.order, registered)
	return registered, nil
}

func (c *CommandCatalog) Get(name string) (sdk.Command, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cmd, ok := c.commands[name]
	return cmd, ok
}

func (c *CommandCatalog) List() []sdk.CommandInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]sdk.CommandInfo, 0, len(c.order))
	for _, name := range c.order {
		if cmd, ok := c.commands[name]; ok {
			out = append(out, sdk.CommandInfo{Name: name, Description: cmd.Description})
		}
	}
	return out
}

type APIOptions struct {
	Catalog       *ToolCatalog
	Commands      *CommandCatalog
	ResolveConfig func(key string) string
}

type api struct {
	catalog  *ToolCatalog
	commands *CommandCatalog
	resolve  func(key string) string
}

var _ sdk.API = (*api)(nil)

func NewAPI(opts APIOptions) sdk.API {
	if opts.Catalog == nil {
		opts.Catalog = NewToolCatalog()
	}
	if opts.Commands == nil {
		opts.Commands = NewCommandCatalog()
	}
	return &api{catalog: opts.Catalog, commands: opts.Commands, resolve: opts.ResolveConfig}
}

func (a *api) RegisterTool(t sdk.Tool) error {
	if t == nil {
		return ErrNilTool
	}
	return a.catalog.RegisterSource(&toolAdapter{Tool: t}, "extension")
}

func (a *api) UnregisterTool(name string) error {
	return a.catalog.Unregister(name)
}

func (a *api) ActiveTools() []string {
	return a.catalog.Names()
}

func (a *api) SetActiveTools(names []string) error {
	return unavailable("SetActiveTools")
}

func (a *api) AllTools() []sdk.ToolInfo {
	return a.catalog.AllInfo()
}

func (a *api) ConfigValue(key string) string {
	if a.resolve == nil {
		return ""
	}
	return a.resolve(key)
}

func (a *api) RegisterCommand(name string, cmd sdk.Command) error {
	_, err := a.commands.Register(name, cmd)
	return err
}

func (a *api) Commands() []sdk.CommandInfo {
	return a.commands.List()
}

func (a *api) SendMessage(msg sdk.CustomMessage, opts sdk.SendOptions) error {
	return unavailable("SendMessage")
}

func (a *api) SendUserMessage(text string, opts sdk.SendOptions) error {
	return unavailable("SendUserMessage")
}

func (a *api) AppendEntry(customType string, data any) error {
	return unavailable("AppendEntry")
}

func (a *api) SetSessionName(name string) error {
	return unavailable("SetSessionName")
}

func (a *api) LabelEntry(entryID, label string) error {
	return unavailable("LabelEntry")
}

func (a *api) SetModel(m sdk.Model) error {
	return unavailable("SetModel")
}

func (a *api) SetThinkingLevel(level sdk.ThinkingLevel) error {
	return unavailable("SetThinkingLevel")
}

func (a *api) RegisterProvider(name string, cfg sdk.ProviderConfig) error {
	return unavailable("RegisterProvider")
}

func (a *api) RemoveProvider(name string) error {
	return unavailable("RemoveProvider")
}

func (a *api) RegisterFlag(name string, opts sdk.FlagOptions) error {
	return unavailable("RegisterFlag")
}

func (a *api) Flags() map[string]any {
	return map[string]any{}
}

func (a *api) Exec(command string, args []string, opts sdk.ExecOptions) (*sdk.ExecResult, error) {
	return nil, unavailable("Exec")
}

func (a *api) EmitCustomEvent(name string, data any) error {
	return unavailable("EmitCustomEvent")
}

type toolAdapter struct {
	sdk.Tool
}

var _ agent.Tool = (*toolAdapter)(nil)

func (t *toolAdapter) Exec(ctx context.Context, args json.RawMessage) agent.Result {
	res := t.Tool.Exec(ctx, args)
	return agent.Result{Content: blocksFromSDK(res.Content), IsError: res.IsError}
}
