package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

type extensionRemote interface {
	RegisterTool(context.Context, envai.ToolDefinition) (envai.ToolDefinition, error)
	SetAgentClientTools(context.Context, string, []envai.ToolReference) (envai.AgentDefinition, error)
}

func (s *Server) refreshExtensions(ctx context.Context) error {
	if s.cfg.Private == nil {
		return nil
	}
	entities := s.cfg.Private.List("extension", false)
	remote, ok := s.cfg.Remote.(extensionRemote)
	if !ok {
		if len(entities) == 0 {
			return nil
		}
		return errors.New("extension registration unavailable")
	}
	s.capabilityMu.Lock()
	defer s.capabilityMu.Unlock()
	nextBindings := map[string]ToolBinding{}
	nextRefs := map[string]string{}
	refs := []envai.ToolReference{}
	for name := range s.cfg.ToolBindings {
		refs = append(refs, envai.ToolReference{Name: name})
	}
	for _, entity := range entities {
		var definition privateconfig.Extension
		if err := json.Unmarshal(entity.Payload, &definition); err != nil {
			return err
		}
		if definition.ClientID != "" && definition.ClientID != "manager" {
			s.mu.Lock()
			name := s.packageToolRefs[definition.ClientID][entity.ID]
			s.mu.Unlock()
			if name != "" {
				nextRefs[entity.ID] = name
				refs = append(refs, envai.ToolReference{Name: name})
			}
			continue
		}
		digest := sha256.Sum256([]byte(s.cfg.WorkspaceID + "/" + s.cfg.CEOTeamID + "/" + entity.ID + "/" + strconv.FormatUint(entity.Revision, 10)))
		name := "smidja_ext_" + hex.EncodeToString(digest[:16])
		key := "extension:" + hex.EncodeToString(digest[:16])
		record, created, err := s.cfg.Store.BeginTool(key, "manager", entity.ID, "extension.register", entity.Payload, "envai")
		if err != nil {
			return err
		}
		if !created && record.Status != managerstate.Completed {
			return errors.New("extension registration uncertain; inspect remote before retry")
		}
		if created {
			timeout, err := privateconfig.ExtensionTimeout(definition.TimeoutSeconds)
			if err != nil {
				return err
			}
			tool, err := remote.RegisterTool(ctx, envai.ToolDefinition{Name: name, Kind: "client", Description: definition.Description, Parameters: definition.Parameters, TimeoutSeconds: int(timeout.Seconds())})
			if err != nil {
				return err
			}
			if tool.Name != name || tool.ID == "" {
				return errors.New("registered extension identity mismatch")
			}
			encoded, _ := json.Marshal(tool)
			if err = s.cfg.Store.FinishTool(key, string(encoded), false); err != nil {
				return err
			}
		}
		nextBindings[name] = ToolBinding{Name: key, ClientID: "manager"}
		nextRefs[entity.ID] = name
		refs = append(refs, envai.ToolReference{Name: name})
	}
	teams, err := s.cfg.Remote.ListTeams(ctx)
	if err != nil {
		return err
	}
	for _, team := range teams {
		if team.ID == s.cfg.CEOTeamID {
			sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
			_, err := remote.SetAgentClientTools(ctx, team.OrchestratorID, refs)
			if err == nil {
				s.mu.Lock()
				s.dynamicBindings = nextBindings
				s.extensionToolRefs = nextRefs
				s.mu.Unlock()
			}
			return err
		}
	}
	return errors.New("configured CEO team was not found")
}
func (s *Server) executeExtension(ctx context.Context, key string, args json.RawMessage) machinetools.Result {
	if s.cfg.Extensions == nil {
		return toolResult(nil, errors.New("extension runner unavailable"))
	}
	record, err := s.cfg.Store.GetTool(key)
	if err != nil {
		return toolResult(nil, err)
	}
	if record.Status != managerstate.Completed || record.ToolName != "extension.register" {
		return toolResult(nil, errors.New("extension is not registered"))
	}
	var definition privateconfig.Extension
	if err = json.Unmarshal(record.Args, &definition); err != nil {
		return toolResult(nil, err)
	}
	if s.cfg.Private == nil {
		return toolResult(nil, errors.New("private extension catalog unavailable"))
	}
	current, err := s.cfg.Private.Get(record.TurnID)
	if err != nil {
		return toolResult(nil, err)
	}
	if current.ArchivedAt != nil {
		return toolResult(nil, errors.New("extension is archived"))
	}
	currentDigest := sha256.Sum256([]byte(s.cfg.WorkspaceID + "/" + s.cfg.CEOTeamID + "/" + current.ID + "/" + strconv.FormatUint(current.Revision, 10)))
	if key != "extension:"+hex.EncodeToString(currentDigest[:16]) {
		return toolResult(nil, errors.New("extension revision is superseded"))
	}
	var active privateconfig.Extension
	if current.Kind != "extension" || current.ArchivedAt != nil || json.Unmarshal(current.Payload, &active) != nil || !reflect.DeepEqual(active, definition) {
		return toolResult(nil, errors.New("extension version is archived or superseded"))
	}
	environment := map[string]string{}
	for name, reference := range definition.Environment {
		for _, protected := range s.cfg.ProtectedEnvironment {
			if reference == protected {
				return toolResult(nil, errors.New("extension cannot request manager authentication credentials"))
			}
		}
		if s.cfg.LookupEnv == nil {
			return toolResult(nil, errors.New("extension environment resolver unavailable"))
		}
		value := s.cfg.LookupEnv(reference)
		if value == "" {
			return toolResult(nil, errors.New("required extension environment variable is missing: "+reference))
		}
		environment[name] = value
	}
	return s.cfg.Extensions.WithEnvironment(environment).ExecuteDefinition(ctx, definition, args)
}

var _ extensionRemote = (*envai.Client)(nil)
