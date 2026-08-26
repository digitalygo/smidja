package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type testExtension struct{}

var (
	_ Extension   = testExtension{}
	_ SetupHook   = testExtension{}
	_ LLMHook     = testExtension{}
	_ ToolHook    = testExtension{}
	_ SessionHook = testExtension{}
)

func (testExtension) ID() string { return "fixture" }

func (testExtension) Setup(api API) error { return nil }

func (testExtension) RegisterLLMHooks(r LLMHookRegistry) {
	r.OnContext(func(ctx HandlerContext, e ContextEvent) (*ContextEventResult, error) {
		return &ContextEventResult{Messages: e.Messages}, nil
	})
	r.OnMessageEnd(func(ctx HandlerContext, e MessageEndEvent) (*MessageEndEventResult, error) {
		return nil, nil
	})
	r.OnAutoRetryStart(func(ctx HandlerContext, e AutoRetryStartEvent) error { return nil })
	r.OnAutoRetryEnd(func(ctx HandlerContext, e AutoRetryEndEvent) error { return nil })
}

func (testExtension) RegisterToolHooks(r ToolHookRegistry) {
	r.OnToolCall(func(ctx HandlerContext, e ToolCallEvent) (*ToolCallDecision, error) {
		return &ToolCallDecision{Block: true, Reason: "fixture denies"}, nil
	})
	r.OnToolResult(func(ctx HandlerContext, e ToolResultEvent) (*ToolResultEventResult, error) {
		return nil, nil
	})
}

func (testExtension) RegisterSessionHooks(r SessionHookRegistry) {
	r.OnSessionStart(func(ctx HandlerContext, e SessionStartEvent) error { return nil })
	r.OnSessionShutdown(func(ctx HandlerContext, e SessionShutdownEvent) error { return nil })
}

type fakeAPI struct{}

var (
	_ API            = fakeAPI{}
	_ HandlerContext = fakeAPI{}
	_ CommandContext = fakeAPI{}
)

func (fakeAPI) RegisterTool(t Tool) error                      { return nil }
func (fakeAPI) UnregisterTool(name string) error               { return nil }
func (fakeAPI) ActiveTools() []string                          { return nil }
func (fakeAPI) SetActiveTools(names []string) error            { return nil }
func (fakeAPI) AllTools() []ToolInfo                           { return nil }
func (fakeAPI) RegisterCommand(name string, cmd Command) error { return nil }
func (fakeAPI) Commands() []CommandInfo                        { return nil }
func (fakeAPI) SendMessage(msg CustomMessage, opts SendOptions) error {
	return nil
}
func (fakeAPI) SendUserMessage(text string, opts SendOptions) error { return nil }
func (fakeAPI) AppendEntry(customType string, data any) error       { return nil }
func (fakeAPI) SetSessionName(name string) error                    { return nil }
func (fakeAPI) LabelEntry(entryID, label string) error              { return nil }
func (fakeAPI) SetModel(m Model) error                              { return nil }
func (fakeAPI) SetThinkingLevel(level ThinkingLevel) error          { return nil }
func (fakeAPI) RegisterProvider(name string, cfg ProviderConfig) error {
	return nil
}
func (fakeAPI) RemoveProvider(name string) error                 { return nil }
func (fakeAPI) RegisterFlag(name string, opts FlagOptions) error { return nil }
func (fakeAPI) Flags() map[string]any                            { return nil }
func (fakeAPI) Exec(command string, args []string, opts ExecOptions) (*ExecResult, error) {
	return nil, nil
}
func (fakeAPI) EmitCustomEvent(name string, data any) error { return nil }
func (fakeAPI) UI() UI                                      { return nil }
func (fakeAPI) Mode() Mode                                  { return ModePrint }
func (fakeAPI) HasUI() bool                                 { return false }
func (fakeAPI) Cwd() string                                 { return "/work" }
func (fakeAPI) SessionManager() SessionView                 { return nil }
func (fakeAPI) ModelRegistry() ModelRegistry                { return nil }
func (fakeAPI) Model() *Model                               { return nil }
func (fakeAPI) ThinkingLevel() ThinkingLevel                { return ThinkingOff }
func (fakeAPI) Signal() context.Context                     { return nil }
func (fakeAPI) Abort()                                      {}
func (fakeAPI) Shutdown()                                   {}
func (fakeAPI) ContextUsage() *ContextUsage                 { return nil }
func (fakeAPI) Compact(opts CompactOptions)                 {}
func (fakeAPI) SystemPrompt() string                        { return "" }
func (fakeAPI) WaitForIdle() error                          { return nil }
func (fakeAPI) NewSession(opts NewSessionOptions) (*SessionSwitchResult, error) {
	return &SessionSwitchResult{}, nil
}
func (fakeAPI) Fork(entryID string, opts ForkOptions) (*SessionSwitchResult, error) {
	return &SessionSwitchResult{}, nil
}
func (fakeAPI) NavigateTree(targetID string, opts TreeOptions) (*SessionSwitchResult, error) {
	return &SessionSwitchResult{}, nil
}
func (fakeAPI) SwitchSession(path string, opts SwitchOptions) (*SessionSwitchResult, error) {
	return &SessionSwitchResult{}, nil
}
func (fakeAPI) Reload() error { return nil }

