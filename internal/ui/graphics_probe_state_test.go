package ui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/tui"
)

func newProbeRunner(t *testing.T, result bool, calls *int) (*Runner, *fakeUITerminal, *probeUITerminal) {
	t.Helper()
	terminal := newFakeUITerminal(80, 24)
	prober := &probeUITerminal{fakeUITerminal: terminal, result: result}
	opts := fakeUIRunnerOptions(terminal)
	opts.Mode = TUIModeFullscreen
	opts.Home = t.TempDir()
	opts.ImagesEnabled = true
	opts.Env = func(key string) string {
		if key == "TERM" {
			return "xterm-kitty"
		}
		return ""
	}
	opts.NewTerminal = func(in io.Reader, out io.Writer) tui.Terminal { return prober }
	runner := NewRunner(opts)
	if calls != nil {
		_ = calls
	}
	return runner, terminal, prober
}

func TestGraphicsFailedProbeToggleStaysUnavailable(t *testing.T) {
	cases := []struct {
		name string
	}{
		{name: "negative"},
		{name: "timeout"},
		{name: "wrong"},
		{name: "late"},
		{name: "malformed"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runner, _, prober := newProbeRunner(t, false, nil)
			if err := runner.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			defer runner.Stop()
			if capability := runner.GraphicsCapability(); capability.Available() {
				t.Fatalf("%s probe must not be available, got %+v", testCase.name, capability)
			}
			if capability := runner.GraphicsCapability(); capability.Protocol == tui.GraphicsKitty {
				t.Fatalf("%s probe must not report kitty, got %+v", testCase.name, capability)
			}
			runner.SetImagesEnabled(false)
			if capability := runner.GraphicsCapability(); capability.Available() {
				t.Fatalf("%s disabled must not be available, got %+v", testCase.name, capability)
			}
			runner.SetImagesEnabled(true)
			capability := runner.GraphicsCapability()
			if capability.Available() {
				t.Fatalf("%s toggle must stay unavailable, got %+v", testCase.name, capability)
			}
			if capability.Protocol == tui.GraphicsKitty {
				t.Fatalf("%s toggle must not become kitty without new probe, got %+v", testCase.name, capability)
			}
			if prober.calls != 1 {
				t.Fatalf("%s expected exactly one probe, got %d", testCase.name, prober.calls)
			}
		})
	}
}

func TestGraphicsSuccessfulProbeToggleRestores(t *testing.T) {
	runner, _, prober := newProbeRunner(t, true, nil)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	capability := runner.GraphicsCapability()
	if capability.Protocol != tui.GraphicsKitty || !capability.Available() {
		t.Fatalf("probe success should enable kitty, got %+v", capability)
	}
	runner.SetImagesEnabled(false)
	disabled := runner.GraphicsCapability()
	if disabled.Available() {
		t.Fatalf("policy disable must win, got %+v", disabled)
	}
	if disabled.Protocol != tui.GraphicsKitty {
		t.Fatalf("disabled should retain kitty protocol, got %+v", disabled)
	}
	runner.SetImagesEnabled(true)
	restored := runner.GraphicsCapability()
	if restored.Protocol != tui.GraphicsKitty || !restored.Available() {
		t.Fatalf("toggle should restore kitty, got %+v", restored)
	}
	if prober.calls != 1 {
		t.Fatalf("expected exactly one probe, got %d", prober.calls)
	}
}

func TestGraphicsExplicitPolicyHonored(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Mode = TUIModeFullscreen
	opts.Home = t.TempDir()
	opts.ImagesEnabled = true
	opts.Env = func(key string) string {
		if key == "TERM" {
			return "xterm-kitty"
		}
		return ""
	}
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	capability := runner.GraphicsCapability()
	if capability.Protocol != tui.GraphicsKitty || !capability.Available() {
		t.Fatalf("injected kitty without prober should be honored, got %+v", capability)
	}
	runner.SetImagesEnabled(false)
	if capability := runner.GraphicsCapability(); capability.Available() {
		t.Fatalf("policy disable must win over injected protocol, got %+v", capability)
	}
}

func TestGraphicsProtocolSwitchCleansPlacements(t *testing.T) {
	runner, terminal, _ := newProbeRunner(t, true, nil)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer runner.Stop()
	alt, ok := runner.view.(*tui.AltScreen)
	if !ok {
		t.Fatal("fullscreen runner should use alt screen")
	}
	alt.ImagePlacements().Allocate()
	if len(alt.ImagePlacements().Active()) == 0 {
		t.Fatal("placement should be active before switch")
	}
	runner.mu.Lock()
	runner.env = func(key string) string {
		if key == "TERM_PROGRAM" {
			return "iTerm.app"
		}
		return ""
	}
	runner.mu.Unlock()
	mark := terminal.WriteCount()
	runner.SetImagesEnabled(true)
	capability := runner.GraphicsCapability()
	if capability.Protocol != tui.GraphicsITerm2 {
		t.Fatalf("env switch should report iterm2, got %+v", capability)
	}
	output := terminal.OutputSince(mark)
	if !strings.Contains(output, "\x1b_Ga=d") {
		t.Fatalf("protocol switch must clean kitty placements, got %q", output)
	}
	time.Sleep(10 * time.Millisecond)
}
