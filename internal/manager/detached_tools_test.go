package manager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/managerstate"
)

type detachedRemote struct {
	chatRemote
	task                         envai.DetachedTask
	taskErr                      error
	lookedUpChat, lookedUpTask   string
	verifiedTask, verifiedOrigin string
	verifies                     int
}

func (f *detachedRemote) Task(_ context.Context, chatID, taskID string) (envai.DetachedTask, error) {
	f.lookedUpChat = chatID
	f.lookedUpTask = taskID
	return f.task, f.taskErr
}
func (f *detachedRemote) VerifyDetachedToolRequest(_ context.Context, event envai.Event, taskID, origin string) (envai.ToolRequest, error) {
	f.verifies++
	f.verifiedTask = taskID
	f.verifiedOrigin = origin
	var wrapped envai.DetachedEvent
	if err := json.Unmarshal(event.Data, &wrapped); err != nil {
		return envai.ToolRequest{}, err
	}
	if wrapped.TaskID != taskID || wrapped.OriginTurnID != origin {
		return envai.ToolRequest{}, errors.New("binding mismatch")
	}
	return f.tool, f.verifyErr
}
func detachedToolEvent() envai.Event {
	return envai.Event{Type: "task.event", ChatID: "chat", Agent: &envai.Agent{InstanceID: "child"}, Data: json.RawMessage(`{"task_id":"native-task","origin_turn_id":"native-origin","event_type":"agent.tool.request","event_payload":{}}`)}
}
func TestDetachedToolReceiptReplaysWithoutExecution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("first value"), 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": path})
	remote := &detachedRemote{task: envai.DetachedTask{TaskID: "native-task", OriginTurnID: "native-origin", Status: "running"}}
	remote.tool = envai.ToolRequest{CallID: "call", ToolName: "registered_read", Kind: "client", Args: args}
	s := testServer(t, func(c *Config) {
		c.Remote = remote
		c.ToolBindings = map[string]ToolBinding{"registered_read": {Name: "read"}}
	})
	event := detachedToolEvent()
	if err := s.handleDetachedTool(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if remote.lookedUpChat != "chat" || remote.lookedUpTask != "native-task" || remote.verifiedTask != "native-task" || remote.verifiedOrigin != "native-origin" {
		t.Fatal(remote)
	}
	receipt, err := s.cfg.Store.GetTool("call")
	if err != nil || receipt.TurnID != "task:native-task" || receipt.ChatID != "chat" || receipt.Status != managerstate.Completed || !strings.Contains(receipt.Output, "first value") {
		t.Fatal(receipt, err)
	}
	if err = os.WriteFile(path, []byte("second value"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.handleDetachedTool(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if remote.submits != 2 || remote.outputs[0] != remote.outputs[1] || strings.Contains(remote.outputs[1], "second value") {
		t.Fatal(remote.outputs)
	}
	remote.tool.Args = json.RawMessage(`{"path":"changed"}`)
	if err = s.handleDetachedTool(context.Background(), event); err == nil {
		t.Fatal("changed args reused immutable receipt")
	}
}
func TestDetachedToolsFailClosed(t *testing.T) {
	for _, mutate := range []func(*detachedRemote){func(f *detachedRemote) { f.taskErr = errors.New("task_unknown") }, func(f *detachedRemote) { f.task.Status = "completed" }, func(f *detachedRemote) { f.task.TaskID = "other" }, func(f *detachedRemote) { f.task.OriginTurnID = "other" }, func(f *detachedRemote) { f.verifyErr = errors.New("signature invalid") }} {
		remote := &detachedRemote{task: envai.DetachedTask{TaskID: "native-task", OriginTurnID: "native-origin", Status: "running"}}
		mutate(remote)
		s := testServer(t, func(c *Config) { c.Remote = remote })
		if err := s.handleDetachedTool(context.Background(), detachedToolEvent()); err == nil || remote.submits != 0 {
			t.Fatal(err, remote.submits)
		}
	}
	s := testServer(t, func(c *Config) { c.Remote = &chatRemote{} })
	if err := s.handleDetachedTool(context.Background(), detachedToolEvent()); err == nil {
		t.Fatal("missing detached verifier")
	}
	event := detachedToolEvent()
	event.Data = json.RawMessage(`bad`)
	if err := s.handleDetachedTool(context.Background(), event); err == nil {
		t.Fatal("bad JSON")
	}
	event.Data = json.RawMessage(`{"event_type":"agent.text.done"}`)
	if err := s.handleDetachedTool(context.Background(), event); err != nil {
		t.Fatal(err)
	}
}
func TestDetachedManagerToolUsesNativeOrigin(t *testing.T) {
	remote := &detachedRemote{task: envai.DetachedTask{TaskID: "native-task", OriginTurnID: "native-origin", Status: "running"}}
	remote.tool = envai.ToolRequest{CallID: "call", ToolName: "registered_tasks", Kind: "client", Args: json.RawMessage(`{}`)}
	s := testServer(t, func(c *Config) {
		c.Remote = remote
		c.ToolBindings = map[string]ToolBinding{"registered_tasks": {Name: "manager.tasks_list"}}
	})
	if err := s.handleDetachedTool(context.Background(), detachedToolEvent()); err != nil {
		t.Fatal(err)
	}
	if remote.submits != 1 || remote.outputErrors[0] {
		t.Fatal(remote.outputs)
	}
	receipt, err := s.cfg.Store.GetTool("call")
	if err != nil || receipt.TurnID != "task:native-task" {
		t.Fatal(receipt, err)
	}
}

func TestDetachedWorkflowStartPinsRealOrigin(t *testing.T) {
	remote := &detachedRemote{task: envai.DetachedTask{TaskID: "native-task", OriginTurnID: "native-origin", Status: "running"}}
	input := workflowInput()
	input.OriginTurnID = "forged"
	args, _ := json.Marshal(input)
	remote.tool = envai.ToolRequest{CallID: "workflow-call", ToolName: "registered_start", Kind: "client", Args: args, Signature: envai.Signature{Alg: "EdDSA", KeyID: "key", Value: "signature"}}
	runtime := &workflowFake{}
	s := testServer(t, func(c *Config) {
		c.Remote = remote
		c.WorkflowRuntime = runtime
		c.ToolBindings = map[string]ToolBinding{"registered_start": {Name: "manager.tasks_start"}}
	})
	session, err := s.cfg.Store.CreateSession("origin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachSessionRemote(session.ID, "chat"); err != nil {
		t.Fatal(err)
	}
	if err = s.handleDetachedTool(context.Background(), detachedToolEvent()); err != nil {
		t.Fatal(err)
	}
	s.wg.Wait()
	tasks := s.cfg.Store.ListTasks()
	if len(tasks) != 1 || tasks[0].OriginTurnID != "native-origin" || tasks[0].OriginChatID != "chat" || tasks[0].OriginSessionID != session.ID || tasks[0].InvocationKey != "workflow-call" {
		t.Fatal(tasks, remote.outputs)
	}
	if remote.submits != 1 || remote.outputErrors[0] {
		t.Fatal(remote.outputs)
	}
	receipt, err := s.cfg.Store.GetTool("workflow-call")
	if err != nil || receipt.TurnID != "task:native-task" {
		t.Fatal(receipt, err)
	}
}
