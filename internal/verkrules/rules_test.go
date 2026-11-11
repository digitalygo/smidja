package verkrules

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

func literal(value any) *Operand { return &Operand{Mode: "literal", Type: typeOf(value), Value: value} }
func ref(path string) *Operand   { return &Operand{Mode: "ref", Value: path} }
func condition(op string, a, b *Operand) *Rule {
	return &Rule{Kind: "condition", Operator: op, Left: a, Right: b}
}
func truth(value bool) *Rule { return condition("eq", literal(value), literal(true)) }
func group(op string, children ...*Rule) *Rule {
	return &Rule{Kind: "group", Op: op, Children: children}
}
func values(entries map[string]any) Resolver {
	return func(path string) (Resolution, error) {
		value, found := entries[path]
		return Resolution{Found: found, Value: value}, nil
	}
}
func expect(t *testing.T, got Result, status string) {
	t.Helper()
	if got.Status != status {
		t.Fatalf("result %+v, want %s", got, status)
	}
	if status == "invalid" {
		if got.Value != nil || len(got.Errors) == 0 {
			t.Fatalf("invalid result shape: %+v", got)
		}
	} else if got.Value == nil || *got.Value != (status == "true") || len(got.Errors) != 0 {
		t.Fatalf("valid result shape: %+v", got)
	}
}

func TestGroupTruthTables(t *testing.T) {
	for _, op := range []string{"AND", "OR", "XOR", "NAND", "NOR", "XNOR", "ONE"} {
		for size := 1; size <= 6; size++ {
			for bits := 0; bits < 1<<size; bits++ {
				children := make([]*Rule, size)
				count := 0
				for i := range children {
					v := bits&(1<<i) != 0
					if v {
						count++
					}
					children[i] = truth(v)
				}
				want := map[string]bool{"AND": count == size, "OR": count > 0, "XOR": count%2 == 1, "NAND": count != size, "NOR": count == 0, "XNOR": count%2 == 0, "ONE": count == 1}[op]
				expect(t, Evaluate(group(op, children...), nil), fmt.Sprint(want))
			}
		}
	}
	for _, v := range []bool{false, true} {
		expect(t, Evaluate(group("NOT", truth(v)), nil), fmt.Sprint(!v))
	}
}

func TestConditionSemantics(t *testing.T) {
	cases := []struct {
		op     string
		a, b   any
		status string
	}{
		{"eq", 1, json.Number("1"), "true"}, {"eq", true, true, "true"}, {"eq", "A", "a", "false"}, {"neq", 1, 2, "true"}, {"neq", false, false, "false"},
		{"eq", 1, "1", "invalid"}, {"neq", nil, nil, "invalid"}, {"eq", []any{1}, []any{1}, "invalid"}, {"eq", map[string]any{}, map[string]any{}, "invalid"},
		{"gt", 2, 1, "true"}, {"gt", 1, 1, "false"}, {"gte", 1, 1, "true"}, {"gte", 0, 1, "false"}, {"lt", 0, 1, "true"}, {"lt", 1, 1, "false"}, {"lte", 1, 1, "true"}, {"lte", 2, 1, "false"}, {"lt", "a", "b", "invalid"},
		{"contains", "hello World", "World", "true"}, {"contains", "hello World", "world", "false"}, {"contains", "hello", "", "true"}, {"contains", 1, 1, "invalid"}, {"contains", "abc", false, "invalid"},
		{"contains", []any{"1", true, nil, map[string]any{}}, 1, "false"}, {"contains", []any{1, "x"}, json.Number("1"), "true"}, {"contains", []string{"red", "blue"}, "blue", "true"}, {"contains", []any{true}, true, "true"}, {"contains", []any{nil}, nil, "invalid"}, {"contains", []any{[]any{1}}, []any{1}, "invalid"},
		{"starts_with", "Résumé", "Rés", "true"}, {"starts_with", "Alpha", "alpha", "false"}, {"starts_with", 1, "1", "invalid"}, {"ends_with", "café", "fé", "true"}, {"ends_with", "café", "FE", "false"}, {"ends_with", "a", 1, "invalid"},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%d_%s", i, tc.op), func(t *testing.T) {
			expect(t, Evaluate(condition(tc.op, ref("a"), ref("b")), values(map[string]any{"a": tc.a, "b": tc.b})), tc.status)
		})
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name    string
		rule    *Rule
		message string
	}{
		{"nil", nil, "expected a rule object"}, {"unknown_kind", &Rule{Kind: "code"}, "unknown rule kind"},
		{"unknown_group", group("EVAL", truth(true)), "unknown group operator"}, {"nil_children", group("AND"), "children must be an array"}, {"empty_children", group("OR", []*Rule{}...), "requires at least one child"}, {"not_arity", group("NOT", truth(true), truth(false)), "requires exactly one child"},
		{"unknown_condition", condition("execute", literal(1), literal(2)), "unknown condition operator"}, {"missing_operand", condition("eq", nil, literal(2)), "expected an operand"},
		{"unknown_mode", condition("exists", &Operand{Mode: "code", Value: "do()"}, nil), "unknown operand mode"}, {"empty_reference", condition("exists", ref(" \t"), nil), "reference path must be nonempty"}, {"numeric_reference", condition("exists", &Operand{Mode: "ref", Value: 1}, nil), "reference path must be nonempty"},
		{"mismatched_literal", condition("exists", &Operand{Mode: "literal", Type: "number", Value: "1"}, nil), "literal must match"}, {"null_literal", condition("exists", literal(nil), nil), "literal must match"}, {"array_literal", condition("exists", literal([]any{1}), nil), "literal must match"}, {"nan", condition("exists", literal(math.NaN()), nil), "finite primitive"}, {"infinity", condition("exists", literal(math.Inf(1)), nil), "finite primitive"}, {"bad_json_number", condition("exists", literal(json.Number("oops")), nil), "finite primitive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Validate(tc.rule)
			if len(got) == 0 || !strings.Contains(strings.Join(got, ";"), tc.message) {
				t.Fatalf("%v, want %s", got, tc.message)
			}
			expect(t, Evaluate(tc.rule, nil), "invalid")
		})
	}
	ignoredRight := condition("exists", literal(false), &Operand{Mode: "invalid"})
	expect(t, Evaluate(ignoredRight, nil), "true")
}

