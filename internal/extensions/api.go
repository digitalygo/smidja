package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensionui"
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
	mu      sync.RWMutex
	tools   map[string]agent.Tool
	source  map[string]string
	order   []string
	enabled map[string]struct{}
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
		if t, ok := c.tools[name]; ok && c.enabledLocked(name) {
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

func (c *ToolCatalog) GetActive(name string) (agent.Tool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.tools[name]
	if !ok || !c.enabledLocked(name) {
		return nil, false
	}
	return t, true
}

func (c *ToolCatalog) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.order))
	for _, name := range c.order {
		if _, ok := c.tools[name]; ok && c.enabledLocked(name) {
			out = append(out, name)
		}
	}
	return out
}

func (c *ToolCatalog) SetActive(names []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if names == nil {
		c.enabled = nil
		return nil
	}
	enabled := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := c.tools[name]; ok {
			enabled[name] = struct{}{}
		}
	}
	c.enabled = enabled
	return nil
}

func (c *ToolCatalog) enabledLocked(name string) bool {
	if c.enabled == nil {
		return true
	}
	_, ok := c.enabled[name]
	return ok
}

type toolCatalogSnapshot struct {
	tools   map[string]agent.Tool
	source  map[string]string
	order   []string
	enabled map[string]struct{}
}

