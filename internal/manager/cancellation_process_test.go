package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/machineclient"
	"github.com/digitalygo/smidja/internal/machinetools"
	"github.com/digitalygo/smidja/internal/managerstate"
)

type cancellationProcessRemote struct {
	chatRemote
	request      envai.ToolRequest
	phaseStopped chan struct{}
	ceoStarted   chan struct{}
	ceoStopped   chan struct{}
}

func (f *cancellationProcessRemote) VerifyToolRequest(context.Context, envai.Event) (envai.ToolRequest, error) {
	return f.request, nil
}
func (f *cancellationProcessRemote) SubmitToolOutput(context.Context, string, string, string, bool) error {
	return nil
}
func (f *cancellationProcessRemote) StreamEvents(ctx context.Context, chat string, _ int64, handler envai.EventHandler) error {
	if chat == "ceo-live" {
		close(f.ceoStarted)
		defer close(f.ceoStopped)
		<-ctx.Done()
		return ctx.Err()
	}
	defer close(f.phaseStopped)
	err := handler(envai.Event{ID: "process-event", ChatID: chat, TurnID: "phase-turn", Sequence: 1, Type: "agent.tool.request"})
	if err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestWorkflowCancelAPIStopsOwnedObserverProcess(t *testing.T) {
	for _, mode := range []string{"local", "http-client", "remote-error"} {
		t.Run(mode, func(t *testing.T) {
			pidPath := filepath.Join(t.TempDir(), "helper.pid")
			args, _ := json.Marshal(machinetools.Arguments{Command: fmt.Sprintf("echo $$ > %q; exec sleep 60", pidPath), TimeoutSeconds: 60})
			remote := &cancellationProcessRemote{request: envai.ToolRequest{CallID: "process-call", ToolName: "long-tool", Args: args}, phaseStopped: make(chan struct{}), ceoStarted: make(chan struct{}), ceoStopped: make(chan struct{})}
			broker := machineclient.NewBroker(machineclient.BrokerConfig{PollTimeout: 20 * time.Millisecond})
			defer broker.Close()
			clientID := "manager"
			if mode == "http-client" {
				clientID = "laptop"
			}
			s := testServer(t, func(c *Config) {
				c.Remote = remote
				c.Clients = broker
				c.ToolBindings = map[string]ToolBinding{"long-tool": {Name: "bash", ClientID: clientID}}
			})
			if mode == "remote-error" {
				s.cfg.WorkflowRuntime = &nativeWorkflow{server: s, remote: &nativeFake{cancelErr: errors.New("HTTP502")}}
			}
			if mode == "http-client" {
				if err := broker.AddClient("laptop", "Laptop", testToken); err != nil {
					t.Fatal(err)
				}
				endpoint := httptest.NewServer(broker)
				defer endpoint.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() {
					done <- machineclient.Run(ctx, machineclient.Config{ManagerURL: endpoint.URL, ClientID: "laptop", Name: "Laptop", Token: testToken, HeartbeatInterval: 10 * time.Millisecond, Inventory: machineclient.Inventory{OS: "linux", Arch: "amd64", Tools: []string{"bash"}}}, machinetools.New(machinetools.Config{}))
				}()
				defer func() {
					cancel()
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("client did not stop")
					}
				}()
				awaitWatch(t, func() bool {
					for _, client := range broker.List() {
						if client.ID == "laptop" && client.Online {
							return true
						}
					}
					return false
				})
			}
			task := cancellationReceipt(t, s, cancellationHandle(), true)
			var owned managerstate.Session
			for _, session := range s.cfg.Store.ListSessions() {
				if session.WorkflowTaskID == task.ID {
					owned = session
				}
			}
			ceo, err := s.cfg.Store.CreateSession("unrelated CEO")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.cfg.Store.AttachSessionRemote(ceo.ID, "ceo-live"); err != nil {
				t.Fatal(err)
			}
			if !s.observeSession(ceo.ID) || !s.observeSession(owned.ID) {
				t.Fatal("observer unavailable")
			}
			<-remote.ceoStarted
			pid := 0
			awaitWatch(t, func() bool {
				raw, err := os.ReadFile(pidPath)
				if err != nil {
					return false
				}
				pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
				return pid > 0
			})
			defer syscall.Kill(pid, syscall.SIGKILL)
			if err = syscall.Kill(pid, 0); err != nil {
				t.Fatal("helper not initially live", err)
			}
			result := request(s, "POST", "/api/tasks/"+task.ID+"/cancel", "", ownerHeaders())
			expected := 200
			if mode == "remote-error" {
				expected = 502
			}
			if result.Code != expected {
				t.Fatal(result.Code, result.Body)
			}
			awaitWatch(t, func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) })
			select {
			case <-remote.phaseStopped:
			case <-time.After(time.Second):
				t.Fatal("phase observer still live")
			}
			select {
			case <-remote.ceoStopped:
				t.Fatal("unrelated CEO stopped")
			default:
			}
			if s.observeSession(owned.ID) {
				t.Fatal("cancelled observer restarted")
			}
		})
	}
}

func TestCancelledPhaseBeforeRemoteAttemptAttachment(t *testing.T) {
	s := testServer(t, nil)
	task := workflowTask(t, s)
	session, err := s.cfg.Store.CreateWorkflowSession("phase", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.AttachSessionRemote(session.ID, "early-chat"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.cfg.Store.RequestCancellation(task.ID); err != nil {
		t.Fatal(err)
	}
	if !s.workflowToolCancelled("early-chat") {
		t.Fatal("cancelled phase admitted tool before attempt attachment")
	}
}
