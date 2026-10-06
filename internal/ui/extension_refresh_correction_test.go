package ui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui/interactive"
	"github.com/digitalygo/smidja/sdk"
)

func TestWidgetFactoryNestedComponentRegistration(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	var builds atomic.Int32
	boundedCall(t, "widget factory nested component registration", func() {
		err := registry.RegisterWidget("nested-component-widget", func() sdk.Component {
			builds.Add(1)
			if registerErr := registry.RegisterComponent("nested-component", func() sdk.Component {
				return &panicComponent{text: "NESTED COMPONENT"}
			}); registerErr != nil {
				t.Errorf("nested RegisterComponent: %v", registerErr)
			}
			return &panicComponent{text: "NESTED WIDGET"}
		})
		if err != nil {
			t.Errorf("RegisterWidget: %v", err)
		}
	})
	waitFor(t, "nested widget frame", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "NESTED WIDGET")
	})
	if got := builds.Load(); got != 1 {
		t.Fatalf("nested widget built %d times, want 1", got)
	}
	keys := registry.ComponentKeys()
	if len(keys) != 1 || keys[0] != "nested-component" {
		t.Fatalf("component keys = %v, want [nested-component]", keys)
	}
}

func TestWidgetFactoryNestedWidgetKeepsOtherWidgets(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	var buildsA, buildsB atomic.Int32
	if err := registry.RegisterWidget("stable-a", func() sdk.Component {
		buildsA.Add(1)
		return &panicComponent{text: "STABLE A"}
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterWidget("stable-b", func() sdk.Component {
		buildsB.Add(1)
		return &panicComponent{text: "STABLE B"}
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stable widget frames", func() bool {
		text := extensionFrameText(t, runner)
		return strings.Contains(text, "STABLE A") && strings.Contains(text, "STABLE B")
	})
	var buildsC, buildsD atomic.Int32
	boundedCall(t, "widget factory nested widget registration", func() {
		err := registry.RegisterWidget("nested-widget", func() sdk.Component {
			buildsC.Add(1)
			if registerErr := registry.RegisterWidget("nested-widget-child", func() sdk.Component {
				buildsD.Add(1)
				return &panicComponent{text: "NESTED CHILD"}
			}); registerErr != nil {
				t.Errorf("nested RegisterWidget: %v", registerErr)
			}
			return &panicComponent{text: "NESTED PARENT"}
		})
		if err != nil {
			t.Errorf("RegisterWidget: %v", err)
		}
	})
	waitFor(t, "nested child frame", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "NESTED CHILD")
	})
	if buildsA.Load() != 1 || buildsB.Load() != 1 {
		t.Fatalf("unrelated widgets rebuilt: stable-a=%d stable-b=%d, want 1/1", buildsA.Load(), buildsB.Load())
	}
	if buildsC.Load() != 1 || buildsD.Load() != 1 {
		t.Fatalf("nested widgets built parent=%d child=%d, want 1/1", buildsC.Load(), buildsD.Load())
	}
}

func TestRendererReplacementNestedRegistrationDoesNotDeadlock(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	if err := registry.RegisterMessageRenderer("note", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return &panicComponent{text: "FIRST NOTE"}
	}); err != nil {
		t.Fatal(err)
	}
	runner.Surface().AddCustomMessage(interactive.CustomEntryView{CustomType: "note", Text: "payload"})
	waitFor(t, "first note frame", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "FIRST NOTE")
	})
	boundedCall(t, "renderer replacement with nested registration", func() {
		err := registry.RegisterMessageRenderer("note", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
			if registerErr := registry.RegisterEntryRenderer("nested-entry", func(ctx sdk.RenderContext, entry sdk.Entry) sdk.Component {
				return &panicComponent{text: "NESTED ENTRY"}
			}); registerErr != nil {
				t.Errorf("nested RegisterEntryRenderer: %v", registerErr)
			}
			return &panicComponent{text: "SECOND NOTE"}
		})
		if err != nil {
			t.Errorf("RegisterMessageRenderer: %v", err)
		}
	})
	waitFor(t, "second note frame", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "SECOND NOTE")
	})
	keys := registry.EntryRendererTypes()
	if len(keys) != 1 || keys[0] != "nested-entry" {
		t.Fatalf("entry renderer types = %v, want [nested-entry]", keys)
	}
}

func TestRendererRegistrationStormIsBounded(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	if err := registry.RegisterMessageRenderer("storm", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
		return &panicComponent{text: "STORM FIRST"}
	}); err != nil {
		t.Fatal(err)
	}
	runner.Surface().AddCustomMessage(interactive.CustomEntryView{CustomType: "storm", Text: "payload"})
	waitFor(t, "storm first frame", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "STORM FIRST")
	})
	var runs atomic.Int32
	boundedCall(t, "renderer registration storm", func() {
		err := registry.RegisterMessageRenderer("storm", func(ctx sdk.RenderContext, message sdk.CustomMessage) sdk.Component {
			count := runs.Add(1)
			if registerErr := registry.RegisterEntryRenderer(fmt.Sprintf("storm-%d", count), func(ctx sdk.RenderContext, entry sdk.Entry) sdk.Component {
				return &panicComponent{text: "STORM ENTRY"}
			}); registerErr != nil {
				t.Errorf("storm RegisterEntryRenderer: %v", registerErr)
			}
			return &panicComponent{text: "STORM FRAME"}
		})
		if err != nil {
			t.Errorf("RegisterMessageRenderer: %v", err)
		}
	})
	if got := runs.Load(); got == 0 {
		t.Fatal("storm renderer never ran")
	} else if got > maxCoalescedExtensionRefreshes+1 {
		t.Fatalf("storm renderer ran %d times, want at most %d", got, maxCoalescedExtensionRefreshes+1)
	}
	waitFor(t, "storm frame", func() bool {
		return strings.Contains(extensionFrameText(t, runner), "STORM FRAME")
	})
}

func TestSlowRegistryFactoryRacingStopDisposesOnce(t *testing.T) {
	runner, _ := startTestRunner(t, TUIModeRegular, nil)
	registry := attachExtensionRegistry(t, runner)
	entered := make(chan struct{})
	release := make(chan struct{})
	late := &panicComponent{text: "SLOW REGISTRY"}
	registered := make(chan error, 1)
	go func() {
		registered <- registry.RegisterWidget("slow-registry", func() sdk.Component {
			close(entered)
			<-release
			return late
		})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("slow registry factory never started")
	}
	stopDone := make(chan struct{})
	go func() {
		runner.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop blocked on a slow factory inside a registry refresh")
	}
	close(release)
	select {
	case err := <-registered:
		if err != nil {
			t.Fatalf("RegisterWidget: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RegisterWidget did not return after the factory was released")
	}
	waitFor(t, "late registry widget disposal", func() bool { return late.disposes.Load() == 1 })
	if got := late.disposes.Load(); got != 1 {
		t.Fatalf("late widget dispose count = %d, want 1", got)
	}
}
