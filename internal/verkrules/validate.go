package verkrules

import (
	"fmt"
	"strings"
)

func Validate(rule *Rule) []string {
	errors := []string{}
	active := map[*Rule]bool{}
	count := 0
	exhausted := false
	operand := func(value *Operand, path string) {
		if value == nil {
			errors = append(errors, path+": expected an operand")
			return
		}
		switch value.Mode {
		case "ref":
			pathValue, ok := value.Value.(string)
			if !ok || strings.TrimSpace(pathValue) == "" {
				errors = append(errors, path+": reference path must be nonempty")
			}
		case "literal":
			if !primitive(value.Type) || value.Type != typeOf(value.Value) || !finite(value.Value) {
				errors = append(errors, path+": literal must match its declared finite primitive type")
			}
		default:
			errors = append(errors, path+": unknown operand mode")
		}
	}
	var visit func(*Rule, string, int)
	visit = func(node *Rule, path string, depth int) {
		if exhausted {
			return
		}
		count++
		if count > MaxNodes {
			errors = append(errors, path+": rule exceeds 512 nodes")
			exhausted = true
			return
		}
		if depth > MaxDepth {
			errors = append(errors, path+": rule exceeds depth 32")
			return
		}
		if node == nil {
			errors = append(errors, path+": expected a rule object")
			return
		}
		if active[node] {
			errors = append(errors, path+": cyclic rule")
			return
		}
		active[node] = true
		defer delete(active, node)
		switch node.Kind {
		case "group":
			switch node.Op {
			case "AND", "OR", "NOT", "XOR", "NAND", "NOR", "XNOR", "ONE":
			default:
				errors = append(errors, path+": unknown group operator")
			}
			if node.Children == nil {
				errors = append(errors, path+": children must be an array")
			} else {
				if node.Op == "NOT" && len(node.Children) != 1 {
					errors = append(errors, path+": NOT requires exactly one child")
				} else if node.Op != "NOT" && len(node.Children) < 1 {
					errors = append(errors, path+": "+node.Op+" requires at least one child")
				}
				for i, child := range node.Children {
					if exhausted {
						break
					}
					visit(child, fmt.Sprintf("%s.children[%d]", path, i), depth+1)
				}
			}
		case "condition":
			switch node.Operator {
			case "eq", "neq", "gt", "gte", "lt", "lte", "contains", "starts_with", "ends_with", "exists":
			default:
				errors = append(errors, path+": unknown condition operator")
			}
			operand(node.Left, path+".left")
			if node.Operator != "exists" {
				operand(node.Right, path+".right")
			}
		default:
			errors = append(errors, path+": unknown rule kind")
		}
	}
	visit(rule, "rule", 1)
	return errors
}
