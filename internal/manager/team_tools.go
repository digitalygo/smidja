package manager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

func (s *Server) teamCatalog(ctx context.Context) ([]map[string]any, error) {
	result := []map[string]any{}
	if s.cfg.Remote != nil {
		teams, err := s.cfg.Remote.ListTeams(ctx)
		if err != nil {
			return nil, err
		}
		for _, team := range teams {
			if team.ID == s.cfg.CEOTeamID || strings.HasPrefix(team.Name, "smidja_") {
				continue
			}
			result = append(result, map[string]any{"id": team.ID, "name": team.Name, "origin": "envai", "description": team.Description, "members": team.Members, "orchestrator_id": team.OrchestratorID})
		}
	}
	if s.cfg.Private != nil {
		for _, entity := range s.cfg.Private.List("team", false) {
			var team privateconfig.Team
			if err := json.Unmarshal(entity.Payload, &team); err != nil {
				return nil, err
			}
			result = append(result, map[string]any{"id": entity.ID, "name": team.Name, "origin": "private", "revision": entity.Revision, "description": team.Description, "agents": catalogAgents(team.Agents)})
		}
	}
	if s.cfg.Packages != nil {
		for _, revision := range s.cfg.Packages.List() {
			if _, err := s.cfg.Packages.Resolve(revision); err != nil {
				continue
			}
			for _, path := range revision.Manifest.Teams {
				body, err := s.cfg.Packages.ReadFile(revision, path)
				if err != nil {
					return nil, err
				}
				var identity struct {
					ID          string                `json:"id"`
					Name        string                `json:"name"`
					Description string                `json:"description"`
					Agents      []privateconfig.Agent `json:"agents"`
				}
				if err = json.Unmarshal(body, &identity); err != nil {
					return nil, err
				}
				result = append(result, map[string]any{"id": identity.ID, "name": identity.Name, "origin": "package", "version": revision.Version, "description": identity.Description, "agents": catalogAgents(identity.Agents)})
			}
		}
	}
	for _, client := range s.cfg.Clients.List() {
		if !client.Online {
			continue
		}
		for _, definition := range client.Inventory.TeamDefinitions {
			var team privateconfig.Team
			if err := json.Unmarshal(definition.Definition, &team); err != nil {
				return nil, err
			}
			result = append(result, map[string]any{"id": clientReference(client.ID, definition.ID), "name": client.Name + " / " + team.Name, "origin": "client", "client_id": client.ID, "version": definition.Package.Version, "description": team.Description, "agents": catalogAgents(team.Agents)})
		}
	}
	return result, nil
}
func (s *Server) executeTeamTool(ctx context.Context, event envai.Event, name string, args json.RawMessage) machinetools.Result {
	if name == "teams_list" {
		catalog, err := s.teamCatalog(ctx)
		return toolResult(catalog, err)
	}
	remote, ok := s.cfg.Remote.(NativeRemote)
	if !ok {
		return toolResult(nil, errors.New("native Lid delegation unavailable"))
	}
	var input struct {
		TeamRef string `json:"team_ref"`
		Prompt  string `json:"prompt"`
		TaskID  string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return toolResult(nil, err)
	}
	switch name {
	case "teams_task":
		task, err := remote.Task(ctx, event.ChatID, input.TaskID)
		return toolResult(task, err)
	case "teams_cancel":
		result, err := remote.CancelTask(ctx, event.ChatID, input.TaskID, true)
		return toolResult(result, err)
	case "teams_delegate":
		request, err := event.ToolRequest()
		if err != nil {
			return toolResult(nil, err)
		}
		return s.delegateTeam(ctx, event.ChatID, event.TurnID, request.CallID, input.TeamRef, input.Prompt)

	default:
		return toolResult(nil, errors.New("unknown Lid operation"))
	}
}

func (s *Server) delegateTeam(ctx context.Context, chatID, turnID, eventID, teamRef, prompt string) machinetools.Result {
	remote, ok := s.cfg.Remote.(NativeRemote)
	if !ok {
		return toolResult(nil, errors.New("native Lid delegation unavailable"))
	}
	if teamRef == "" || prompt == "" {
		return toolResult(nil, errors.New("team_ref and prompt are required"))
	}
	if err := remote.RequireFeatures(ctx, "cross_team_delegation", "detached_background", "delegated_job_lifecycle", "strict_model"); err != nil {
		return toolResult(nil, err)
	}
	teamID, err := s.ensureTeam(ctx, teamRef)
	if err != nil {
		return toolResult(nil, err)
	}
	target, err := remote.CreateChat(ctx, s.cfg.WorkspaceID, teamID)
	if err != nil {
		return toolResult(nil, err)
	}
	session, err := s.cfg.Store.CreateSession("Lid " + teamRef)
	if err != nil {
		return toolResult(nil, err)
	}
	if _, err = s.cfg.Store.AttachSessionIdentity(session.ID, target.ID, teamRef, teamID); err != nil {
		return toolResult(nil, err)
	}
	s.observeSession(session.ID)
	strictModel := true
	result, err := remote.Delegate(ctx, chatID, envai.DelegationInput{StrictModel: &strictModel, WorkspaceID: s.cfg.WorkspaceID, EventID: eventID, OriginTurnID: turnID, TargetChatID: target.ID, Prompt: prompt})
	return toolResult(result, err)
}

func catalogAgents(agents []privateconfig.Agent) []map[string]any {
	result := make([]map[string]any, 0, len(agents))
	for _, agent := range agents {
		result = append(result, map[string]any{"name": agent.Name, "model": agent.Model, "tools": agent.Tools})
	}
	return result
}
