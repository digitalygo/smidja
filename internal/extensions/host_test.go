package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/sdk"
)

func TestToolCatalogActiveViewFiltersAndKeepsOrder(t *testing.T) {
	catalog := NewToolCatalog()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := catalog.RegisterSource(&apiProbeTool{name: name}, "test"); err != nil {
			t.Fatalf("RegisterSource(%s): %v", name, err)
		}
	}
	if err := catalog.SetActive([]string{"gamma", "missing", "alpha"}); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if names := catalog.Names(); len(names) != 2 || names[0] != "alpha" || names[1] != "gamma" {
		t.Fatalf("Names() = %v, want [alpha gamma] in registration order", names)
	}
	if tools := toolNames(catalog.Tools()); len(tools) != 2 || tools[0] != "alpha" || tools[1] != "gamma" {
		t.Fatalf("Tools() = %v, want the enabled view", tools)
	}
	if _, ok := catalog.Get("beta"); !ok {
		t.Fatal("Get must keep returning disabled registered tools")
	}
	if _, ok := catalog.GetActive("beta"); ok {
		t.Fatal("GetActive must not return a disabled tool")
	}
	if _, ok := catalog.GetActive("missing"); ok {
		t.Fatal("GetActive must not return an unknown tool")
	}
	if infos := catalog.AllInfo(); len(infos) != 3 {
		t.Fatalf("AllInfo() = %d entries, want all 3 registered tools", len(infos))
	}
}

func TestToolCatalogActiveViewReplacementAndReset(t *testing.T) {
	catalog := NewToolCatalog()
	catalog.Register(&apiProbeTool{name: "alpha"})
	catalog.Register(&apiProbeTool{name: "beta"})
	if err := catalog.SetActive([]string{"beta"}); err != nil {
		t.Fatal(err)
	}
	replacement := &apiProbeTool{name: "beta"}
	if err := catalog.Register(replacement); err != nil {
		t.Fatal(err)
	}
	if got, ok := catalog.GetActive("beta"); !ok || got != replacement {
		t.Fatal("replacement must stay active in place")
	}
	alphaReplacement := &apiProbeTool{name: "alpha"}
	catalog.Register(alphaReplacement)
	if _, ok := catalog.GetActive("alpha"); ok {
		t.Fatal("replacing a disabled tool must not activate it")
	}
	if err := catalog.SetActive(nil); err != nil {
		t.Fatal(err)
	}
	if names := catalog.Names(); len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("Names() after reset = %v, want all registered tools", names)
	}
	if err := catalog.SetActive([]string{}); err != nil {
		t.Fatal(err)
	}
	if names := catalog.Names(); len(names) != 0 {
		t.Fatalf("Names() after empty SetActive = %v, want none", names)
	}
}

