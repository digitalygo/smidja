package verk

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func clone[T any](value T) T {
	raw, _ := json.Marshal(value)
	var out T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&out)
	return out
}
func NewState(g Graph, inputs map[string]any) (State, error) {
	if _, err := json.Marshal(inputs); err != nil {
		return State{}, fmt.Errorf("inputs must contain JSON values: %w", err)
	}
	if err := Validate(g, true); err != nil {
		return State{}, err
	}
	s := State{Status: "queued", Inputs: clone(inputs), Variables: map[string]any{}, Loops: map[string]Loop{}, Runs: []Run{}, Clarifications: []string{}}
	if s.Inputs == nil {
		s.Inputs = map[string]any{}
	}
	for _, input := range g.InputValues {
		value, provided := s.Inputs[input.Name]
		if !provided {
			s.Inputs[input.Name] = clone(input.Value)
			continue
		}
		if !matchesType(value, input.Type) {
			return State{}, fmt.Errorf("input %s must be %s", input.Name, input.Type)
		}
	}
	for _, n := range g.Nodes {
		if n.Type == "entry" {
			s.Cursor = n.ID
		}
		if n.Type == "repeat" {
			limit, _ := integer(n.Params["limit"])
			s.Loops[n.ID] = Loop{Budget: limit}
		}
	}
	for _, v := range g.Variables {
		s.Variables[v.Name] = clone(v.Value)
	}
	return s, nil
}
func CurrentNode(g Graph, s State) (Node, error) {
	for _, n := range g.Nodes {
		if n.ID == s.Cursor {
			return n, nil
		}
	}
	return Node{}, fmt.Errorf("current graph node missing")
}
func CurrentRun(s State) (Run, bool) {
	if len(s.Runs) == 0 {
		return Run{}, false
	}
	return s.Runs[len(s.Runs)-1], true
}
func ProposalDigest(proposal string) string {
	sum := sha256.Sum256([]byte(proposal))
	return hex.EncodeToString(sum[:])
}

