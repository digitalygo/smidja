package manager

import (
	"errors"
	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
	"strings"
)

func (s *Server) workflowPrincipal(event envai.Event) (string, error) {
	if event.ChatID == "" {
		return "", errors.New("workflow caller has no chat identity")
	}
	for _, session := range s.cfg.Store.ListSessions() {
		if session.ChatID != event.ChatID {
			continue
		}
		if session.TeamRef == "" || session.TeamID == "" {
			return "", errors.New("workflow caller lacks verified team provenance")
		}
		if session.TeamRef == "ceo" {
			if session.TeamID != s.cfg.CEOTeamID {
				return "", errors.New("CEO team identity mismatch")
			}
			return "ceo", nil
		}
		return "team:" + session.TeamRef, nil
	}
	return "", errors.New("workflow caller chat is not tracked")
}
func (s *Server) authorizeWorkflow(event envai.Event, workflow managerstate.Workflow) error {
	principal, err := s.workflowPrincipal(event)
	if err != nil {
		return err
	}
	if workflow.AllowedInvokers == nil && principal == "ceo" {
		return nil
	}
	for _, allowed := range workflow.AllowedInvokers {
		if allowed == principal {
			return nil
		}
	}
	return errors.New("team is not an allowed workflow invoker")
}
func (s *Server) resolveAgentWorkflow(event envai.Event, input managerstate.Workflow) (managerstate.Workflow, error) {
	principal, err := s.workflowPrincipal(event)
	if err != nil {
		return input, err
	}
	if principal != "ceo" && !strings.Contains(input.ID, "/") && !strings.HasPrefix(input.ID, "private:") && !strings.HasPrefix(input.ID, "client:") {
		return input, errors.New("non-CEO teams require an approved catalog workflow")
	}
	workflow, err := s.resolveWorkflow(input)
	if err != nil {
		return input, err
	}
	return workflow, s.authorizeWorkflow(event, workflow)
}
