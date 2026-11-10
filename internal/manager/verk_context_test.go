package manager

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/verk"
)

func TestGraphPayloadResolvesContextWithoutChangingProse(t *testing.T) {
	state := verk.State{Inputs: map[string]any{"goal": "Complete goal", "items": []any{"first", "second"}}, Variables: map[string]any{"ready": true}, Runs: []verk.Run{{NodeID: "review", Status: "completed", Fields: map[string]any{"final_message": "Full\nreview output", "count": json.Number("9007199254740993")}}}}
	cases := []struct {
		name        string
		input, want any
		fail        bool
	}{
		{"input", "inputs.goal", "Complete goal", false},
		{"variable", "variables.ready", true, false},
		{"completed node", "nodes.review.final_message", "Full\nreview output", false},
		{"array", "inputs.items.1", "second", false},
		{"typed reference", map[string]any{"mode": "ref", "type": "string", "value": "nodes.review.final_message"}, "Full\nreview output", false},
		{"literal path", map[string]any{"mode": "literal", "type": "string", "value": "inputs.goal"}, "inputs.goal", false},
		{"prose", "inputs.goal describes the requested outcome.", "inputs.goal describes the requested outcome.", false},
		{"ordinary object", map[string]any{"instructions": "Use full output"}, map[string]any{"instructions": "Use full output"}, false},
		{"missing input", "inputs.missing", nil, true},
		{"missing node", "nodes.unexecuted.final_message", nil, true},
		{"incomplete node path", "nodes.review", nil, true},
		{"typed missing", map[string]any{"mode": "ref", "value": "inputs.missing"}, nil, true},
		{"typed mismatch", map[string]any{"mode": "ref", "type": "number", "value": "inputs.goal"}, nil, true},
		{"invalid mode", map[string]any{"mode": "unknown", "value": "inputs.goal"}, nil, true},
		{"absent", nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := graphPayload(managerstate.Task{GraphState: &state}, verk.Node{Params: map[string]any{"context": tc.input, "instruction": "Instruction"}})
			if tc.fail {
				if err == nil {
					t.Fatal("unresolved context accepted", string(raw))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err = json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(payload["context"], tc.want) {
				t.Fatalf("context=%#v want=%#v", payload["context"], tc.want)
			}
			if payload["instruction"] != "Instruction" {
				t.Fatal(payload)
			}
		})
	}
	value, err := graphContext(state, "nodes.review.count")
	if err != nil || value != json.Number("9007199254740993") {
		t.Fatal("numeric context precision lost", value, err)
	}
}
