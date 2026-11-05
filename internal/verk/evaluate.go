package verk

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/digitalygo/smidja/internal/verkrules"
)

func Resolve(s State, path string) (verkrules.Resolution, error) {
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return verkrules.Resolution{}, fmt.Errorf("invalid reference %s", path)
	}
	var value any
	switch parts[0] {
	case "inputs":
		value = s.Inputs
	case "variables":
		value = s.Variables
	case "nodes":
		if len(parts) < 3 {
			return verkrules.Resolution{}, fmt.Errorf("node reference needs field")
		}
		for i := len(s.Runs) - 1; i >= 0; i-- {
			run := s.Runs[i]
			if run.NodeID == parts[1] && run.Status == "completed" {
				value = run.Fields
				break
			}
		}
		parts = parts[1:]
	default:
		return verkrules.Resolution{}, fmt.Errorf("unknown reference namespace")
	}
	for _, part := range parts[1:] {
		switch current := value.(type) {
		case map[string]any:
			var found bool
			value, found = current[part]
			if !found {
				return verkrules.Resolution{Reason: "reference missing"}, nil
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(current) {
				return verkrules.Resolution{Reason: "array index missing"}, nil
			}
			value = current[index]
		default:
			return verkrules.Resolution{Reason: "reference missing"}, nil
		}
	}
	return verkrules.Resolution{Found: true, Value: clone(value)}, nil
}
func Bind(s State, raw any) (any, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var operand verkrules.Operand
	if err = json.Unmarshal(encoded, &operand); err != nil {
		return nil, err
	}
	var value any
	switch operand.Mode {
	case "literal":
		value = operand.Value
	case "ref":
		path, ok := operand.Value.(string)
		if !ok {
			return nil, fmt.Errorf("reference must be string")
		}
		resolved, resolveErr := Resolve(s, path)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !resolved.Found {
			return nil, fmt.Errorf("reference %s missing", path)
		}
		value = resolved.Value
	default:
		return nil, fmt.Errorf("invalid operand mode")
	}
	if operand.Type != "" && !matchesType(value, operand.Type) {
		return nil, fmt.Errorf("operand type mismatch")
	}
	return clone(value), nil
}
func Bindings(s State, raw any) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("bindings must be object")
	}
	out := map[string]any{}
	for name, operand := range values {
		value, err := Bind(s, operand)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = value
	}
	return out, nil
}
func Evaluate(g Graph, s State) (Evaluation, error) {
	n, err := CurrentNode(g, s)
	if err != nil {
		return Evaluation{}, err
	}
	out := Evaluation{Fields: map[string]any{"final_message": ""}}
	resolver := func(path string) (verkrules.Resolution, error) { return Resolve(s, path) }
	switch n.Type {
	case "entry":
		out.Port = "next"
	case "data":
		out.Port = "done"
		out.Variables, err = Bindings(s, n.Params["mapping"])
		if err == nil {
			copyState := clone(s)
			err = applyVariables(g, &copyState, out.Variables)
		}
		if err != nil {
			out.Port = "invalid"
			out.Variables = nil
			out.Fields["final_message"] = err.Error()
		}
	case "if":
		result := verkrules.Route(n.Branches, n.DefaultID, resolver)
		out.Trace, _ = json.Marshal(result)
		out.Port = "invalid"
		if result.BranchID != nil {
			out.Port = *result.BranchID
		}
	case "switch":
		out.Port = "invalid"
		resolved, resolveErr := Resolve(s, text(n.Params["variable"]))
		if resolveErr != nil || !resolved.Found {
			break
		}
		out.Port = n.DefaultID
		cases, choiceErr := choices(n, "cases")
		if choiceErr != nil {
			return Evaluation{}, choiceErr
		}
		for _, choice := range cases {
			rule := &verkrules.Rule{Kind: "condition", Operator: "eq", Left: &verkrules.Operand{Mode: "literal", Type: primitiveType(resolved.Value), Value: resolved.Value}, Right: &verkrules.Operand{Mode: "literal", Type: primitiveType(choice.Value), Value: choice.Value}}
			result := verkrules.Evaluate(rule, resolver)
			if result.Status == "invalid" {
				out.Port = "invalid"
				break
			}
			if result.Value != nil && *result.Value {
				out.Port = choice.ID
				break
			}
		}
	case "repeat":
		out.Port = "again"
		if raw, ok := n.Params["condition"]; ok && raw != nil {
			rule, decodeErr := decodeRule(raw)
			if decodeErr != nil {
				return Evaluation{}, decodeErr
			}
			result := verkrules.Evaluate(rule, resolver)
			out.Trace, _ = json.Marshal(result)
			if result.Status == "invalid" {
				return Evaluation{}, fmt.Errorf("invalid repeat condition: %s", strings.Join(result.Errors, ", "))
			}
			if result.Value != nil && *result.Value {
				out.Port = "done"
				break
			}
		}
		counter := s.Loops[n.ID]
		if counter.Used >= counter.Budget {
			out.Port = "exhausted"
			out.BlockReason = "loop budget exhausted; explicit additional budget required"
		}
	case "end":
		if raw, ok := n.Params["result"]; ok {
			out.Fields, err = Bindings(s, raw)
			if err != nil {
				return Evaluation{}, err
			}
			if _, exists := out.Fields["final_message"]; !exists {
				out.Fields["final_message"] = ""
			}
		}
	default:
		out.External = true
	}
	return out, nil
}

func primitiveType(value any) string {
	for _, kind := range []string{"null", "boolean", "string", "number"} {
		if matchesType(value, kind) {
			return kind
		}
	}
	return "invalid"
}