func TestAPIHostBackedActionsDelegate(t *testing.T) {
	catalog := NewToolCatalog()
	catalog.Register(&apiProbeTool{name: "alpha"})
	var calls []string
	host := &Host{
		SetActiveTools: func(names []string) error {
			calls = append(calls, "SetActiveTools")
			return catalog.SetActive(names)
		},
		AppendEntry: func(customType string, data any) error {
			calls = append(calls, "AppendEntry:"+customType)
			return nil
		},
		SetSessionName: func(name string) error {
			calls = append(calls, "SetSessionName:"+name)
			return nil
		},
		LabelEntry: func(entryID, label string) error {
			calls = append(calls, "LabelEntry:"+entryID+":"+label)
			return nil
		},
		Exec: func(ctx context.Context, command string, args []string, opts sdk.ExecOptions) (*sdk.ExecResult, error) {
			calls = append(calls, "Exec:"+command)
			return &sdk.ExecResult{Stdout: "out", Code: 7}, nil
		},
	}
	api := NewAPI(APIOptions{Catalog: catalog, Host: host})
	if err := api.SetActiveTools([]string{"alpha"}); err != nil {
		t.Fatalf("SetActiveTools: %v", err)
	}
	if err := api.AppendEntry("note", map[string]int{"x": 1}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	if err := api.SetSessionName("renamed"); err != nil {
		t.Fatalf("SetSessionName: %v", err)
	}
	if err := api.LabelEntry("entry", "tag"); err != nil {
		t.Fatalf("LabelEntry: %v", err)
	}
	res, err := api.Exec("/bin/echo", []string{"hi"}, sdk.ExecOptions{})
	if err != nil || res == nil || res.Stdout != "out" || res.Code != 7 {
		t.Fatalf("Exec = %v, %v", res, err)
	}
	want := []string{"SetActiveTools", "AppendEntry:note", "SetSessionName:renamed", "LabelEntry:entry:tag", "Exec:/bin/echo"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	}
}

func TestAPIHostBackedErrorsPropagate(t *testing.T) {
	boom := errors.New("boom")
	api := NewAPI(APIOptions{Host: &Host{
		SetActiveTools: func([]string) error { return boom },
		AppendEntry:    func(string, any) error { return boom },
		SetSessionName: func(string) error { return boom },
		LabelEntry:     func(string, string) error { return boom },
		Exec:           func(context.Context, string, []string, sdk.ExecOptions) (*sdk.ExecResult, error) { return nil, boom },
	}})
	if err := api.SetActiveTools(nil); !errors.Is(err, boom) {
		t.Fatalf("SetActiveTools error = %v", err)
	}
	if err := api.AppendEntry("x", nil); !errors.Is(err, boom) {
		t.Fatalf("AppendEntry error = %v", err)
	}
	if err := api.SetSessionName("x"); !errors.Is(err, boom) {
		t.Fatalf("SetSessionName error = %v", err)
	}
	if err := api.LabelEntry("x", "y"); !errors.Is(err, boom) {
		t.Fatalf("LabelEntry error = %v", err)
	}
	if _, err := api.Exec("x", nil, sdk.ExecOptions{}); !errors.Is(err, boom) {
		t.Fatalf("Exec error = %v", err)
	}
}

func TestAPIHostPartialBindingKeepsUnavailable(t *testing.T) {
	api := NewAPI(APIOptions{Host: &Host{
		AppendEntry: func(string, any) error { return nil },
	}})
	if err := api.AppendEntry("note", nil); err != nil {
		t.Fatalf("bound AppendEntry failed: %v", err)
	}
	if err := api.SetActiveTools([]string{"a"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SetActiveTools = %v, want ErrUnavailable", err)
	}
	if err := api.SetSessionName("n"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SetSessionName = %v, want ErrUnavailable", err)
	}
	if err := api.LabelEntry("e", "l"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("LabelEntry = %v, want ErrUnavailable", err)
	}
	if _, err := api.Exec("ls", nil, sdk.ExecOptions{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Exec = %v, want ErrUnavailable", err)
	}
}

type boundSignalContext struct {
	sdk.HandlerContext
	signal context.Context
}

func (c *boundSignalContext) WithSignal(signal context.Context) sdk.HandlerContext {
	clone := *c
	clone.signal = signal
	return &clone
}

func (c *boundSignalContext) Signal() context.Context { return c.signal }

func TestRuntimeBindsDispatchSignalToBoundContext(t *testing.T) {
	reg := NewRegistry()
	var signals []context.Context
	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			signals = append(signals, ctx.Signal())
			return nil, nil
		}).
		build())

	base := &boundSignalContext{HandlerContext: &fakeContext{mode: sdk.ModeInteractive}}
	rt := NewRuntime(reg).SetContext(func() sdk.HandlerContext { return base })
	d := rt.Dispatcher()
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	if _, err := d.Context(first, agent.ContextRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Context(second, agent.ContextRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(signals) != 2 || signals[0] != first || signals[1] != second {
		t.Fatalf("signals = %v, want one per dispatch", signals)
	}
	if base.signal != nil {
		t.Fatal("the shared base context must not be mutated by signal binding")
	}
	if base.Mode() != sdk.ModeInteractive {
		t.Fatal("signal binding must preserve the bound host context")
	}
}

func TestRuntimeKeepsDecoratorForBoundContext(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("a").
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			return nil, nil
		}).
		build())
	base := &boundSignalContext{HandlerContext: &fakeContext{mode: sdk.ModeInteractive}}
	rt := NewRuntime(reg).SetContext(func() sdk.HandlerContext { return base })
	var decorated bool
	rt.SetContextDecorator(func(signal context.Context, provided sdk.HandlerContext) sdk.HandlerContext {
		decorated = true
		if _, ok := provided.(*boundSignalContext); !ok {
			t.Errorf("decorator base = %T, want the signal-bound context", provided)
		}
		return provided
	})
	if _, err := rt.Dispatcher().Context(context.Background(), agent.ContextRequest{}); err != nil {
		t.Fatal(err)
	}
	if !decorated {
		t.Fatal("the context decorator was not applied to the bound context")
	}
}

func TestHostContextUnboundStillUnavailable(t *testing.T) {
	api := NewAPI(APIOptions{})
	if err := api.AppendEntry("note", json.RawMessage(`{}`)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("AppendEntry = %v, want ErrUnavailable for the bare API", err)
	}
}
