package verk

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/digitalygo/smidja/internal/verkrules"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var nodeKinds = map[string]bool{"entry": true, "lid": true, "tool": true, "verk": true, "data": true, "if": true, "switch": true, "ai": true, "clarify": true, "approve": true, "wait": true, "repeat": true, "end": true}

func Validate(g Graph, runnable bool) error {
	if _, err := json.Marshal(g); err != nil {
		return fmt.Errorf("graph must contain JSON values: %w", err)
	}
	if g.SchemaVersion != 1 || len(g.Nodes) == 0 || len(g.Nodes) > 256 || len(g.Edges) > 1024 {
		return fmt.Errorf("invalid graph version or size")
	}
	nodes := map[string]Node{}
	entry := ""
	for _, n := range g.Nodes {
		if !identifier.MatchString(n.ID) || nodes[n.ID].ID != "" || !nodeKinds[n.Type] || math.IsNaN(n.X) || math.IsNaN(n.Y) || math.IsInf(n.X, 0) || math.IsInf(n.Y, 0) {
			return fmt.Errorf("invalid or duplicate node %q", n.ID)
		}
		nodes[n.ID] = n
		if n.Type == "entry" {
			if entry != "" {
				return fmt.Errorf("graph requires one entry")
			}
			entry = n.ID
		}
		ports := map[string]bool{}
		for _, p := range n.Outputs {
			if !identifier.MatchString(p.ID) || ports[p.ID] || strings.TrimSpace(p.Label) == "" {
				return fmt.Errorf("invalid output on %s", n.ID)
			}
			ports[p.ID] = true
		}
		if runnable {
			required := map[string][]string{"entry": {"next"}, "lid": {"done", "error"}, "tool": {"done", "error"}, "verk": {"done", "error"}, "data": {"done", "invalid"}, "clarify": {"answer", "cancel"}, "approve": {"approved", "rejected", "timeout"}, "wait": {"resume", "timeout"}, "repeat": {"again", "done", "exhausted"}}
			for _, port := range required[n.Type] {
				if !ports[port] {
					return fmt.Errorf("node %s missing output %s", n.ID, port)
				}
			}
		}
		if n.Type == "end" && len(n.Outputs) > 0 {
			return fmt.Errorf("end node cannot have outputs")
		}
		if n.Type == "repeat" {
			limit, ok := integer(n.Params["limit"])
			if !ok || limit < 1 || limit > 100 {
				return fmt.Errorf("repeat limit must be 1..100")
			}
			if condition, ok := n.Params["condition"]; ok && condition != nil {
				if _, err := decodeRule(condition); err != nil {
					return err
				}
			}
		}
		if n.Type == "if" {
			if len(n.Branches) == 0 {
				return fmt.Errorf("gate needs branches")
			}
			seen := map[string]bool{}
			for _, b := range n.Branches {
				if seen[b.ID] || !ports[b.ID] || len(verkrules.Validate(b.Rule)) > 0 {
					return fmt.Errorf("invalid gate branch")
				}
				seen[b.ID] = true
			}
			if !ports[n.DefaultID] || seen[n.DefaultID] || !ports["invalid"] {
				return fmt.Errorf("gate needs distinct default and invalid ports")
			}
		}
		if n.Type == "ai" || n.Type == "switch" {
			key := "choices"
			if n.Type == "switch" {
				key = "cases"
			}
			choices, err := choices(n, key)
			if err != nil {
				return err
			}
			seen := map[string]bool{}
			for _, c := range choices {
				if !ports[c.ID] || seen[c.ID] {
					return fmt.Errorf("invalid choice port")
				}
				seen[c.ID] = true
			}
			if n.Type == "switch" && (!ports[n.DefaultID] || seen[n.DefaultID] || !ports["invalid"]) {
				return fmt.Errorf("switch requires default and invalid outputs")
			}
			if n.Type == "ai" && len(choices) < 2 {
				return fmt.Errorf("AI decision needs at least two choices")
			}
		}
		if runnable && (n.Type == "lid" || n.Type == "tool" || n.Type == "verk") && text(n.Params["ref"]) == "" {
			return fmt.Errorf("%s requires registered reference", n.ID)
		}
		if n.Type == "entry" {
			for _, trigger := range n.Triggers {
				if err := validateTrigger(trigger); err != nil {
					return err
				}
			}
		}
		if n.ExitActions != nil && n.ExitActions.State != "" && n.ExitActions.State != "completed" && n.ExitActions.State != "blocked" && n.ExitActions.State != "cancelled" {
			return fmt.Errorf("invalid exit state")
		}
	}
	if entry == "" {
		return fmt.Errorf("graph requires one entry")
	}
	for _, values := range [][]Value{g.Variables, g.InputValues} {
		seen := map[string]bool{}
		for _, v := range values {
			if !variableName.MatchString(v.Name) || seen[v.Name] || !matchesType(v.Value, v.Type) {
				return fmt.Errorf("invalid typed variable %s", v.Name)
			}
			seen[v.Name] = true
		}
	}
	seenEdges := map[string]bool{}
	seenPorts := map[string]bool{}
	for _, e := range g.Edges {
		source, target := nodes[e.From], nodes[e.To]
		if e.ID == "" || seenEdges[e.ID] || source.ID == "" || target.ID == "" || target.Type == "entry" || !hasPort(source, e.Port) || seenPorts[e.From+"/"+e.Port] {
			return fmt.Errorf("invalid graph edge %q", e.ID)
		}
		seenEdges[e.ID] = true
		seenPorts[e.From+"/"+e.Port] = true
	}
	active, done := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if active[id] {
			return false
		}
		if done[id] {
			return true
		}
		active[id] = true
		for _, e := range g.Edges {
			if e.From == id && !(nodes[id].Type == "repeat" && e.Port == "again") && !visit(e.To) {
				return false
			}
		}
		delete(active, id)
		done[id] = true
		return true
	}
	for _, n := range g.Nodes {
		if !visit(n.ID) {
			return fmt.Errorf("cycle must pass through a bounded repeat")
		}
	}
	if runnable {
		reached := map[string]bool{}
		var reach func(string)
		reach = func(id string) {
			if reached[id] {
				return
			}
			reached[id] = true
			for _, e := range g.Edges {
				if e.From == id {
					reach(e.To)
				}
			}
		}
		reach(entry)
		for _, n := range g.Nodes {
			if !reached[n.ID] {
				return fmt.Errorf("unreachable node %s", n.ID)
			}
			if n.Type != "end" && len(n.Outputs) == 0 {
				return fmt.Errorf("node %s needs output ports", n.ID)
			}
			for _, p := range n.Outputs {
				if !seenPorts[n.ID+"/"+p.ID] && p.ID != "error" && p.ID != "invalid" && p.ID != "exhausted" {
					return fmt.Errorf("unconnected output %s/%s", n.ID, p.ID)
				}
			}
		}
	}
	return nil
}
func validateTrigger(t Trigger) error {
	switch t.Type {
	case "smidr":
	case "push", "pr", "ci":
		if len(t.Repositories) == 0 {
			return fmt.Errorf("repository trigger requires authorized scope")
		}
	case "schedule":
		if t.Schedule == "" || t.Timezone == "" {
			return fmt.Errorf("schedule requires expression and timezone")
		}
	case "webhook":
		if t.SourceRef == "" {
			return fmt.Errorf("webhook requires registered source")
		}
	default:
		return fmt.Errorf("unknown trigger")
	}
	return nil
}
func hasPort(n Node, id string) bool {
	for _, p := range n.Outputs {
		if p.ID == id {
			return true
		}
	}
	return false
}
func text(value any) string { s, _ := value.(string); return strings.TrimSpace(s) }
func integer(value any) (int, bool) {
	switch n := value.(type) {
	case int:
		return n, true
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n < 0 || n > 1000000 {
			return 0, false
		}
		return int(n), true
	case json.Number:
		f, e := n.Float64()
		if e == nil {
			return integer(f)
		}
	}
	return 0, false
}
func matchesType(v any, kind string) bool {
	switch kind {
	case "null":
		return v == nil
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number", "integer":
		switch n := v.(type) {
		case json.Number:
			f, e := n.Float64()
			return e == nil && !math.IsInf(f, 0) && !math.IsNaN(f) && (kind != "integer" || f == math.Trunc(f))
		case float64:
			return !math.IsInf(n, 0) && !math.IsNaN(n) && (kind != "integer" || n == math.Trunc(n))
		case int:
			return true
		}
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	}
	return false
}

type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value any    `json:"value"`
}

func choices(n Node, key string) ([]Choice, error) {
	raw, err := json.Marshal(n.Params[key])
	if err != nil {
		return nil, err
	}
	var result []Choice
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}
func decodeRule(value any) (*verkrules.Rule, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var rule verkrules.Rule
	if err = json.Unmarshal(raw, &rule); err != nil {
		return nil, err
	}
	if errors := verkrules.Validate(&rule); len(errors) > 0 {
		return nil, fmt.Errorf("invalid rule: %v", errors)
	}
	return &rule, nil
}
