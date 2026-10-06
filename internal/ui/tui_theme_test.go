package ui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/tui"
)

func writeRunnerTheme(t *testing.T, home, name, accent string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatalf("read built-in theme: %v", err)
	}
	body := strings.Replace(string(source), `"name": "dark"`, `"name": "`+name+`"`, 1)
	if accent != "" {
		body = strings.Replace(body, `"accent": "#8abeb7"`, `"accent": "`+accent+`"`, 1)
	}
	dir := tui.UserThemesDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForAccent(t *testing.T, runner *Runner, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		theme := runner.ThemeRegistry().Active()
		if theme != nil && strings.Contains(theme.Fg("accent", "x"), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("active theme never reached accent %s", want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRunnerLoadsUserKeybindings(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".smidja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(`{"app.exit": ["x"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = home
	runner := NewRunner(opts)
	t.Cleanup(runner.Stop)
	if got := runner.keys.UserBindings()["app.exit"]; len(got) != 1 || got[0] != "x" {
		t.Fatalf("app.exit bindings = %v, want [x]", got)
	}
	if !tui.GlobalKeybindings().Matches("x", "app.exit") {
		t.Fatal("the user keybinding must be installed globally for components")
	}
}

func TestRunnerInvalidKeybindingsFallBackWithNotice(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".smidja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(`{nope`), 0o644); err != nil {
		t.Fatal(err)
	}
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = home
	runner := NewRunner(opts)
	t.Cleanup(runner.Stop)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := runner.keys.UserBindings(); len(got) != 0 {
		t.Fatalf("user bindings = %v, want defaults only", got)
	}
	frame := runner.Surface().RenderFrame(80, 24)
	text := ""
	for _, line := range frame.Lines {
		text += tui.StripTerminalSequences(line) + "\n"
	}
	if !strings.Contains(text, "keybindings") {
		t.Fatalf("frame missing the keybindings warning:\n%s", text)
	}
}

func TestRunnerAppliesSingleUserTheme(t *testing.T) {
	home := t.TempDir()
	writeRunnerTheme(t, home, "custom", "#00ff88")
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = home
	opts.Theme = config.ThemeSetting{Single: "custom"}
	runner := NewRunner(opts)
	t.Cleanup(runner.Stop)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := runner.ThemeRegistry().ActiveName(); got != "custom" {
		t.Fatalf("active theme = %q, want custom", got)
	}
	waitForAccent(t, runner, tui.SGRFgRGB(0, 255, 136))
}

func TestRunnerThemePairFollowsTerminalBackground(t *testing.T) {
	home := t.TempDir()
	terminal := newFakeUITerminal(80, 24)
	terminal.SetBackground(&tui.RGBColor{R: 250, G: 250, B: 250})
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = home
	opts.Theme = config.ThemeSetting{Light: "light", Dark: "dark"}
	runner := NewRunner(opts)
	t.Cleanup(runner.Stop)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := runner.ThemeRegistry().ActiveName(); got != "light" {
		t.Fatalf("active theme = %q, want light for a light background", got)
	}
}

func TestRunnerThemePairFallsBackToDark(t *testing.T) {
	home := t.TempDir()
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = home
	opts.Theme = config.ThemeSetting{Light: "light", Dark: "dark"}
	runner := NewRunner(opts)
	t.Cleanup(runner.Stop)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := runner.ThemeRegistry().ActiveName(); got != "dark" {
		t.Fatalf("active theme = %q, want the safe dark fallback", got)
	}
}

func TestRunnerDefaultThemeFollowsTerminalBackground(t *testing.T) {
	home := t.TempDir()
	terminal := newFakeUITerminal(80, 24)
	terminal.SetBackground(&tui.RGBColor{R: 20, G: 20, B: 20})
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = home
	runner := NewRunner(opts)
	t.Cleanup(runner.Stop)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := runner.ThemeRegistry().ActiveName(); got != "dark" {
		t.Fatalf("active theme = %q, want dark for a dark background", got)
	}
}

func TestRunnerMissingThemeFallsBackWithNotice(t *testing.T) {
	home := t.TempDir()
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = home
	opts.Theme = config.ThemeSetting{Single: "missing"}
	runner := NewRunner(opts)
	t.Cleanup(runner.Stop)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := runner.ThemeRegistry().ActiveName(); got != "dark" {
		t.Fatalf("active theme = %q, want the dark fallback", got)
	}
	frame := runner.Surface().RenderFrame(80, 24)
	text := ""
	for _, line := range frame.Lines {
		text += tui.StripTerminalSequences(line) + "\n"
	}
	if !strings.Contains(text, "theme") {
		t.Fatalf("frame missing the theme warning:\n%s", text)
	}
}

func TestRunnerCustomThemeHotReloadAndStop(t *testing.T) {
	home := t.TempDir()
	path := writeRunnerTheme(t, home, "hot", "#00ff88")
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = home
	opts.Theme = config.ThemeSetting{Single: "hot"}
	opts.ThemeWatchInterval = 5 * time.Millisecond
	runner := NewRunner(opts)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !runner.ThemeRegistry().Watching() {
		t.Fatal("an active custom theme must be watched")
	}
	waitForAccent(t, runner, tui.SGRFgRGB(0, 255, 136))
	time.Sleep(20 * time.Millisecond)

	source, err := os.ReadFile(filepath.Join("..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(source), `"name": "dark"`, `"name": "hot"`, 1)
	updated = strings.Replace(updated, `"accent": "#8abeb7"`, `"accent": "#ff00aa"`, 1)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForAccent(t, runner, tui.SGRFgRGB(255, 0, 170))

	runner.Stop()
	if runner.ThemeRegistry().Watching() {
		t.Fatal("Stop must release the theme watcher")
	}
	again := strings.Replace(updated, `"accent": "#ff00aa"`, `"accent": "#00aaff"`, 1)
	later := time.Now().Add(4 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(again), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	theme := runner.ThemeRegistry().Active()
	if theme == nil || !strings.Contains(theme.Fg("accent", "x"), tui.SGRFgRGB(255, 0, 170)) {
		t.Fatal("a stopped runner must not reload the theme")
	}
}

func TestRunnerImagesDisabledByDefaultThenEnabledByOption(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	runner := NewRunner(opts)
	if runner.ImagesEnabled() {
		t.Fatal("a plain runner must keep images disabled")
	}
	enabled := NewRunner(RunnerOptions{
		Stdin:         strings.NewReader(""),
		Stdout:        os.Stdout,
		Home:          t.TempDir(),
		ImagesEnabled: true,
		NewTerminal:   fakeUIRunnerOptions(terminal).NewTerminal,
	})
	if !enabled.ImagesEnabled() {
		t.Fatal("the interactive entry point must enable images by default")
	}
	enabled.SetImagesEnabled(false)
	if enabled.ImagesEnabled() {
		t.Fatal("the runtime disable selector must win over the default")
	}
}

func TestRunnerAppliesThemeToFullscreenAndDialogs(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	opts := fakeUIRunnerOptions(terminal)
	opts.Home = t.TempDir()
	opts.Mode = TUIModeFullscreen
	runner := NewRunner(opts)
	t.Cleanup(runner.Stop)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := runner.ApplyTheme("light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	if got := runner.ThemeRegistry().ActiveName(); got != "light" {
		t.Fatalf("active theme = %q, want light", got)
	}
	if err := runner.ApplyTheme("missing"); err == nil {
		t.Fatal("an unknown theme must error visibly instead of panicking")
	}
}

func TestRunnerWithoutHomeUsesBuiltins(t *testing.T) {
	terminal := newFakeUITerminal(80, 24)
	runner := NewRunner(RunnerOptions{
		Stdin:       strings.NewReader(""),
		Stdout:      io.Discard,
		NewTerminal: fakeUIRunnerOptions(terminal).NewTerminal,
	})
	t.Cleanup(runner.Stop)
	if err := runner.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := runner.ThemeRegistry().ActiveName(); got != "dark" {
		t.Fatalf("active theme = %q, want the dark default without a home", got)
	}
	if got := runner.keys.UserBindings(); len(got) != 0 {
		t.Fatalf("user bindings = %v, want none without a home", got)
	}
}
