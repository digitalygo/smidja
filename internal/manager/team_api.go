package manager

import (
	"encoding/json"
	"errors"
	"net/http"
)

func (s *Server) delegateTeamAPI(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SessionID string `json:"session_id"`
		TeamRef   string `json:"team_ref"`
		Prompt    string `json:"prompt"`
	}
	if decode(w, r, &input, 1<<20) != nil {
		return
	}
	session, err := s.cfg.Store.GetSession(input.SessionID)
	if err != nil || session.Turn == nil || session.Turn.TurnID == "" {
		writeError(w, 400, errors.New("select a CEO session with a remote turn"))
		return
	}
	requestID := randomID()
	args, _ := json.Marshal(input)
	if _, _, err = s.cfg.Store.BeginTool(requestID, session.ChatID, session.Turn.TurnID, "ui.teams_delegate", args, "manager"); err != nil {
		writeError(w, 500, err)
		return
	}
	result := s.delegateTeam(r.Context(), session.ChatID, session.Turn.TurnID, requestID, input.TeamRef, input.Prompt)
	encoded, _ := json.Marshal(result)
	if err = s.cfg.Store.FinishTool(requestID, string(encoded), !result.OK); err != nil {
		writeError(w, 500, err)
		return
	}
	if !result.OK {
		writeJSON(w, 502, result)
		return
	}
	writeJSON(w, 202, result)
}
