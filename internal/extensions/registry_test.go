package extensions

import (
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/sdk"
)

func TestRegisterInOrder(t *testing.T) {
	reg := NewRegistry()

	first := ext("first").
		context(nil).
		context(nil).
		toolCall(nil).
		build()
	second := ext("second").
		messageEnd(nil).
		build()

	if err := reg.Register(first); err != nil {
		t.Fatalf("register first: %v", err)
	}
	if err := reg.Register(second); err != nil {
		t.Fatalf("register second: %v", err)
	}

	if len(reg.extensions) != 2 {
		t.Fatalf("extensions = %d, want 2", len(reg.extensions))
	}
	if reg.extensions[0].id != "first" || reg.extensions[1].id != "second" {
		t.Fatalf("extension order = [%s, %s], want [first, second]",
			reg.extensions[0].id, reg.extensions[1].id)
	}

	if len(reg.extensions[0].context) != 2 {
		t.Fatalf("first.context = %d handlers, want 2", len(reg.extensions[0].context))
	}
	if len(reg.extensions[1].messageEnd) != 1 {
		t.Fatalf("second.messageEnd = %d handlers, want 1", len(reg.extensions[1].messageEnd))
	}
}

func TestRegisterDuplicateIDRejected(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(ext("a").build()); err != nil {
		t.Fatalf("register a: %v", err)
	}
	err := reg.Register(ext("a").context(nil).build())
	if !errors.Is(err, ErrDuplicateExtension) {
		t.Fatalf("duplicate register error = %v, want ErrDuplicateExtension", err)
	}
	if len(reg.extensions) != 1 {
		t.Fatalf("extensions = %d, want 1 (duplicate must not be added)", len(reg.extensions))
	}
}

func TestRegisterNilAndEmptyID(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(nil); !errors.Is(err, ErrNilExtension) {
		t.Fatalf("nil register error = %v, want ErrNilExtension", err)
	}
	noID := &hookExtension{b: &builder{}}
	if err := reg.Register(noID); !errors.Is(err, ErrEmptyExtensionID) {
		t.Fatalf("empty id register error = %v, want ErrEmptyExtensionID", err)
	}
}

func TestSetupRunsOncePerExtensionInOrder(t *testing.T) {
	reg := NewRegistry()
	api := &stubAPI{}
	var order []string

	reg.Register(ext("a").setup(func(api sdk.API) error {
		order = append(order, "a")
		return api.SetSessionName("a")
	}).build())
	reg.Register(ext("plain").build())
	reg.Register(ext("b").setup(func(api sdk.API) error {
		order = append(order, "b")
		return nil
	}).build())

	log := &recLogger{}
	if err := reg.Setup(api, log); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("setup order = %v, want [a b]", order)
	}
	if len(api.calls) != 1 || api.calls[0] != "SetSessionName:a" {
		t.Fatalf("api calls = %v, want [SetSessionName:a]", api.calls)
	}
	if log.count() != 0 {
		t.Fatalf("setup logs = %d, want 0 for a clean setup", log.count())
	}

	if err := reg.Setup(api, log); !errors.Is(err, ErrSetupAlreadyRun) {
		t.Fatalf("second setup error = %v, want ErrSetupAlreadyRun", err)
	}
}

func TestSetupErrorIsolation(t *testing.T) {
	reg := NewRegistry()
	log := &recLogger{}
	var ran []string

	bad := ext("bad").
		setup(func(sdk.API) error { return errors.New("no api key") }).
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			ran = append(ran, "bad")
			return nil, nil
		}).
		build()
	good := ext("good").
		setup(func(sdk.API) error {
			ran = append(ran, "good-setup")
			return nil
		}).
		context(func(ctx sdk.HandlerContext, e sdk.ContextEvent) (*sdk.ContextEventResult, error) {
			ran = append(ran, "good")
			return nil, nil
		}).
		build()

	reg.Register(bad)
	reg.Register(good)
	if err := reg.Setup(&stubAPI{}, log); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if len(ran) != 1 || ran[0] != "good-setup" {
		t.Fatalf("setup ran = %v, want [good-setup]", ran)
	}
	lines := log.all()
	if len(lines) != 1 || !strings.Contains(lines[0], "bad") || !strings.Contains(lines[0], "setup failed") {
		t.Fatalf("setup logs = %v, want one line naming extension bad", lines)
	}

	rt := NewRuntime(reg).SetLogger(log)
	d := rt.Dispatcher()
	_, _ = d.Context(t.Context(), reqWith("hi"))
	if len(ran) != 2 || ran[1] != "good" {
		t.Fatalf("dispatch ran = %v, want [good-setup good] (bad must be disabled)", ran)
	}
}

func TestSetupPanicIsolation(t *testing.T) {
	reg := NewRegistry()
	log := &recLogger{}
	var ran []string

	reg.Register(ext("boom").
		setup(func(sdk.API) error { panic("kaboom") }).
		build())
	reg.Register(ext("ok").
		setup(func(sdk.API) error { ran = append(ran, "ok"); return nil }).
		build())

	if err := reg.Setup(&stubAPI{}, log); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if len(ran) != 1 || ran[0] != "ok" {
		t.Fatalf("setup ran = %v, want [ok]", ran)
	}
	lines := log.all()
	if len(lines) != 1 || !strings.Contains(lines[0], "boom") || !strings.Contains(lines[0], "panic: kaboom") {
		t.Fatalf("setup logs = %v, want one panic line naming extension boom", lines)
	}
}

func TestSetupNilLoggerIsSafe(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("bad").setup(func(sdk.API) error { return errors.New("boom") }).build())
	reg.Register(ext("ok").build())
	if err := reg.Setup(&stubAPI{}, nil); err != nil {
		t.Fatalf("setup with nil logger: %v", err)
	}
}

func TestRegisterAfterSetupSkipsSetup(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ext("a").build())
	if err := reg.Setup(&stubAPI{}, &recLogger{}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	setupRan := false
	if err := reg.Register(ext("late").setup(func(sdk.API) error { setupRan = true; return nil }).build()); err != nil {
		t.Fatalf("register late: %v", err)
	}
	if setupRan {
		t.Fatal("late extension received Setup after the setup phase")
	}
}
