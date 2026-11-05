package verk

import (
	"encoding/json"
	"fmt"
	"github.com/digitalygo/smidja/internal/verkrules"
	"testing"
	"time"
)

func ports(names ...string) []Port {
	out := []Port{}
	for _, name := range names {
		out = append(out, Port{ID: name, Label: name})
	}
	return out
}
func fixture(kind string) Graph {
	p := []Port{}
	switch kind {
	case "entry":
		p = ports("next")
	case "if":
		p = ports("yes", "default", "invalid")
	case "switch":
		p = ports("one", "default", "invalid")
	case "data":
		p = ports("done", "invalid")
	case "ai":
		p = ports("yes", "no")
	case "repeat":
		p = ports("again", "done", "exhausted")
	case "approve":
		p = ports("approved", "rejected", "timeout")
	case "clarify":
		p = ports("answer", "cancel")
	case "wait":
		p = ports("resume", "timeout")
	default:
		p = ports("done", "error")
	}
	n := Node{ID: "work", Type: kind, Outputs: p, Params: map[string]any{"ref": "registered", "limit": 2}, DefaultID: "default"}
	if kind == "if" {
		n.Branches = []verkrules.Branch{{ID: "yes", Rule: truth(true)}}
	}
	if kind == "ai" {
		n.Params["choices"] = []Choice{{ID: "yes"}, {ID: "no"}}
	}
	if kind == "switch" {
		n.Params["cases"] = []Choice{{ID: "one", Value: 1}}
		n.Params["variable"] = "inputs.n"
	}
	g := Graph{SchemaVersion: 1, Nodes: []Node{{ID: "entry", Type: "entry", Outputs: ports("next")}, n, {ID: "end", Type: "end"}}, Edges: []Edge{{ID: "initial", From: "entry", Port: "next", To: "work"}}, Variables: []Value{{Name: "count", Type: "number", Value: 0}}}
	for _, port := range p {
		g.Edges = append(g.Edges, Edge{ID: port.ID, From: "work", Port: port.ID, To: "end"})
	}
	return g
}
func truth(value bool) *verkrules.Rule {
	return &verkrules.Rule{Kind: "condition", Operator: "eq", Left: &verkrules.Operand{Mode: "literal", Type: "boolean", Value: value}, Right: &verkrules.Operand{Mode: "literal", Type: "boolean", Value: true}}
}

