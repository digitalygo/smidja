package extensionui

import (
	"errors"
	"testing"

	"github.com/digitalygo/smidja/sdk"
)

type idComponent struct{ id string }

func (c idComponent) Render(width int) []string { return []string{c.id} }

func (c idComponent) Invalidate() {}

func componentID(component sdk.Component) string {
	lines := component.Render(10)
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func rendererFor(id string) sdk.MessageRenderer {
	return func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return idComponent{id: id}
	}
}

func entryRendererFor(id string) sdk.EntryRenderer {
	return func(ctx sdk.RenderContext, entry sdk.Entry) sdk.Component {
		return idComponent{id: id}
	}
}

func TestRegistryRejectsEmptyAndNil(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterComponent("", func() sdk.Component { return idComponent{} }); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("RegisterComponent empty key = %v, want ErrEmptyKey", err)
	}
	if err := registry.RegisterComponent("key", nil); !errors.Is(err, ErrNilValue) {
		t.Fatalf("RegisterComponent nil factory = %v, want ErrNilValue", err)
	}
	if err := registry.RegisterMessageRenderer("", rendererFor("a")); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("RegisterMessageRenderer empty type = %v, want ErrEmptyKey", err)
	}
	if err := registry.RegisterMessageRenderer("type", nil); !errors.Is(err, ErrNilValue) {
		t.Fatalf("RegisterMessageRenderer nil renderer = %v, want ErrNilValue", err)
	}
	if err := registry.RegisterMarkdownTransformer("", func(markdown string, ctx sdk.MarkdownTransformContext) string { return markdown }); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("RegisterMarkdownTransformer empty name = %v, want ErrEmptyKey", err)
	}
	if err := registry.RegisterTerminalInputHook("", func(data string) sdk.TerminalInputResult { return sdk.TerminalInputResult{} }); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("RegisterTerminalInputHook empty key = %v, want ErrEmptyKey", err)
	}
	if err := registry.UnregisterComponent("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UnregisterComponent missing = %v, want ErrNotFound", err)
	}
}

func TestRegistryOrderAndReplacement(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterMessageRenderer("a", rendererFor("a1")); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterMessageRenderer("b", rendererFor("b1")); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterMessageRenderer("a", rendererFor("a2")); err != nil {
		t.Fatal(err)
	}
	if got := registry.MessageRendererTypes(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("types after replacement = %v, want [a b] with a in place", got)
	}
	renderers := registry.MessageRenderers()
	if len(renderers) != 2 || componentID(renderers[0](sdk.RenderContext{}, sdk.CustomMessage{})) != "a2" {
		t.Fatalf("first renderer = %v, want the replacement a2", len(renderers))
	}
	selected, ok := registry.MessageRenderer("a")
	if !ok || componentID(selected(sdk.RenderContext{}, sdk.CustomMessage{})) != "a2" {
		t.Fatal("MessageRenderer lookup did not return the replacement")
	}
	if err := registry.UnregisterMessageRenderer("a"); err != nil {
		t.Fatal(err)
	}
	if got := registry.MessageRendererTypes(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("types after unregister = %v, want [b]", got)
	}
	if err := registry.RegisterEntryRenderer("entry", entryRendererFor("e1")); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterEntryRenderer("entry", entryRendererFor("e2")); err != nil {
		t.Fatal(err)
	}
	entries := registry.EntryRenderers()
	if len(entries) != 1 || componentID(entries[0](sdk.RenderContext{}, sdk.Entry{})) != "e2" {
		t.Fatal("entry replacement did not keep a single ordered entry")
	}
}

func TestRegistryWidgetsAndInputHooks(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterWidget("w1", func() sdk.Component { return idComponent{id: "one"} }); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterWidget("w2", func() sdk.Component { return idComponent{id: "two"} }); err != nil {
		t.Fatal(err)
	}
	widgets := registry.Widgets()
	if len(widgets) != 2 || componentID(widgets["w2"]()) != "two" {
		t.Fatalf("widgets = %v", widgets)
	}
	if got := registry.WidgetKeys(); len(got) != 2 || got[0] != "w1" || got[1] != "w2" {
		t.Fatalf("widget order = %v", got)
	}
	if err := registry.UnregisterWidget("w1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Widgets()["w1"]; ok {
		t.Fatal("unregistered widget is still present")
	}
	calls := 0
	handler := func(data string) sdk.TerminalInputResult {
		calls++
		return sdk.TerminalInputResult{Replace: true, Data: data + "!"}
	}
	if err := registry.RegisterTerminalInputHook("hook", handler); err != nil {
		t.Fatal(err)
	}
	hooks := registry.TerminalInputHooks()
	if len(hooks) != 1 || !hooks[0]("x").Replace {
		t.Fatal("terminal input hook not returned")
	}
	if calls != 1 {
		t.Fatalf("hook calls = %d, want 1", calls)
	}
	if err := registry.UnregisterTerminalInputHook("hook"); err != nil {
		t.Fatal(err)
	}
	if got := registry.TerminalInputHooks(); len(got) != 0 {
		t.Fatalf("hooks after unregister = %d, want 0", len(got))
	}
}

