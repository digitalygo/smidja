package extensions

import (
	"errors"
	"flag"
	"io"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensionui"
	"github.com/digitalygo/smidja/sdk"
)

func TestFlagRegistryRegisterValidateAndCapture(t *testing.T) {
	flags := NewFlagRegistry("model")
	if err := flags.Register("ext-text", sdk.FlagOptions{Type: "string", Default: "fallback", Description: "text"}); err != nil {
		t.Fatalf("Register string: %v", err)
	}
	if err := flags.Register("ext-bool", sdk.FlagOptions{Type: "boolean", Default: true}); err != nil {
		t.Fatalf("Register boolean: %v", err)
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags.Apply(fs)
	if err := fs.Parse([]string{"-ext-text=changed", "-ext-bool=false"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	flags.Capture(fs)
	values := flags.Values()
	if values["ext-text"] != "changed" || values["ext-bool"] != false {
		t.Fatalf("Values = %#v", values)
	}
	values["ext-text"] = "mutated"
	if again := flags.Values(); again["ext-text"] != "changed" {
		t.Fatalf("Values must be cloned, got %#v", again)
	}

	defaults := NewFlagRegistry()
	defaults.Register("blank", sdk.FlagOptions{Type: "string"})
	defaults.Register("off", sdk.FlagOptions{Type: "boolean"})
	fsDefaults := flag.NewFlagSet("defaults", flag.ContinueOnError)
	fsDefaults.SetOutput(io.Discard)
	defaults.Apply(fsDefaults)
	if err := fsDefaults.Parse(nil); err != nil {
		t.Fatal(err)
	}
	defaults.Capture(fsDefaults)
	got := defaults.Values()
	if got["blank"] != "" || got["off"] != false {
		t.Fatalf("defaults = %#v", got)
	}
}

func TestFlagRegistryRejectsInvalidDeclarations(t *testing.T) {
	flags := NewFlagRegistry("model")
	cases := []struct {
		name string
		flag string
		opts sdk.FlagOptions
		want error
	}{
		{"empty name", "", sdk.FlagOptions{Type: "string"}, ErrFlagName},
		{"bad name", "has space", sdk.FlagOptions{Type: "string"}, ErrFlagName},
		{"reserved", "model", sdk.FlagOptions{Type: "string"}, ErrFlagReserved},
		{"unknown type", "x", sdk.FlagOptions{Type: "number"}, ErrFlagType},
		{"bool default mismatch", "x", sdk.FlagOptions{Type: "boolean", Default: "yes"}, ErrFlagDefault},
		{"string default mismatch", "x", sdk.FlagOptions{Type: "string", Default: true}, ErrFlagDefault},
	}
	for _, tc := range cases {
		if err := flags.Register(tc.flag, tc.opts); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", tc.name, err, tc.want)
		}
	}
	if err := flags.Register("dup", sdk.FlagOptions{Type: "string"}); err != nil {
		t.Fatal(err)
	}
	if err := flags.Register("dup", sdk.FlagOptions{Type: "string"}); !errors.Is(err, ErrFlagDuplicate) {
		t.Fatalf("duplicate error = %v, want ErrFlagDuplicate", err)
	}
	var nilFlags *FlagRegistry
	if err := nilFlags.Register("x", sdk.FlagOptions{Type: "string"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil registry error = %v", err)
	}
	if values := nilFlags.Values(); len(values) != 0 {
		t.Fatalf("nil values = %#v", values)
	}
}

func TestFlagRegistryCaptureSkipsUnappliedFlags(t *testing.T) {
	flags := NewFlagRegistry()
	if err := flags.Register("ext", sdk.FlagOptions{Type: "string"}); err != nil {
		t.Fatal(err)
	}
	fs := flag.NewFlagSet("other", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags.Capture(fs)
	if values := flags.Values(); len(values) != 0 {
		t.Fatalf("values = %#v, want empty before the flag set is applied", values)
	}
}

func TestCustomEventBusOrderAndSelfUnsubscribe(t *testing.T) {
	bus := NewCustomEventBus()
	var calls []string
	var secondUnsub func()
	first, err := bus.Subscribe("tick", func(event sdk.CustomEvent) error {
		if event.Name != "tick" || event.Data != 7 {
			t.Errorf("event = %+v", event)
		}
		calls = append(calls, "first")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	secondUnsub, err = bus.Subscribe("tick", func(event sdk.CustomEvent) error {
		calls = append(calls, "second")
		secondUnsub()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit("tick", 7); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if strings.Join(calls, ",") != "first,second" {
		t.Fatalf("calls = %v", calls)
	}
	if err := bus.Emit("tick", 7); err != nil {
		t.Fatalf("Emit after unsubscribe: %v", err)
	}
	if strings.Join(calls, ",") != "first,second,first" {
		t.Fatalf("calls after unsubscribe = %v", calls)
	}
	secondUnsub()
	first()
	first()
	if err := bus.Emit("tick", 7); err != nil {
		t.Fatalf("Emit after all unsubscribes: %v", err)
	}
	if strings.Join(calls, ",") != "first,second,first" {
		t.Fatalf("unsubscribed handlers ran: %v", calls)
	}
}

func TestCustomEventBusNestedEmitIsOwnedAndOrdered(t *testing.T) {
	bus := NewCustomEventBus()
	var calls []string
	bus.Subscribe("outer", func(event sdk.CustomEvent) error {
		calls = append(calls, "outer:1")
		if err := bus.Emit("inner", "payload"); err != nil {
			t.Errorf("nested emit: %v", err)
		}
		calls = append(calls, "outer:2")
		return nil
	})
	bus.Subscribe("outer", func(event sdk.CustomEvent) error {
		calls = append(calls, "outer:other")
		return nil
	})
	bus.Subscribe("inner", func(event sdk.CustomEvent) error {
		calls = append(calls, "inner:"+event.Data.(string))
		return nil
	})
	if err := bus.Emit("outer", nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	want := "outer:1,outer:2,outer:other,inner:payload"
	if strings.Join(calls, ",") != want {
		t.Fatalf("calls = %v, want %s", calls, want)
	}
}

func TestCustomEventBusIsolatesErrorsAndPanics(t *testing.T) {
	bus := NewCustomEventBus()
	boom := errors.New("boom")
	var later bool
	bus.Subscribe("tick", func(sdk.CustomEvent) error { return boom })
	bus.Subscribe("tick", func(sdk.CustomEvent) error { panic("handler panic") })
	bus.Subscribe("tick", func(sdk.CustomEvent) error { later = true; return nil })
	err := bus.Emit("tick", nil)
	if err == nil || !errors.Is(err, boom) || !strings.Contains(err.Error(), "handler panic") {
		t.Fatalf("Emit error = %v", err)
	}
	if !later {
		t.Fatal("a failing handler stopped later handlers")
	}
	if err := bus.Emit("tick", nil); err == nil {
		t.Fatal("Emit must stay usable after handler failures")
	}
}

func TestFailedSetupRollsBackToolsCommandsUIAndActiveView(t *testing.T) {
	catalog := NewToolCatalog()
	core := &apiProbeTool{name: "core-tool"}
	if err := catalog.Register(core); err != nil {
		t.Fatal(err)
	}
	commands := NewCommandCatalog()
	if err := apiRegisterCommand(commands, "keep-cmd", "keep command"); err != nil {
		t.Fatal(err)
	}
	uiRegistry := extensionui.NewRegistry()
	flags := NewFlagRegistry()
	providers := NewProviderRegistry()
	bus := NewCustomEventBus()
	api := NewAPI(APIOptions{
		Catalog:   catalog,
		Commands:  commands,
		UI:        uiRegistry,
		Flags:     flags,
		Providers: providers,
		Events:    bus,
		Host:      &Host{SetActiveTools: func(names []string) error { return catalog.SetActive(names) }},
	})
	keeperTool := &sdkProbeTool{name: "keeper-tool"}
	keeperEntry := func(sdk.RenderContext, sdk.Entry) sdk.Component { return nil }
	keeperWidget := func() sdk.Component { return nil }
	registry := NewRegistry()
	keeperEvent := false
	registry.Register(ext("keeper").setup(func(api sdk.API) error {
		if err := api.RegisterTool(keeperTool); err != nil {
			return err
		}
		if err := api.RegisterCommand("keeper-cmd", sdk.Command{Description: "keeper command"}); err != nil {
			return err
		}
		if err := api.RegisterFlag("keeper-flag", sdk.FlagOptions{Type: "boolean"}); err != nil {
			return err
		}
		registration, ok := api.(sdk.UIRegistrationAPI)
		if !ok {
			return errors.New("keeper: UI registration unavailable")
		}
		if err := registration.RegisterEntryRenderer("keeper.entry", keeperEntry); err != nil {
			return err
		}
		if err := registration.RegisterWidget("keeper.widget", keeperWidget); err != nil {
			return err
		}
		subscription, ok := api.(sdk.CustomEventSubscription)
		if !ok {
			return errors.New("keeper: event subscription unavailable")
		}
		_, err := subscription.SubscribeCustomEvent("keeper.event", func(sdk.CustomEvent) error {
			keeperEvent = true
			return nil
		})
		return err
	}).build())
	boom := errors.New("ghost setup failed")
	ghostEvent := false
	registry.Register(ext("ghost").setup(func(api sdk.API) error {
		if err := api.RegisterTool(&sdkProbeTool{name: "core-tool"}); err != nil {
			return err
		}
		if err := api.RegisterTool(&sdkProbeTool{name: "ghost-tool"}); err != nil {
			return err
		}
		if err := api.RegisterCommand("ghost-cmd", sdk.Command{Description: "ghost command"}); err != nil {
			return err
		}
		if err := api.RegisterFlag("ghost-flag", sdk.FlagOptions{Type: "string", Default: "ghost"}); err != nil {
			return err
		}
		if err := api.RegisterProvider("ghost-provider", sdk.ProviderConfig{BaseURL: "https://example.com", APIKey: "sk-ghost", Models: []sdk.Model{{ID: "ghost/model"}}}); err != nil {
			return err
		}
		if err := api.SetActiveTools([]string{"ghost-tool", "keeper-tool"}); err != nil {
			return err
		}
		registration, ok := api.(sdk.UIRegistrationAPI)
		if !ok {
			return errors.New("ghost: UI registration unavailable")
		}
		if err := registration.RegisterEntryRenderer("ghost.entry", func(sdk.RenderContext, sdk.Entry) sdk.Component { return nil }); err != nil {
			return err
		}
		if err := registration.RegisterWidget("ghost.widget", func() sdk.Component { return nil }); err != nil {
			return err
		}
		subscription, ok := api.(sdk.CustomEventSubscription)
		if !ok {
			return errors.New("ghost: event subscription unavailable")
		}
		if _, err := subscription.SubscribeCustomEvent("ghost.event", func(sdk.CustomEvent) error {
			ghostEvent = true
			return nil
		}); err != nil {
			return err
		}
		return boom
	}).build())
	logger := &recLogger{}
	if err := registry.Setup(api, logger); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if got, ok := catalog.Get("core-tool"); !ok || got != core {
		t.Fatalf("core tool was not restored: %v ok=%v", got, ok)
	}
	if _, ok := catalog.Get("ghost-tool"); ok {
		t.Fatal("ghost tool survived the rollback")
	}
	if got, ok := catalog.Get("keeper-tool"); !ok || !isWrappedTool(got, keeperTool) {
		t.Fatalf("keeper tool changed: %v ok=%v", got, ok)
	}
	if names := catalog.Names(); strings.Join(names, ",") != "core-tool,keeper-tool" {
		t.Fatalf("active tools = %v, want the original active view", names)
	}
	if _, ok := commands.Get("ghost-cmd"); ok {
		t.Fatal("ghost command survived the rollback")
	}
	if _, ok := commands.Get("keep-cmd"); !ok {
		t.Fatal("pre-existing command was lost")
	}
	if _, ok := commands.Get("keeper-cmd"); !ok {
		t.Fatal("keeper command was lost")
	}
	if keys := uiRegistry.WidgetKeys(); len(keys) != 1 || keys[0] != "keeper.widget" {
		t.Fatalf("widget keys = %v", keys)
	}
	if keys := uiRegistry.EntryRendererTypes(); len(keys) != 1 || keys[0] != "keeper.entry" {
		t.Fatalf("entry renderer keys = %v", keys)
	}
	declarations := flags.Declarations()
	if len(declarations) != 1 || declarations[0].Name != "keeper-flag" {
		t.Fatalf("flags after rollback = %+v", declarations)
	}
	if names := providers.Names(); len(names) != 0 {
		t.Fatalf("ghost provider survived: %v", names)
	}
	if models := providers.Models(); len(models) != 0 {
		t.Fatalf("ghost provider models survived: %+v", models)
	}
	if _, ok := providers.FindModel("ghost/model"); ok {
		t.Fatal("ghost provider model still resolves")
	}
	if err := bus.Emit("keeper.event", nil); err != nil || !keeperEvent {
		t.Fatalf("keeper subscription lost: %v hit=%v", err, keeperEvent)
	}
	if err := bus.Emit("ghost.event", nil); err != nil {
		t.Fatalf("ghost emit: %v", err)
	}
	if ghostEvent {
		t.Fatal("ghost subscription survived the rollback")
	}
}

func apiRegisterCommand(commands *CommandCatalog, name, description string) error {
	_, err := commands.Register(name, sdk.Command{Description: description})
	return err
}

func isWrappedTool(tool agent.Tool, want sdk.Tool) bool {
	adapter, ok := tool.(*toolAdapter)
	return ok && adapter.Tool == want
}

func TestCustomEventBusCloseIsAdmissionBarrierAndWaitJoins(t *testing.T) {
	bus := NewCustomEventBus()
	if !bus.Wait(0) {
		t.Fatal("Wait must report a finished drain before the first emit")
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	queued := make(chan struct{})
	var late atomic.Bool
	if _, err := bus.Subscribe("job", func(sdk.CustomEvent) error {
		close(entered)
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Subscribe("job", func(sdk.CustomEvent) error {
		late.Store(true)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Subscribe("queued", func(sdk.CustomEvent) error {
		close(queued)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	emitDone := make(chan error, 1)
	go func() { emitDone <- bus.Emit("job", nil) }()
	<-entered

	admitted := make(chan error, 1)
	go func() { admitted <- bus.Emit("queued", nil) }()
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("admitted event error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the queued event was not admitted while the drain was active")
	}

	closed := make(chan struct{})
	go func() {
		bus.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for the active handler")
	}
	if !bus.Closed() {
		t.Fatal("Close did not mark the bus closed")
	}
	if bus.Wait(50 * time.Millisecond) {
		t.Fatal("Wait reported the drain finished while a cooperative handler was still blocked")
	}
	if late.Load() {
		t.Fatal("a handler started after Close")
	}
	select {
	case <-queued:
		t.Fatal("Close delivered a queued event")
	default:
	}

	joined := make(chan bool, 1)
	go func() { joined <- bus.Wait(5 * time.Second) }()
	time.Sleep(20 * time.Millisecond)
	close(release)
	select {
	case err := <-emitDone:
		if err != nil {
			t.Fatalf("emit error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the drain did not finish")
	}
	select {
	case ok := <-joined:
		if !ok {
			t.Fatal("Wait did not report the joined drain")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not join the cooperative drain")
	}
	if !bus.Wait(2 * time.Second) {
		t.Fatal("Wait did not join the finished drain")
	}
	if !bus.Wait(0) {
		t.Fatal("Wait probe did not report the finished drain")
	}
	if late.Load() {
		t.Fatal("a handler started after Close")
	}
	select {
	case <-queued:
		t.Fatal("a queued event ran after Close")
	default:
	}
}

func TestCustomEventBusFloodDeliversEveryAcceptedEvent(t *testing.T) {
	bus := NewCustomEventBus()
	entered := make(chan struct{})
	release := make(chan struct{})
	var delivered atomic.Int64
	var once sync.Once
	if _, err := bus.Subscribe("flood", func(sdk.CustomEvent) error {
		delivered.Add(1)
		once.Do(func() {
			close(entered)
			<-release
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	drainDone := make(chan error, 1)
	go func() { drainDone <- bus.Emit("flood", nil) }()
	<-entered
	const total = 2048
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := bus.Emit("flood", nil)
			if err == nil {
				accepted.Add(1)
				return
			}
			if !errors.Is(err, ErrCustomEventOverflow) {
				t.Errorf("emit error = %v", err)
			}
		}()
	}
	wg.Wait()
	close(release)
	select {
	case err := <-drainDone:
		if err != nil {
			t.Fatalf("drain error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not finish")
	}
	if accepted.Load() == 0 {
		t.Fatal("the flood admitted no events")
	}
	if accepted.Load() == total {
		t.Fatalf("the %d-event flood admitted everything without rejection", total)
	}
	if delivered.Load() != accepted.Load()+1 {
		t.Fatalf("delivered = %d, accepted plus the first event = %d", delivered.Load(), accepted.Load()+1)
	}
	bus.Close()
	if delivered.Load() != accepted.Load()+1 {
		t.Fatal("Close dropped accepted events")
	}
}

func TestCustomEventBusReentrantCloseUnsubscribeAndEmit(t *testing.T) {
	bus := NewCustomEventBus()
	var mu sync.Mutex
	var calls []string
	var unsubscribe func()
	if _, err := bus.Subscribe("outer", func(sdk.CustomEvent) error {
		mu.Lock()
		calls = append(calls, "outer")
		mu.Unlock()
		if err := bus.Emit("inner", nil); err != nil {
			return err
		}
		unsubscribe()
		bus.Close()
		if !bus.Closed() {
			return errors.New("Close from a handler did not mark the bus closed")
		}
		if bus.Wait(0) {
			return errors.New("Wait self-joined the handler drain")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var err error
	unsubscribe, err = bus.Subscribe("outer", func(sdk.CustomEvent) error {
		mu.Lock()
		calls = append(calls, "second")
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Subscribe("inner", func(sdk.CustomEvent) error {
		mu.Lock()
		calls = append(calls, "inner")
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- bus.Emit("outer", nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("emit error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reentrant close, unsubscribe and emit deadlocked")
	}
	if !bus.Closed() {
		t.Fatal("bus must be closed after the handler closed it")
	}
	if !bus.Wait(2 * time.Second) {
		t.Fatal("the drain did not finish after a self-close")
	}
	mu.Lock()
	joined := strings.Join(calls, ",")
	mu.Unlock()
	if joined != "outer" {
		t.Fatalf("calls = %v, want only the first handler", joined)
	}
	if err := bus.Emit("x", nil); !errors.Is(err, ErrCustomEventClosed) {
		t.Fatalf("closed emit error = %v", err)
	}
	if _, err := bus.Subscribe("x", func(sdk.CustomEvent) error { return nil }); !errors.Is(err, ErrCustomEventClosed) {
		t.Fatalf("closed subscribe error = %v", err)
	}
}

func TestFailedSetupCloseDoesNotResurrectSubscriptions(t *testing.T) {
	bus := NewCustomEventBus()
	keeperCalled := false
	if _, err := bus.Subscribe("keeper.event", func(sdk.CustomEvent) error {
		keeperCalled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ghostCalled := false
	boom := errors.New("ghost setup failed")
	registry := NewRegistry()
	registry.Register(ext("ghost-close").setup(func(api sdk.API) error {
		subscription, ok := api.(sdk.CustomEventSubscription)
		if !ok {
			return errors.New("ghost: event subscription unavailable")
		}
		if _, err := subscription.SubscribeCustomEvent("ghost.event", func(sdk.CustomEvent) error {
			ghostCalled = true
			return nil
		}); err != nil {
			return err
		}
		bus.Close()
		return boom
	}).build())
	if err := registry.Setup(NewAPI(APIOptions{Events: bus}), &recLogger{}); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !bus.Closed() {
		t.Fatal("rollback reopened a closed bus")
	}
	if err := bus.Emit("ghost.event", nil); !errors.Is(err, ErrCustomEventClosed) {
		t.Fatalf("ghost emit error = %v, want closed", err)
	}
	if _, err := bus.Subscribe("late", func(sdk.CustomEvent) error { return nil }); !errors.Is(err, ErrCustomEventClosed) {
		t.Fatalf("late subscribe error = %v, want closed", err)
	}
	if err := bus.Emit("keeper.event", nil); !errors.Is(err, ErrCustomEventClosed) {
		t.Fatalf("keeper emit error = %v, want closed", err)
	}
	if keeperCalled || ghostCalled {
		t.Fatalf("closed bus delivered events: keeper=%v ghost=%v", keeperCalled, ghostCalled)
	}
}

func TestProviderRegistryRedactsSecretsInErrors(t *testing.T) {
	providers := NewProviderRegistry()
	secret := "sk-provider-secret"
	cases := []struct {
		name string
		url  string
	}{
		{"userinfo with bad scheme", "ftp://user:" + secret + "@example.com"},
		{"userinfo with no host", "https://user:" + secret + "@"},
		{"userinfo on a valid scheme", "https://user:" + secret + "@example.com"},
		{"malformed userinfo URL", "https://user:" + secret + "@exa mple.com"},
		{"query credential with bad scheme", "ftp://example.com?api_key=" + secret},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := providers.Register("proxy", sdk.ProviderConfig{BaseURL: tc.url, APIKey: secret})
			if err == nil {
				t.Fatal("invalid URL must fail")
			}
			for _, secretPart := range []string{secret, "user:", "pass@"} {
				if strings.Contains(err.Error(), secretPart) {
					t.Fatalf("error leaked %q: %v", secretPart, err)
				}
			}
			if strings.Contains(err.Error(), tc.url) {
				t.Fatalf("error echoed the raw URL: %v", err)
			}
		})
	}
	valid := "https://example.com/v1?api_key=" + secret
	if err := providers.Register("query", sdk.ProviderConfig{BaseURL: valid, APIKey: secret}); err != nil {
		t.Fatalf("valid URL with query credentials must register: %v", err)
	}
	entry, ok := providers.Lookup("query")
	if !ok || entry.BaseURL != valid {
		t.Fatalf("entry = %+v ok=%v", entry, ok)
	}
}

func TestCustomEventBusValidatesAndCloses(t *testing.T) {
	bus := NewCustomEventBus()
	if err := bus.Emit("", nil); !errors.Is(err, ErrCustomEventName) {
		t.Fatalf("empty name error = %v", err)
	}
	if _, err := bus.Subscribe("", func(sdk.CustomEvent) error { return nil }); !errors.Is(err, ErrCustomEventName) {
		t.Fatalf("empty subscription error = %v", err)
	}
	if _, err := bus.Subscribe("tick", nil); !errors.Is(err, ErrCustomEventHandler) {
		t.Fatalf("nil handler error = %v", err)
	}
	if err := bus.Emit("missing", nil); err != nil {
		t.Fatalf("zero-subscriber emit = %v, want a ready bus", err)
	}
	unsubscribe, err := bus.Subscribe("tick", func(sdk.CustomEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	bus.Close()
	bus.Close()
	if !bus.Closed() {
		t.Fatal("bus must report closed")
	}
	if err := bus.Emit("tick", nil); !errors.Is(err, ErrCustomEventClosed) {
		t.Fatalf("closed emit error = %v", err)
	}
	if _, err := bus.Subscribe("tick", func(sdk.CustomEvent) error { return nil }); !errors.Is(err, ErrCustomEventClosed) {
		t.Fatalf("closed subscribe error = %v", err)
	}
	unsubscribe()
	var nilBus *CustomEventBus
	if err := nilBus.Emit("tick", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil bus error = %v", err)
	}
	if !nilBus.Closed() {
		t.Fatal("nil bus must report closed")
	}
	nilBus.Close()
	if !nilBus.Wait(0) {
		t.Fatal("nil bus must report a finished drain")
	}
	if _, err := nilBus.Subscribe("tick", func(sdk.CustomEvent) error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil subscribe error = %v", err)
	}
}

func TestCustomEventBusBoundsRunawayNesting(t *testing.T) {
	bus := NewCustomEventBus()
	var unsubscribe func()
	unsubscribe, err := bus.Subscribe("loop", func(sdk.CustomEvent) error {
		return bus.Emit("loop", nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if err := bus.Emit("loop", nil); !errors.Is(err, ErrCustomEventOverflow) {
		t.Fatalf("overflow error = %v, want ErrCustomEventOverflow", err)
	}
	if bus.Closed() {
		t.Fatal("overflow must not close the bus")
	}
	if err := bus.Emit("loop", nil); !errors.Is(err, ErrCustomEventOverflow) {
		t.Fatalf("second overflow error = %v", err)
	}
}

func TestCustomEventBusSubscriptionAliasAndRedactionEdges(t *testing.T) {
	bus := NewCustomEventBus()
	seen := false
	if _, err := bus.SubscribeCustomEvent("alias", func(sdk.CustomEvent) error {
		seen = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Emit("alias", nil); err != nil || !seen {
		t.Fatalf("alias emit = %v seen=%v", err, seen)
	}
	if got := redactedProviderURL(nil); got == "" {
		t.Fatal("nil URL redaction must stay non-empty")
	}
	parsed, err := url.Parse("https://example.com/path?token=sk-secret#fragment")
	if err != nil {
		t.Fatal(err)
	}
	redacted := redactedProviderURL(parsed)
	if strings.Contains(redacted, "sk-secret") || strings.Contains(redacted, "fragment") {
		t.Fatalf("redacted URL = %q", redacted)
	}
	if !strings.Contains(redacted, "example.com/path") {
		t.Fatalf("redacted URL lost the routing host or path: %q", redacted)
	}
}

func TestProviderRegistryValidationAndLifecycle(t *testing.T) {
	providers := NewProviderRegistry("openrouter")
	cases := []struct {
		label string
		name  string
		cfg   sdk.ProviderConfig
		want  error
	}{
		{"empty name", "", sdk.ProviderConfig{BaseURL: "https://example.com"}, ErrProviderName},
		{"reserved", "openrouter", sdk.ProviderConfig{BaseURL: "https://example.com"}, ErrProviderReserved},
		{"missing scheme", "p", sdk.ProviderConfig{BaseURL: "example.com"}, ErrProviderURL},
		{"userinfo", "p", sdk.ProviderConfig{BaseURL: "https://user:pass@example.com"}, ErrProviderURL},
		{"bad dialect", "p", sdk.ProviderConfig{BaseURL: "https://example.com", API: "anthropic-messages"}, ErrProviderDialect},
		{"empty model", "p", sdk.ProviderConfig{BaseURL: "https://example.com", Models: []sdk.Model{{ID: " "}}}, ErrProviderModel},
	}
	for _, tc := range cases {
		if err := providers.Register(tc.name, tc.cfg); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", tc.label, err, tc.want)
		}
	}
	cfg := sdk.ProviderConfig{
		BaseURL: "https://example.com/v1/",
		API:     "openai-completions",
		APIKey:  "sk-secret",
		Models:  []sdk.Model{{ID: "vendor/alpha", Provider: "ignored"}, {ID: "vendor/alpha"}, {ID: "vendor/beta"}},
	}
	if err := providers.Register("proxy", cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	entry, ok := providers.Lookup("proxy")
	if !ok {
		t.Fatal("proxy missing")
	}
	if entry.BaseURL != "https://example.com/v1" || entry.API != "openai-completions" {
		t.Fatalf("entry = %+v", entry)
	}
	if len(entry.Models) != 2 || entry.Models[0].ID != "vendor/alpha" || entry.Models[0].Provider != "proxy" {
		t.Fatalf("models = %+v", entry.Models)
	}
	if key, ok := providers.Credential("proxy"); !ok || key != "sk-secret" {
		t.Fatalf("credential = %q ok=%v", key, ok)
	}
	if found, ok := providers.FindModel("vendor/beta"); !ok || found.Name != "proxy" {
		t.Fatalf("FindModel = %+v ok=%v", found, ok)
	}
	if _, ok := providers.FindModel("missing"); ok {
		t.Fatal("unknown model must not resolve")
	}
	if names := providers.Names(); len(names) != 1 || names[0] != "proxy" {
		t.Fatalf("Names = %v", names)
	}
	replacement := cfg
	replacement.BaseURL = "https://example.net"
	if err := providers.Register("proxy", replacement); err != nil {
		t.Fatalf("replace: %v", err)
	}
	entry, _ = providers.Lookup("proxy")
	if entry.BaseURL != "https://example.net" {
		t.Fatalf("replacement entry = %+v", entry)
	}
	if err := providers.Remove("missing"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("remove missing error = %v", err)
	}
	if err := providers.Remove("proxy"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if names := providers.Names(); len(names) != 0 {
		t.Fatalf("Names after removal = %v", names)
	}
	var nilProviders *ProviderRegistry
	if err := nilProviders.Register("p", cfg); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil register error = %v", err)
	}
	if err := nilProviders.Remove("p"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil remove error = %v", err)
	}
}

func TestFailedSetupRollsBackFlagsProvidersAndSubscriptions(t *testing.T) {
	flags := NewFlagRegistry()
	providers := NewProviderRegistry()
	bus := NewCustomEventBus()
	api := NewAPI(APIOptions{Flags: flags, Providers: providers, Events: bus})
	registry := NewRegistry()
	boom := errors.New("setup failed")
	registry.Register(ext("ghost").setup(func(api sdk.API) error {
		if err := api.RegisterFlag("ghost-flag", sdk.FlagOptions{Type: "string"}); err != nil {
			return err
		}
		if err := api.RegisterProvider("ghost-provider", sdk.ProviderConfig{BaseURL: "https://example.com", APIKey: "sk-ghost-secret"}); err != nil {
			return err
		}
		if sub, ok := api.(sdk.CustomEventSubscription); ok {
			if _, err := sub.SubscribeCustomEvent("ghost-event", func(sdk.CustomEvent) error { return nil }); err != nil {
				return err
			}
		}
		return boom
	}).build())
	registry.Register(ext("keeper").setup(func(api sdk.API) error {
		return api.RegisterFlag("keeper-flag", sdk.FlagOptions{Type: "boolean"})
	}).build())
	logger := &recLogger{}
	if err := registry.Setup(api, logger); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if len(flags.Values()) != 0 {
		t.Fatalf("ghost flags survived: %#v", flags.Values())
	}
	if names := providers.Names(); len(names) != 0 {
		t.Fatalf("ghost providers survived: %v", names)
	}
	if err := bus.Emit("ghost-event", nil); err != nil {
		t.Fatalf("ghost subscription survived: %v", err)
	}
	found := false
	for _, decl := range flags.Declarations() {
		if decl.Name == "keeper-flag" {
			found = true
		}
		if decl.Name == "ghost-flag" {
			t.Fatal("ghost flag declaration survived")
		}
	}
	if !found {
		t.Fatal("successful setup contributions were discarded")
	}
	if logger.count() != 1 || !strings.Contains(logger.all()[0], "setup failed") {
		t.Fatalf("logger lines = %v", logger.all())
	}
	if strings.Contains(strings.Join(logger.all(), "; "), "sk-ghost-secret") {
		t.Fatalf("logger leaked the provider key: %v", logger.all())
	}
	emitted := false
	bus.Subscribe("ghost-event", func(sdk.CustomEvent) error { emitted = true; return nil })
	if err := bus.Emit("ghost-event", nil); err != nil || !emitted {
		t.Fatalf("bus unusable after rollback: %v emitted=%v", err, emitted)
	}
}

func TestAPIEventSubscriptionAndFlagsVisibility(t *testing.T) {
	flags := NewFlagRegistry()
	bus := NewCustomEventBus()
	api := NewAPI(APIOptions{Flags: flags, Events: bus})
	subscription, ok := api.(sdk.CustomEventSubscription)
	if !ok {
		t.Fatal("composed API must expose custom event subscriptions")
	}
	seen := make([]string, 0, 2)
	if _, err := subscription.SubscribeCustomEvent("note", func(event sdk.CustomEvent) error {
		seen = append(seen, "a:"+event.Data.(string))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := subscription.SubscribeCustomEvent("note", func(event sdk.CustomEvent) error {
		seen = append(seen, "b:"+event.Data.(string))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := api.EmitCustomEvent("note", "payload"); err != nil {
		t.Fatalf("EmitCustomEvent: %v", err)
	}
	if strings.Join(seen, ",") != "a:payload,b:payload" {
		t.Fatalf("seen = %v", seen)
	}
	if err := api.RegisterFlag("ext", sdk.FlagOptions{Type: "string"}); err != nil {
		t.Fatal(err)
	}
	fs := flag.NewFlagSet("flags", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flags.Apply(fs)
	if err := fs.Parse([]string{"--ext=value"}); err != nil {
		t.Fatal(err)
	}
	flags.Capture(fs)
	if got := api.Flags(); got["ext"] != "value" {
		t.Fatalf("Flags = %#v", got)
	}
}

func TestAPIProvidersReachableFromComposedOptions(t *testing.T) {
	providers := NewProviderRegistry()
	api := NewAPI(APIOptions{Providers: providers})
	if err := api.RegisterProvider("proxy", sdk.ProviderConfig{BaseURL: "https://example.com", APIKey: "sk"}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	if _, ok := providers.Lookup("proxy"); !ok {
		t.Fatal("provider missing")
	}
	if err := api.RemoveProvider("proxy"); err != nil {
		t.Fatalf("RemoveProvider: %v", err)
	}
	if err := api.RemoveProvider("proxy"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("second remove = %v", err)
	}
}

func TestAPIEventSubscriptionUnavailableWithoutBus(t *testing.T) {
	api := NewAPI(APIOptions{})
	subscription, ok := api.(sdk.CustomEventSubscription)
	if !ok {
		t.Fatal("API type must satisfy the optional interface")
	}
	if _, err := subscription.SubscribeCustomEvent("note", func(sdk.CustomEvent) error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("subscription error = %v, want ErrUnavailable", err)
	}
}
