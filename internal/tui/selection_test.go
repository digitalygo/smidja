package tui

import (
	"strings"
	"testing"
)

func TestSelectionRangeAndSegments(t *testing.T) {
	selection := NewSelection()
	if _, _, ok := selection.Range(); ok {
		t.Fatal("empty selection should have no range")
	}
	selection.Begin(SelectionPoint{Line: 2, Column: 5}, 3)
	if selection.Active() {
		t.Fatal("fresh selection should be empty")
	}
	selection.Update(SelectionPoint{Line: 4, Column: 1})
	start, end, ok := selection.Range()
	if !ok || start.Line != 2 || end.Line != 4 || end.Column != 1 {
		t.Fatalf("range %+v %+v ok=%v", start, end, ok)
	}
	if selection.Generated() != 3 {
		t.Fatalf("generation not retained: %d", selection.Generated())
	}
	selection.End()
	if selection.Active() {
		t.Fatal("End should deactivate")
	}
}

func TestSelectionReversedRangeNormalized(t *testing.T) {
	selection := NewSelection()
	selection.Begin(SelectionPoint{Line: 5, Column: 9}, 1)
	selection.Update(SelectionPoint{Line: 1, Column: 2})
	start, end, ok := selection.Range()
	if !ok || start.Line != 1 || end.Line != 5 || start.Column != 2 || end.Column != 9 {
		t.Fatalf("reversed range not normalized: %+v %+v", start, end)
	}
}

func TestSelectionUpdateInactiveIgnored(t *testing.T) {
	selection := NewSelection()
	selection.Update(SelectionPoint{Line: 1, Column: 1})
	if selection.Active() {
		t.Fatal("update on inactive selection should be ignored")
	}
}

func TestSelectionClear(t *testing.T) {
	selection := NewSelection()
	selection.Begin(SelectionPoint{Line: 0, Column: 0}, 1)
	selection.Update(SelectionPoint{Line: 1, Column: 1})
	selection.Clear()
	if selection.Active() {
		t.Fatal("clear should reset")
	}
	if _, _, ok := selection.Range(); ok {
		t.Fatal("cleared selection should have no range")
	}
}

func TestSelectionTextPlainAndCells(t *testing.T) {
	lines := []string{"\x1b[31mhello world\x1b[0m", "second 世界 line", "third"}
	text := SelectionText(lines, SelectionPoint{Line: 0, Column: 0}, SelectionPoint{Line: 1, Column: 7})
	if text != "hello world\nsecond" {
		t.Fatalf("unexpected selection text %q", text)
	}
	wide := SelectionText(lines, SelectionPoint{Line: 1, Column: 7}, SelectionPoint{Line: 1, Column: 11})
	if wide != "世界" {
		t.Fatalf("wide rune selection %q", wide)
	}
	reversed := SelectionText(lines, SelectionPoint{Line: 2, Column: 5}, SelectionPoint{Line: 0, Column: 0})
	if !strings.HasPrefix(reversed, "hello world") {
		t.Fatalf("reversed selection text %q", reversed)
	}
	outOfRange := SelectionText(lines, SelectionPoint{Line: 9, Column: 0}, SelectionPoint{Line: 12, Column: 0})
	if outOfRange != "" {
		t.Fatalf("out-of-range selection should be empty, got %q", outOfRange)
	}
}

func TestSelectionInvalidateOnGeneration(t *testing.T) {
	selection := NewSelection()
	selection.Begin(SelectionPoint{Line: 0, Column: 0}, 7)
	selection.Update(SelectionPoint{Line: 1, Column: 1})
	selection.InvalidateOnGeneration(7)
	if !selection.Active() {
		t.Fatal("same generation should keep the selection")
	}
	selection.InvalidateOnGeneration(8)
	if selection.Active() {
		t.Fatal("generation change should clear the selection")
	}
	if selection.Generated() != 8 {
		t.Fatalf("generation should update: %d", selection.Generated())
	}
}