func TestToolCallDecisionZeroValueAllows(t *testing.T) {
	if got := (ToolCallDecision{}); got.Block {
		t.Fatal("zero ToolCallDecision must not block")
	}
}

func TestToolCallDecisionFinalArgs(t *testing.T) {
	dec := ToolCallDecision{Block: true, Reason: "denied", FinalArgs: json.RawMessage(`{"path":"safe"}`)}
	if !dec.Block || dec.Reason != "denied" || string(dec.FinalArgs) != `{"path":"safe"}` {
		t.Errorf("decision = %+v, want block with reason and final args", dec)
	}
}

func TestEventTypeConstantsMatchPi(t *testing.T) {
	want := map[string]string{
		EventContext:         "context",
		EventMessageEnd:      "message_end",
		EventAutoRetryStart:  "auto_retry_start",
		EventAutoRetryEnd:    "auto_retry_end",
		EventToolCall:        "tool_call",
		EventToolResult:      "tool_result",
		EventSessionStart:    "session_start",
		EventSessionShutdown: "session_shutdown",
	}
	for constant, piName := range want {
		if constant != piName {
			t.Errorf("event constant = %q, want Pi name %q", constant, piName)
		}
	}
}

func TestToolResultEventResultPartialPatch(t *testing.T) {
	res := Result{Content: []Block{{Type: "text", Text: "original"}}, IsError: true}
	patch := &ToolResultEventResult{Content: []Block{{Type: "text", Text: "patched"}}}
	if patch.IsError != nil {
		t.Fatal("nil IsError must mean 'keep the current value'")
	}
	_ = res
}

type printModeUI struct{}

var _ UI = printModeUI{}

func (printModeUI) Notify(message string, kind NotifyKind) {}
func (printModeUI) Confirm(title, message string) (bool, error) {
	return false, ErrModeUnsupported
}
func (printModeUI) Select(title string, options []string) (string, error) {
	return "", ErrModeUnsupported
}
func (printModeUI) Input(title, placeholder string) (string, error) {
	return "", ErrModeUnsupported
}
func (printModeUI) Editor(title, prefill string) (string, error) {
	return "", ErrModeUnsupported
}
func (printModeUI) SetStatus(key, text string)             {}
func (printModeUI) SetWidget(key string, content []string) {}
func (printModeUI) SetWorkingMessage(message string)       {}
func (printModeUI) SetTitle(title string)                  {}

func TestPrintModeDialogSemantics(t *testing.T) {
	ui := printModeUI{}
	if _, err := ui.Confirm("t", "m"); !errors.Is(err, ErrModeUnsupported) {
		t.Errorf("Confirm error = %v, want ErrModeUnsupported", err)
	}
	if _, err := ui.Select("t", []string{"a"}); !errors.Is(err, ErrModeUnsupported) {
		t.Errorf("Select error = %v, want ErrModeUnsupported", err)
	}
	if _, err := ui.Input("t", ""); !errors.Is(err, ErrModeUnsupported) {
		t.Errorf("Input error = %v, want ErrModeUnsupported", err)
	}
	if _, err := ui.Editor("t", ""); !errors.Is(err, ErrModeUnsupported) {
		t.Errorf("Editor error = %v, want ErrModeUnsupported", err)
	}
}

func TestModelAndUsageShapes(t *testing.T) {
	m := Model{ID: "anthropic/claude-sonnet-4.5", Name: "Claude Sonnet 4.5", Provider: "openrouter"}
	if m.ID == "" || m.Name == "" || m.Provider == "" {
		t.Fatalf("Model fields not populated: %+v", m)
	}
	u := Usage{Input: 1, Output: 2, TotalTokens: 3}
	if u.TotalTokens != 3 {
		t.Errorf("Usage.TotalTokens = %d, want 3", u.TotalTokens)
	}
	b := Block{Type: "toolCall", ID: "call_1", Name: "read", Arguments: json.RawMessage(`{"path":"a.go"}`)}
	if !strings.Contains(string(b.Arguments), "a.go") {
		t.Errorf("Block.Arguments = %s, want the path inside", b.Arguments)
	}
}

func TestBundleShape(t *testing.T) {
	b := Bundle{ID: "digitalygo", Origin: "github.com/digitalygo/smidja", MinimumHarness: "0.1.0"}
	if b.ID != "digitalygo" || b.MinimumHarness != "0.1.0" {
		t.Errorf("Bundle fields not populated: %+v", b)
	}
	bi := BuildInfo{Origin: "github.com/digitalygo/smidja", Version: "0.1.0", Commit: "abc123"}
	if bi.Version != "0.1.0" || bi.Commit != "abc123" {
		t.Errorf("BuildInfo fields not populated: %+v", bi)
	}
}
