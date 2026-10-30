package manager

import (
	"context"
	"errors"

	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/managerstate"
)

func (s *Server) availableWorkflows(ctx context.Context) ([]managerstate.Workflow, error) {
	workflows, err := s.packageWorkflows()
	if err != nil {
		return nil, err
	}
	if s.cfg.Private != nil {
		for _, entity := range s.cfg.Private.List("workflow", false) {
			workflow, err := s.privateWorkflow(entity.ID)
			if err == nil {
				workflows = append(workflows, workflow)
			}
		}
	}
	for _, client := range s.cfg.Clients.List() {
		if client.Online {
			for _, definition := range client.Inventory.WorkflowDefinitions {
				workflow, err := s.clientWorkflow(clientReference(client.ID, definition.ID))
				if err == nil {
					workflows = append(workflows, workflow)
				}
			}
		}
	}
	return workflows, nil
}
func (s *Server) availableSkills(ctx context.Context) ([]machineclient.SkillDefinition, error) {
	skills := []machineclient.SkillDefinition{}
	if s.cfg.PackageRuntime != nil {
		inventory, err := s.cfg.PackageRuntime.Inventory(ctx)
		if err != nil {
			return nil, err
		}
		skills = append(skills, inventory.SkillDefinitions...)
	}
	for _, client := range s.cfg.Clients.List() {
		if client.Online {
			for _, skill := range client.Inventory.SkillDefinitions {
				skill.ID = clientReference(client.ID, skill.ID)
				skills = append(skills, skill)
			}
		}
	}
	return skills, nil
}
func configurationSchema(kind string) (any, error) {
	switch kind {
	case "team":
		return map[string]any{"id": "private:team-name", "kind": "team", "payload": map[string]any{"name": "Team display name", "description": "Purpose", "agents": []map[string]any{{"name": "Manager", "instructions": "Coordinate specialists by role", "model": "explicit-model-id", "tools": []string{"read", "manager.tasks_start"}}, {"name": "Specialist", "instructions": "Specific task instructions", "model": "explicit-model-id", "tools": []string{"read"}}}}}, nil
	case "workflow":
		return map[string]any{"id": "private:workflow-name", "kind": "workflow", "payload": map[string]any{"id": "private:workflow-name", "version": "1", "phases": []map[string]string{{"id": "review", "name": "Review", "team_ref": "private:team-name"}}}}, nil
	case "extension":
		return map[string]any{"id": "private:tool-name", "kind": "extension", "payload": map[string]any{"name": "tool_name", "description": "What the tool does", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}, "command": []string{"/absolute/path/to/helper"}, "environment": map[string]string{"ODOO_API_KEY": "ODOO_API_KEY"}, "client_id": "manager"}, "protocol": "Literal command argv; JSON arguments on stdin; stdout result, stderr diagnostics. Environment values are names of variables, not secrets."}, nil
	default:
		return nil, errors.New("kind must be team, workflow or extension")
	}
}
