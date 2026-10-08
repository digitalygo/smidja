package interactive

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/digitalygo/smidja/internal/tui"
)

func dialogLineContaining(lines []string, text string) string {
	for _, line := range lines {
		stripped := tui.StripTerminalSequences(line)
		if strings.Contains(stripped, text) {
			return stripped
		}
	}
	return ""
}

func dialogSelectItems() []tui.SelectItem {
	return []tui.SelectItem{
		{Value: "alpha", Label: "alpha"},
		{Value: "beta", Label: "beta"},
		{Value: "gamma", Label: "gamma"},
	}
}

func TestSelectDialogFilterTransitionsAndSelection(t *testing.T) {
	theme := testDialogTheme(t)
	var selected string
	var dialog *SelectDialog
	dialog = NewSelectDialog(SelectDialogOptions{Title: "Pick", Items: dialogSelectItems(), Searchable: true}, theme,
		func(value string) {
			selected = value
			_ = dialog.Render(50)
		}, func() {})

	if rendered := frameText(dialog.Render(50)); !strings.Contains(rendered, "alpha") {
		t.Fatalf("initial render missing items:\n%s", rendered)
	}
	dialog.HandleInput("ga")
	rendered := frameText(dialog.Render(50))
	if !strings.Contains(rendered, "gamma") || strings.Contains(rendered, "alpha") {
		t.Fatalf("filtered render = \n%s", rendered)
	}
	dialog.HandleInput("\r")
	if selected != "gamma" {
		t.Fatalf("filtered selection = %q, want gamma", selected)
	}
}

func TestSelectDialogSelectionPersistsAcrossRender(t *testing.T) {
	theme := testDialogTheme(t)
	var selected string
	dialog := NewSelectDialog(SelectDialogOptions{Title: "Pick", Items: dialogSelectItems()}, theme,
		func(value string) { selected = value }, func() {})
	dialog.HandleInput("\x1b[B")
	line := dialogLineContaining(dialog.Render(50), "beta")
	if !strings.Contains(line, "→") {
		t.Fatalf("selected row missing cursor: %q", line)
	}
	dialog.HandleInput("x")
	if line := dialogLineContaining(dialog.Render(50), "beta"); !strings.Contains(line, "→") {
		t.Fatalf("selection moved after ignored input: %q", line)
	}
	dialog.HandleInput("\r")
	if selected != "beta" {
		t.Fatalf("selected = %q, want beta", selected)
	}
}

func TestSelectDialogConcurrentInputFilterRender(t *testing.T) {
	theme := testDialogTheme(t)
	items := make([]tui.SelectItem, 8)
	for i, value := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"} {
		items[i] = tui.SelectItem{Value: value, Label: value}
	}
	var settles atomic.Int64
	var dialog *SelectDialog
	dialog = NewSelectDialog(SelectDialogOptions{Title: "Pick", Items: items, Searchable: true}, theme,
		func(string) {
			settles.Add(1)
			_ = dialog.Render(50)
		},
		func() {
			settles.Add(1)
			_ = dialog.Render(50)
		})

	const iterations = 100
	var wg sync.WaitGroup
	start := make(chan struct{})
	launch := func(run func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run()
		}()
	}
	launch(func() {
		for i := 0; i < iterations; i++ {
			dialog.HandleInput("\x1b[B")
			dialog.HandleInput("\x1b[A")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			dialog.HandleInput("a")
			dialog.HandleInput("\x7f")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			_ = dialog.Render(50)
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			dialog.HandleInput("\r")
			dialog.HandleInput("\x1b")
		}
	})
	close(start)
	wg.Wait()

	if settles.Load() == 0 {
		t.Fatal("dialog never settled during concurrent run")
	}
	if lines := dialog.Render(50); len(lines) == 0 {
		t.Fatal("dialog rendered empty after concurrent run")
	}
}

