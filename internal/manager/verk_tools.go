package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
	"github.com/digitalygo/smidja/internal/verk"
)

type graphToolBinding struct {
	ClientID   string                       `json:"client_id"`
	Definition machineclient.ToolDefinition `json:"definition"`
}

func graphToolTarget(workflow managerstate.Workflow, node verk.Node) (string, string, error) {
	ref, client := graphParam(node, "ref"), graphParam(node, "client_id")
	if client == "" {
		client = workflow.ClientID
	}
	if client == "" {
		client = "manager"
	}
	if strings.HasPrefix(ref, "client:") {
		owner, id, err := parseClientReference(ref)
		if err != nil {
			return "", "", err
		}
		if graphParam(node, "client_id") != "" && client != owner {
			return "", "", errors.New("tool client reference conflicts with selected client")
		}
		client, ref = owner, id
	}
	return ref, client, nil
}
func (s *Server) pinGraphTools(workflow *managerstate.Workflow) error {
	workflow.PrivateToolRevisions = map[string]uint64{}
	if workflow.ToolDefinitions == nil {
		workflow.ToolDefinitions = map[string]json.RawMessage{}
	}
	for _, node := range workflow.Graph.Nodes {
		if node.Type != "tool" {
			continue
		}
		ref, client, err := graphToolTarget(*workflow, node)
		if err != nil {
			return err
		}
		if strings.HasPrefix(ref, "private:") {
			if s.cfg.Private == nil {
				return errors.New("private tool catalog unavailable")
			}
			entity, e := s.cfg.Private.Get(ref)
			if e != nil {
				return e
			}
			var definition privateconfig.Extension
			if entity.Kind != "extension" || entity.ArchivedAt != nil || json.Unmarshal(entity.Payload, &definition) != nil {
				return errors.New("private graph tool unavailable")
			}
			owner := definition.ClientID
			if owner == "" {
				owner = "manager"
			}
			if (graphParam(node, "client_id") != "" || strings.HasPrefix(graphParam(node, "ref"), "client:")) && client != owner {
				return errors.New("private tool belongs to a different client")
			}
			client = owner
			workflow.ToolDefinitions[ref] = append(json.RawMessage(nil), entity.Payload...)
			workflow.PrivateToolRevisions[ref] = entity.Revision
			if client == "manager" {
				continue
			}
		} else if !strings.Contains(ref, "/") {
			if client != "manager" && !graphBuiltinTool(ref) {
				return errors.New("unregistered client tool reference")
			}
			continue
		}
		var definition machineclient.ToolDefinition
		if client != "manager" {
			online, e := s.onlineClient(client)
			if e != nil {
				return e
			}
			for _, candidate := range online.Inventory.ToolDefinitions {
				if candidate.ID == ref {
					if strings.HasPrefix(ref, "private:") && !s.approvedClientDefinition(client, candidate) {
						continue
					}
					definition = candidate
					break
				}
			}
		} else {
			if raw := workflow.ToolDefinitions[ref]; len(raw) > 0 {
				if err = json.Unmarshal(raw, &definition); err != nil {
					return err
				}
			} else {
				if s.cfg.Packages == nil {
					return errors.New("package catalog unavailable")
				}
				for _, revision := range s.cfg.Packages.List() {
					if revision.ID != strings.SplitN(ref, "/", 2)[0] {
						continue
					}
					snapshot, e := s.snapshotPackageTools(revision)
					if e != nil {
						return e
					}
					if raw := snapshot[ref]; len(raw) > 0 {
						if err = json.Unmarshal(raw, &definition); err != nil {
							return err
						}
					}
				}
			}
		}
		if definition.ID != ref || definition.Route == "" {
			return errors.New("registered graph tool unavailable")
		}
		if strings.HasPrefix(ref, "private:") {
			if definition.PrivateRevision == 0 {
				return errors.New("private client tool revision missing")
			}
		} else if definition.Package.ID == "" || !strings.HasPrefix(definition.Route, "package:") {
			return errors.New("package tool revision missing")
		}
		raw, err := json.Marshal(graphToolBinding{ClientID: client, Definition: definition})
		if err != nil {
			return err
		}
		workflow.ToolDefinitions["graph:"+node.ID] = raw
	}
	return nil
}
func (s *Server) graphRegisteredTool(ctx context.Context, task managerstate.Task, node verk.Node, args json.RawMessage) (machinetools.Result, bool, error) {
	raw := task.Workflow.ToolDefinitions["graph:"+node.ID]
	if len(raw) == 0 {
		return machinetools.Result{}, false, nil
	}
	var binding graphToolBinding
	if json.Unmarshal(raw, &binding) != nil || binding.ClientID == "" || binding.Definition.Route == "" {
		return machinetools.Result{}, true, errors.New("invalid pinned graph tool binding")
	}
	ref, _, err := graphToolTarget(task.Workflow, node)
	if err != nil || binding.Definition.ID != ref {
		return machinetools.Result{}, true, errors.New("pinned graph tool identity mismatch")
	}
	if binding.ClientID != "manager" {
		return s.executeClientTool(ctx, binding.ClientID, binding.Definition.Route, args), true, nil
	}
	if !strings.HasPrefix(binding.Definition.Route, "package:") || s.cfg.PackageRuntime == nil {
		return machinetools.Result{}, true, errors.New("package tool runner unavailable")
	}
	if _, err = s.cfg.PackageRuntime.Inventory(ctx); err != nil {
		return machinetools.Result{}, true, err
	}
	return s.cfg.PackageRuntime.Execute(ctx, binding.Definition.Route, args), true, nil
}

func graphBuiltinTool(name string) bool {
	for _, definition := range machinetools.Definitions() {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) checkPrivateGraphTool(workflow managerstate.Workflow, ref string) error {
	revision := workflow.PrivateToolRevisions[ref]
	if revision == 0 {
		return errors.New("private graph tool revision is not pinned")
	}
	entity, err := s.cfg.Private.Get(ref)
	if err != nil {
		return err
	}
	if entity.Kind != "extension" || entity.ArchivedAt != nil || entity.Revision != revision || !bytes.Equal(entity.Payload, workflow.ToolDefinitions[ref]) {
		return errors.New("private graph tool approval changed; resolve a new workflow before execution")
	}
	return nil
}
