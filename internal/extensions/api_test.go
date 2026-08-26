package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

type apiProbeTool struct {
	name    string
	execCtx context.Context
	args    json.RawMessage
	calls   int
}

func (t *apiProbeTool) Name() string        { return t.name }
func (t *apiProbeTool) Description() string { return "probe " + t.name }
func (t *apiProbeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *apiProbeTool) Exec(ctx context.Context, args json.RawMessage) agent.Result {
	t.execCtx = ctx
	t.args = args
	t.calls++
	return agent.Result{Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "ok"}}}
}

func sdkTestTool(name string) *sdkProbeTool {
	return &sdkProbeTool{name: name}
}

type sdkProbeTool struct {
	name  string
	args  json.RawMessage
	calls int
}

func (t *sdkProbeTool) Name() string        { return t.name }
func (t *sdkProbeTool) Description() string { return "sdk probe " + t.name }
func (t *sdkProbeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t *sdkProbeTool) Exec(ctx context.Context, args json.RawMessage) sdk.Result {
	t.args = args
	t.calls++
	return sdk.Result{Content: []sdk.Block{{Type: agent.BlockTypeText, Text: "ok"}}}
}

func TestToolCatalogRegisterToolsAndGet(t *testing.T) {
	c := NewToolCatalog()
	if err := c.Register(&apiProbeTool{name: "alpha"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := c.Register(&apiProbeTool{name: "beta"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	tools := c.Tools()
	if len(tools) != 2 || tools[0].Name() != "alpha" || tools[1].Name() != "beta" {
		t.Fatalf("Tools() = %v, want [alpha beta] in registration order", toolNames(tools))
	}
	if got, ok := c.Get("beta"); !ok || got.Name() != "beta" {
		t.Fatalf("Get(beta) = %v, %v", got, ok)
	}
	if _, ok := c.Get("missing"); ok {
		t.Fatal("Get(missing) found a tool")
	}
	if names := c.Names(); len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("Names() = %v", names)
	}
}

func TestToolCatalogReplaceKeepsPosition(t *testing.T) {
	c := NewToolCatalog()
	c.Register(&apiProbeTool{name: "alpha"})
	c.Register(&apiProbeTool{name: "beta"})
	replacement := &apiProbeTool{name: "alpha"}
	if err := c.Register(replacement); err != nil {
		t.Fatalf("Register: %v", err)
	}
	tools := c.Tools()
	if len(tools) != 2 || tools[0].Name() != "alpha" {
		t.Fatalf("Tools() = %v, want replaced alpha first", toolNames(tools))
	}
	if got, _ := c.Get("alpha"); got != replacement {
		t.Fatal("Get(alpha) did not return the replacement")
	}
}

func TestToolCatalogUnregister(t *testing.T) {
	c := NewToolCatalog()
	c.Register(&apiProbeTool{name: "alpha"})
	c.Register(&apiProbeTool{name: "beta"})
	if err := c.Unregister("alpha"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if tools := c.Tools(); len(tools) != 1 || tools[0].Name() != "beta" {
		t.Fatalf("Tools() = %v, want [beta]", toolNames(tools))
	}
	if err := c.Unregister("alpha"); !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("Unregister(missing) error = %v, want ErrToolNotFound", err)
	}
}

func TestToolCatalogRejectsNilAndEmptyName(t *testing.T) {
	c := NewToolCatalog()
	if err := c.Register(nil); !errors.Is(err, ErrNilTool) {
		t.Fatalf("Register(nil) = %v, want ErrNilTool", err)
	}
	if err := c.Register(&apiProbeTool{}); err == nil {
		t.Fatal("Register with empty name must fail")
	}
}

func TestToolCatalogAllInfo(t *testing.T) {
	c := NewToolCatalog()
	c.RegisterSource(&apiProbeTool{name: "alpha"}, "mcp:server-a")
	c.RegisterSource(&apiProbeTool{name: "beta"}, "extension")
	infos := c.AllInfo()
	if len(infos) != 2 {
		t.Fatalf("AllInfo() = %d entries, want 2", len(infos))
	}
	if infos[0].Name != "alpha" || infos[0].Source != "mcp:server-a" {
		t.Fatalf("AllInfo()[0] = %+v", infos[0])
	}
	if infos[1].Name != "beta" || infos[1].Source != "extension" {
		t.Fatalf("AllInfo()[1] = %+v", infos[1])
	}
	if string(infos[0].Schema) != `{"type":"object"}` {
		t.Fatalf("AllInfo()[0].Schema = %s", infos[0].Schema)
	}
}

func TestCommandCatalogNumericSuffixes(t *testing.T) {
	c := NewCommandCatalog()
	cmd := sdk.Command{Description: "first"}
	first, err := c.Register("repeat", cmd)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	second, err := c.Register("repeat", cmd)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	third, err := c.Register("repeat", cmd)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if first != "repeat" || second != "repeat2" || third != "repeat3" {
		t.Fatalf("registered names = %q %q %q", first, second, third)
	}
	list := c.List()
	if len(list) != 3 || list[0].Name != "repeat" || list[1].Name != "repeat2" || list[2].Name != "repeat3" {
		t.Fatalf("List() = %+v", list)
	}
	if _, err := c.Register("", cmd); err == nil {
		t.Fatal("Register with empty name must fail")
	}
}

func TestAPIUnavailableMethodsAreExplicit(t *testing.T) {
	api := NewAPI(APIOptions{})
	cases := []struct {
		name string
		call func() error
	}{
		{"SetActiveTools", func() error { return api.SetActiveTools([]string{"a"}) }},
		{"SendMessage", func() error { return api.SendMessage(sdk.CustomMessage{}, sdk.SendOptions{}) }},
		{"SendUserMessage", func() error { return api.SendUserMessage("hi", sdk.SendOptions{}) }},
		{"AppendEntry", func() error { return api.AppendEntry("custom", nil) }},
		{"SetSessionName", func() error { return api.SetSessionName("s") }},
		{"LabelEntry", func() error { return api.LabelEntry("e", "l") }},
		{"SetModel", func() error { return api.SetModel(sdk.Model{}) }},
		{"SetThinkingLevel", func() error { return api.SetThinkingLevel(sdk.ThinkingHigh) }},
		{"RegisterProvider", func() error { return api.RegisterProvider("p", sdk.ProviderConfig{}) }},
		{"RemoveProvider", func() error { return api.RemoveProvider("p") }},
		{"RegisterFlag", func() error { return api.RegisterFlag("f", sdk.FlagOptions{}) }},
		{"Exec", func() error { _, err := api.Exec("ls", nil, sdk.ExecOptions{}); return err }},
		{"EmitCustomEvent", func() error { return api.EmitCustomEvent("e", nil) }},
	}
	for _, tc := range cases {
		if err := tc.call(); err == nil {
			t.Errorf("%s: want an explicit error", tc.name)
		} else if !errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: error %v does not wrap ErrUnavailable", tc.name, err)
		} else if !strings.Contains(err.Error(), "not available in this release") {
			t.Errorf("%s: error %q lacks the availability note", tc.name, err)
		}
	}
}

func TestAPIUnavailableMethodsReturnNilResults(t *testing.T) {
	api := NewAPI(APIOptions{})
	if res, err := api.Exec("ls", nil, sdk.ExecOptions{}); res != nil || err == nil {
		t.Fatalf("Exec = %v, %v; want nil result and error", res, err)
	}
	if flags := api.Flags(); flags == nil || len(flags) != 0 {
		t.Fatalf("Flags() = %v, want an empty map", flags)
	}
}

func TestAPIRegisterToolReachesCatalog(t *testing.T) {
	catalog := NewToolCatalog()
	api := NewAPI(APIOptions{Catalog: catalog})
	probe := sdkTestTool("ext-tool")
	if err := api.RegisterTool(probe); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	if got, ok := catalog.Get("ext-tool"); !ok {
		t.Fatal("registered tool missing from the catalog")
	} else if got.Name() != "ext-tool" {
		t.Fatalf("catalog tool name = %q", got.Name())
	}
	infos := api.AllTools()
	if len(infos) != 1 || infos[0].Name != "ext-tool" || infos[0].Source != "extension" {
		t.Fatalf("AllTools() = %+v", infos)
	}
	active := api.ActiveTools()
	if len(active) != 1 || active[0] != "ext-tool" {
		t.Fatalf("ActiveTools() = %v", active)
	}
	if err := api.UnregisterTool("ext-tool"); err != nil {
		t.Fatalf("UnregisterTool: %v", err)
	}
	if err := api.UnregisterTool("ext-tool"); !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("UnregisterTool(missing) = %v, want ErrToolNotFound", err)
	}
}

func TestAPIRegisteredToolExecutesWithAgentArgs(t *testing.T) {
	catalog := NewToolCatalog()
	api := NewAPI(APIOptions{Catalog: catalog})
	probe := sdkTestTool("ext-tool")
	if err := api.RegisterTool(probe); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	got, ok := catalog.Get("ext-tool")
	if !ok {
		t.Fatal("tool not found")
	}
	args := json.RawMessage(`{"x":1}`)
	res := got.Exec(context.Background(), args)
	if probe.calls != 1 {
		t.Fatalf("probe executed %d times, want 1", probe.calls)
	}
	if string(probe.args) != `{"x":1}` {
		t.Fatalf("probe args = %s", probe.args)
	}
	if res.IsError || len(res.Content) != 1 || res.Content[0].Text != "ok" {
		t.Fatalf("result = %+v", res)
	}
}

func TestAPIConfigValueUsesResolver(t *testing.T) {
	api := NewAPI(APIOptions{ResolveConfig: func(key string) string {
		if key == "SMIDJA_MODEL" {
			return "resolved/model"
		}
		return ""
	}})
	if got := api.ConfigValue("SMIDJA_MODEL"); got != "resolved/model" {
		t.Fatalf("ConfigValue = %q", got)
	}
	if got := api.ConfigValue("SMIDJA_UNKNOWN"); got != "" {
		t.Fatalf("ConfigValue(unknown) = %q, want empty", got)
	}
}

func TestAPIConfigValueNilResolverIsEmpty(t *testing.T) {
	api := NewAPI(APIOptions{})
	if got := api.ConfigValue("SMIDJA_MODEL"); got != "" {
		t.Fatalf("ConfigValue without resolver = %q, want empty", got)
	}
}

func TestAPICommandsReachCatalog(t *testing.T) {
	commands := NewCommandCatalog()
	api := NewAPI(APIOptions{Commands: commands})
	cmd := sdk.Command{Description: "hello command", Handler: func(ctx sdk.CommandContext, args string) error { return nil }}
	if err := api.RegisterCommand("hello", cmd); err != nil {
		t.Fatalf("RegisterCommand: %v", err)
	}
	list := api.Commands()
	if len(list) != 1 || list[0].Name != "hello" || list[0].Description != "hello command" {
		t.Fatalf("Commands() = %+v", list)
	}
	if got, ok := commands.Get("hello"); !ok || got.Description != "hello command" {
		t.Fatal("command missing from the catalog")
	}
}

func TestNewAPIWithNilOptionsDefaults(t *testing.T) {
	api := NewAPI(APIOptions{})
	if err := api.RegisterTool(sdkTestTool("t")); err != nil {
		t.Fatalf("RegisterTool with default catalog: %v", err)
	}
	if len(api.ActiveTools()) != 1 {
		t.Fatalf("ActiveTools() = %v", api.ActiveTools())
	}
}

func toolNames(tools []agent.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t != nil {
			out = append(out, t.Name())
		}
	}
	return out
}

func TestRuntimeHandlerContextExposesAPI(t *testing.T) {
	catalog := NewToolCatalog()
	api := NewAPI(APIOptions{Catalog: catalog})
	rt := NewRuntime(NewRegistry()).SetAPI(func() sdk.API { return api })
	hc := rt.HandlerContext(context.Background())
	if hc == nil {
		t.Fatal("HandlerContext returned nil")
	}
	if hc.Mode() != sdk.ModePrint {
		t.Fatalf("default HandlerContext Mode = %q, want ModePrint", hc.Mode())
	}
	if _, ok := hc.(sdk.CommandContext); ok {
		t.Fatal("HandlerContext must not be a CommandContext")
	}
}
