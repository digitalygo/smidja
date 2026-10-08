package tui

import (
	"strings"
	"testing"
)

func TestStackVerticalRender(t *testing.T) {
	stack := NewVStack(1, AlignStart)
	stack.AddChild(&plainComponent{lines: []string{"one"}})
	stack.AddChildWithOptions(&plainComponent{lines: []string{"two", "three"}}, StackEntryOptions{Basis: intPtr(1)})

	lines := stack.Render(10)
	if len(lines) != 3 {
		t.Fatalf("vertical render height = %d, want 3 (%q)", len(lines), lines)
	}
	if strings.TrimSpace(lines[0]) != "one" || strings.TrimSpace(lines[1]) != "" || strings.TrimSpace(lines[2]) != "two" {
		t.Fatalf("vertical render = %q", lines)
	}
	if stack.StackLayout().Vertical != true || stack.StackLayout().Gap != 1 {
		t.Fatalf("stack layout = %+v", stack.StackLayout())
	}
}

func TestStackVerticalEmpty(t *testing.T) {
	stack := NewVStack(0, AlignStart)
	if lines := stack.Render(10); lines != nil {
		t.Fatalf("empty stack render = %q", lines)
	}
}

func TestStackHorizontalRender(t *testing.T) {
	stack := NewHStack(1, AlignStart)
	stack.AddChild(&plainComponent{lines: []string{"aaaa"}})
	stack.AddChild(&plainComponent{lines: []string{"bb"}})
	lines := stack.Render(20)
	if len(lines) != 1 {
		t.Fatalf("horizontal render height = %d, want 1 (%q)", len(lines), lines)
	}
	plain := StripTerminalSequences(lines[0])
	if !strings.Contains(plain, "aaaa") || !strings.Contains(plain, "bb") {
		t.Fatalf("horizontal render missing children: %q", plain)
	}
	if stack.StackLayout().Vertical {
		t.Fatal("horizontal stack reported vertical layout")
	}
}

func TestStackHorizontalAlignment(t *testing.T) {
	for _, align := range []StackAlign{AlignStretch, AlignStart, AlignCenter, AlignEnd} {
		stack := NewHStack(0, align)
		stack.AddChild(&plainComponent{lines: []string{"one"}})
		stack.AddChild(&plainComponent{lines: []string{"two", "three", "four"}})
		lines := stack.Render(20)
		if len(lines) != 3 {
			t.Fatalf("align %v render height = %d", align, len(lines))
		}
	}
}

func TestStackEntryManagement(t *testing.T) {
	stack := NewVStack(0, AlignStart)
	first := &plainComponent{lines: []string{"first"}}
	second := &plainComponent{lines: []string{"second"}}
	stack.AddChild(first)
	stack.AddChild(second)
	stack.Invalidate()
	if len(stack.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(stack.entries))
	}
	stack.RemoveChild(first)
	if len(stack.entries) != 1 || stack.entries[0].Component != Component(second) {
		t.Fatalf("remove left entries %+v", stack.entries)
	}
	stack.RemoveChild(first)
	if len(stack.entries) != 1 {
		t.Fatalf("removing absent entry changed entries: %d", len(stack.entries))
	}
	stack.Clear()
	if len(stack.entries) != 0 || len(stack.Children()) != 0 {
		t.Fatal("clear should drop entries and children")
	}
}

func TestStackEntryRenderWidths(t *testing.T) {
	vertical := NewVStack(0, AlignStart)
	first := &plainComponent{lines: []string{"aa"}}
	second := &plainComponent{lines: []string{"bb"}}
	entries := []StackLayoutEntry{
		{Component: first},
		{Component: second, Options: StackEntryOptions{Basis: intPtr(1)}},
	}
	widths := vertical.entryRenderWidths(entries, 10)
	if len(widths) != 2 || widths[0] == nil || widths[1] == nil {
		t.Fatalf("vertical entry widths = %+v", widths)
	}

	horizontal := NewHStack(0, AlignStart)
	widths = horizontal.entryRenderWidths(entries, 10)
	if len(widths) != 2 || widths[0] == nil || widths[1] != nil {
		t.Fatalf("horizontal entry widths = %+v", widths)
	}
}
