package managerstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func normalizeArguments(input json.RawMessage) (json.RawMessage, error) {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("tool arguments must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("invalid tool arguments")
	}
	return json.Marshal(value)
}

func (s *Store) BeginTool(callID, chatID, turnID, toolName string, args json.RawMessage, clientID string) (ToolOperation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	normalized, err := normalizeArguments(args)
	if err != nil {
		return ToolOperation{}, false, err
	}
	operation := ToolOperation{CallID: callID, ChatID: chatID, TurnID: turnID, ToolName: toolName, Args: normalized, ClientID: clientID, Status: Running}
	if existing, ok := s.tools[callID]; ok {
		if existing.ChatID != chatID || existing.TurnID != turnID || existing.ToolName != toolName || existing.ClientID != clientID || !bytes.Equal(existing.Args, normalized) {
			return ToolOperation{}, false, ErrConflict
		}
		return clone(existing), false, nil
	}
	if err = s.commit(event{Type: "tool.started", Tool: &operation}); err != nil {
		return ToolOperation{}, false, err
	}
	return clone(s.tools[callID]), true, nil
}

func (s *Store) FinishTool(callID, output string, isError bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commit(event{Type: "tool.finished", Tool: &ToolOperation{CallID: callID, Output: output, IsError: isError}})
}

func (s *Store) GetTool(callID string) (ToolOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.tools[callID]
	if !ok {
		return ToolOperation{}, ErrNotFound
	}
	return clone(value), nil
}

func (s *Store) reduceTool(e event) (func(), error) {
	if e.Tool == nil || e.Tool.CallID == "" {
		return nil, fmt.Errorf("tool call id is required")
	}
	next := clone(*e.Tool)
	switch e.Type {
	case "tool.started":
		if _, ok := s.tools[next.CallID]; ok {
			return nil, ErrConflict
		}
		if next.ChatID == "" || next.TurnID == "" || next.ToolName == "" || next.ClientID == "" || next.Status != Running {
			return nil, fmt.Errorf("tool binding is required")
		}
		args, err := normalizeArguments(next.Args)
		if err != nil {
			return nil, err
		}
		next.Args = args
	case "tool.finished":
		existing, ok := s.tools[next.CallID]
		if !ok {
			return nil, ErrNotFound
		}
		if existing.Status != Running {
			return nil, ErrConflict
		}
		existing.Output = next.Output
		existing.IsError = next.IsError
		existing.Status = Completed
		next = existing
	}
	return func() { s.tools[next.CallID] = next }, nil
}