func TestSettingsDialogTwoTogglesApplyAndCancel(t *testing.T) {
	theme := testDialogTheme(t)
	items := []tui.SettingItem{{ID: "retry", Label: "Retry", Values: []string{"on", "off"}, CurrentValue: "on"}}
	var applied map[string]string
	var dialog *SettingsDialog
	dialog = NewSettingsDialog("Settings", items, theme, func(values map[string]string) {
		applied = values
		_ = dialog.Render(50)
	}, func() {})

	if line := dialogLineContaining(dialog.Render(50), "Retry"); !strings.Contains(line, " on ") {
		t.Fatalf("initial value = %q", line)
	}
	dialog.HandleInput("\r")
	if line := dialogLineContaining(dialog.Render(50), "Retry"); !strings.Contains(line, " off ") {
		t.Fatalf("first toggle value = %q", line)
	}
	dialog.HandleInput("\r")
	if line := dialogLineContaining(dialog.Render(50), "Retry"); !strings.Contains(line, " on ") {
		t.Fatalf("second toggle value = %q", line)
	}
	dialog.HandleInput("ret")
	if line := dialogLineContaining(dialog.Render(50), "Retry"); !strings.Contains(line, " on ") {
		t.Fatalf("filtered value = %q", line)
	}
	dialog.HandleInput("\x13")
	if applied == nil || applied["retry"] != "on" {
		t.Fatalf("applied draft = %v", applied)
	}

	applied = nil
	dialog.HandleInput("\x13")
	if applied != nil {
		t.Fatalf("late apply changed the draft: %v", applied)
	}
	dialog.HandleInput("\r")
	if applied != nil {
		t.Fatalf("input after settlement mutated the draft: %v", applied)
	}
}

func TestSettingsDialogCancelAppliesNothing(t *testing.T) {
	theme := testDialogTheme(t)
	items := []tui.SettingItem{{ID: "retry", Label: "Retry", Values: []string{"on", "off"}, CurrentValue: "on"}}
	applied := false
	cancelled := false
	var dialog *SettingsDialog
	dialog = NewSettingsDialog("Settings", items, theme,
		func(map[string]string) { applied = true },
		func() {
			cancelled = true
			_ = dialog.Render(50)
		})

	dialog.HandleInput("\r")
	if line := dialogLineContaining(dialog.Render(50), "Retry"); !strings.Contains(line, " off ") {
		t.Fatalf("toggle before cancel = %q", line)
	}
	dialog.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("cancel callback did not fire")
	}
	if applied {
		t.Fatal("cancel applied the draft")
	}
}

func TestSettingsDialogConcurrentInputRender(t *testing.T) {
	theme := testDialogTheme(t)
	items := []tui.SettingItem{
		{ID: "retry", Label: "Retry", Values: []string{"on", "off"}, CurrentValue: "on"},
		{ID: "verbose", Label: "Verbose", Values: []string{"on", "off"}, CurrentValue: "on"},
	}
	var applies atomic.Int64
	var cancels atomic.Int64
	var dialog *SettingsDialog
	dialog = NewSettingsDialog("Settings", items, theme,
		func(map[string]string) {
			applies.Add(1)
			_ = dialog.Render(60)
		},
		func() {
			cancels.Add(1)
			_ = dialog.Render(60)
		})

	const iterations = 100
	var wg sync.WaitGroup
	start := make(chan struct{})
	launch := func(run func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run()
		}()
	}
	launch(func() {
		for i := 0; i < iterations; i++ {
			dialog.HandleInput("\r")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			dialog.HandleInput("re")
			dialog.HandleInput("\x7f")
			dialog.HandleInput("\x7f")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			dialog.HandleInput("\x1b[B")
			dialog.HandleInput("\x1b[A")
		}
	})
	launch(func() {
		for i := 0; i < iterations; i++ {
			_ = dialog.Render(60)
		}
	})
	close(start)
	wg.Wait()

	dialog.HandleInput("\x13")
	dialog.HandleInput("\x1b")
	if applies.Load()+cancels.Load() == 0 {
		t.Fatal("dialog never settled during concurrent run")
	}
	if lines := dialog.Render(60); len(lines) == 0 {
		t.Fatal("dialog rendered empty after concurrent run")
	}
}

func TestSelectDialogSettleIsIdempotent(t *testing.T) {
	theme := testDialogTheme(t)
	settles := 0
	dialog := NewSelectDialog(SelectDialogOptions{Title: "Pick", Items: dialogSelectItems()}, theme, func(string) {}, func() {})
	dialog.SetFocused(true)
	dialog.settle("select", func() { settles++ })
	dialog.settle("select", func() { settles++ })
	if settles != 1 {
		t.Fatalf("settle count = %d", settles)
	}
}

func TestSettingsDialogSettleIsIdempotent(t *testing.T) {
	theme := testDialogTheme(t)
	settles := 0
	dialog := NewSettingsDialog("Settings", nil, theme, func(map[string]string) {}, func() {})
	dialog.SetFocused(true)
	dialog.settle(func() { settles++ })
	dialog.settle(func() { settles++ })
	if settles != 1 {
		t.Fatalf("settle count = %d", settles)
	}
}
