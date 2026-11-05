package verk

import (
	"encoding/json"
	"testing"
)

func TestDeclaredInputDefaultsAndTypes(t *testing.T) {
	graph := fixture("tool")
	graph.InputValues = []Value{{Name: "count", Type: "number", Value: 3}, {Name: "settings", Type: "object", Value: map[string]any{"enabled": true}}}
	state, err := NewState(graph, map[string]any{"count": 7})
	if err != nil {
		t.Fatal(err)
	}
	if state.Inputs["count"] != json.Number("7") {
		t.Fatal(state.Inputs)
	}
	state.Inputs["settings"].(map[string]any)["enabled"] = false
	again, err := NewState(graph, nil)
	if err != nil || again.Inputs["count"] != json.Number("3") || again.Inputs["settings"].(map[string]any)["enabled"] != true {
		t.Fatal(again.Inputs, err)
	}
	if _, err = NewState(graph, map[string]any{"count": "7"}); err == nil {
		t.Fatal("type mismatch accepted")
	}
}
