package manager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/digitalygo/smidja/internal/managerstate"
	"github.com/digitalygo/smidja/internal/verk"
	"github.com/digitalygo/smidja/internal/verktriggers"
)

var verkTriggerIngressMu sync.Mutex

func (s *Server) verkTriggerHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, 405, errors.New("POST required"))
		return
	}
	secret := ""
	if s.cfg.LookupEnv != nil {
		secret = s.cfg.LookupEnv("SMIDJA_WEBHOOK_SECRET")
	}
	if secret == "" || s.cfg.Private == nil {
		writeError(w, 503, errors.New("Verk webhook configuration unavailable"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, 413, errors.New("webhook body exceeds limit or could not be read"))
		return
	}
	source, delivery, err := verktriggers.Authenticate(r.Header, body, secret)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	event, err := verktriggers.Decode(r.Header, body, source, delivery)
	if errors.Is(err, verktriggers.ErrUnsupported) {
		writeJSON(w, 202, map[string]any{"tasks": []string{}, "ignored": true})
		return
	}
	if err != nil {
		writeError(w, 400, err)
		return
	}
	verkTriggerIngressMu.Lock()
	defer verkTriggerIngressMu.Unlock()
	resumed := s.wakeVerkWaits(event, r.Header.Get("X-GitHub-Event"))
	tasks := []string{}
	for _, workflow := range s.verkTriggerWorkflows() {
		matches := false
		for _, node := range workflow.Graph.Nodes {
			if node.Type != "entry" {
				continue
			}
			for _, trigger := range node.Triggers {
				if verktriggers.Match(trigger, event) {
					matches = true
					break
				}
			}
		}
		if !matches {
			continue
		}
		task, found, err := s.verkDeliveredTask(workflow.ID, event)
		if err != nil {
			writeError(w, 409, err)
			return
		}
		if !found {
			inputs, _ := json.Marshal(map[string]any{"trigger": event})
			task, err = s.startTriggeredGraph(r.Context(), workflow, inputs, verktriggers.Key(workflow.ID, source, delivery))
			if err != nil {
				writeError(w, 409, err)
				return
			}
		}
		tasks = append(tasks, task.ID)
	}
	writeJSON(w, 202, map[string]any{"tasks": tasks, "resumed_tasks": resumed})
}

func (s *Server) verkDeliveredTask(workflowID string, event verktriggers.Event) (managerstate.Task, bool, error) {
	var samePayload *managerstate.Task
	for _, task := range s.cfg.Store.ListTasks() {
		if task.Workflow.ID != workflowID || task.InvocationKey == "" {
			continue
		}
		var inputs struct {
			Trigger verktriggers.Event `json:"trigger"`
		}
		if json.Unmarshal(task.Inputs, &inputs) != nil || inputs.Trigger.Source != event.Source {
			continue
		}
		old := inputs.Trigger
		if old.DeliveryID == event.DeliveryID {
			if old.PayloadHash != event.PayloadHash {
				return managerstate.Task{}, false, errors.New("delivery identity already used with different signed payload")
			}
			return task, true, nil
		}
		if event.Source == "github" && old.PayloadHash == event.PayloadHash {
			copy := task
			samePayload = &copy
		}
	}
	if samePayload != nil {
		return *samePayload, true, nil
	}
	return managerstate.Task{}, false, nil
}

func (s *Server) verkTriggerWorkflows() []managerstate.Workflow {
	workflows := []managerstate.Workflow{}
	if s.cfg.Private == nil {
		return workflows
	}
	for _, entity := range s.cfg.Private.List("workflow", false) {
		workflow, err := s.resolveWorkflow(managerstate.Workflow{ID: entity.ID})
		if err != nil || workflow.Graph == nil || verk.Validate(*workflow.Graph, true) != nil {
			continue
		}
		valid := true
		for _, node := range workflow.Graph.Nodes {
			if node.Type != "entry" {
				continue
			}
			for _, trigger := range node.Triggers {
				if verktriggers.Validate(trigger) != nil {
					valid = false
				}
			}
		}
		if valid {
			workflows = append(workflows, workflow)
		}
	}
	return workflows
}

func (s *Server) startVerkTriggers() {
	catalog := &Server{cfg: s.cfg}
	s.backgroundWG.Add(1)
	go func() {
		defer s.backgroundWG.Done()
		ctx := s.ctx
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		lastMinute := ""
		for {
			now := time.Now().UTC()
			s.expireGraphGates(ctx, now)
			minute := now.Format("2006-01-02T15:04")
			if minute != lastMinute {
				s.runVerkScheduleWorkflows(ctx, now, catalog.verkTriggerWorkflows())
				lastMinute = minute
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Server) runVerkSchedules(ctx context.Context, now time.Time) {
	s.runVerkScheduleWorkflows(ctx, now, s.verkTriggerWorkflows())
}

func (s *Server) runVerkScheduleWorkflows(ctx context.Context, now time.Time, workflows []managerstate.Workflow) {
	for _, workflow := range workflows {
		if ctx.Err() != nil {
			return
		}
		for _, node := range workflow.Graph.Nodes {
			if node.Type != "entry" {
				continue
			}
			for _, trigger := range node.Triggers {
				if trigger.Type != "schedule" {
					continue
				}
				schedule, err := verktriggers.ParseSchedule(trigger.Schedule, trigger.Timezone)
				if err != nil {
					continue
				}
				slot, due := schedule.Due(now)
				if !due {
					continue
				}
				key := verktriggers.Key(workflow.ID, "schedule", trigger.ID, trigger.Schedule, slot)
				inputs, _ := json.Marshal(map[string]any{"trigger": map[string]any{"type": "schedule", "source": "schedule", "delivery_id": key, "scheduled_slot": slot, "schedule": trigger.Schedule, "timezone": trigger.Timezone}})
				_, _ = s.startTriggeredGraph(ctx, workflow, inputs, key)
			}
		}
	}
}

func (s *Server) wakeVerkWaits(event verktriggers.Event, githubEvent string) []string {
	resumed := []string{}
	source := event.Source
	if source == "github" {
		source = "github:" + githubEvent
	}
	for _, task := range s.cfg.Store.ListTasks() {
		state := task.GraphState
		if state == nil || state.Status != "blocked" || state.Gate == nil || state.Gate.Kind != "wait" || state.Gate.Status != "pending" || state.Gate.SourceRef != source {
			continue
		}
		duplicate := false
		for _, run := range state.Runs {
			raw, err := json.Marshal(run.Fields["trigger"])
			if err != nil {
				continue
			}
			var previous verktriggers.Event
			if json.Unmarshal(raw, &previous) == nil && previous.Source == event.Source && (previous.DeliveryID == event.DeliveryID || event.Source == "github" && previous.PayloadHash == event.PayloadHash) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		run, ok := verk.CurrentRun(*state)
		if !ok {
			continue
		}
		fields := map[string]any{"trigger": event}
		_, err := s.cfg.Store.ApplyGraphEvent(task.ID, verk.Event{Type: "signal", ExpectedRevision: state.Revision, RunID: run.ID, GateID: state.Gate.ID, SourceRef: source, Port: "resume", Text: string(event.Payload), Fields: fields, At: time.Now().UTC()})
		if err != nil {
			continue
		}
		s.scheduleTask(task.ID)
		resumed = append(resumed, task.ID)
	}
	return resumed
}