func Apply(g Graph, state State, e Event, validate SchemaValidator) (State, error) {
	if _, err := json.Marshal(e); err != nil {
		return State{}, fmt.Errorf("event must contain JSON values: %w", err)
	}
	if state.Revision != e.ExpectedRevision {
		return State{}, fmt.Errorf("graph revision conflict")
	}
	if state.Status == "completed" || state.Status == "cancelled" {
		return State{}, fmt.Errorf("graph is terminal")
	}
	s := clone(state)
	n, err := CurrentNode(g, s)
	if err != nil {
		return State{}, err
	}
	if e.At.IsZero() {
		return State{}, fmt.Errorf("graph event time required")
	}
	var run *Run
	if len(s.Runs) > 0 {
		run = &s.Runs[len(s.Runs)-1]
	}
	active := func() bool {
		return s.Status == "running" && run != nil && run.NodeID == n.ID && run.ID == e.RunID && run.Status == "running"
	}
	switch e.Type {
	case "start":
		if s.Status != "queued" || e.RunID == "" || e.RequestID == "" || len(s.Runs) >= 10000 {
			return State{}, fmt.Errorf("cannot start graph block")
		}
		visit := 1
		for _, old := range s.Runs {
			if old.ID == e.RunID || old.RequestID == e.RequestID {
				return State{}, fmt.Errorf("run identity reused")
			}
			if old.NodeID == n.ID {
				visit++
			}
		}
		s.Runs = append(s.Runs, Run{ID: e.RunID, NodeID: n.ID, Visit: visit, Status: "running", RequestID: e.RequestID, StartedAt: e.At})
		s.Status = "running"
		s.Reason = ""
		s.Gate = nil
	case "remote":
		if !active() || run.OutputRecorded || !json.Valid(e.Remote) {
			return State{}, fmt.Errorf("invalid remote binding")
		}
		if len(run.Remote) > 0 && !bytes.Equal(run.Remote, e.Remote) {
			return State{}, fmt.Errorf("remote binding cannot change")
		}
		run.Remote = clone(e.Remote)
	case "output":
		if !active() || run.OutputRecorded {
			return State{}, fmt.Errorf("output already recorded or stale run")
		}
		run.Output = e.Output
		run.Fields = clone(e.Fields)
		if run.Fields == nil {
			run.Fields = map[string]any{}
		}
		if _, ok := run.Fields["final_message"]; !ok {
			run.Fields["final_message"] = e.Output
		}
		run.OutputRecorded = true
	case "complete":
		if !active() {
			return State{}, fmt.Errorf("block not running")
		}
		external := n.Type == "lid" || n.Type == "tool" || n.Type == "verk" || n.Type == "ai"
		if n.Type == "ai" {
			allowed, choiceErr := choices(n, "choices")
			if choiceErr != nil {
				return State{}, choiceErr
			}
			found := false
			for _, choice := range allowed {
				if choice.ID == e.Port {
					found = true
				}
			}
			if !found {
				return State{}, fmt.Errorf("AI route is not a declared choice")
			}
		}
		if external && !run.OutputRecorded {
			return State{}, fmt.Errorf("persist full output before routing")
		}
		if n.Type == "clarify" || n.Type == "approve" || n.Type == "wait" {
			return State{}, fmt.Errorf("interaction requires authenticated signal")
		}
		if !external {
			evaluated, evaluateErr := Evaluate(g, s)
			if evaluateErr != nil {
				return State{}, evaluateErr
			}
			if evaluated.BlockReason != "" {
				return State{}, fmt.Errorf("%s", evaluated.BlockReason)
			}
			if e.Port != evaluated.Port {
				return State{}, fmt.Errorf("incorrect deterministic route")
			}
			e.Fields = evaluated.Fields
			e.Variables = evaluated.Variables
			e.Trace = evaluated.Trace
			run.Fields = clone(e.Fields)
			run.OutputRecorded = true
		}
		if err = validateOutput(n, *run, validate); err != nil {
			return State{}, err
		}
		if err = applyVariables(g, &s, e.Variables); err != nil {
			return State{}, err
		}
		if n.Type == "repeat" && e.Port == "again" {
			counter := s.Loops[n.ID]
			if counter.Used >= counter.Budget {
				return State{}, fmt.Errorf("loop budget exhausted")
			}
			counter.Used++
			s.Loops[n.ID] = counter
		}
		run.Trace = clone(e.Trace)
		if err = advance(g, &s, n, run, e.Port); err != nil {
			return State{}, err
		}
	case "wait":
		if !active() || e.Gate == nil || e.Gate.ID == "" || e.Gate.RunID != run.ID || e.Gate.Kind != n.Type || (n.Type != "clarify" && n.Type != "approve" && n.Type != "wait") {
			return State{}, fmt.Errorf("invalid wait gate")
		}
		if e.Gate.Kind == "approve" && (e.Gate.Prompt == "" || e.Gate.ProposalDigest != ProposalDigest(e.Gate.Prompt)) {
			return State{}, fmt.Errorf("approval requires exact proposal digest")
		}
		if e.Gate.Kind == "wait" && e.Gate.Deadline == nil && e.Gate.SourceRef == "" {
			return State{}, fmt.Errorf("wait needs deadline or authorized source")
		}
		s.Gate = clone(e.Gate)
		s.Gate.Status = "pending"
		s.Status = "blocked"
		s.Reason = e.Gate.Prompt
	case "signal":
		gate := s.Gate
		if s.Status != "blocked" || gate == nil || gate.Status != "pending" || gate.ID != e.GateID || gate.RunID != e.RunID || run == nil || run.ID != e.RunID {
			return State{}, fmt.Errorf("stale or duplicate gate signal")
		}
		if e.Port == "timeout" {
			if gate.Deadline == nil || e.At.Before(*gate.Deadline) {
				return State{}, fmt.Errorf("gate has not expired")
			}
		} else {
			if gate.Deadline != nil && !e.At.Before(*gate.Deadline) {
				return State{}, fmt.Errorf("gate expired")
			}
			if gate.Kind == "approve" && (!e.TrustedOwner || e.ProposalDigest != gate.ProposalDigest || (e.Port != "approved" && e.Port != "rejected")) {
				return State{}, fmt.Errorf("owner approval identity or proposal mismatch")
			}
			if gate.Kind == "clarify" && (!e.TrustedOwner || (e.Port != "answer" && e.Port != "cancel") || strings.TrimSpace(e.Text) == "") {
				return State{}, fmt.Errorf("clarification requires owner response")
			}
			if gate.Kind == "wait" && (e.Port != "resume" || gate.SourceRef == "" || gate.SourceRef != e.SourceRef) {
				return State{}, fmt.Errorf("unauthorized wait source")
			}
		}
		run.Output = e.Text
		run.OutputRecorded = true
		run.Fields = clone(e.Fields)
		if run.Fields == nil {
			run.Fields = map[string]any{"final_message": e.Text, "answer": e.Text}
		}
		if err = validateOutput(n, *run, validate); err != nil {
			return State{}, err
		}
		gate.Status = e.Port
		if err = advance(g, &s, n, run, e.Port); err != nil {
			return State{}, err
		}
	case "block", "pause":
		if (s.Status != "running" && s.Status != "queued") || strings.TrimSpace(e.Text) == "" {
			return State{}, fmt.Errorf("cannot pause block")
		}
		s.Status = "paused"
		if e.Type == "block" {
			s.Status = "blocked"
		}
		s.Reason = e.Text
	case "resume":
		if (s.Status != "paused" && s.Status != "blocked") || strings.TrimSpace(e.Text) == "" || (s.Gate != nil && s.Gate.Status == "pending") {
			return State{}, fmt.Errorf("cannot resume pending gate or nonpaused block")
		}
		if n.Type == "repeat" {
			counter := s.Loops[n.ID]
			if counter.Used >= counter.Budget {
				return State{}, fmt.Errorf("explicit additional loop budget required")
			}
		}
		if n.Type == "end" && run != nil && run.Status == "completed" && s.Status == "blocked" {
			s.Clarifications = append(s.Clarifications, e.Text)
			s.Status = "completed"
			s.Reason = ""
			break
		}
		s.Clarifications = append(s.Clarifications, e.Text)
		s.Reason = ""
		if run != nil && run.NodeID == n.ID && run.Status == "running" {
			if s.Status == "paused" {
				s.Status = "running"
			} else {
				run.Status = "blocked"
				s.Status = "queued"
			}
		} else {
			s.Status = "queued"
		}
	case "budget":
		if !e.TrustedOwner || s.Status != "blocked" || n.Type != "repeat" || e.Budget < 1 || e.Budget > 100 {
			return State{}, fmt.Errorf("invalid loop budget grant")
		}
		counter := s.Loops[n.ID]
		if counter.Budget+e.Budget > 10000 {
			return State{}, fmt.Errorf("loop budget hard limit exceeded")
		}
		counter.Budget += e.Budget
		s.Loops[n.ID] = counter
		if run != nil && run.NodeID == n.ID && run.Status == "running" {
			s.Status = "running"
		} else {
			s.Status = "queued"
		}
		s.Reason = ""
	case "cancel":
		s.Status = "cancelled"
		s.Reason = e.Text
		if run != nil && run.Status == "running" {
			run.Status = "cancelled"
		}
		if s.Gate != nil && s.Gate.Status == "pending" {
			s.Gate.Status = "cancelled"
		}
	default:
		return State{}, fmt.Errorf("unknown graph event %q", e.Type)
	}
	s.Revision++
	return s, nil
}
func validateOutput(n Node, run Run, validate SchemaValidator) error {
	if len(n.OutputSchema) == 0 {
		return nil
	}
	if validate == nil {
		return fmt.Errorf("output schema validator unavailable")
	}
	raw, err := json.Marshal(run.Fields)
	if err != nil {
		return err
	}
	return validate(n.OutputSchema, raw)
}
func applyVariables(g Graph, s *State, values map[string]any) error {
	for name, value := range values {
		found := false
		for _, definition := range g.Variables {
			if definition.Name == name {
				if !matchesType(value, definition.Type) {
					return fmt.Errorf("variable %s type mismatch", name)
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("undeclared variable %s", name)
		}
		s.Variables[name] = clone(value)
	}
	return nil
}
func advance(g Graph, s *State, n Node, run *Run, port string) error {
	if n.Type == "end" {
		s.Status = "completed"
		if n.ExitActions != nil {
			if n.ExitActions.SetState && n.ExitActions.State != "" {
				s.Status = n.ExitActions.State
			}
			if n.ExitActions.Intervention {
				s.Status = "blocked"
			}
		}
		if s.Status == "blocked" {
			s.Reason = text(n.Params["reason"])
			if s.Reason == "" {
				s.Reason = "owner intervention requested"
			}
		}
		s.Result = clone(run.Fields)
		run.Status = "completed"
		return nil
	}
	if !hasPort(n, port) {
		return fmt.Errorf("unknown output port %s", port)
	}
	target := ""
	for _, edge := range g.Edges {
		if edge.From == n.ID && edge.Port == port {
			target = edge.To
			break
		}
	}
	if target == "" {
		return fmt.Errorf("unconnected output %s/%s", n.ID, port)
	}
	run.Port = port
	run.Status = "completed"
	s.Cursor = target
	s.Status = "queued"
	s.Reason = ""
	return nil
}
