package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/sdk"
)

const eventTestPayload = "r3-secret-payload"

type r3EventProbe struct {
	mu         sync.Mutex
	calls      []string
	api        sdk.API
	subscribeA error
	subscribeB error
	unsubB     func()
	nestedErr  error
}

func (p *r3EventProbe) ID() string { return "r3-events" }

func (p *r3EventProbe) Setup(api sdk.API) error {
	p.api = api
	subscription, ok := api.(sdk.CustomEventSubscription)
	if !ok {
		return errors.New("r3-events: custom event subscription is unavailable")
	}
	_, p.subscribeA = subscription.SubscribeCustomEvent("r3.outer", func(event sdk.CustomEvent) error {
		p.mu.Lock()
		p.calls = append(p.calls, "a1")
		p.mu.Unlock()
		if err := p.api.EmitCustomEvent("r3.inner", event.Data); err != nil {
			p.mu.Lock()
			p.nestedErr = err
			p.mu.Unlock()
		}
		p.mu.Lock()
		p.calls = append(p.calls, "a2")
		p.mu.Unlock()
		return nil
	})
	var err error
	p.unsubB, err = subscription.SubscribeCustomEvent("r3.outer", func(sdk.CustomEvent) error {
		p.mu.Lock()
		p.calls = append(p.calls, "b")
		p.mu.Unlock()
		unsub := p.unsubB
		if unsub != nil {
			unsub()
		}
		return nil
	})
	p.subscribeB = err
	if _, err := subscription.SubscribeCustomEvent("r3.outer", func(sdk.CustomEvent) error {
		p.mu.Lock()
		p.calls = append(p.calls, "c")
		p.mu.Unlock()
		panic("r3-event panic")
	}); err != nil {
		return err
	}
	if _, err := subscription.SubscribeCustomEvent("r3.outer", func(event sdk.CustomEvent) error {
		if event.Data != eventTestPayload {
			return errors.New("r3-events: payload was not forwarded")
		}
		p.mu.Lock()
		p.calls = append(p.calls, "d")
		p.mu.Unlock()
		return nil
	}); err != nil {
		return err
	}
	if _, err := subscription.SubscribeCustomEvent("r3.inner", func(sdk.CustomEvent) error {
		p.mu.Lock()
		p.calls = append(p.calls, "i")
		p.mu.Unlock()
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (p *r3EventProbe) RegisterLLMHooks(r sdk.LLMHookRegistry) {
	r.OnContext(func(ctx sdk.HandlerContext, ev sdk.ContextEvent) (*sdk.ContextEventResult, error) {
		if err := ctx.EmitCustomEvent("r3.outer", eventTestPayload); err != nil {
			return nil, err
		}
		return nil, nil
	})
}

func (p *r3EventProbe) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func TestComposedCustomEventBusDispatchAndLifecycle(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("one"), textStop("two")}}
	registry := r3ModelRegistry(t, models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"})
	probe := &r3EventProbe{}
	deps := r3ReplDeps(t, cwd, client, registry, "first\nsecond\n/quit\n", probe)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if probe.subscribeA != nil || probe.subscribeB != nil {
		t.Fatalf("subscription errors = %v, %v", probe.subscribeA, probe.subscribeB)
	}
	if probe.nestedErr != nil {
		t.Fatalf("nested emit error = %v", probe.nestedErr)
	}
	want := "a1,a2,b,c,d,i,a1,a2,c,d,i"
	if got := strings.Join(probe.snapshot(), ","); got != want {
		t.Fatalf("dispatch order = %s, want %s", got, want)
	}
	if raw := readOnlySession(t, deps.Store.Root()); strings.Contains(raw, eventTestPayload) {
		t.Fatal("custom event payload was persisted")
	}
	for _, req := range client.reqs {
		for _, message := range req.Messages {
			if message != nil && message.User != nil && strings.Contains(string(message.User.Content), eventTestPayload) {
				t.Fatal("custom event payload reached the model request")
			}
		}
	}
	if probe.api == nil {
		t.Fatal("setup did not capture the API")
	}
	if err := probe.api.EmitCustomEvent("r3.empty", nil); !errors.Is(err, extensions.ErrCustomEventClosed) {
		t.Fatalf("post-run emit error = %v, want closed bus", err)
	}
	subscription := probe.api.(sdk.CustomEventSubscription)
	if _, err := subscription.SubscribeCustomEvent("r3.late", func(sdk.CustomEvent) error { return nil }); !errors.Is(err, extensions.ErrCustomEventClosed) {
		t.Fatalf("post-run subscribe error = %v, want closed bus", err)
	}
}

func TestComposedCustomEventTeardownJoinsActiveDrain(t *testing.T) {
	cwd := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	var late atomic.Bool
	ext := &hostHookExtension{
		id: "teardown-join",
		setupFn: func(api sdk.API) error {
			subscription, ok := api.(sdk.CustomEventSubscription)
			if !ok {
				return errors.New("teardown-join: custom event subscription is unavailable")
			}
			if _, err := subscription.SubscribeCustomEvent("r3.block", func(sdk.CustomEvent) error {
				close(entered)
				<-release
				return nil
			}); err != nil {
				return err
			}
			_, err := subscription.SubscribeCustomEvent("r3.block", func(sdk.CustomEvent) error {
				late.Store(true)
				return nil
			})
			return err
		},
	}
	registry := r3ModelRegistry(t, models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"})
	deps := r3ReplDeps(t, cwd, &capturingClient{}, registry, "", ext)
	bootstrap, err := bootstrapExtensions(deps)
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.close()

	emitDone := make(chan error, 1)
	go func() { emitDone <- bootstrap.events.Emit("r3.block", nil) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the blocking handler did not start")
	}

	closed := make(chan struct{})
	go func() {
		bootstrap.close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("teardown returned while an accepted handler was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("teardown did not join the accepted drain")
	}
	select {
	case err := <-emitDone:
		if err != nil {
			t.Fatalf("emit error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the accepted drain did not finish")
	}
	if late.Load() {
		t.Fatal("a handler started after teardown closed the bus")
	}
	if !bootstrap.events.Closed() {
		t.Fatal("teardown left the custom event bus open")
	}
	if err := bootstrap.events.Emit("r3.late", nil); !errors.Is(err, extensions.ErrCustomEventClosed) {
		t.Fatalf("post-teardown emit = %v, want closed", err)
	}
}

func TestComposedCustomEventShutdownFromHandlerDoesNotSelfWait(t *testing.T) {
	bus := extensions.NewCustomEventBus()
	host := newHostRuntime(context.Background(), t.TempDir(), nil, extensions.NewToolCatalog())
	host.setEventBus(bus)
	if _, err := bus.Subscribe("r3.shutdown", func(sdk.CustomEvent) error {
		host.shutdown()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	emitDone := make(chan error, 1)
	go func() { emitDone <- bus.Emit("r3.shutdown", nil) }()
	select {
	case err := <-emitDone:
		if err != nil {
			t.Fatalf("emit error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a custom event handler calling Shutdown self-waited")
	}
	if !bus.Closed() {
		t.Fatal("handler shutdown did not close the event bus")
	}
	host.waitCompacts()
	host.waitCallbacks()
}

func TestComposedCustomEventReadyBusWithoutSubscribers(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("one")}}
	registry := r3ModelRegistry(t, models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"})
	var apiRef sdk.API
	var emitErr error
	ext := &hostHookExtension{
		id: "no-subscribers",
		setupFn: func(api sdk.API) error {
			apiRef = api
			return nil
		},
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call == 0 {
				emitErr = ctx.EmitCustomEvent("r3.silent", map[string]string{"key": "value"})
			}
			return nil, nil
		},
	}
	deps := r3ReplDeps(t, cwd, client, registry, "first\n/quit\n", ext)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if emitErr != nil {
		t.Fatalf("zero-subscriber emit = %v, want a ready bus", emitErr)
	}
	if apiRef == nil {
		t.Fatal("setup did not capture the API")
	}
	if err := apiRef.EmitCustomEvent("r3.silent", nil); !errors.Is(err, extensions.ErrCustomEventClosed) {
		t.Fatalf("post-run emit = %v, want closed bus", err)
	}
}
