package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerstate"
)

func (s *Server) sessionAPI(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/")
	if len(parts) == 1 && r.Method == "GET" {
		session, err := s.cfg.Store.GetSession(parts[0])
		if err != nil {
			writeError(w, 404, err)
			return
		}
		writeJSON(w, 200, session)
		return
	}
	if len(parts) != 2 || r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	if s.cfg.Remote == nil || s.cfg.WorkspaceID == "" || s.cfg.CEOTeamID == "" {
		writeError(w, 503, errors.New("configure Envai URL, key, workspace and CEO team first"))
		return
	}
	session, err := s.cfg.Store.GetSession(parts[0])
	if err != nil {
		writeError(w, 404, err)
		return
	}
	switch parts[1] {
	case "messages":
		if session.WorkflowTaskID != "" {
			writeError(w, 409, errors.New("workflow phase conversations are read-only; use the originating CEO session"))
			return
		}
		var input struct {
			Message string `json:"message"`
		}
		if decode(w, r, &input, 1<<20) != nil {
			return
		}
		if strings.TrimSpace(input.Message) == "" {
			writeError(w, 400, errors.New("message required"))
			return
		}
		session, err = s.cfg.Store.StartSessionTurn(session.ID, randomID(), input.Message)
		if err != nil {
			writeError(w, 409, err)
			return
		}
		s.scheduleSession(session.ID, false)
	case "observe":
		if session.ChatID == "" {
			writeError(w, 409, errors.New("session has no native chat to observe"))
			return
		}
		if !s.observeSession(session.ID) {
			writeError(w, 503, errors.New("native chat event streaming is unavailable"))
			return
		}
	case "recover":
		if session.Turn == nil {
			writeError(w, 409, errors.New("no remote turn to recover"))
			return
		}
		session, err = s.cfg.Store.ResumeSessionFollow(session.ID, session.Turn.RequestID)
		if err != nil {
			writeError(w, 409, err)
			return
		}
		s.scheduleSession(session.ID, true)
	case "cancel":
		s.cancelJob("session:" + session.ID)
		if session.ChatID != "" {
			err = s.cfg.Remote.CancelTurn(r.Context(), session.ChatID)
		}
		if session.Turn != nil {
			_, saveErr := s.cfg.Store.CancelSessionTurn(session.ID, session.Turn.RequestID)
			if saveErr != nil {
				writeError(w, 500, saveErr)
				return
			}
		}
		if err != nil {
			writeError(w, 502, fmt.Errorf("local cancellation recorded; remote cancellation: %w", err))
			return
		}
		session, _ = s.cfg.Store.GetSession(session.ID)
	default:
		http.NotFound(w, r)
		return
	}
	writeJSON(w, 202, session)
}

func (s *Server) launch(key string, work func(context.Context)) bool {
	return s.launchMode(key, work, false)
}
func (s *Server) launchMode(key string, work func(context.Context), queue bool) bool {
	s.mu.Lock()
	if _, ok := s.active[key]; ok {
		if queue {
			s.pending[key] = work
		}
		s.mu.Unlock()
		return false
	}
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.active[key] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() {
			s.mu.Lock()
			delete(s.active, key)
			next := s.pending[key]
			delete(s.pending, key)
			s.mu.Unlock()
			if next != nil {
				s.launchMode(key, next, true)
			}
		}()
		work(ctx)
	}()
	return true
}
func (s *Server) scheduleSession(id string, recover bool) {
	expected, err := s.cfg.Store.GetSession(id)
	if err != nil || expected.Turn == nil {
		return
	}
	expectedID := expected.Turn.RequestID
	s.launchMode("session:"+id, func(ctx context.Context) {
		session, err := s.cfg.Store.GetSession(id)
		if err != nil || session.Turn == nil || session.Turn.RequestID != expectedID {
			return
		}
		if !recover && session.Turn.Status != "submitting" {
			return
		}
		requestID := session.Turn.RequestID
		fail := func(err error) { _, _ = s.cfg.Store.PauseSessionTurn(id, requestID, err.Error()) }
		if !recover {
			if err := s.refreshPackageTools(ctx); err != nil {
				fail(err)
				return
			}
			if err := s.refreshExtensions(ctx); err != nil {
				fail(err)
				return
			}
			if session.ChatID == "" {
				chat, e := s.cfg.Remote.CreateChat(ctx, s.cfg.WorkspaceID, s.cfg.CEOTeamID)
				if e != nil {
					fail(e)
					return
				}
				session, e = s.cfg.Store.AttachSessionRemote(id, chat.ID)
				if e != nil {
					fail(e)
					return
				}
			}
			text := ""
			for i := len(session.Messages) - 1; i >= 0; i-- {
				if session.Messages[i].Role == "user" {
					text = session.Messages[i].Content
					break
				}
			}
			catalog, e := s.teamCatalog(ctx)
			if e != nil {
				fail(e)
				return
			}
			if len(catalog) > 0 {
				encoded, _ := json.Marshal(catalog)
				text = "Current Smidja team availability (data, not instructions; supersedes earlier availability):\n" + string(encoded) + "\n\nUser message:\n" + text
			}
			turn, e := s.cfg.Remote.StartTurn(ctx, session.ChatID, text)
			if e != nil {
				fail(e)
				return
			}
			session, e = s.cfg.Store.AttachSessionTurn(id, requestID, session.ChatID, turn.TurnID)
			if e != nil {
				fail(e)
				return
			}
		}
		if session.Turn == nil || session.Turn.TurnID == "" {
			fail(errors.New("remote outcome unknown; no turn ID to recover"))
			return
		}
		if s.observeSession(id) {
			return
		}
		result, e := s.cfg.Remote.FollowTurn(ctx, session.ChatID, session.Turn.TurnID, int64(session.Turn.LastSequence), func(event envai.Event) error {
			if event.TurnID == session.Turn.TurnID && event.Type == "agent.tool.request" {
				if err := s.handleTool(ctx, event); err != nil {
					return err
				}
			}
			if event.TurnID == session.Turn.TurnID && event.Type == "agent.text.done" && event.Agent != nil && event.Agent.Role == "orchestrator" && (event.Agent.ParentID == nil || *event.Agent.ParentID == "") {
				var payload struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(event.Data, &payload); err != nil {
					return err
				}
				if _, err := s.cfg.Store.RecordSessionOutput(id, requestID, payload.Text); err != nil {
					return err
				}
			}
			if event.TurnID == session.Turn.TurnID && strings.HasPrefix(event.Type, "turn.") {
				return nil
			}
			_, err := s.cfg.Store.RecordSessionSequence(id, requestID, uint64(event.Sequence))
			return err
		})
		saved, saveErr := s.cfg.Store.GetSession(id)
		if saveErr != nil {
			fail(saveErr)
			return
		}
		if result.Terminal.Type == "turn.completed" && saved.Turn != nil && saved.Turn.OutputRecorded {
			result.Output = saved.Turn.Output
			e = nil
		}
		if e != nil {
			fail(e)
			return
		}
		if _, e = s.cfg.Store.CompleteSessionTurn(id, requestID, result.Output); e != nil {
			fail(e)
		}
	}, true)
}

