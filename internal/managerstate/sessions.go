package managerstate

import (
	"fmt"
	"sort"
	"strings"
)

func (s *Store) CreateSession(title string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := newID()
	if err := s.commit(event{Type: "session.created", SessionID: id, Text: title}); err != nil {
		return Session{}, err
	}
	return clone(s.sessions[id]), nil
}

func (s *Store) GetSession(id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.sessions[id]
	if !ok {
		return Session{}, ErrNotFound
	}
	return clone(value), nil
}

func (s *Store) ListSessions() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		result = append(result, clone(session))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}

func (s *Store) AttachSessionRemote(id, chatID string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.commit(event{Type: "session.remote_attached", SessionID: id, ChatID: chatID}); err != nil {
		return Session{}, err
	}
	return clone(s.sessions[id]), nil
}

func (s *Store) AppendMessage(id string, message Message) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return Session{}, ErrNotFound
	}
	if message.ID != "" {
		for _, existing := range session.Messages {
			if existing.ID == message.ID {
				if existing.Role != message.Role || existing.Content != message.Content || existing.ChatID != message.ChatID || existing.TurnID != message.TurnID {
					return Session{}, ErrConflict
				}
				return clone(session), nil
			}
		}
	}
	if message.ID == "" {
		message.ID = newID()
	}
	if err := s.commit(event{Type: "session.message_appended", SessionID: id, Message: &message}); err != nil {
		return Session{}, err
	}
	return clone(s.sessions[id]), nil
}

func (s *Store) RecordChatSequence(id string, sequence uint64) (Session, error) {
	return s.changeSession(event{Type: "session.chat_sequence", SessionID: id, RemoteSequence: sequence})
}

func (s *Store) reduceSession(e event) (func(), error) {
	if e.SessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if e.Type == "session.created" {
		if _, ok := s.sessions[e.SessionID]; ok {
			return nil, ErrConflict
		}
		if e.TaskID != "" {
			if _, ok := s.tasks[e.TaskID]; !ok {
				return nil, ErrNotFound
			}
		}
		session := Session{WorkflowTaskID: e.TaskID, ID: e.SessionID, Title: e.Text, Messages: []Message{}, CreatedAt: e.At}
		return func() { s.sessions[session.ID] = session }, nil
	}
	session, ok := s.sessions[e.SessionID]
	if !ok {
		return nil, ErrNotFound
	}
	session = clone(session)
	switch e.Type {
	case "session.admission_rejected":
		if session.ChatID == "" || session.Turn != nil {
			return nil, ErrConflict
		}
		session.AdmissionRejected = true
	case "session.identity_attached":
		if strings.TrimSpace(e.ChatID) == "" || strings.TrimSpace(e.TeamRef) == "" || strings.TrimSpace(e.TeamID) == "" || (session.ChatID != "" && session.ChatID != e.ChatID) || (session.TeamRef != "" && (session.TeamRef != e.TeamRef || session.TeamID != e.TeamID)) {
			return nil, ErrConflict
		}
		for id, other := range s.sessions {
			if id != session.ID && other.ChatID == e.ChatID && other.TeamRef != "" && (other.TeamRef != e.TeamRef || other.TeamID != e.TeamID) {
				return nil, ErrConflict
			}
		}
		session.ChatID = e.ChatID
		session.TeamRef = e.TeamRef
		session.TeamID = e.TeamID
	case "session.chat_sequence":
		if session.ChatID == "" || e.RemoteSequence != session.LastSequence+1 {
			return nil, ErrConflict
		}
		session.LastSequence = e.RemoteSequence
	case "session.remote_attached":
		if strings.TrimSpace(e.ChatID) == "" || (session.ChatID != "" && session.ChatID != e.ChatID) {
			return nil, ErrConflict
		}
		session.ChatID = e.ChatID
	case "session.turn_output", "session.turn_started", "session.turn_attached", "session.turn_completed", "session.turn_paused", "session.turn_followed", "session.turn_sequence", "session.turn_cancelled":
		if err := reduceSessionTurn(&session, e); err != nil {
			return nil, err
		}
	case "session.message_appended":
		if e.Message == nil || e.Message.ID == "" {
			return nil, fmt.Errorf("message id is required")
		}
		message := *e.Message
		switch message.Role {
		case "user", "assistant", "system", "tool":
		default:
			return nil, fmt.Errorf("invalid message role")
		}
		if message.ChatID != "" && message.ChatID != session.ChatID {
			return nil, ErrConflict
		}
		for _, old := range session.Messages {
			if old.ID == message.ID {
				return nil, ErrConflict
			}
		}
		if message.At.IsZero() {
			message.At = e.At
		}
		session.Messages = append(session.Messages, message)
	}
	return func() { s.sessions[session.ID] = session }, nil
}

func (s *Store) CreateWorkflowSession(title, taskID string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if taskID == "" {
		return Session{}, fmt.Errorf("workflow task id required")
	}
	id := newID()
	if err := s.commit(event{Type: "session.created", SessionID: id, TaskID: taskID, Text: title}); err != nil {
		return Session{}, err
	}
	return clone(s.sessions[id]), nil
}

func (s *Store) AttachSessionIdentity(id, chatID, teamRef, teamID string) (Session, error) {
	return s.changeSession(event{Type: "session.identity_attached", SessionID: id, ChatID: chatID, TeamRef: teamRef, TeamID: teamID})
}

func (s *Store) RejectSessionAdmission(id string) (Session, error) {
	return s.changeSession(event{Type: "session.admission_rejected", SessionID: id})
}
