package tui

import "testing"

type sliceFrameComponent struct{ values []int }

func (c sliceFrameComponent) Render(width int) []string { return nil }

func (c sliceFrameComponent) Invalidate() {}

type pointerFrameComponent struct{ text string }

func (c *pointerFrameComponent) Render(width int) []string { return []string{c.text} }

func (c *pointerFrameComponent) Invalidate() {}

func TestSameComponentIdentity(t *testing.T) {
	first := &pointerFrameComponent{text: "first"}
	second := &pointerFrameComponent{text: "second"}
	if !SameComponent(nil, nil) {
		t.Fatal("nil components must be equal")
	}
	if SameComponent(first, nil) || SameComponent(nil, first) {
		t.Fatal("a component must not equal nil")
	}
	if !SameComponent(first, first) {
		t.Fatal("the same pointer must be equal")
	}
	if SameComponent(first, second) {
		t.Fatal("different pointers must not be equal")
	}
	if SameComponent(first, sliceFrameComponent{values: []int{1}}) {
		t.Fatal("different component types must not be equal")
	}
	if SameComponent(sliceFrameComponent{values: []int{1}}, sliceFrameComponent{values: []int{1}}) {
		t.Fatal("uncomparable component values must not be treated as equal")
	}
}
