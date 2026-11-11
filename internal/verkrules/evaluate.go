package verkrules

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

func primitive(kind string) bool {
	return kind == "number" || kind == "boolean" || kind == "string"
}

func number(value any) (float64, bool) {
	switch value := value.(type) {
	case json.Number:
		n, err := strconv.ParseFloat(string(value), 64)
		return n, err == nil
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	default:
		return 0, false
	}
}

func typeOf(value any) string {
	if value == nil {
		return "null"
	}
	if _, ok := value.(json.Number); ok {
		return "number"
	}
	if _, ok := number(value); ok {
		return "number"
	}
	switch value.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	}
	switch reflect.TypeOf(value).Kind() {
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Func:
		return "function"
	default:
		return "object"
	}
}

func finite(value any) bool {
	if typeOf(value) != "number" {
		return true
	}
	n, ok := number(value)
	return ok && !math.IsInf(n, 0) && !math.IsNaN(n)
}

func equal(a, b any) bool {
	if typeOf(a) != typeOf(b) {
		return false
	}
	switch typeOf(a) {
	case "number":
		x, xok := number(a)
		y, yok := number(b)
		return xok && yok && x == y
	case "string":
		return a.(string) == b.(string)
	case "boolean":
		return a.(bool) == b.(bool)
	default:
		return false
	}
}

type readResult struct {
	found  bool
	value  any
	reason string
	err    string
}

func read(operand *Operand, path string, resolve Resolver) (out readResult) {
	if operand.Mode == "literal" {
		return readResult{found: true, value: operand.Value}
	}
	ref := operand.Value.(string)
	if resolve == nil {
		return readResult{err: path + ": reference resolver is unavailable"}
	}
	defer func() {
		if recover() != nil {
			out = readResult{err: path + ": resolver failed for " + ref}
		}
	}()
	resolved, err := resolve(ref)
	if err != nil {
		return readResult{err: path + ": resolver failed for " + ref}
	}
	if !resolved.Found {
		reason := resolved.Reason
		if reason == "" {
			reason = "Missing reference: " + ref
		}
		return readResult{reason: reason}
	}
	if resolved.Type != "" && resolved.Type != typeOf(resolved.Value) {
		return readResult{err: path + ": resolved value does not match declared type " + resolved.Type}
	}
	if !finite(resolved.Value) {
		return readResult{err: path + ": number must be finite"}
	}
	return readResult{found: true, value: resolved.Value}
}

func Evaluate(rule *Rule, resolve Resolver) Result {
	if errors := Validate(rule); len(errors) > 0 {
		return invalid(errors)
	}
	return evaluate(rule, "rule", resolve)
}

func evaluate(node *Rule, path string, resolve Resolver) Result {
	if node.Kind == "group" {
		if node.Op == "AND" || node.Op == "OR" || node.Op == "NAND" || node.Op == "NOR" {
			conjunction := node.Op == "AND" || node.Op == "NAND"
			inverse := node.Op == "NAND" || node.Op == "NOR"
			for i, child := range node.Children {
				value := evaluate(child, fmt.Sprintf("%s.children[%d]", path, i), resolve)
				if value.Status == "invalid" {
					return value
				}
				if conjunction && !*value.Value || !conjunction && *value.Value {
					return result(*value.Value != inverse)
				}
			}
			return result(conjunction != inverse)
		}
		errors := []string{}
		n := 0
		for i, child := range node.Children {
			value := evaluate(child, fmt.Sprintf("%s.children[%d]", path, i), resolve)
			errors = append(errors, value.Errors...)
			if value.Value != nil && *value.Value {
				n++
			}
		}
		if len(errors) > 0 {
			return invalid(errors)
		}
		switch node.Op {
		case "NOT":
			return result(n == 0)
		case "XOR":
			return result(n%2 == 1)
		case "XNOR":
			return result(n%2 == 0)
		default:
			return result(n == 1)
		}
	}
	left := read(node.Left, path+".left", resolve)
	if node.Operator == "exists" {
		if left.err != "" {
			return invalid([]string{left.err})
		}
		return result(left.found)
	}
	right := read(node.Right, path+".right", resolve)
	errors := []string{}
	for _, item := range []readResult{left, right} {
		if item.err != "" {
			errors = append(errors, item.err)
		} else if !item.found {
			errors = append(errors, path+": "+item.reason)
		}
	}
	if len(errors) > 0 {
		return invalid(errors)
	}
	a, b := left.value, right.value
	at, bt := typeOf(a), typeOf(b)
	mismatch := func(message string) Result { return invalid([]string{path + ": " + message}) }
	switch node.Operator {
	case "eq", "neq":
		if at != bt || !primitive(at) {
			return mismatch("equality requires matching primitive types")
		}
		return result(equal(a, b) == (node.Operator == "eq"))
	case "gt", "gte", "lt", "lte":
		if at != "number" || bt != "number" {
			return mismatch("relational comparison requires numbers")
		}
		x, _ := number(a)
		y, _ := number(b)
		switch node.Operator {
		case "gt":
			return result(x > y)
		case "gte":
			return result(x >= y)
		case "lt":
			return result(x < y)
		default:
			return result(x <= y)
		}
	case "contains":
		if at == "array" {
			if !primitive(bt) {
				return mismatch("array membership requires a primitive right operand")
			}
			array := reflect.ValueOf(a)
			for i := 0; i < array.Len(); i++ {
				if equal(array.Index(i).Interface(), b) {
					return result(true)
				}
			}
			return result(false)
		}
		if at != "string" || bt != "string" {
			return mismatch("contains requires strings or an array and primitive")
		}
		return result(strings.Contains(a.(string), b.(string)))
	default:
		if at != "string" || bt != "string" {
			return mismatch("text comparison requires strings")
		}
		if node.Operator == "starts_with" {
			return result(strings.HasPrefix(a.(string), b.(string)))
		}
		return result(strings.HasSuffix(a.(string), b.(string)))
	}
}