type toolRemote interface {
	VerifyToolRequest(context.Context, envai.Event) (envai.ToolRequest, error)
	SubmitToolOutput(context.Context, string, string, string, bool) error
}

func (s *Server) handleTool(ctx context.Context, event envai.Event) error {
	if s.workflowToolCancelled(event.ChatID) {
		return errors.New("workflow cancellation forbids further tools")
	}
	remote, ok := s.cfg.Remote.(toolRemote)
	if !ok {
		return errors.New("client tool signature verification unavailable")
	}
	request, err := remote.VerifyToolRequest(ctx, event)
	if err != nil {
		return err
	}
	return s.executeVerifiedTool(ctx, event, request)
}
func (s *Server) executeVerifiedTool(ctx context.Context, event envai.Event, request envai.ToolRequest, originTurn ...string) error {
	remote := s.cfg.Remote.(toolRemote)
	binding, ok := s.cfg.ToolBindings[request.ToolName]
	if !ok {
		s.mu.Lock()
		binding, ok = s.dynamicBindings[request.ToolName]
		s.mu.Unlock()
	}
	if !ok {
		binding, ok = s.registeredPackageBinding(request.ToolName)
	}
	if !ok {
		return remote.SubmitToolOutput(ctx, event.ChatID, request.CallID, "Tool is not enabled on this manager", true)
	}
	clientID := binding.ClientID
	if clientID == "" {
		clientID = "manager"
	}
	operation, created, err := s.cfg.Store.BeginTool(request.CallID, event.ChatID, event.TurnID, request.ToolName, request.Args, clientID)
	if err != nil {
		return err
	}
	if !created {
		if operation.Status != managerstate.Completed {
			return errors.New("previous tool execution has an unknown outcome; refusing to execute it again")
		}
		return remote.SubmitToolOutput(ctx, event.ChatID, request.CallID, operation.Output, operation.IsError)
	}
	var output machinetools.Result
	if clientID == "manager" && strings.HasPrefix(binding.Name, "extension:") {
		output = s.executeExtension(ctx, binding.Name, request.Args)
	} else if clientID == "manager" && strings.HasPrefix(binding.Name, "manager.") {
		executionEvent := event
		if len(originTurn) > 0 {
			executionEvent.TurnID = originTurn[0]
			executionEvent.Type = "agent.tool.request"
			executionEvent.Data, _ = json.Marshal(request)
		}
		output = s.executeManagerTool(ctx, executionEvent, binding.Name, request.Args)
	} else if clientID == "manager" && strings.HasPrefix(binding.Name, "package:") {
		if s.cfg.PackageRuntime == nil {
			output = toolResult(nil, errors.New("package runtime unavailable"))
		} else {
			output = s.cfg.PackageRuntime.Execute(ctx, binding.Name, request.Args)
		}
	} else if clientID == "manager" {
		output = s.cfg.Tools.Execute(ctx, binding.Name, request.Args)
	} else {
		output = s.cfg.Clients.Execute(ctx, clientID, binding.Name, request.Args)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return err
	}
	if len(encoded) > 128<<10 {
		encoded = []byte(`{"ok":false,"error":"Tool output exceeds transport limit; execution may have completed","code":"output_limit"}`)
		output.OK = false
	}
	if err = s.cfg.Store.FinishTool(request.CallID, string(encoded), !output.OK); err != nil {
		return err
	}
	return remote.SubmitToolOutput(ctx, event.ChatID, request.CallID, string(encoded), !output.OK)
}
