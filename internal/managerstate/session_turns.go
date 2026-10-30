package managerstate

import "strings"

func (s *Store) changeSession(e event) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.commit(e); err != nil {
		return Session{}, err
	}
	return clone(s.sessions[e.SessionID]), nil
}

func (s *Store) StartSessionTurn(id, requestID, text string) (Session, error) {
	return s.changeSession(event{Type: "session.turn_started", SessionID: id, RequestID: requestID, Text: text})
}
func (s *Store) AttachSessionTurn(id, requestID, chatID, turnID string) (Session, error) {
	return s.changeSession(event{Type: "session.turn_attached", SessionID: id, RequestID: requestID, ChatID: chatID, TurnID: turnID})
}
func (s *Store) RecordSessionOutput(id, requestID, output string) (Session, error) {
	return s.changeSession(event{Type: "session.turn_output", SessionID: id, RequestID: requestID, Text: output})
}
func (s *Store) CompleteSessionTurn(id, requestID, output string) (Session, error) {
	return s.changeSession(event{Type: "session.turn_completed", SessionID: id, RequestID: requestID, Text: output})
}
func (s *Store) PauseSessionTurn(id, requestID, reason string) (Session, error) {
	return s.changeSession(event{Type: "session.turn_paused", SessionID: id, RequestID: requestID, Text: reason})
}
func (s *Store) ResumeSessionFollow(id, requestID string) (Session, error) {
	return s.changeSession(event{Type: "session.turn_followed", SessionID: id, RequestID: requestID})
}
func (s *Store) RecordSessionSequence(id, requestID string, sequence uint64) (Session, error) {
	return s.changeSession(event{Type: "session.turn_sequence", SessionID: id, RequestID: requestID, RemoteSequence: sequence})
}
func (s *Store) CancelSessionTurn(id, requestID string) (Session, error) {
	return s.changeSession(event{Type: "session.turn_cancelled", SessionID: id, RequestID: requestID})
}

func reduceSessionTurn(session *Session, e event) error {
	if strings.TrimSpace(e.RequestID) == "" {
		return ErrConflict
	}
	turn := session.Turn
	if e.Type == "session.turn_started" {
		if turn != nil && turn.Status != Completed && turn.Status != Cancelled {
			return ErrConflict
		}
		if strings.TrimSpace(e.Text) == "" {
			return ErrConflict
		}
		for _, message := range session.Messages {
			if message.ID == e.RequestID+"-user" {
				return ErrConflict
			}
		}
		session.Turn = &SessionTurn{RequestID: e.RequestID, Status: "submitting"}
		session.Messages = append(session.Messages, Message{ID: e.RequestID + "-user", Role: "user", Content: e.Text, ChatID: session.ChatID, At: e.At})
		return nil
	}
	if turn == nil || turn.RequestID != e.RequestID || turn.Status == Completed || turn.Status == Cancelled {
		return ErrConflict
	}
	switch e.Type {
	case "session.turn_attached":
		if turn.Status != "submitting" || e.ChatID == "" || e.TurnID == "" || (session.ChatID != "" && session.ChatID != e.ChatID) {
			return ErrConflict
		}
		session.ChatID = e.ChatID
		turn.TurnID = e.TurnID
		turn.Status = Running
	case "session.turn_output":
		if turn.Status != Running || turn.TurnID == "" || (turn.OutputRecorded && turn.Output != e.Text) {
			return ErrConflict
		}
		turn.Output = e.Text
		turn.OutputRecorded = true
	case "session.turn_completed":
		if turn.Status != Running || turn.TurnID == "" || (turn.OutputRecorded && turn.Output != e.Text) {
			return ErrConflict
		}
		session.Messages = append(session.Messages, Message{ID: e.RequestID + "-assistant", Role: "assistant", Content: e.Text, ChatID: session.ChatID, TurnID: turn.TurnID, At: e.At})
		turn.Output = e.Text
		turn.OutputRecorded = true
		turn.Status = Completed
		turn.Reason = ""
	case "session.turn_paused":
		if (turn.Status != Running && turn.Status != "submitting") || strings.TrimSpace(e.Text) == "" {
			return ErrConflict
		}
		turn.Status = Paused
		turn.Reason = e.Text
	case "session.turn_followed":
		if (turn.Status != Paused && turn.Status != Running) || turn.TurnID == "" {
			return ErrConflict
		}
		turn.Status = Running
		turn.Reason = ""
	case "session.turn_sequence":
		if turn.Status != Running || e.RemoteSequence <= turn.LastSequence {
			return ErrConflict
		}
		turn.LastSequence = e.RemoteSequence
	case "session.turn_cancelled":
		turn.Status = Cancelled
	}
	return nil
}