var now = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func apply(t *testing.T, g Graph, s State, e Event) State {
	t.Helper()
	e.ExpectedRevision = s.Revision
	e.At = now
	next, err := Apply(g, s, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func started(t *testing.T, g Graph) State {
	t.Helper()
	s, err := NewState(g, map[string]any{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	s = apply(t, g, s, Event{Type: "start", RunID: "entry_run", RequestID: "entry_request"})
	s = apply(t, g, s, Event{Type: "complete", RunID: "entry_run", Port: "next"})
	return apply(t, g, s, Event{Type: "start", RunID: "work_run", RequestID: "work_request"})
}
func reject(t *testing.T, g Graph, s State, e Event) {
	t.Helper()
	before, _ := json.Marshal(s)
	e.ExpectedRevision = s.Revision
	e.At = now
	if _, err := Apply(g, s, e, nil); err == nil {
		t.Fatalf("accepted invalid event %+v", e)
	}
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		t.Fatal("failed event mutated state")
	}
}
func TestExternalRawOutputRecoveryAndTerminal(t *testing.T) {
	for _, kind := range []string{"lid", "tool", "verk", "ai"} {
		t.Run(kind, func(t *testing.T) {
			g := fixture(kind)
			s := started(t, g)
			reject(t, g, s, Event{Type: "complete", RunID: "work_run", Port: "done"})
			s = apply(t, g, s, Event{Type: "remote", RunID: "work_run", Remote: json.RawMessage(`{"turn_id":"t"}`)})
			reject(t, g, s, Event{Type: "remote", RunID: "work_run", Remote: json.RawMessage(`{"turn_id":"different"}`)})
			raw := "exact\nraw\toutput"
			s = apply(t, g, s, Event{Type: "output", RunID: "work_run", Output: raw})
			reject(t, g, s, Event{Type: "output", RunID: "work_run", Output: "duplicate"})
			s = apply(t, g, s, Event{Type: "pause", Text: "classifier unavailable"})
			s = clone(s)
			s = apply(t, g, s, Event{Type: "resume", Text: "retry saved output"})
			run, _ := CurrentRun(s)
			if run.Output != raw || !run.OutputRecorded || len(s.Runs) != 2 {
				t.Fatal("recovery lost raw output")
			}
			port := "done"
			if kind == "ai" {
				port = "yes"
			}
			s = apply(t, g, s, Event{Type: "complete", RunID: run.ID, Port: port})
			s = apply(t, g, s, Event{Type: "start", RunID: "end_run", RequestID: "end_request"})
			s = apply(t, g, s, Event{Type: "complete", RunID: "end_run"})
			if s.Status != "completed" {
				t.Fatal(s)
			}
			reject(t, g, s, Event{Type: "cancel"})
		})
	}
}
func TestDeterministicBlocks(t *testing.T) {
	for _, kind := range []string{"data", "if", "switch", "repeat"} {
		t.Run(kind, func(t *testing.T) {
			g := fixture(kind)
			g.Nodes[1].Params["mapping"] = map[string]any{"count": map[string]any{"mode": "ref", "value": "inputs.n", "type": "number"}}
			s := started(t, g)
			evaluation, err := Evaluate(g, s)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"data": "done", "if": "yes", "switch": "one", "repeat": "again"}[kind]
			if evaluation.Port != want {
				t.Fatalf("%+v", evaluation)
			}
			reject(t, g, s, Event{Type: "complete", RunID: "work_run", Port: "fabricated"})
			s = apply(t, g, s, Event{Type: "complete", RunID: "work_run", Port: want})
			if s.Status != "queued" {
				t.Fatal(s)
			}
			if kind == "data" && fmt.Sprint(s.Variables["count"]) != "1" {
				t.Fatal(s.Variables)
			}
		})
	}
}
func TestGateAuthorizationReplayAndTimeout(t *testing.T) {
	for _, kind := range []string{"approve", "clarify", "wait"} {
		t.Run(kind, func(t *testing.T) {
			g := fixture(kind)
			s := started(t, g)
			deadline := now.Add(time.Hour)
			gate := &Gate{ID: "gate", RunID: "work_run", Kind: kind, Prompt: "exact proposal", ProposalDigest: ProposalDigest("exact proposal"), Deadline: &deadline, SourceRef: "registered_source"}
			s = apply(t, g, s, Event{Type: "wait", RunID: "work_run", Gate: gate})
			reject(t, g, s, Event{Type: "resume", Text: "bypass"})
			signal := Event{Type: "signal", RunID: "work_run", GateID: "gate", Text: "answer", Port: map[string]string{"approve": "approved", "clarify": "answer", "wait": "resume"}[kind]}
			reject(t, g, s, signal)
			signal.TrustedOwner = true
			signal.ProposalDigest = gate.ProposalDigest
			signal.SourceRef = gate.SourceRef
			s = apply(t, g, s, signal)
			reject(t, g, s, signal)
			if s.Status != "queued" || s.Gate.Status != signal.Port {
				t.Fatal(s)
			}
			if kind != "clarify" {
				fresh := started(t, g)
				fresh = apply(t, g, fresh, Event{Type: "wait", RunID: "work_run", Gate: gate})
				reject(t, g, fresh, Event{Type: "signal", RunID: "work_run", GateID: "gate", Port: "timeout"})
				timeout := Event{Type: "signal", RunID: "work_run", GateID: "gate", Port: "timeout", At: deadline, ExpectedRevision: fresh.Revision}
				next, err := Apply(g, fresh, timeout, nil)
				if err != nil || next.Status != "queued" {
					t.Fatalf("%+v %v", next, err)
				}
			}
		})
	}
}
func TestLoopBudgetPreservedAcrossResume(t *testing.T) {
	g := fixture("repeat")
	for i := range g.Edges {
		if g.Edges[i].Port == "again" {
			g.Edges[i].To = "work"
		}
	}
	s := started(t, g)
	for i := 0; i < 2; i++ {
		s = apply(t, g, s, Event{Type: "complete", RunID: s.Runs[len(s.Runs)-1].ID, Port: "again"})
		s = apply(t, g, s, Event{Type: "start", RunID: fmt.Sprintf("run%d", i), RequestID: fmt.Sprintf("req%d", i)})
	}
	evaluation, err := Evaluate(g, s)
	if err != nil || evaluation.BlockReason == "" {
		t.Fatalf("%+v %v", evaluation, err)
	}
	reject(t, g, s, Event{Type: "complete", RunID: s.Runs[len(s.Runs)-1].ID, Port: "again"})
	s = apply(t, g, s, Event{Type: "block", Text: evaluation.BlockReason})
	reject(t, g, s, Event{Type: "resume", Text: "reset budget"})
	reject(t, g, s, Event{Type: "budget", Budget: 1})
	s = apply(t, g, clone(s), Event{Type: "budget", Budget: 1, TrustedOwner: true})
	if s.Loops["work"].Used != 2 || s.Loops["work"].Budget != 3 {
		t.Fatal(s.Loops)
	}
	s = apply(t, g, s, Event{Type: "cancel"})
	if s.Status != "cancelled" {
		t.Fatal(s)
	}
}
func TestOutputSchemaFailureRetainsRaw(t *testing.T) {
	g := fixture("lid")
	g.Nodes[1].OutputSchema = json.RawMessage(`{"type":"object"}`)
	s := started(t, g)
	s = apply(t, g, s, Event{Type: "output", RunID: "work_run", Output: "keep me"})
	e := Event{Type: "complete", RunID: "work_run", Port: "done", ExpectedRevision: s.Revision, At: now}
	if _, err := Apply(g, s, e, func(json.RawMessage, json.RawMessage) error { return fmt.Errorf("wrong type") }); err == nil {
		t.Fatal("accepted bad schema")
	}
	if s.Runs[1].Output != "keep me" {
		t.Fatal("lost evidence")
	}
	if _, err := Apply(g, s, e, func(json.RawMessage, json.RawMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
func TestReferencesAndBindings(t *testing.T) {
	s := State{Inputs: map[string]any{"list": []any{"a"}, "null": nil}, Variables: map[string]any{"x": true}, Runs: []Run{{NodeID: "a", Status: "completed", Fields: map[string]any{"answer": 42}}}}
	for _, path := range []string{"inputs.list.0", "inputs.null", "variables.x", "nodes.a.answer"} {
		resolved, err := Resolve(s, path)
		if err != nil || !resolved.Found {
			t.Fatalf("%s %+v %v", path, resolved, err)
		}
	}
	for _, path := range []string{"inputs.missing", "inputs.list.4", "inputs.list.bad", "nodes.no.field", "inputs.null.child"} {
		r, e := Resolve(s, path)
		if e != nil || r.Found {
			t.Fatal(path, r, e)
		}
	}
	for _, path := range []string{"invalid", "other.path", "nodes.a"} {
		if _, e := Resolve(s, path); e == nil {
			t.Fatal(path)
		}
	}
	for _, operand := range []any{map[string]any{"mode": "bad"}, map[string]any{"mode": "ref", "value": 5}, map[string]any{"mode": "ref", "value": "inputs.missing"}, map[string]any{"mode": "literal", "value": true, "type": "string"}} {
		if _, e := Bind(s, operand); e == nil {
			t.Fatal(operand)
		}
	}
	if _, e := Bindings(s, []any{}); e == nil {
		t.Fatal("bindings array")
	}
}
func TestValidationRejectsUnsafeGraphs(t *testing.T) {
	cases := map[string]func(*Graph){"version": func(g *Graph) { g.SchemaVersion = 3 }, "duplicate": func(g *Graph) { g.Nodes[1].ID = "entry" }, "kind": func(g *Graph) { g.Nodes[1].Type = "script" }, "extraentry": func(g *Graph) { g.Nodes[1].Type = "entry" }, "badport": func(g *Graph) { g.Nodes[1].Outputs[0].Label = "" }, "endedge": func(g *Graph) { g.Nodes[2].Outputs = ports("done") }, "noref": func(g *Graph) { g.Nodes[1].Params = nil }, "variable": func(g *Graph) { g.Variables[0].Value = "wrong" }, "edge": func(g *Graph) { g.Edges[0].To = "missing" }, "cycle": func(g *Graph) { g.Edges[1].To = "work" }, "unreachable": func(g *Graph) { g.Nodes = append(g.Nodes, Node{ID: "orphan", Type: "end"}) }, "unconnected": func(g *Graph) { g.Edges = g.Edges[:1] }, "nostart": func(g *Graph) { g.Nodes[0].Type = "data" }}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			g := fixture("tool")
			mutate(&g)
			if Validate(g, true) == nil {
				t.Fatal("accepted invalid graph")
			}
		})
	}
	g := fixture("tool")
	g.Edges = nil
	if err := Validate(g, false); err != nil {
		t.Fatal(err)
	}
}
func TestReducerRejectsInvalidEvents(t *testing.T) {
	g := fixture("tool")
	s := started(t, g)
	for _, e := range []Event{{Type: "unknown"}, {Type: "start", RunID: "again", RequestID: "again"}, {Type: "output", RunID: "wrong"}, {Type: "remote", RunID: "work_run", Remote: json.RawMessage("bad")}, {Type: "wait", RunID: "work_run"}, {Type: "signal"}, {Type: "resume", Text: "wrong"}, {Type: "pause"}, {Type: "budget", TrustedOwner: true, Budget: 1}} {
		reject(t, g, s, e)
	}
	if _, err := Apply(g, s, Event{ExpectedRevision: 99}, nil); err == nil {
		t.Fatal("revision")
	}
	if _, err := Apply(g, s, Event{ExpectedRevision: s.Revision}, nil); err == nil {
		t.Fatal("missing time")
	}
	s = apply(t, g, s, Event{Type: "block", Text: "missing requirement"})
	s = apply(t, g, s, Event{Type: "resume", Text: "provided"})
	if s.Status != "queued" || s.Runs[1].Status != "blocked" {
		t.Fatal(s)
	}
	reject(t, g, s, Event{Type: "start", RunID: "work_run", RequestID: "new"})
}

func TestDeterministicInvalidAndDefaultRoutes(t *testing.T) {
	g := fixture("data")
	g.Nodes[1].Params["mapping"] = map[string]any{"unknown": map[string]any{"mode": "literal", "value": 1}}
	s := started(t, g)
	e, err := Evaluate(g, s)
	if err != nil || e.Port != "invalid" {
		t.Fatal(e, err)
	}
	g = fixture("if")
	g.Nodes[1].Branches[0].Rule = truth(false)
	s = started(t, g)
	e, err = Evaluate(g, s)
	if err != nil || e.Port != "default" {
		t.Fatal(e, err)
	}
	g = fixture("switch")
	s = started(t, g)
	s.Inputs["n"] = 2
	e, err = Evaluate(g, s)
	if err != nil || e.Port != "default" {
		t.Fatal(e, err)
	}
	delete(s.Inputs, "n")
	e, err = Evaluate(g, s)
	if err != nil || e.Port != "invalid" {
		t.Fatal(e, err)
	}
	g = fixture("repeat")
	g.Nodes[1].Params["condition"] = truth(true)
	s = started(t, g)
	e, err = Evaluate(g, s)
	if err != nil || e.Port != "done" {
		t.Fatal(e, err)
	}
	g.Nodes[1].Params["condition"] = map[string]any{"kind": "invalid"}
	if _, err = Evaluate(g, s); err == nil {
		t.Fatal("bad repeat")
	}
}
func TestEndResultAndOwnerInterventionResolution(t *testing.T) {
	g := fixture("data")
	g.Nodes[2].Params = map[string]any{"result": map[string]any{"value": map[string]any{"mode": "ref", "value": "inputs.n"}}}
	g.Nodes[2].ExitActions = &ExitActions{Intervention: true}
	s := started(t, g)
	s = apply(t, g, s, Event{Type: "complete", RunID: "work_run", Port: "done"})
	s = apply(t, g, s, Event{Type: "start", RunID: "end", RequestID: "end"})
	s = apply(t, g, s, Event{Type: "complete", RunID: "end"})
	if s.Status != "blocked" || fmt.Sprint(s.Result["value"]) != "1" {
		t.Fatal(s)
	}
	s = apply(t, g, s, Event{Type: "resume", Text: "resolved"})
	if s.Status != "completed" || len(s.Runs) != 3 || s.Cursor != "end" {
		t.Fatal(s)
	}
}
func TestValidationNodeRulesAndTriggerFailures(t *testing.T) {
	for _, kind := range []string{"repeat", "if", "ai", "switch"} {
		g := fixture(kind)
		if kind == "repeat" {
			g.Nodes[1].Params["limit"] = 0
		} else if kind == "if" {
			g.Nodes[1].Branches = nil
		} else if kind == "ai" {
			g.Nodes[1].Params["choices"] = []Choice{{ID: "yes"}}
		} else {
			g.Nodes[1].Params["cases"] = "invalid"
		}
		if Validate(g, true) == nil {
			t.Fatal(kind)
		}
	}
	for _, trigger := range []Trigger{{Type: "unknown"}, {Type: "push"}, {Type: "schedule"}, {Type: "webhook"}} {
		g := fixture("tool")
		g.Nodes[0].Triggers = []Trigger{trigger}
		if Validate(g, true) == nil {
			t.Fatal(trigger)
		}
	}
	for _, trigger := range []Trigger{{Type: "smidr"}, {Type: "push", Repositories: []string{"org/repo"}}, {Type: "schedule", Schedule: "0 * * * *", Timezone: "UTC"}, {Type: "webhook", SourceRef: "registered"}} {
		g := fixture("tool")
		g.Nodes[0].Triggers = []Trigger{trigger}
		if err := Validate(g, true); err != nil {
			t.Fatal(err)
		}
	}
	g := fixture("if")
	g.Nodes[1].DefaultID = "yes"
	if Validate(g, true) == nil {
		t.Fatal("overlapping default")
	}
	g = fixture("repeat")
	g.Nodes[1].Params["condition"] = map[string]any{"kind": "invalid"}
	if Validate(g, true) == nil {
		t.Fatal("invalid condition")
	}
	g = fixture("tool")
	g.Nodes[2].ExitActions = &ExitActions{State: "unknown"}
	if Validate(g, true) == nil {
		t.Fatal("exit state")
	}
}
func TestTypedValuesAndBindingFailures(t *testing.T) {
	for _, tc := range []struct {
		value any
		kind  string
	}{{nil, "null"}, {true, "boolean"}, {"s", "string"}, {json.Number("2"), "integer"}, {float64(1), "number"}, {map[string]any{}, "object"}, {[]any{}, "array"}} {
		if !matchesType(tc.value, tc.kind) {
			t.Fatal(tc)
		}
	}
	for _, value := range []any{json.Number("NaN"), json.Number("bad"), "2"} {
		if matchesType(value, "number") {
			t.Fatal(value)
		}
	}
	if _, ok := integer(2.1); ok {
		t.Fatal("fraction limit")
	}
	if _, ok := integer(json.Number("bad")); ok {
		t.Fatal("bad limit")
	}
	s := State{}
	if _, err := Bind(s, map[string]any{"mode": "ref", "value": "unknown.x"}); err == nil {
		t.Fatal("bad reference")
	}
	if _, err := Bind(s, []any{true}); err == nil {
		t.Fatal("bad operand")
	}
	if _, err := Bindings(s, map[string]any{"x": map[string]any{"mode": "bad"}}); err == nil {
		t.Fatal("bad binding")
	}
	if _, err := CurrentNode(Graph{}, s); err == nil {
		t.Fatal("no cursor")
	}
	if _, ok := CurrentRun(s); ok {
		t.Fatal("no run")
	}
}

func TestLoopCannotBypassCounterAndGlobalStepBound(t *testing.T) {
	g := fixture("repeat")
	for i := range g.Edges {
		if g.Edges[i].Port == "done" {
			g.Edges[i].To = "work"
		}
	}
	if Validate(g, true) == nil {
		t.Fatal("done edge bypasses repeat budget")
	}
	g = fixture("tool")
	s := started(t, g)
	s.Status = "queued"
	s.Runs = make([]Run, 10000)
	reject(t, g, s, Event{Type: "start", RunID: "new", RequestID: "new"})
	s = apply(t, g, s, Event{Type: "pause", Text: "global step bound"})
	if s.Status != "paused" {
		t.Fatal(s.Status)
	}
}
func TestRejectNonJSONInputsEventsAndGraph(t *testing.T) {
	g := fixture("tool")
	if _, err := NewState(g, map[string]any{"unsupported": func() {}}); err == nil {
		t.Fatal("non JSON inputs")
	}
	s := started(t, g)
	reject(t, g, s, Event{Type: "output", RunID: "work_run", Fields: map[string]any{"unsupported": func() {}}})
	g.Nodes[1].Params["unsupported"] = func() {}
	if Validate(g, true) == nil {
		t.Fatal("non JSON graph")
	}
}
