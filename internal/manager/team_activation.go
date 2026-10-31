package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

func (s *Server) materialize(ctx context.Context, id, revision string, team privateconfig.Team) (TeamMaterialization, error) {
	return s.materializeOwner(ctx, "manager", id, revision, team)
}
func (s *Server) materializeOwner(ctx context.Context, owner, id, revision string, team privateconfig.Team, pinned ...map[string]json.RawMessage) (TeamMaterialization, error) {
	var result TeamMaterialization
	remote, ok := s.cfg.Remote.(TeamMaterializer)
	if !ok {
		return result, errors.New("remote Lid materialization unavailable")
	}
	if err := s.refreshPackageTools(ctx); err != nil {
		return result, err
	}
	if owner == "manager" {
		if err := s.refreshExtensions(ctx); err != nil {
			return result, err
		}
	}
	toolRefs := map[string]string{}
	for registered, binding := range s.cfg.ToolBindings {
		if (owner == "manager" && binding.ClientID == "") || binding.ClientID == owner {
			toolRefs[binding.Name] = registered
			toolRefs[registered] = registered
		}
	}
	s.mu.Lock()
	if owner == "manager" {
		for id, name := range s.extensionToolRefs {
			toolRefs[id] = name
		}
	}
	for id, name := range s.packageToolRefs[owner] {
		toolRefs[id] = name
	}
	s.mu.Unlock()
	s.capabilityMu.Lock()
	defer s.capabilityMu.Unlock()
	if len(pinned) > 0 {
		for id, data := range pinned[0] {
			var definition machineclient.ToolDefinition
			if err := json.Unmarshal(data, &definition); err != nil {
				return result, err
			}
			registrar, ok := s.cfg.Remote.(extensionRemote)
			if !ok {
				return result, errors.New("pinned tool registration unavailable")
			}
			name, err := s.registerPackageDefinition(ctx, registrar, owner, definition)
			if err != nil {
				return result, err
			}
			toolRefs[id] = name
		}
	}
	used := map[string]string{}
	for _, agent := range team.Agents {
		for _, id := range agent.Tools {
			if name, ok := toolRefs[id]; ok {
				used[id] = name
			} else {
				return result, errors.New("Lid tool has no registered capability: " + id)
			}
		}
	}
	toolRefs = used
	body, _ := json.Marshal(struct {
		Owner    string             `json:"owner"`
		ID       string             `json:"id"`
		Revision string             `json:"revision"`
		Team     privateconfig.Team `json:"team"`
		Tools    map[string]string  `json:"tools"`
	}{owner, id, revision, team, toolRefs})
	hash := sha256.Sum256(body)
	key := "team:" + hex.EncodeToString(hash[:24])
	record, created, err := s.cfg.Store.BeginTool(key, "manager", id, "team.materialize", body, "envai")
	if err != nil {
		return result, err
	}
	if !created {
		if record.Status != managerstate.Completed {
			return result, errors.New("Lid materialization outcome uncertain; inspect remote resources before retry")
		}
		err = json.Unmarshal([]byte(record.Output), &result)
		return result, err
	}
	namespaceHash := sha256.Sum256([]byte(s.cfg.WorkspaceID + "/" + s.cfg.CEOTeamID + "/" + owner + "/" + id))
	result, err = MaterializeTeam(ctx, remote, "smidja_"+hex.EncodeToString(namespaceHash[:16]), revision, team, toolRefs)
	if err != nil {
		return result, err
	}
	encoded, _ := json.Marshal(result)
	err = s.cfg.Store.FinishTool(key, string(encoded), false)
	return result, err
}
func (s *Server) ensureTeam(ctx context.Context, ref string) (string, error) {
	if strings.HasPrefix(ref, "client:") {
		owner, id, err := parseClientReference(ref)
		if err != nil {
			return "", err
		}
		client, err := s.onlineClient(owner)
		if err != nil {
			return "", err
		}
		for _, definition := range client.Inventory.TeamDefinitions {
			if definition.ID == id {
				var team privateconfig.Team
				if err = json.Unmarshal(definition.Definition, &team); err != nil {
					return "", err
				}
				result, err := s.materializeOwner(ctx, owner, ref, definition.Package.Version+"+"+definition.Package.Digest, team)
				return result.TeamID, err
			}
		}
		return "", errors.New("client Lid unavailable")
	}
	if strings.HasPrefix(ref, "private:") {
		if s.cfg.Private == nil {
			return "", errors.New("private Lid catalog unavailable")
		}
		entity, err := s.cfg.Private.Get(ref)
		if err != nil {
			return "", err
		}
		if entity.Kind != "team" || entity.ArchivedAt != nil {
			return "", errors.New("private Lid unavailable")
		}
		var team privateconfig.Team
		if err = json.Unmarshal(entity.Payload, &team); err != nil {
			return "", err
		}
		result, err := s.materialize(ctx, ref, strconv.FormatUint(entity.Revision, 10), team)
		return result.TeamID, err
	}
	if strings.Contains(ref, "/") {
		if s.cfg.Packages == nil {
			return "", errors.New("package Lid catalog unavailable")
		}
		for _, revision := range s.cfg.Packages.List() {
			if _, err := s.cfg.Packages.Resolve(revision); err != nil {
				continue
			}
			for _, path := range revision.Manifest.Teams {
				data, err := s.cfg.Packages.ReadFile(revision, path)
				if err != nil {
					return "", err
				}
				var payload struct {
					ID string `json:"id"`
					privateconfig.Team
				}
				if err = json.Unmarshal(data, &payload); err != nil {
					return "", err
				}
				if payload.ID == ref {
					result, err := s.materialize(ctx, ref, revision.Version+"+"+revision.Digest, payload.Team)
					return result.TeamID, err
				}
			}
		}
		return "", fmt.Errorf("package Lid %s unavailable", ref)
	}
	return ref, nil
}

func (s *Server) ensureWorkflowTeam(ctx context.Context, workflow managerstate.Workflow, ref string) (string, error) {
	if data, ok := workflow.TeamDefinitions[ref]; ok {
		var team privateconfig.Team
		if err := json.Unmarshal(data, &team); err != nil {
			return "", err
		}
		owner := "manager"
		if workflow.ClientID != "" {
			owner = workflow.ClientID
			if _, err := s.onlineClient(owner); err != nil {
				return "", err
			}
		}
		result, err := s.materializeOwner(ctx, owner, ref, workflow.Version, team, workflow.ToolDefinitions)
		return result.TeamID, err
	}
	return s.ensureTeam(ctx, ref)
}
