package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/verk"
)

func (s *Server) validateGraphAPI(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Workflow managerstate.Workflow `json:"workflow"`
	}
	if decode(w, r, &input, 2<<20) != nil {
		return
	}
	if err := managerstate.ValidateWorkflow(input.Workflow); err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"valid": true})
}

func (s *Server) signalGraphAPI(w http.ResponseWriter, r *http.Request, id, action string) {
	var input struct {
		RequestID      string `json:"request_id"`
		GateID         string `json:"gate_id"`
		Port           string `json:"port"`
		Value          string `json:"value"`
		ProposalDigest string `json:"proposal_digest"`
		Budget         int    `json:"budget"`
	}
	if decode(w, r, &input, 1<<20) != nil {
		return
	}
	task, err := s.cfg.Store.GetTask(id)
	if err != nil || task.GraphState == nil {
		writeError(w, 404, errors.New("graph task not found"))
		return
	}
	state := task.GraphState
	run, ok := verk.CurrentRun(*state)
	if !ok || input.RequestID != run.RequestID {
		writeError(w, 409, errors.New("gate execution changed; refresh the task"))
		return
	}
	event := verk.Event{Type: action, ExpectedRevision: state.Revision, RunID: run.ID, GateID: input.GateID, Port: input.Port, Text: input.Value, ProposalDigest: input.ProposalDigest, Budget: input.Budget, TrustedOwner: true, At: time.Now().UTC()}
	task, err = s.cfg.Store.ApplyGraphEvent(id, event)
	if err != nil {
		writeError(w, 409, err)
		return
	}
	s.scheduleTask(id)
	writeJSON(w, 200, task)
}

func (s *Server) startTriggeredGraph(ctx context.Context, workflow managerstate.Workflow, inputs json.RawMessage, eventKey string) (managerstate.Task, error) {
	if workflow.Graph == nil || eventKey == "" {
		return managerstate.Task{}, errors.New("trigger requires graph and event identity")
	}
	task, err := s.cfg.Store.CreateTask(managerstate.StartTask{Workflow: workflow, Inputs: inputs, InvocationKey: "verk-trigger:" + eventKey})
	if err == nil {
		s.scheduleTask(task.ID)
	}
	return task, err
}
