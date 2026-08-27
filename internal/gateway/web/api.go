package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/gateway"
	"github.com/digitalygo/smidja/internal/session"
)

type sendRequest struct {
	Workspace string `json:"workspace"`
	Text      string `json:"text"`
}

type cancelRequest struct {
	ID string `json:"id"`
}

type transcriptMsg struct {
	Role string `json:"role"`
	Text string `json:"text"`
	Ts   string `json:"ts"`
}

type transcriptView struct {
	ID       string          `json:"id"`
	Cwd      string          `json:"cwd"`
	Messages []transcriptMsg `json:"messages"`
}

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request, a authCtx) {
	body, err := readLimited(w, r, maxSendBytes)
	if err != nil {
		writeBodyError(w, err)
		return
	}
	var req sendRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Workspace == "" || req.Text == "" {
		writeError(w, http.StatusBadRequest, "workspace and text are required")
		return
	}
	if len(req.Text) > maxSendText {
		writeError(w, http.StatusRequestEntityTooLarge, "text too long")
		return
	}
	if _, ok := s.cfg.Workspaces[req.Workspace]; !ok {
		writeError(w, http.StatusBadRequest, "unknown workspace")
		return
	}
	msg := gateway.InboundMessage{
		ID:              newMessageID(),
		Transport:       transportWeb,
		ExternalChatKey: a.userID,
		UserIDHash:      gateway.HashUserIdentity(transportWeb + ":" + a.userID),
		Text:            req.Text,
	}
	receipt, err := s.cfg.Gateway.Submit(r.Context(), msg)
	if err != nil {
		switch {
		case errors.Is(err, gateway.ErrRateLimited), errors.Is(err, gateway.ErrTooManyActive):
			writeError(w, http.StatusTooManyRequests, "rate limited")
		case errors.Is(err, gateway.ErrInboundTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, "message too large")
		case errors.Is(err, gateway.ErrDuplicate):
			writeError(w, http.StatusConflict, "duplicate message")
		case errors.Is(err, gateway.ErrClosed), errors.Is(err, gateway.ErrNotStarted):
			writeError(w, http.StatusServiceUnavailable, "gateway unavailable")
		default:
			writeError(w, http.StatusInternalServerError, "submit failed")
		}
		return
	}
	s.remember(a.userID, req.Workspace)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":            receipt.ID,
		"queuePosition": receipt.QueuePosition,
	})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request, a authCtx) {
	body, err := readLimited(w, r, maxCancelBytes)
	if err != nil {
		writeBodyError(w, err)
		return
	}
	var req cancelRequest
	if err := json.Unmarshal(body, &req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "invalid cancel body")
		return
	}
	if !s.cfg.Gateway.Cancel(transportWeb, a.userID) {
		writeError(w, http.StatusNotFound, "no active turn")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"cancelled": true})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request, a authCtx) {
	s.mu.Lock()
	list := make([]sessionInfo, len(s.known[a.userID]))
	copy(list, s.known[a.userID])
	s.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
	workspaces := make([]string, 0, len(s.cfg.Workspaces))
	for name := range s.cfg.Workspaces {
		workspaces = append(workspaces, name)
	}
	sort.Strings(workspaces)
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions":   list,
		"workspaces": workspaces,
	})
}

func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request, a authCtx) {
	id := r.URL.Query().Get("id")
	if !sessionIDRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}
	path, ok := s.findSessionFile(id)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	loader, err := session.LoadWithOptions(path, session.LoadOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read session")
		return
	}
	view := transcriptView{
		ID:       id,
		Cwd:      loader.Header().Cwd,
		Messages: transcriptMessages(loader),
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) findSessionFile(id string) (string, bool) {
	for _, root := range s.cfg.Workspaces {
		store, err := session.NewStore(filepath.Join(root, ".smidja", "sessions"))
		if err != nil {
			continue
		}
		dir, err := store.DirForCwd(root)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), "_"+id+".jsonl") {
				return filepath.Join(dir, e.Name()), true
			}
		}
	}
	return "", false
}

func transcriptMessages(loader *session.Loader) []transcriptMsg {
	branch, err := loader.ActiveBranch()
	if err != nil {
		return nil
	}
	var out []transcriptMsg
	for _, e := range branch {
		me, ok := e.(*session.MessageEntry)
		if !ok {
			continue
		}
		switch me.MessageRole() {
		case string(agent.RoleUser):
			msg, err := me.DecodeMessage()
			if err != nil || msg.User == nil {
				continue
			}
			out = append(out, transcriptMsg{
				Role: "user",
				Text: userText(msg.User.Content),
				Ts:   me.Timestamp,
			})
		case string(agent.RoleAssistant):
			msg, err := me.DecodeMessage()
			if err != nil || msg.Assistant == nil {
				continue
			}
			out = append(out, transcriptMsg{
				Role: "assistant",
				Text: assistantText(msg.Assistant.Content),
				Ts:   me.Timestamp,
			})
		}
	}
	return out
}

func userText(raw []byte) string {
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return plain
	}
	var blocks []agent.ContentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Type == agent.BlockTypeText {
				b.WriteString(blk.Text)
			}
		}
		return b.String()
	}
	var single agent.ContentBlock
	if json.Unmarshal(raw, &single) == nil && single.Type == agent.BlockTypeText {
		return single.Text
	}
	return ""
}

func assistantText(blocks []agent.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == agent.BlockTypeText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

func (s *Server) remember(userID, workspace string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.known[userID] {
		if s.known[userID][i].Workspace == workspace {
			return
		}
	}
	s.known[userID] = append(s.known[userID], sessionInfo{
		Key:       transportWeb + ":" + userID,
		Workspace: workspace,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Server) rememberSession(userID, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs := s.known[userID]
	if len(recs) == 0 {
		return
	}
	recs[len(recs)-1].SessionID = sessionID
}

func (s *Server) Deliver(ctx context.Context, d gateway.Delivery) error {
	ev := deliveryEvent{
		key:       d.ExternalChatKey,
		ID:        d.ID,
		SessionID: d.Result.SessionID,
		Type:      d.Kind,
		Text:      d.Text,
	}
	if d.Err != nil {
		ev.Error = d.Err.Error()
	} else {
		ev.Result = d.Result.Text
	}
	s.events.append(ev)
	if ev.SessionID != "" {
		s.rememberSession(d.ExternalChatKey, ev.SessionID)
	}
	return nil
}
