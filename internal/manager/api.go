package manager

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerstate"
)

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "POST" && r.URL.Path == "/api/workflows/validate":
		s.validateGraphAPI(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/pr-review"):
		s.prReviewAPI(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/private") || strings.HasPrefix(r.URL.Path, "/api/approvals"):
		s.privateAPI(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/packages"):
		s.packageAPI(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/workflows":
		workflows, err := s.availableWorkflows(r.Context())
		if err != nil {
			writeError(w, 500, err)
			return
		}
		writeJSON(w, 200, workflows)
	case r.Method == "GET" && r.URL.Path == "/api/status":
		s.mu.Lock()
		active := len(s.active)
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"configured": s.cfg.Remote != nil, "workspace_id": s.cfg.WorkspaceID, "ceo_team_id": s.cfg.CEOTeamID, "ceo_model": s.cfg.CEOModel, "classifier_model": s.cfg.ClassifierModel, "active": active, "architecture": "manager-client-v1"})
	case r.Method == "POST" && r.URL.Path == "/api/logout":
		if cookie, err := r.Cookie("smidja_manager"); err == nil {
			s.mu.Lock()
			delete(s.sessions, cookie.Value)
			s.mu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{Name: "smidja_manager", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
		writeJSON(w, 200, map[string]bool{"ok": true})
	case r.Method == "GET" && r.URL.Path == "/api/sessions":
		writeJSON(w, 200, s.cfg.Store.ListSessions())
	case r.Method == "POST" && r.URL.Path == "/api/sessions":
		var input struct {
			Title string `json:"title"`
		}
		if decode(w, r, &input, 8192) != nil {
			return
		}
		session, err := s.cfg.Store.CreateSession(input.Title)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		writeJSON(w, 201, session)
	case strings.HasPrefix(r.URL.Path, "/api/sessions/"):
		s.sessionAPI(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/tasks":
		writeJSON(w, 200, s.cfg.Store.ListTasks())
	case r.Method == "POST" && r.URL.Path == "/api/tasks":
		s.startTaskAPI(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/tasks/"):
		s.taskAPI(w, r)
	case (r.Method == "POST" && r.URL.Path == "/api/devices") || strings.HasPrefix(r.URL.Path, "/api/devices/"):
		s.deviceAPI(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/devices":
		writeJSON(w, 200, s.cfg.Clients.List())
	case r.Method == "GET" && r.URL.Path == "/api/tools":
		writeJSON(w, 200, machinetools.Definitions())
	case r.Method == "POST" && r.URL.Path == "/api/tools/execute":
		var input struct {
			ClientID string          `json:"client_id"`
			Name     string          `json:"name"`
			Args     json.RawMessage `json:"args"`
		}
		if decode(w, r, &input, 2<<20) != nil {
			return
		}
		if input.ClientID == "" || input.ClientID == "manager" {
			writeJSON(w, 200, s.cfg.Tools.Execute(r.Context(), input.Name, input.Args))
		} else {
			writeJSON(w, 200, s.executeClientTool(r.Context(), input.ClientID, input.Name, input.Args))
		}
	case r.Method == "POST" && r.URL.Path == "/api/team-delegations":
		s.delegateTeamAPI(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/teams":
		if s.cfg.Remote == nil {
			writeError(w, 503, errors.New("Envai is not configured"))
			return
		}
		result, err := s.teamCatalog(r.Context())
		if err != nil {
			writeError(w, 502, err)
			return
		}
		writeJSON(w, 200, result)
	case r.Method == "GET" && r.URL.Path == "/api/capabilities":
		if s.cfg.Remote == nil {
			writeError(w, 503, errors.New("Envai is not configured"))
			return
		}
		result, err := s.cfg.Remote.Capabilities(r.Context())
		if err != nil {
			writeError(w, 502, err)
			return
		}
		writeJSON(w, 200, result)
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) taskAPI(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/")
	if len(parts) == 1 && r.Method == "GET" {
		task, err := s.cfg.Store.GetTask(parts[0])
		if err != nil {
			writeError(w, 404, err)
			return
		}
		writeJSON(w, 200, task)
		return
	}
	if len(parts) != 2 || r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	var task managerstate.Task
	var err error
	switch parts[1] {
	case "signal", "budget":
		s.signalGraphAPI(w, r, parts[0], parts[1])
		return
	case "resume":
		var in struct {
			Clarification string `json:"clarification"`
		}
		if decode(w, r, &in, 1<<20) != nil {
			return
		}
		task, err = s.cfg.Store.Resume(parts[0], in.Clarification)
	case "retry-notification":
		if current, readErr := s.cfg.Store.GetTask(parts[0]); readErr == nil && current.GraphState != nil {
			if err := s.notifyGraph(r.Context(), current); err != nil {
				writeError(w, 502, err)
				return
			}
			writeJSON(w, 200, current)
			return
		}
		var in struct {
			EventID string `json:"event_id"`
		}
		if decode(w, r, &in, 1<<20) != nil {
			return
		}
		task, err = s.cfg.Store.GetTask(parts[0])
		if err == nil {
			operation, lookupErr := s.cfg.Store.GetTool(in.EventID)
			if lookupErr != nil || operation.ToolName != "workflow.notify" || in.EventID != notificationEventID(task) {
				err = errors.New("pending notification identity not found for current task state")
			} else if task.Status != managerstate.Completed && task.Status != managerstate.Blocked {
				err = errors.New("notification redelivery requires a completed or blocked task")
			} else if s.cfg.WorkflowRuntime == nil {
				err = errors.New("Verk runtime unavailable")
			} else {
				err = s.cfg.WorkflowRuntime.Notify(r.Context(), task)
			}
		}
	case "retry-classification":
		task, err = s.cfg.Store.RetryClassification(parts[0])
	case "cancel":
		task, err = s.cancelTask(r.Context(), parts[0])
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		if parts[1] == "cancel" && task.CancelRequested {
			writeJSON(w, 502, map[string]any{"error": err.Error(), "task": task})
			return
		}
		writeError(w, 409, err)
		return
	}
	if parts[1] != "cancel" && parts[1] != "retry-notification" {
		s.scheduleTask(task.ID)
	}
	writeJSON(w, 200, task)
}
func (s *Server) cancelJob(key string) {
	s.mu.Lock()
	cancel := s.active[key]
	delete(s.pending, key)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