func TestResolutionFailuresAndPresence(t *testing.T) {
	expect(t, Evaluate(condition("exists", ref("missing"), nil), values(map[string]any{})), "false")
	expect(t, Evaluate(condition("exists", ref("null"), nil), values(map[string]any{"null": nil})), "true")
	expect(t, Evaluate(condition("exists", ref("false"), nil), values(map[string]any{"false": false})), "true")
	cases := []struct {
		name     string
		resolver Resolver
		message  string
	}{
		{"unavailable", nil, "resolver is unavailable"},
		{"error", func(string) (Resolution, error) { return Resolution{}, errors.New("private failure") }, "resolver failed for x"},
		{"panic", func(string) (Resolution, error) { panic("private failure") }, "resolver failed for x"},
		{"type", func(string) (Resolution, error) { return Resolution{Found: true, Type: "number", Value: "1"}, nil }, "resolved value does not match declared type number"},
		{"infinite", values(map[string]any{"x": math.Inf(1)}), "number must be finite"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(condition("exists", ref("x"), nil), tc.resolver)
			expect(t, got, "invalid")
			if !strings.Contains(got.Errors[0], tc.message) {
				t.Fatal(got.Errors)
			}
		})
	}
	got := Evaluate(condition("eq", ref("left"), ref("right")), values(map[string]any{}))
	expect(t, got, "invalid")
	if len(got.Errors) != 2 || got.Errors[0] != "rule: Missing reference: left" || got.Errors[1] != "rule: Missing reference: right" {
		t.Fatal(got.Errors)
	}
	got = Evaluate(condition("eq", ref("left"), literal(1)), func(string) (Resolution, error) {
		return Resolution{Reason: "Unavailable node output", Type: "wrong"}, nil
	})
	if len(got.Errors) != 1 || got.Errors[0] != "rule: Unavailable node output" {
		t.Fatal(got.Errors)
	}
	expect(t, Evaluate(condition("eq", ref("x"), literal(1)), nil), "invalid")
	expect(t, Evaluate(condition("eq", ref("x"), literal(1)), func(string) (Resolution, error) {
		return Resolution{Found: true, Type: "number", Value: json.Number("1")}, nil
	}), "true")
}

func TestEvaluationOrderAndFailClosed(t *testing.T) {
	for _, tc := range []struct {
		op    string
		first bool
		want  string
	}{{"AND", false, "false"}, {"OR", true, "true"}, {"NAND", false, "true"}, {"NOR", true, "false"}} {
		calls := 0
		resolver := func(string) (Resolution, error) { calls++; return Resolution{}, errors.New("unavailable") }
		expect(t, Evaluate(group(tc.op, truth(tc.first), condition("exists", ref("later"), nil)), resolver), tc.want)
		if calls != 0 {
			t.Fatalf("%s resolved skipped child", tc.op)
		}
		expect(t, Evaluate(group(tc.op, condition("exists", ref("first"), nil), truth(tc.first)), resolver), "invalid")
		if calls != 1 {
			t.Fatal(calls)
		}
		expect(t, Evaluate(group(tc.op, truth(tc.first), &Rule{Kind: "invalid"}), resolver), "invalid")
	}
	for _, op := range []string{"XOR", "XNOR", "ONE"} {
		paths := []string{}
		resolver := func(path string) (Resolution, error) {
			paths = append(paths, path)
			return Resolution{}, errors.New("no")
		}
		got := Evaluate(group(op, condition("exists", ref("a"), nil), condition("exists", ref("b"), nil)), resolver)
		expect(t, got, "invalid")
		if len(got.Errors) != 2 || strings.Join(paths, ",") != "a,b" {
			t.Fatalf("%s errors %v paths %v", op, got.Errors, paths)
		}
	}
}

