package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/digitalygo/smidja/internal/clientpackages"
	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

type registeredCapability struct {
	Binding    ToolBinding                  `json:"binding"`
	Definition machineclient.ToolDefinition `json:"definition"`
}

func clientReference(clientID, id string) string {
	return "client:" + url.QueryEscape(clientID) + ":" + id
}
func parseClientReference(ref string) (string, string, error) {
	parts := strings.SplitN(strings.TrimPrefix(ref, "client:"), ":", 2)
	if !strings.HasPrefix(ref, "client:") || len(parts) != 2 {
		return "", "", errors.New("invalid client capability reference")
	}
	id, err := url.QueryUnescape(parts[0])
	if err != nil || id == "" || parts[1] == "" {
		return "", "", errors.New("invalid client capability reference")
	}
	return id, parts[1], nil
}
func (s *Server) onlineClient(id string) (machineclient.ClientStatus, error) {
	for _, client := range s.cfg.Clients.List() {
		if client.ID == id {
			if !client.Online {
				return client, errors.New("client " + client.Name + " is offline")
			}
			return client, nil
		}
	}
	return machineclient.ClientStatus{}, errors.New("client not found")
}
func (s *Server) refreshPackageTools(ctx context.Context) error {
	remote, ok := s.cfg.Remote.(extensionRemote)
	if !ok {
		return nil
	}
	s.capabilityMu.Lock()
	defer s.capabilityMu.Unlock()
	inventories := map[string]machineclient.Inventory{}
	if s.cfg.PackageRuntime != nil {
		inventory, err := s.cfg.PackageRuntime.Inventory(ctx)
		if err != nil {
			return err
		}
		inventories["manager"] = inventory
	}
	for _, client := range s.cfg.Clients.List() {
		if client.Online {
			inventory := client.Inventory
			for _, base := range machinetools.Definitions() {
				for _, name := range inventory.Tools {
					if name == base.Name {
						inventory.ToolDefinitions = append(inventory.ToolDefinitions, machineclient.ToolDefinition{ID: base.Name, Route: base.Name, Description: base.Description, Parameters: base.Parameters})
						break
					}
				}
			}
			inventories[client.ID] = inventory
		}
	}
	refs := map[string]map[string]string{}
	for owner, inventory := range inventories {
		refs[owner] = map[string]string{}
		for _, definition := range inventory.ToolDefinitions {
			if definition.PrivateRevision != 0 && !s.approvedClientDefinition(owner, definition) {
				continue
			}
			name, err := s.registerPackageDefinition(ctx, remote, owner, definition)
			if err != nil {
				return err
			}
			refs[owner][definition.ID] = name
		}
	}
	s.mu.Lock()
	s.packageToolRefs = refs
	s.mu.Unlock()
	return nil
}
func (s *Server) registeredPackageBinding(name string) (ToolBinding, bool) {
	record, err := s.cfg.Store.GetTool("register:" + name)
	if err != nil || record.Status != managerstate.Completed || record.ToolName != "package.register" {
		return ToolBinding{}, false
	}
	var capability registeredCapability
	if json.Unmarshal(record.Args, &capability) != nil {
		return ToolBinding{}, false
	}
	return capability.Binding, true
}
func (s *Server) clientWorkflow(ref string) (managerstate.Workflow, error) {
	var workflow managerstate.Workflow
	clientID, id, err := parseClientReference(ref)
	if err != nil {
		return workflow, err
	}
	client, err := s.onlineClient(clientID)
	if err != nil {
		return workflow, err
	}
	for _, definition := range client.Inventory.WorkflowDefinitions {
		if definition.ID == id {
			if err = json.Unmarshal(definition.Definition, &workflow); err != nil {
				return workflow, err
			}
			workflow.ID = ref
			workflow.ClientID = clientID
			workflow.Version = definition.Package.Version + "+" + definition.Package.Digest
			return workflow, nil
		}
	}
	return workflow, errors.New("client workflow unavailable")
}

func (s *Server) registerPackageDefinition(ctx context.Context, remote extensionRemote, owner string, definition machineclient.ToolDefinition) (string, error) {
	identity, _ := json.Marshal(struct {
		CEO        string
		Owner      string
		Definition machineclient.ToolDefinition
	}{s.cfg.CEOTeamID, owner, definition})
	digest := sha256.Sum256(identity)
	name := "smidja_pkg_" + hex.EncodeToString(digest[:16])
	key := "register:" + name
	binding := ToolBinding{Name: definition.Route, ClientID: owner}
	payload, _ := json.Marshal(registeredCapability{binding, definition})
	record, created, err := s.cfg.Store.BeginTool(key, "manager", owner, "package.register", payload, owner)
	if err != nil {
		return "", err
	}
	if !created && record.Status != managerstate.Completed {
		return "", errors.New("package tool registration outcome uncertain; inspect remote resources")
	}
	if created {
		registered, err := remote.RegisterTool(ctx, envai.ToolDefinition{Name: name, Kind: "client", Description: definition.Description, Parameters: definition.Parameters, TimeoutSeconds: 600})
		if err != nil {
			return "", err
		}
		if registered.ID == "" || registered.Name != name {
			return "", errors.New("registered package tool identity mismatch")
		}
		output, _ := json.Marshal(registered)
		if err = s.cfg.Store.FinishTool(key, string(output), false); err != nil {
			return "", err
		}
	}
	return name, nil
}

func (s *Server) snapshotPackageTools(revision managerpackages.Revision) (map[string]json.RawMessage, error) {
	result := map[string]json.RawMessage{}
	dependencies, err := s.cfg.Packages.Resolve(revision)
	if err != nil {
		return nil, err
	}
	for _, dependency := range dependencies {
		for _, path := range dependency.Manifest.Tools {
			data, err := s.cfg.Packages.ReadFile(dependency, path)
			if err != nil {
				return nil, err
			}
			var document struct {
				ID string `json:"id"`
				privateconfig.Extension
			}
			if err = json.Unmarshal(data, &document); err != nil {
				return nil, err
			}
			definition := machineclient.ToolDefinition{ID: document.ID, Route: clientpackages.Route(dependency, document.ID), Description: document.Description, Parameters: document.Parameters, Package: machineclient.PackageRevision{ID: dependency.ID, Version: dependency.Version, Digest: dependency.Digest}}
			encoded, err := json.Marshal(definition)
			if err != nil {
				return nil, err
			}
			if previous, ok := result[document.ID]; ok && !bytes.Equal(previous, encoded) {
				return nil, errors.New("conflicting pinned package tool revisions")
			}
			result[document.ID] = encoded
		}
	}
	return result, nil
}
