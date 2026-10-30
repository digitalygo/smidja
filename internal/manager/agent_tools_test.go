package manager

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/managerstate"
)

func managerToolEvent() envai.Event {
	data, _ := json.Marshal(envai.ToolRequest{CallID: "manager-tool-call", ToolName: "tasks_start", Kind: "client", Args: json.RawMessage(`{}`), Signature: envai.Signature{Alg: "EdDSA", KeyID: "test-key", Value: "not-a-real-signature"}})
	return envai.Event{Type: "agent.tool.request", ChatID: "tool-chat", TurnID: "tool-turn", Agent: &envai.Agent{InstanceID: "ceo-instance"}, Data: data}
}

func TestManagerToolDefinitionsAndReadOperations(t *testing.T) {
	definitions := ManagerToolDefinitions()
	seen := map[string]bool{}
	for _, definition := range definitions {
		if definition.Name == "" || seen[definition.Name] || definition.Description == "" || !json.Valid(definition.Parameters) {
			t.Fatal(definition)
		}
		seen[definition.Name] = true
	}
	for _, name := range []string{"tasks_list", "tasks_get", "tasks_start", "tasks_resume", "clients_list", "clients_get", "config_propose", "config_list", "workflows_list"} {
		if !seen[name] {
			t.Fatalf("missing manager tool %s", name)
		}
	}
	s := testServer(t, nil)
	task := workflowTask(t, s)
	ctx := context.Background()
	if result := s.executeManagerTool(ctx, envai.Event{}, "tasks_list", json.RawMessage(`{}`)); !result.OK || len(result.Data.([]managerstate.Task)) != 1 {
		t.Fatal(result)
	}
	args, _ := json.Marshal(map[string]string{"id": task.ID})
	if result := s.executeManagerTool(ctx, envai.Event{}, "manager.tasks_get", args); !result.OK || result.Data.(managerstate.Task).ID != task.ID {
		t.Fatal(result)
	}
	for _, name := range []string{"unknown", "tasks_get", "clients_get"} {
		if result := s.executeManagerTool(ctx, envai.Event{}, name, json.RawMessage(`{}`)); result.OK || result.Code != "manager_error" {
			t.Fatal(result)
		}
	}
	if result := s.executeManagerTool(ctx, envai.Event{}, "tasks_list", json.RawMessage(`{`)); result.OK {
		t.Fatal(result)
	}
	if err := s.cfg.Clients.AddClient("laptop", "Laptop", "test-token-with-enough-bytes"); err != nil {
		t.Fatal(err)
	}
	result := s.executeManagerTool(ctx, envai.Event{}, "clients_list", json.RawMessage(`{}`))
	clients := result.Data.([]map[string]any)
	if !result.OK || len(clients) != 1 || clients[0]["id"] != "laptop" || clients[0]["online"] != false {
		t.Fatal(result)
	}
	if _, exists := clients[0]["inventory"]; exists {
		t.Fatal("offline tools leaked into list")
	}
	result = s.executeManagerTool(ctx, envai.Event{}, "clients_get", json.RawMessage(`{"id":"laptop"}`))
	if !result.OK || result.Data.(machineclient.ClientStatus).Name != "Laptop" {
		t.Fatal(result)
	}
}

func TestManagerTaskStartBindingAndErrors(t *testing.T) {
	ctx := context.Background()
	input := workflowInput()
	args, _ := json.Marshal(input)
	noRuntime := testServer(t, nil)
	if result := noRuntime.executeManagerTool(ctx, envai.Event{}, "tasks_start", args); result.OK || !strings.Contains(result.Error, "runtime unavailable") {
		t.Fatal(result)
	}
	notReady := testServer(t, func(c *Config) { c.WorkflowRuntime = &workflowFake{readyErr: errors.New("native features missing")} })
	if result := notReady.executeManagerTool(ctx, envai.Event{}, "tasks_start", args); result.OK || !strings.Contains(result.Error, "features missing") {
		t.Fatal(result)
	}
	s := testServer(t, func(c *Config) { c.WorkflowRuntime = &workflowFake{} })
	event := managerToolEvent()
	if result := s.executeManagerTool(ctx, event, "tasks_start", args); result.OK || !strings.Contains(result.Error, "tracked CEO conversation") {
		t.Fatal(result)
	}
	session, err := s.cfg.Store.CreateSession("CEO")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.StartSessionTurn(session.ID, "tool-request", "work"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachSessionTurn(session.ID, "tool-request", event.ChatID, event.TurnID); err != nil {
		t.Fatal(err)
	}
	badEvent := event
	badEvent.Data = json.RawMessage(`{`)
	if result := s.executeManagerTool(ctx, badEvent, "tasks_start", args); result.OK {
		t.Fatal(result)
	}
	if result := s.executeManagerTool(ctx, event, "tasks_start", json.RawMessage(`{"workflow":1}`)); result.OK {
		t.Fatal(result)
	}
	if result := s.executeManagerTool(ctx, event, "tasks_start", json.RawMessage(`{}`)); result.OK {
		t.Fatal(result)
	}
	input.OriginSessionID = "spoofed"
	input.OriginChatID = "spoofed"
	input.OriginTurnID = "spoofed"
	input.InvocationKey = "spoofed"
	args, _ = json.Marshal(input)
	result := s.executeManagerTool(ctx, event, "tasks_start", args)
	if !result.OK {
		t.Fatal(result)
	}
	task := result.Data.(managerstate.Task)
	if task.OriginSessionID != session.ID || task.OriginChatID != event.ChatID || task.OriginTurnID != event.TurnID || task.InvocationKey != "manager-tool-call" {
		t.Fatal(task)
	}
	deadline := time.Now().Add(time.Second)
	for getWorkflowTask(t, s, task.ID).Status != managerstate.Completed {
		if time.Now().After(deadline) {
			t.Fatal("workflow was not scheduled")
		}
		time.Sleep(time.Millisecond)
	}
	repeated := s.executeManagerTool(ctx, event, "tasks_start", args)
	if !repeated.OK || repeated.Data.(managerstate.Task).ID != task.ID || len(s.cfg.Store.ListTasks()) != 1 {
		t.Fatal(repeated)
	}
}

func TestManagerTaskResume(t *testing.T) {
	s := testServer(t, nil)
	task := workflowTask(t, s)
	args, _ := json.Marshal(map[string]string{"id": task.ID, "clarification": "resolved"})
	if result := s.executeManagerTool(context.Background(), envai.Event{}, "tasks_resume", args); result.OK {
		t.Fatal(result)
	}
	if _, err := s.cfg.Store.Pause(task.ID, "blocked"); err != nil {
		t.Fatal(err)
	}
	if result := s.executeManagerTool(context.Background(), envai.Event{}, "tasks_resume", args); !result.OK || result.Data.(managerstate.Task).Status != managerstate.Queued {
		t.Fatal(result)
	}
}
