package manager

import (
	"encoding/json"
 "bytes"
	"errors"
	"net/http"
	"strings"

	"github.com/digitalygo/smidja/internal/managerpackages"
	"github.com/digitalygo/smidja/internal/managerstate"
)

func (s *Server) packageAPI(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Packages == nil {
		writeError(w, 503, errors.New("package store is not configured"))
		return
	}
	if r.Method == "GET" && r.URL.Path == "/api/packages/update-status" {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, s.packageUpdateErrors)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/api/packages" {
		writeJSON(w, 200, s.cfg.Packages.List())
		return
	}
	if r.Method == "POST" && r.URL.Path == "/api/packages/install" {
		var source managerpackages.Source
		if decode(w, r, &source, 8192) != nil {
			return
		}
		revision, err := s.cfg.Packages.Install(r.Context(), source)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		writeJSON(w, 201, revision)
		return
	}
	if r.Method == "POST" && r.URL.Path == "/api/packages/update" {
		var input struct {
			ID string `json:"id"`
		}
		if decode(w, r, &input, 8192) != nil {
			return
		}
		revision, err := s.cfg.Packages.Update(r.Context(), input.ID)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		writeJSON(w, 200, revision)
		return
	}
	http.NotFound(w, r)
}
func (s *Server) packageWorkflows() ([]managerstate.Workflow, error) {
	result := []managerstate.Workflow{}
	if s.cfg.Packages == nil {
		return result, nil
	}
	for _, revision := range s.cfg.Packages.List() {
		if _, err := s.cfg.Packages.Resolve(revision); err != nil {
			continue
		}
		for _, path := range revision.Manifest.Workflows {
			data, err := s.cfg.Packages.ReadFile(revision, path)
			if err != nil {
				return nil, err
			}
			var workflow managerstate.Workflow
			if err = json.Unmarshal(data, &workflow); err != nil {
				return nil, err
			}
			workflow.Version = revision.Version + "+" + revision.Digest
			workflow.TeamDefinitions = map[string]json.RawMessage{}
			workflow.ToolDefinitions, err = s.snapshotPackageTools(revision)
			if err != nil {
				return nil, err
			}
			dependencies, err := s.cfg.Packages.Resolve(revision)
			if err != nil {
				return nil, err
			}
			for _, dependency := range dependencies {
				for _, teamPath := range dependency.Manifest.Teams {
					teamData, err := s.cfg.Packages.ReadFile(dependency, teamPath)
					if err != nil {
						return nil, err
					}
					var identity struct {
						ID string `json:"id"`
					}
					if err = json.Unmarshal(teamData, &identity); err != nil {
						return nil, err
					}
					if previous,ok:=workflow.TeamDefinitions[identity.ID];ok && !bytes.Equal(previous,teamData){return nil,errors.New("conflicting pinned package team definitions")};workflow.TeamDefinitions[identity.ID] = teamData
				}
			}
			result = append(result, workflow)
		}
	}
	return result, nil
}
func (s *Server) resolveWorkflow(input managerstate.Workflow) (managerstate.Workflow, error) {
	if strings.HasPrefix(input.ID, "client:") {
		return s.clientWorkflow(input.ID)
	}
	if strings.HasPrefix(input.ID, "private:") {
		return s.privateWorkflow(input.ID)
	}
	if !strings.Contains(input.ID, "/") {
		input.ClientID = ""
		if len(input.TeamDefinitions) > 0 || len(input.ToolDefinitions) > 0 {
			return input, errors.New("inline team definitions require private configuration approval")
		}
		return input, nil
	}
	workflows, err := s.packageWorkflows()
	if err != nil {
		return input, err
	}
	for _, workflow := range workflows {
		if workflow.ID == input.ID {
			return workflow, nil
		}
	}
	return input, errors.New("package workflow is unavailable or has unresolved dependencies")
}