func (c *ToolCatalog) snapshot() toolCatalogSnapshot {
	if c == nil {
		return toolCatalogSnapshot{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap := toolCatalogSnapshot{
		tools:  make(map[string]agent.Tool, len(c.tools)),
		source: make(map[string]string, len(c.source)),
		order:  append([]string(nil), c.order...),
	}
	for name, tool := range c.tools {
		snap.tools[name] = tool
	}
	for name, source := range c.source {
		snap.source[name] = source
	}
	if c.enabled != nil {
		snap.enabled = make(map[string]struct{}, len(c.enabled))
		for name := range c.enabled {
			snap.enabled[name] = struct{}{}
		}
	}
	return snap
}

func (c *ToolCatalog) restore(snap toolCatalogSnapshot) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools = snap.tools
	c.source = snap.source
	c.order = snap.order
	c.enabled = snap.enabled
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

type commandCatalogSnapshot struct {
	commands map[string]sdk.Command
	order    []string
}

func (c *CommandCatalog) snapshot() commandCatalogSnapshot {
	if c == nil {
		return commandCatalogSnapshot{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	snap := commandCatalogSnapshot{
		commands: make(map[string]sdk.Command, len(c.commands)),
		order:    append([]string(nil), c.order...),
	}
	for name, command := range c.commands {
		snap.commands[name] = command
	}
	return snap
}

func (c *CommandCatalog) restore(snap commandCatalogSnapshot) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commands = snap.commands
	c.order = snap.order
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

type Host struct {
	SetActiveTools   func(names []string) error
	AppendEntry      func(customType string, data any) error
	SetSessionName   func(name string) error
	LabelEntry       func(entryID, label string) error
	SendMessage      func(msg sdk.CustomMessage, opts sdk.SendOptions) error
	SendUserMessage  func(text string, opts sdk.SendOptions) error
	Exec             func(ctx context.Context, command string, args []string, opts sdk.ExecOptions) (*sdk.ExecResult, error)
	SetModel         func(m sdk.Model) error
	SetThinkingLevel func(level sdk.ThinkingLevel) error
	RemoveProvider   func(name string) error
}

type APIOptions struct {
	Catalog       *ToolCatalog
	Commands      *CommandCatalog
	ResolveConfig func(key string) string
	UI            *extensionui.Registry
	Host          *Host
	Flags         *FlagRegistry
	Providers     *ProviderRegistry
	Events        *CustomEventBus
}

type api struct {
	catalog   *ToolCatalog
	commands  *CommandCatalog
	resolve   func(key string) string
	ui        *extensionui.Registry
	host      *Host
	flags     *FlagRegistry
	providers *ProviderRegistry
	events    *CustomEventBus
}

var _ sdk.API = (*api)(nil)

var _ sdk.CustomEventSubscription = (*api)(nil)

func NewAPI(opts APIOptions) sdk.API {
	if opts.Catalog == nil {
		opts.Catalog = NewToolCatalog()
	}
	if opts.Commands == nil {
		opts.Commands = NewCommandCatalog()
	}
	if opts.UI == nil {
		opts.UI = extensionui.NewRegistry()
	}
	return &api{
		catalog:   opts.Catalog,
		commands:  opts.Commands,
		resolve:   opts.ResolveConfig,
		ui:        opts.UI,
		host:      opts.Host,
		flags:     opts.Flags,
		providers: opts.Providers,
		events:    opts.Events,
	}
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
	if a.host == nil || a.host.SetActiveTools == nil {
		return unavailable("SetActiveTools")
	}
	return a.host.SetActiveTools(names)
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
	if a.host == nil || a.host.SendMessage == nil {
		return unavailable("SendMessage")
	}
	return a.host.SendMessage(msg, opts)
}

func (a *api) SendUserMessage(text string, opts sdk.SendOptions) error {
	if a.host == nil || a.host.SendUserMessage == nil {
		return unavailable("SendUserMessage")
	}
	return a.host.SendUserMessage(text, opts)
}

func (a *api) AppendEntry(customType string, data any) error {
	if a.host == nil || a.host.AppendEntry == nil {
		return unavailable("AppendEntry")
	}
	return a.host.AppendEntry(customType, data)
}

func (a *api) SetSessionName(name string) error {
	if a.host == nil || a.host.SetSessionName == nil {
		return unavailable("SetSessionName")
	}
	return a.host.SetSessionName(name)
}

func (a *api) LabelEntry(entryID, label string) error {
	if a.host == nil || a.host.LabelEntry == nil {
		return unavailable("LabelEntry")
	}
	return a.host.LabelEntry(entryID, label)
}

func (a *api) SetModel(m sdk.Model) error {
	if a.host == nil || a.host.SetModel == nil {
		return unavailable("SetModel")
	}
	return a.host.SetModel(m)
}

func (a *api) SetThinkingLevel(level sdk.ThinkingLevel) error {
	if a.host == nil || a.host.SetThinkingLevel == nil {
		return unavailable("SetThinkingLevel")
	}
	return a.host.SetThinkingLevel(level)
}

func (a *api) RegisterProvider(name string, cfg sdk.ProviderConfig) error {
	if a.providers == nil {
		return unavailable("RegisterProvider")
	}
	return a.providers.Register(name, cfg)
}

func (a *api) RemoveProvider(name string) error {
	if a.host != nil && a.host.RemoveProvider != nil {
		return a.host.RemoveProvider(name)
	}
	if a.providers == nil {
		return unavailable("RemoveProvider")
	}
	return a.providers.Remove(name)
}

func (a *api) RegisterFlag(name string, opts sdk.FlagOptions) error {
	if a.flags == nil {
		return unavailable("RegisterFlag")
	}
	return a.flags.Register(name, opts)
}

func (a *api) Flags() map[string]any {
	if a.flags == nil {
		return map[string]any{}
	}
	return a.flags.Values()
}

func (a *api) Exec(command string, args []string, opts sdk.ExecOptions) (*sdk.ExecResult, error) {
	if a.host == nil || a.host.Exec == nil {
		return nil, unavailable("Exec")
	}
	return a.host.Exec(context.Background(), command, args, opts)
}

func (a *api) EmitCustomEvent(name string, data any) error {
	if a.events == nil {
		return unavailable("EmitCustomEvent")
	}
	return a.events.Emit(name, data)
}

func (a *api) SubscribeCustomEvent(name string, handler sdk.CustomEventHandler) (func(), error) {
	if a.events == nil {
		return nil, unavailable("SubscribeCustomEvent")
	}
	return a.events.Subscribe(name, handler)
}

type toolAdapter struct {
	sdk.Tool
}

var _ agent.Tool = (*toolAdapter)(nil)

func (t *toolAdapter) Exec(ctx context.Context, args json.RawMessage) agent.Result {
	res := t.Tool.Exec(ctx, args)
	return agent.Result{Content: blocksFromSDK(res.Content), IsError: res.IsError}
}
