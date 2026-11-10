package manager

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/digitalygo/smidja/internal/managerstate"
)

func (s *Server) resolveGraphSnapshot(input managerstate.Workflow, stack map[string]bool, depth int) (managerstate.Workflow, error) {
	remaining := 512
	return s.resolveGraphSnapshotBounded(input, stack, depth, &remaining)
}
func (s *Server) resolveGraphSnapshotBounded(input managerstate.Workflow, stack map[string]bool, depth int, remaining *int) (managerstate.Workflow, error) {
	if *remaining <= 0 {
		return input, errors.New("nested Verk snapshot limit exceeded")
	}
	*remaining--
	if depth > 8 || stack[input.ID] {
		return input, errors.New("nested Verk cycle or maximum depth exceeded")
	}
	workflow, err := s.resolveWorkflowDefinition(input)
	if err != nil {
		return workflow, err
	}
	if workflow.Graph == nil {
		return workflow, nil
	}
	if err := s.pinGraphTools(&workflow); err != nil {
		return workflow, err
	}
	stack[input.ID] = true
	defer delete(stack, input.ID)
	workflow.WorkflowDefinitions = map[string]json.RawMessage{}
	for _, node := range workflow.Graph.Nodes {
		if node.Type != "verk" {
			continue
		}
		ref, version := graphParam(node, "ref"), graphParam(node, "version")
		child, e := s.resolveGraphSnapshotBounded(managerstate.Workflow{ID: ref, Version: version}, stack, depth+1, remaining)
		if e != nil {
			return workflow, e
		}
		if version != "" && child.Version != version && !strings.HasPrefix(child.Version, version+"+") {
			return workflow, errors.New("requested nested Verk version unavailable")
		}
		raw, e := json.Marshal(child)
		if e != nil {
			return workflow, e
		}
		workflow.WorkflowDefinitions[node.ID] = raw
	}
	return workflow, nil
}