func TestRegistryChangeNotificationIsReentrant(t *testing.T) {
	registry := NewRegistry()
	var observed int
	registry.SetOnChange(func() {
		observed++
		_ = registry.MessageRendererTypes()
		_ = registry.WidgetKeys()
	})
	if err := registry.RegisterComponent("c", func() sdk.Component { return idComponent{id: "c"} }); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterMarkdownTransformer("t", func(markdown string, ctx sdk.MarkdownTransformContext) string { return markdown }); err != nil {
		t.Fatal(err)
	}
	if observed != 2 {
		t.Fatalf("change notifications = %d, want 2", observed)
	}
	registry.SetOnChange(nil)
	if err := registry.RegisterComponent("d", func() sdk.Component { return idComponent{id: "d"} }); err != nil {
		t.Fatal(err)
	}
	if observed != 2 {
		t.Fatalf("notifications after clearing = %d, want 2", observed)
	}
}

func TestRegistryComponentLookupAndOrder(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterComponent("first", func() sdk.Component { return idComponent{id: "1"} }); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterComponent("second", func() sdk.Component { return idComponent{id: "2"} }); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterComponent("first", func() sdk.Component { return idComponent{id: "one"} }); err != nil {
		t.Fatal(err)
	}
	if got := registry.ComponentKeys(); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("component order = %v", got)
	}
	factory, ok := registry.Component("first")
	if !ok || componentID(factory()) != "one" {
		t.Fatal("Component did not return the replacement factory")
	}
	components := registry.Components()
	if len(components) != 2 || componentID(components[0]()) != "one" || componentID(components[1]()) != "2" {
		t.Fatalf("Components order = %d", len(components))
	}
	if err := registry.UnregisterComponent("first"); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Component("first"); ok {
		t.Fatal("unregistered component still resolves")
	}
}

func TestRegistryEntryAndTransformerLookup(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterEntryRenderer("audit", entryRendererFor("e1")); err != nil {
		t.Fatal(err)
	}
	if got := registry.EntryRendererTypes(); len(got) != 1 || got[0] != "audit" {
		t.Fatalf("entry types = %v", got)
	}
	renderer, ok := registry.EntryRenderer("audit")
	if !ok || componentID(renderer(sdk.RenderContext{}, sdk.Entry{})) != "e1" {
		t.Fatal("EntryRenderer lookup failed")
	}
	if err := registry.UnregisterEntryRenderer("audit"); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.EntryRenderer("audit"); ok {
		t.Fatal("unregistered entry renderer still resolves")
	}
	if err := registry.UnregisterEntryRenderer("audit"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeated entry unregister = %v, want ErrNotFound", err)
	}
	first := func(markdown string, ctx sdk.MarkdownTransformContext) string { return markdown + "1" }
	second := func(markdown string, ctx sdk.MarkdownTransformContext) string { return markdown + "2" }
	if err := registry.RegisterMarkdownTransformer("a", first); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterMarkdownTransformer("b", second); err != nil {
		t.Fatal(err)
	}
	names := registry.MarkdownTransformerNames()
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("transformer names = %v", names)
	}
	transformers := registry.MarkdownTransformers()
	if len(transformers) != 2 || transformers[0]("x", sdk.MarkdownTransformContext{}) != "x1" || transformers[1]("x", sdk.MarkdownTransformContext{}) != "x2" {
		t.Fatal("transformer order is wrong")
	}
	if err := registry.UnregisterMarkdownTransformer("a"); err != nil {
		t.Fatal(err)
	}
	if got := registry.MarkdownTransformers(); len(got) != 1 || got[0]("x", sdk.MarkdownTransformContext{}) != "x2" {
		t.Fatalf("transformers after unregister = %d", len(got))
	}
	if err := registry.UnregisterMarkdownTransformer("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeated transformer unregister = %v, want ErrNotFound", err)
	}
	if got := registry.TerminalInputHookKeys(); len(got) != 0 {
		t.Fatalf("input hook keys = %v, want empty", got)
	}
}

func TestRegistryDuplicateValuesAndLookups(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterMessageRenderer("a", rendererFor("a1")); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterEntryRenderer("a", entryRendererFor("e-any")); err != nil {
		t.Fatal(err)
	}
	if got := registry.EntryRenderers(); len(got) != 1 {
		t.Fatalf("entry renderers = %d", len(got))
	}
	if err := registry.RegisterComponent("", func() sdk.Component { return nil }); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("component empty key = %v", err)
	}
	if _, ok := registry.Component("missing"); ok {
		t.Fatal("missing component resolved")
	}
	if _, ok := registry.MessageRenderer("missing"); ok {
		t.Fatal("missing message renderer resolved")
	}
	if got := registry.Components(); len(got) != 0 {
		t.Fatalf("components = %v, want empty", got)
	}
}

func TestRegistryIsolation(t *testing.T) {
	first := NewRegistry()
	second := NewRegistry()
	if err := first.RegisterMessageRenderer("shared", rendererFor("first")); err != nil {
		t.Fatal(err)
	}
	if _, ok := second.MessageRenderer("shared"); ok {
		t.Fatal("renderer leaked between registry instances")
	}
	if err := second.RegisterMessageRenderer("shared", rendererFor("second")); err != nil {
		t.Fatal(err)
	}
	firstRenderer, _ := first.MessageRenderer("shared")
	secondRenderer, _ := second.MessageRenderer("shared")
	if componentID(firstRenderer(sdk.RenderContext{}, sdk.CustomMessage{})) == componentID(secondRenderer(sdk.RenderContext{}, sdk.CustomMessage{})) {
		t.Fatal("registry instances share registration values")
	}
}
