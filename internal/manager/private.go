package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/privateconfig"
)

func (s *Server) privateAPI(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Private == nil {
		writeError(w, 503, errors.New("private configuration store not configured"))
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/private/activation":
		writeJSON(w, 200, s.clientExtensionActivations())
	case r.Method == "GET" && r.URL.Path == "/api/private":
		writeJSON(w, 200, s.cfg.Private.List(r.URL.Query().Get("kind"), r.URL.Query().Get("archived") == "true"))
	case r.Method == "GET" && r.URL.Path == "/api/approvals":
		writeJSON(w, 200, s.cfg.Private.Pending())
	case r.Method == "POST" && r.URL.Path == "/api/private":
		var change privateconfig.Change
		if decode(w, r, &change, 2<<20) != nil {
			return
		}
		entity, err := s.cfg.Private.ApplyOwner(change)
		if err != nil {
			writeError(w, 409, err)
			return
		}
		writeJSON(w, 200, entity)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/approvals/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/approvals/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		switch parts[1] {
		case "approve":
			entity, err := s.cfg.Private.Approve(parts[0])
			if err != nil {
				writeError(w, 409, err)
				return
			}
			writeJSON(w, 200, entity)
		case "reject":
			if err := s.cfg.Private.Reject(parts[0]); err != nil {
				writeError(w, 409, err)
				return
			}
			writeJSON(w, 200, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) privateWorkflow(id string) (managerstate.Workflow, error) {
	var workflow managerstate.Workflow
	if s.cfg.Private == nil {
		return workflow, errors.New("private configuration store unavailable")
	}
	entity, err := s.cfg.Private.Get(id)
	if err != nil {
		return workflow, err
	}
	if entity.Kind != "workflow" || entity.ArchivedAt != nil {
		return workflow, errors.New("private workflow unavailable")
	}
	if err = json.Unmarshal(entity.Payload, &workflow); err != nil {
		return workflow, err
	}
	workflow.ID = entity.ID
	workflow.TeamDefinitions = map[string]json.RawMessage{}
	workflow.ToolDefinitions = map[string]json.RawMessage{}
	for _, phase := range workflow.Phases {
		if strings.HasPrefix(phase.TeamRef, "private:") {
			team, err := s.cfg.Private.Get(phase.TeamRef)
			if err != nil {
				return workflow, err
			}
			if team.Kind != "team" || team.ArchivedAt != nil {
				return workflow, errors.New("referenced private team unavailable")
			}
			workflow.TeamDefinitions[phase.TeamRef] = append(json.RawMessage(nil), team.Payload...)
		} else if strings.Contains(phase.TeamRef, "/") {
			if s.cfg.Packages == nil {
				return workflow, errors.New("referenced package team catalog unavailable")
			}
			packageID := strings.SplitN(phase.TeamRef, "/", 2)[0]
			for _, revision := range s.cfg.Packages.List() {
				if revision.ID != packageID {
					continue
				}
				if _, err := s.cfg.Packages.Resolve(revision); err != nil {
					return workflow, err
				}
				snapshot, err := s.snapshotPackageTools(revision)
				if err != nil {
					return workflow, err
				}
				for id, data := range snapshot {
					workflow.ToolDefinitions[id] = data
				}
				for _, path := range revision.Manifest.Teams {
					data, err := s.cfg.Packages.ReadFile(revision, path)
					if err != nil {
						return workflow, err
					}
					var identity struct {
						ID string `json:"id"`
					}
					if err = json.Unmarshal(data, &identity); err != nil {
						return workflow, err
					}
					if identity.ID == phase.TeamRef {
						workflow.TeamDefinitions[phase.TeamRef] = data
					}
				}
			}
			if _, exists := workflow.TeamDefinitions[phase.TeamRef]; !exists {
				return workflow, errors.New("referenced package team unavailable")
			}
		}
	}
	pinned, _ := json.Marshal(struct {
		Teams map[string]json.RawMessage
		Tools map[string]json.RawMessage
	}{workflow.TeamDefinitions, workflow.ToolDefinitions})
	digest := sha256.Sum256(pinned)
	workflow.Version += "+private." + strconv.FormatUint(entity.Revision, 10) + "." + hex.EncodeToString(digest[:8])
	return workflow, nil
}