func TestLimitsAndCycles(t *testing.T) {
	rule := truth(true)
	for i := 1; i < MaxDepth; i++ {
		rule = group("NOT", rule)
	}
	if errors := Validate(rule); len(errors) > 0 {
		t.Fatal(errors)
	}
	expect(t, Evaluate(group("NOT", rule), nil), "invalid")
	children := make([]*Rule, MaxNodes-1)
	leaf := truth(true)
	for i := range children {
		children[i] = leaf
	}
	if errors := Validate(group("AND", children...)); len(errors) > 0 {
		t.Fatal(errors)
	}
	oversized := group("AND", append(children, leaf)...)
	if errors := Validate(oversized); len(errors) != 1 || !strings.Contains(errors[0], "512 nodes") {
		t.Fatal(errors)
	}
	cycle := group("NOT", leaf)
	cycle.Children[0] = cycle
	if errors := Validate(cycle); len(errors) != 1 || errors[0] != "rule.children[0]: cyclic rule" {
		t.Fatal(errors)
	}
	children = make([]*Rule, MaxNodes*2)
	for i := range children {
		children[i] = leaf
	}
	if errors := Validate(group("AND", children...)); len(errors) != 1 {
		t.Fatal(errors)
	}
}

func TestRoutingPriorityAndTrace(t *testing.T) {
	resolverCalls := 0
	resolver := func(string) (Resolution, error) { resolverCalls++; return Resolution{}, errors.New("unexpected") }
	branches := []Branch{{ID: "skip", Rule: truth(false)}, {ID: "first", Label: "First match", Rule: truth(true)}, {ID: "later", Rule: condition("exists", ref("x"), nil)}}
	got := Route(branches, "", resolver)
	if got.Status != "matched" || got.BranchID == nil || *got.BranchID != "first" || len(got.Trace) != 2 || got.Trace[0].Label != "skip" || got.Trace[1].Label != "First match" || resolverCalls != 0 {
		t.Fatalf("%+v", got)
	}
	got = Route([]Branch{{ID: "bad", Rule: condition("eq", ref("missing"), literal(1))}, {ID: "yes", Rule: truth(true)}}, "fallback", values(map[string]any{}))
	if got.Status != "invalid" || got.BranchID != nil || len(got.Trace) != 1 || len(got.Errors) != 1 {
		t.Fatalf("%+v", got)
	}
	for _, branches := range [][]Branch{{}, {{ID: "no", Rule: truth(false)}}} {
		got = Route(branches, "fallback", nil)
		if got.Status != "default" || got.BranchID == nil || *got.BranchID != "fallback" {
			t.Fatalf("%+v", got)
		}
		got = Route(branches, " \n", nil)
		if got.Status != "invalid" || got.Errors[0] != "No branch matched and default route is missing" {
			t.Fatalf("%+v", got)
		}
	}
	for _, branches := range [][]Branch{nil, {{ID: "", Rule: truth(true)}}, {{ID: "same", Rule: truth(true)}, {ID: "same", Rule: truth(false)}}} {
		got = Route(branches, "default", nil)
		if got.Status != "invalid" || len(got.Trace) != 0 || got.BranchID != nil {
			t.Fatalf("%+v", got)
		}
	}
}

func TestJSONAndNumericInputs(t *testing.T) {
	for _, value := range []any{float64(1), float32(1), int(1), int8(1), int16(1), int32(1), int64(1), uint(1), uint8(1), uint16(1), uint32(1), uint64(1), json.Number("1")} {
		expect(t, Evaluate(condition("eq", literal(value), literal(float64(1))), nil), "true")
	}
	raw := `{"kind":"condition","operator":"eq","left":{"mode":"literal","type":"number","value":1},"right":{"mode":"ref","value":"node.count"}}`
	for _, useNumber := range []bool{false, true} {
		var rule Rule
		decoder := json.NewDecoder(strings.NewReader(raw))
		if useNumber {
			decoder.UseNumber()
		}
		if err := decoder.Decode(&rule); err != nil {
			t.Fatal(err)
		}
		got := Evaluate(&rule, values(map[string]any{"node.count": json.Number("1")}))
		expect(t, got, "true")
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != `{"status":"true","value":true,"errors":[]}` {
			t.Fatal(string(encoded))
		}
	}
	invalidJSON, err := json.Marshal(Evaluate(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(invalidJSON), `"value":null`) {
		t.Fatal(string(invalidJSON))
	}
	expect(t, Evaluate(condition("eq", ref("fn"), ref("fn")), values(map[string]any{"fn": func() {}})), "invalid")
}

func FuzzFiniteLiteralRoundTrip(f *testing.F) {
	f.Add(0.0)
	f.Add(-1.5)
	f.Add(math.MaxFloat64)
	f.Fuzz(func(t *testing.T, value float64) {
		rule := condition("eq", literal(value), literal(value))
		if math.IsNaN(value) || math.IsInf(value, 0) {
			expect(t, Evaluate(rule, nil), "invalid")
			return
		}
		encoded, err := json.Marshal(rule)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Rule
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		expect(t, Evaluate(&decoded, nil), "true")
	})
}
