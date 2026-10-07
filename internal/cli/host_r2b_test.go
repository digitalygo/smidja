package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

func runR2bHostPrompt(t *testing.T, cwd string, extension sdk.Extension, script ...*agent.AssistantMessage) (*Deps, *capturingClient, *bytes.Buffer, *bytes.Buffer) {
	deps, client, stdout, stderr := runHostPrompt(t, cwd, extension, script...)
	home := t.TempDir()
	deps.Home = func() string { return home }
	return deps, client, stdout, stderr
}

func TestComposedSendMessageIdlePersistsOnceAndExpandsOnlyWhenRequested(t *testing.T) {
	cwd := t.TempDir()
	promptDir := filepath.Join(cwd, ".smidja", "prompts")
	if err := os.MkdirAll(promptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "greet.md"), []byte("Hello $1!"), 0o600); err != nil {
		t.Fatal(err)
	}
	details := map[string]any{"state": "before"}
	extension := &hostHookExtension{}
	extension.startFn = func(ctx sdk.HandlerContext) error {
		if err := ctx.SendMessage(sdk.CustomMessage{Type: "plain", Content: "/prompt greet Alice", Display: false}, sdk.SendOptions{}); err != nil {
			return err
		}
		if err := ctx.SendMessage(sdk.CustomMessage{Type: "expanded", Content: "/prompt greet Alice", Display: false, Details: details}, sdk.SendOptions{ExpandPromptTemplates: true}); err != nil {
			return err
		}
		details["state"] = "after"
		return nil
	}
	deps, client, _, stderr := runR2bHostPrompt(t, cwd, extension, textStop("done"))
	if err := RunWithDeps([]string{"-p", "external prompt", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != 1 {
		t.Fatalf("model calls = %d, want 1", client.calls)
	}
	wireText := joinedRequestUserText(client.reqs[0])
	if !strings.Contains(wireText, "/prompt greet Alice") || !strings.Contains(wireText, "Hello Alice!") {
		t.Fatalf("wire history = %q", wireText)
	}
	loader, err := session.LoadWithOptions(firstSessionPath(t, deps.Store.Root()), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	var customEntries []*session.CustomMessageEntry
	var customIDs []string
	for _, entry := range loader.Entries() {
		if custom, ok := entry.(*session.CustomMessageEntry); ok {
			customEntries = append(customEntries, custom)
			customIDs = append(customIDs, session.EntryID(custom))
		}
	}
	if len(customEntries) != 2 {
		t.Fatalf("custom message entries = %d, want 2", len(customEntries))
	}
	var storedDetails map[string]string
	if err := json.Unmarshal(customEntries[1].Details, &storedDetails); err != nil {
		t.Fatal(err)
	}
	if storedDetails["state"] != "before" {
		t.Fatalf("stored details = %v, want pre-enqueue snapshot", storedDetails)
	}
	transcript, _ := projectTranscript(loader)
	for _, item := range transcript {
		if item.Kind == "custom" {
			t.Fatalf("Display=false custom message reached transcript: %+v", item)
		}
	}
	history, _, err := projectModelHistory(loader)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(messageUserTexts(history), "\n"); !strings.Contains(got, "/prompt greet Alice") || !strings.Contains(got, "Hello Alice!") {
		t.Fatalf("projected model history = %q", got)
	}
	path := firstSessionPath(t, deps.Store.Root())
	resumeClient := &capturingClient{script: []*agent.AssistantMessage{textStop("resumed")}}
	deps.Client = resumeClient
	deps.ExtensionRuntime = nil
	if err := RunWithDeps([]string{"-continue", path, "-p", "resume turn", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps resume: %v", err)
	}
	resumedText := joinedRequestUserText(resumeClient.reqs[0])
	if strings.Count(resumedText, "[custom plain ") != 1 || strings.Count(resumedText, "[custom expanded ") != 1 {
		t.Fatalf("resumed custom message counts = %q", resumedText)
	}
	resumedLoader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	var resumedIDs []string
	for _, entry := range resumedLoader.Entries() {
		if custom, ok := entry.(*session.CustomMessageEntry); ok {
			resumedIDs = append(resumedIDs, session.EntryID(custom))
		}
	}
	if len(resumedIDs) != len(customIDs) {
		t.Fatalf("custom entries after resume = %d, want %d", len(resumedIDs), len(customIDs))
	}
	for i := range customIDs {
		if resumedIDs[i] != customIDs[i] {
			t.Fatalf("custom entry ids after resume = %v, want %v", resumedIDs, customIDs)
		}
	}
}

func TestComposedSendMessageSteerAndSendUserMessageFollowUp(t *testing.T) {
	cwd := t.TempDir()
	extension := &hostHookExtension{contexts: []sdk.HandlerContext{}}
	extension.contextFn = func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
		if call == 0 {
			if err := ctx.SendMessage(sdk.CustomMessage{Type: "steer", Content: "steer payload", Display: false}, sdk.SendOptions{TriggerTurn: true}); err != nil {
				return nil, err
			}
			if err := ctx.SendUserMessage("steer user payload", sdk.SendOptions{DeliverAs: sdk.DeliverySteer}); err != nil {
				return nil, err
			}
			if err := ctx.SendUserMessage("follow-up payload", sdk.SendOptions{DeliverAs: sdk.DeliveryFollowUp}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	deps, client, stdout, stderr := runR2bHostPrompt(t, cwd, extension,
		textStop("first"), textStop("after custom steer"), textStop("after user steer"), textStop("after follow-up"))
	if err := RunWithDeps([]string{"-p", "external", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != 4 {
		t.Fatalf("model calls = %d, want 4", client.calls)
	}
	if got := joinedRequestUserText(client.reqs[1]); !strings.Contains(got, "[custom steer ") || !strings.Contains(got, "steer payload") {
		t.Fatalf("steer request history = %q", got)
	}
	if got := joinedRequestUserText(client.reqs[2]); !strings.Contains(got, "steer user payload") {
		t.Fatalf("user steer request history = %q", got)
	}
	if got := joinedRequestUserText(client.reqs[3]); !strings.Contains(got, "follow-up payload") {
		t.Fatalf("follow-up request history = %q", got)
	}
	if strings.Count(joinedRequestUserText(client.reqs[3]), "follow-up payload") != 1 {
		t.Fatalf("follow-up was duplicated in history: %q", joinedRequestUserText(client.reqs[3]))
	}
	if strings.Contains(stdout.String(), "follow-up payload") {
		t.Fatalf("follow-up user text leaked to print output: %q", stdout.String())
	}
}

func TestComposedSendMessageSteerArrivesAfterToolResults(t *testing.T) {
	cwd := t.TempDir()
	var toolCalls int
	var contextMessages []sdk.Message
	extension := &hostHookExtension{contexts: []sdk.HandlerContext{}}
	extension.contextFn = func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
		if call == 1 && ctx.SessionManager() != nil {
			contextMessages = ctx.SessionManager().Messages()
		}
		return nil, nil
	}
	extension.toolResultFn = func(ctx sdk.HandlerContext, name string) (*sdk.ToolResultEventResult, error) {
		if err := ctx.SendMessage(sdk.CustomMessage{Type: "after-tool", Content: "steer after tool", Display: false}, sdk.SendOptions{TriggerTurn: true}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	deps, client, _, stderr := runR2bHostPrompt(t, cwd, extension, toolUse("call_1", "probe", `{"x":1}`), textStop("done"))
	deps.Tools = []agent.Tool{&probeTool{calls: &toolCalls}}
	if err := RunWithDeps([]string{"-p", "external", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != 2 || toolCalls != 1 {
		t.Fatalf("model calls = %d, tool calls = %d", client.calls, toolCalls)
	}
	messages := client.reqs[1].Messages
	toolResultIndex, steerIndex := -1, -1
	for i, message := range messages {
		if message == nil {
			continue
		}
		if message.ToolResult != nil && message.ToolResult.ToolCallID == "call_1" {
			toolResultIndex = i
		}
		if message.User != nil && strings.Contains(userMessageText(message.User.Content), "steer after tool") {
			steerIndex = i
		}
	}
	if toolResultIndex < 0 || steerIndex <= toolResultIndex {
		t.Fatalf("tool result index %d, steer index %d; history = %#v", toolResultIndex, steerIndex, messages)
	}
	var contextSteers int
	for _, message := range contextMessages {
		for _, block := range message.Content {
			if strings.Contains(block.Text, "steer after tool") {
				contextSteers++
			}
		}
	}
	if contextSteers != 1 {
		t.Fatalf("next-request context contains %d steers, want 1", contextSteers)
	}
	loader, err := session.LoadWithOptions(firstSessionPath(t, deps.Store.Root()), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := projectModelHistory(loader); err != nil {
		t.Fatalf("tool pair projection: %v", err)
	}
}

func TestComposedSendMessageActiveNonTriggerPersistsAtTurnEnd(t *testing.T) {
	extension := &hostHookExtension{contexts: []sdk.HandlerContext{}}
	extension.contextFn = func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
		if call == 0 {
			return nil, ctx.SendMessage(sdk.CustomMessage{Type: "deferred", Content: "deferred payload"}, sdk.SendOptions{})
		}
		return nil, nil
	}
	deps, client, _, stderr := runR2bHostPrompt(t, t.TempDir(), extension, textStop("done"))
	if err := RunWithDeps([]string{"-p", "external", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != 1 || strings.Contains(joinedRequestUserText(client.reqs[0]), "deferred payload") {
		t.Fatalf("model calls = %d, first history = %q", client.calls, joinedRequestUserText(client.reqs[0]))
	}
	loader, err := session.LoadWithOptions(firstSessionPath(t, deps.Store.Root()), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	history, _, err := projectModelHistory(loader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(messageUserTexts(history), "\n"), "deferred payload") {
		t.Fatalf("turn-end model history = %q", strings.Join(messageUserTexts(history), "\n"))
	}
}

func TestComposedSendMessageIdleTriggerStartsContinuation(t *testing.T) {
	extension := &hostHookExtension{}
	extension.startFn = func(ctx sdk.HandlerContext) error {
		return ctx.SendMessage(sdk.CustomMessage{Type: "idle-trigger", Content: "idle continuation payload"}, sdk.SendOptions{TriggerTurn: true})
	}
	deps, client, _, stderr := runR2bHostPrompt(t, t.TempDir(), extension, textStop("continued"), textStop("external"))
	if err := RunWithDeps([]string{"-p", "external prompt", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != 2 {
		t.Fatalf("model calls = %d, want continuation then external turn", client.calls)
	}
	if got := joinedRequestUserText(client.reqs[0]); !strings.Contains(got, "idle continuation payload") || strings.Contains(got, "external prompt") {
		t.Fatalf("idle continuation history = %q", got)
	}
	if got := joinedRequestUserText(client.reqs[1]); !strings.Contains(got, "external prompt") {
		t.Fatalf("external request history = %q", got)
	}
}

func TestComposedSendUserMessageIdleTriggersUserTurnAndWaitsForOwnedWork(t *testing.T) {
	cwd := t.TempDir()
	extension := &hostHookExtension{}
	var captured sdk.HandlerContext
	extension.startFn = func(ctx sdk.HandlerContext) error {
		captured = ctx
		return ctx.SendUserMessage("scheduled idle user", sdk.SendOptions{})
	}
	deps, client, _, stderr := runR2bHostPrompt(t, cwd, extension, textStop("scheduled answer"), textStop("command answer"))
	if err := RunWithDeps([]string{"-p", "command user", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != 2 {
		t.Fatalf("model calls = %d, want scheduled user turn and command turn", client.calls)
	}
	if got := joinedRequestUserText(client.reqs[0]); !strings.Contains(got, "scheduled idle user") {
		t.Fatalf("first request history = %q", got)
	}
	if got := joinedRequestUserText(client.reqs[1]); !strings.Contains(got, "command user") {
		t.Fatalf("second request history = %q", got)
	}
	if err := captured.SendUserMessage("late", sdk.SendOptions{DeliverAs: sdk.DeliverySteer}); err == nil || err.Error() != errHostClosed.Error() {
		t.Fatalf("late SendUserMessage error = %v, want %v", err, errHostClosed)
	}
}

func TestComposedNextTurnWaitsForNextExternalPrompt(t *testing.T) {
	extension := &hostHookExtension{contexts: []sdk.HandlerContext{}}
	extension.contextFn = func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
		if call == 0 {
			return nil, ctx.SendMessage(sdk.CustomMessage{Type: "next", Content: "next turn payload"}, sdk.SendOptions{DeliverAs: sdk.DeliveryNextTurn, TriggerTurn: true})
		}
		return nil, nil
	}
	deps, client, stdout, stderr := runR2bHostPrompt(t, t.TempDir(), extension, textStop("first answer"), textStop("second answer"))
	deps.Stdin = strings.NewReader("first external\nsecond external\n/quit\n")
	if err := RunWithDeps([]string{"-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != 2 {
		t.Fatalf("model calls = %d, want 2", client.calls)
	}
	if got := joinedRequestUserText(client.reqs[0]); strings.Contains(got, "next turn payload") {
		t.Fatalf("next-turn content appeared early: %q", got)
	}
	if got := joinedRequestUserText(client.reqs[1]); !strings.Contains(got, "next turn payload") || !strings.Contains(got, "second external") {
		t.Fatalf("next external request history = %q", got)
	}
	if strings.Count(joinedRequestUserText(client.reqs[1]), "next turn payload") != 1 {
		t.Fatalf("next-turn content was duplicated: %q", joinedRequestUserText(client.reqs[1]))
	}
	if !strings.Contains(stdout.String(), "first answer") || !strings.Contains(stdout.String(), "second answer") {
		t.Fatalf("repl output = %q", stdout.String())
	}
	loader, err := session.LoadWithOptions(firstSessionPath(t, deps.Store.Root()), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range loader.Entries() {
		if custom, ok := entry.(*session.CustomMessageEntry); ok && custom.CustomType == "next" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("persisted next-turn messages = %d, want 1", count)
	}
}

func TestComposedMessageDeliveryValidationErrors(t *testing.T) {
	var got []error
	extension := &hostHookExtension{contexts: []sdk.HandlerContext{}}
	extension.contextFn = func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
		if call == 0 {
			got = append(got,
				ctx.SendMessage(sdk.CustomMessage{Type: "x"}, sdk.SendOptions{DeliverAs: "unknown"}),
				ctx.SendMessage(sdk.CustomMessage{}, sdk.SendOptions{}),
				ctx.SendUserMessage(" \t", sdk.SendOptions{}),
				ctx.SendUserMessage("active without mode", sdk.SendOptions{}),
				ctx.SendMessage(sdk.CustomMessage{Type: "details", Details: func() {}}, sdk.SendOptions{}),
			)
		}
		return nil, nil
	}
	deps, client, _, stderr := runR2bHostPrompt(t, t.TempDir(), extension, textStop("done"))
	if err := RunWithDeps([]string{"-p", "external", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != 1 || len(got) != 5 {
		t.Fatalf("model calls = %d, errors = %v", client.calls, got)
	}
	want := []string{
		"extensions: unknown delivery mode \"unknown\"",
		errHostMessageType.Error(),
		errHostUserText.Error(),
		errHostActiveUserMode.Error(),
	}
	for i, message := range want {
		if got[i] == nil || got[i].Error() != message {
			t.Errorf("error %d = %v, want %q", i, got[i], message)
		}
	}
	if got[4] == nil || !strings.Contains(got[4].Error(), "SendMessage details must be valid JSON") {
		t.Errorf("details error = %v", got[4])
	}
}

func TestHostMessageContextRejectsCanceledStaleAndClosedCalls(t *testing.T) {
	emptyContext := &hostHandlerContext{}
	if err := emptyContext.SendMessage(sdk.CustomMessage{Type: "empty"}, sdk.SendOptions{}); err != errHostClosed {
		t.Fatalf("empty context SendMessage error = %v", err)
	}
	if err := emptyContext.SendUserMessage("empty", sdk.SendOptions{}); err != errHostClosed {
		t.Fatalf("empty context SendUserMessage error = %v", err)
	}
	cwd := t.TempDir()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"first"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := second.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"second"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(first, &sessionRecorder{first}, first.ID(), first.Path(), cwd, "")
	api := extensions.NewAPI(extensions.APIOptions{Host: host.hostOptions()})
	host.bindAPI(api)
	stale := host.context()
	host.bindSession(second, &sessionRecorder{second}, second.ID(), second.Path(), cwd, "")
	if err := stale.SendMessage(sdk.CustomMessage{Type: "stale", Content: "must not write"}, sdk.SendOptions{}); err == nil || err.Error() != errHostStaleSession.Error() {
		t.Fatalf("stale SendMessage error = %v, want %v", err, errHostStaleSession)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	bound := host.context().(*hostHandlerContext).WithSignal(cancelCtx)
	cancel()
	if err := bound.SendUserMessage("canceled", sdk.SendOptions{DeliverAs: sdk.DeliverySteer}); err == nil || !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), errHostMessageCanceled.Error()) {
		t.Fatalf("canceled SendUserMessage error = %v", err)
	}
	host.shutdown()
	if err := api.SendMessage(sdk.CustomMessage{Type: "closed", Content: "must not write"}, sdk.SendOptions{}); err == nil || err.Error() != errHostClosed.Error() {
		t.Fatalf("closed SendMessage error = %v, want %v", err, errHostClosed)
	}
	first.Close()
	second.Close()
	for _, path := range []string{first.Path(), second.Path()} {
		loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range loader.Entries() {
			if custom, ok := entry.(*session.CustomMessageEntry); ok && strings.Contains(string(custom.Content), "must not write") {
				t.Fatalf("message leaked to session %s: %s", path, custom.Content)
			}
		}
	}
}

func TestConcurrentComposedSteeringMessagesArePersistedOnce(t *testing.T) {
	const producers = 8
	clientScript := make([]*agent.AssistantMessage, producers+1)
	for i := range clientScript {
		clientScript[i] = textStop(fmt.Sprintf("answer %d", i))
	}
	extension := &hostHookExtension{contexts: []sdk.HandlerContext{}}
	var sendErrors []error
	extension.contextFn = func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
		if call != 0 {
			return nil, nil
		}
		var mu sync.Mutex
		var group sync.WaitGroup
		for i := 0; i < producers; i++ {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				err := ctx.SendMessage(sdk.CustomMessage{Type: "concurrent", Content: fmt.Sprintf("payload-%d", index)}, sdk.SendOptions{TriggerTurn: true})
				mu.Lock()
				sendErrors = append(sendErrors, err)
				mu.Unlock()
			}(i)
		}
		group.Wait()
		return nil, nil
	}
	deps, client, _, stderr := runR2bHostPrompt(t, t.TempDir(), extension, clientScript...)
	if err := RunWithDeps([]string{"-p", "external", "-model", "test/model"}, deps); err != nil {
		t.Fatalf("RunWithDeps: %v (stderr %q)", err, stderr.String())
	}
	if client.calls != producers+1 {
		t.Fatalf("model calls = %d, want %d", client.calls, producers+1)
	}
	for _, err := range sendErrors {
		if err != nil {
			t.Errorf("concurrent SendMessage: %v", err)
		}
	}
	last := joinedRequestUserText(client.reqs[len(client.reqs)-1])
	for i := 0; i < producers; i++ {
		if strings.Count(last, fmt.Sprintf("payload-%d", i)) != 1 {
			t.Errorf("payload-%d missing or duplicated in final request: %q", i, last)
		}
	}
}

func TestScheduledLoopRejectsStaleTurnsAndRunsContinuations(t *testing.T) {
	cwd := t.TempDir()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	history := []*agent.Message{{User: &agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"existing"`)}}}
	host.setMessages(history)
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("continued")}}
	deps := &runDeps{host: host, model: "test/model", client: client}
	loop := &agent.LoopDeps{Client: client}
	stale := hostScheduledTurn{generation: host.generation + 1}
	if got, err := runTurn(context.Background(), deps, loop, history, "not delivered", stale); err != nil || len(got) != 1 {
		t.Fatalf("stale RunTurn = %v, %v", got, err)
	}
	if got, err := runContinuation(context.Background(), deps, loop, history, stale); err != nil || len(got) != 1 {
		t.Fatalf("stale ContinueTurn = %v, %v", got, err)
	}
	if client.calls != 0 {
		t.Fatalf("stale model calls = %d, want 0", client.calls)
	}
	current := hostScheduledTurn{generation: host.generation}
	got, err := runContinuation(context.Background(), deps, loop, history, current)
	if err != nil {
		t.Fatalf("runContinuation: %v", err)
	}
	if client.calls != 1 || len(got) != 2 || got[1].Assistant == nil {
		t.Fatalf("continuation calls = %d, history = %+v", client.calls, got)
	}
	host.shutdown()
}

type hostScriptClient struct {
	replies    []*agent.AssistantMessage
	beforeCall func(int)
	calls      int
	requests   []*agent.TurnRequest
}

func (c *hostScriptClient) StreamTurn(_ context.Context, request *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	call := c.calls
	c.calls++
	c.requests = append(c.requests, request)
	if c.beforeCall != nil {
		c.beforeCall(call)
	}
	if call >= len(c.replies) {
		return nil, errors.New("hostScriptClient: script exhausted")
	}
	reply := c.replies[call]
	for _, block := range reply.Content {
		switch block.Type {
		case agent.BlockTypeText:
			if onText != nil {
				onText(block.Text)
			}
		case agent.BlockTypeThinking:
			if onThinking != nil {
				onThinking(block.Thinking)
			}
		}
	}
	return reply, nil
}

type hostAssistantHookRecorder struct {
	agent.Recorder
	afterAppend func(*agent.AssistantMessage)
}

func (r *hostAssistantHookRecorder) AppendAssistant(message *agent.AssistantMessage) error {
	if err := r.Recorder.AppendAssistant(message); err != nil {
		return err
	}
	if r.afterAppend != nil {
		r.afterAppend(message)
	}
	return nil
}

func TestLateSDKMessagesAfterFinalStopPollQueueOwnedContinuation(t *testing.T) {
	cwd := t.TempDir()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	api := extensions.NewAPI(extensions.APIOptions{Host: host.hostOptions()})
	host.bindAPI(api)
	jobs := make(chan func(), 4)
	hookContext := host.context().(*hostHandlerContext)
	turnCtx, cancelTurn := context.WithCancel(context.Background())
	defer cancelTurn()
	hookContext = hookContext.WithSignal(turnCtx).(*hostHandlerContext)
	client := &hostScriptClient{
		replies: []*agent.AssistantMessage{
			textStop("initial answer"),
			textStop("after first steer"),
			textStop("after late steer"),
			textStop("after late follow-up"),
			textStop("finished"),
		},
	}
	deps := &runDeps{host: host, model: "test/model", client: client, recorder: &sessionRecorder{sess}, stdout: io.Discard}
	loop := loopDeps(deps, io.Discard)
	finalPoll := make(chan struct{})
	releasePoll := make(chan struct{})
	var pollOnce sync.Once
	mailbox := loop.Mailbox
	loop.Mailbox = func(ctx context.Context, history []*agent.Message, stopping, external bool) (agent.MailboxResult, error) {
		result, err := mailbox(ctx, history, stopping, external)
		if stopping && err == nil && !result.Delivered {
			pollOnce.Do(func() {
				close(finalPoll)
				<-releasePoll
			})
		}
		return result, err
	}
	var scheduledRuns int
	var scheduledErr error
	host.setLifecycle(hostLifecycle{
		dispatch: func(job func()) bool {
			select {
			case jobs <- job:
				return true
			default:
				return false
			}
		},
		runScheduled: func(turn hostScheduledTurn) {
			scheduledRuns++
			history, _ := host.modelHistorySnapshot()
			updated, err := runContinuation(context.Background(), deps, loop, history, turn)
			scheduledErr = err
			if updated != nil {
				host.setMessages(updated)
			}
		},
	})
	if err := api.SendMessage(sdk.CustomMessage{Type: "first-steer", Content: "first steer payload"}, sdk.SendOptions{TriggerTurn: true}); err != nil {
		t.Fatalf("first SDK steer: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("initial idle steer dispatched %d FIFO jobs, want 1", len(jobs))
	}
	type turnResult struct {
		history []*agent.Message
		err     error
	}
	turnDone := make(chan turnResult, 1)
	go func() {
		history, err := runTurn(turnCtx, deps, loop, nil, "external prompt")
		turnDone <- turnResult{history: history, err: err}
	}()
	select {
	case <-finalPoll:
	case <-time.After(5 * time.Second):
		close(releasePoll)
		t.Fatal("the final stop poll did not reach the barrier")
	}
	host.mailboxMu.Lock()
	tokenAtBoundary := host.mailbox.continuationToken
	host.mailboxMu.Unlock()
	if tokenAtBoundary < 2 {
		close(releasePoll)
		t.Fatalf("the first steer did not invalidate its original token: %d", tokenAtBoundary)
	}
	if err := api.SendMessage(sdk.CustomMessage{Type: "late-steer", Content: "late steer payload"}, sdk.SendOptions{TriggerTurn: true}); err != nil {
		close(releasePoll)
		t.Fatalf("late SDK steer: %v", err)
	}
	if err := hookContext.SendUserMessage("late follow-up payload", sdk.SendOptions{DeliverAs: sdk.DeliveryFollowUp}); err != nil {
		close(releasePoll)
		t.Fatalf("late SDK follow-up: %v", err)
	}
	if len(jobs) != 1 {
		close(releasePoll)
		t.Fatalf("late in-turn messages changed queued FIFO jobs to %d before endTurn, want the stale job only", len(jobs))
	}
	close(releasePoll)
	firstTurn := <-turnDone
	if firstTurn.err != nil {
		t.Fatalf("initial turn: %v", firstTurn.err)
	}
	host.setMessages(firstTurn.history)
	var staleJob func()
	select {
	case staleJob = <-jobs:
	case <-time.After(5 * time.Second):
		t.Fatal("the invalidated FIFO job disappeared")
	}
	staleJob()
	if scheduledRuns != 0 {
		t.Fatalf("stale job ran %d scheduled turns, want 0", scheduledRuns)
	}
	var job func()
	select {
	case job = <-jobs:
	case <-time.After(5 * time.Second):
		t.Fatal("stale-token cleanup did not dispatch the newer owned continuation")
	}
	if job == nil {
		t.Fatal("the FIFO dispatcher received a nil continuation job")
	}
	select {
	case <-jobs:
		t.Fatal("endTurn dispatched duplicate continuations")
	default:
	}
	job()
	if scheduledRuns != 1 || scheduledErr != nil {
		t.Fatalf("scheduled runs = %d, error = %v", scheduledRuns, scheduledErr)
	}
	if client.calls != 3 {
		var requests []string
		for _, request := range client.requests {
			requests = append(requests, joinedRequestUserText(request))
		}
		t.Fatalf("wire calls = %d, want one initial request and two late-delivery requests; requests=%q", client.calls, requests)
	}
	lastWire := joinedRequestUserText(client.requests[len(client.requests)-1])
	for _, text := range []string{"first steer payload", "late steer payload", "late follow-up payload"} {
		if strings.Count(lastWire, text) != 1 {
			t.Errorf("last wire request has %d copies of %q: %q", strings.Count(lastWire, text), text, lastWire)
		}
	}
	if strings.Index(lastWire, "late steer payload") > strings.Index(lastWire, "late follow-up payload") {
		t.Fatalf("follow-up overtook the late steer in FIFO history: %q", lastWire)
	}
	select {
	case <-jobs:
		t.Fatal("the completed continuation left an extra FIFO job")
	default:
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	history, entryIDs, _, err := projectModelHistoryWithIDs(loader)
	if err != nil {
		t.Fatalf("project persisted history: %v", err)
	}
	if len(history) != len(entryIDs) {
		t.Fatalf("projected history has %d messages and %d entry ids", len(history), len(entryIDs))
	}
	customCounts := map[string]int{}
	followUpCount := 0
	for _, entry := range loader.Entries() {
		if custom, ok := entry.(*session.CustomMessageEntry); ok {
			customCounts[custom.CustomType]++
		}
		if message, ok := entry.(*session.MessageEntry); ok {
			decoded, decodeErr := message.DecodeMessage()
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if decoded.User != nil && strings.Contains(userMessageText(decoded.User.Content), "late follow-up payload") {
				followUpCount++
			}
		}
	}
	for _, messageType := range []string{"first-steer", "late-steer"} {
		if customCounts[messageType] != 1 {
			t.Errorf("persisted %s entries = %d, want 1", messageType, customCounts[messageType])
		}
	}
	if followUpCount != 1 {
		t.Errorf("persisted late follow-up messages = %d, want 1", followUpCount)
	}
	host.shutdown()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	resumeDeps, resumeClient, _, resumeStderr := runHostPrompt(t, cwd, nil, textStop("resumed"))
	resumeDeps.Store = store
	resumeDeps.Client = resumeClient
	if err := RunWithDeps([]string{"-p", "resume prompt", "-continue", sess.Path(), "-model", "test/model"}, resumeDeps); err != nil {
		t.Fatalf("resume: %v (stderr %q)", err, resumeStderr.String())
	}
	resumeWire := joinedRequestUserText(resumeClient.reqs[0])
	for _, text := range []string{"first steer payload", "late steer payload", "late follow-up payload"} {
		if strings.Count(resumeWire, text) != 1 {
			t.Errorf("resume wire request has %d copies of %q: %q", strings.Count(resumeWire, text), text, resumeWire)
		}
	}
}

func TestLateSDKMessagesOnProviderErrorPathQueueContinuation(t *testing.T) {
	cwd := t.TempDir()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	api := extensions.NewAPI(extensions.APIOptions{Host: host.hostOptions()})
	host.bindAPI(api)
	jobs := make(chan func(), 2)
	hookContext := host.context().(*hostHandlerContext)
	errorReply := &agent.AssistantMessage{
		Role:         string(agent.RoleAssistant),
		StopReason:   "error",
		ErrorMessage: "provider failed",
		Timestamp:    agent.NowMillis(),
	}
	client := &hostScriptClient{
		replies: []*agent.AssistantMessage{errorReply, textStop("after steer"), textStop("after follow-up")},
	}
	recorder := &hostAssistantHookRecorder{
		Recorder: &sessionRecorder{sess},
		afterAppend: func(message *agent.AssistantMessage) {
			if message.StopReason != "error" {
				return
			}
			if err := api.SendMessage(sdk.CustomMessage{Type: "error-steer", Content: "error-path steer"}, sdk.SendOptions{TriggerTurn: true}); err != nil {
				t.Errorf("SDK steer on provider error: %v", err)
			}
			if err := hookContext.SendUserMessage("error-path follow-up", sdk.SendOptions{DeliverAs: sdk.DeliveryFollowUp}); err != nil {
				t.Errorf("SDK follow-up on provider error: %v", err)
			}
		},
	}
	deps := &runDeps{host: host, model: "test/model", client: client, recorder: recorder, stdout: io.Discard}
	loop := &agent.LoopDeps{Client: client, Recorder: recorder, Mailbox: host.mailboxBoundary}
	var scheduledRuns int
	var scheduledErr error
	host.setLifecycle(hostLifecycle{
		dispatch: func(job func()) bool {
			select {
			case jobs <- job:
				return true
			default:
				return false
			}
		},
		runScheduled: func(turn hostScheduledTurn) {
			scheduledRuns++
			history, _ := host.modelHistorySnapshot()
			updated, err := runContinuation(context.Background(), deps, loop, history, turn)
			scheduledErr = err
			if updated != nil {
				host.setMessages(updated)
			}
		},
	})
	_, turnErr := runTurn(context.Background(), deps, loop, nil, "external prompt")
	if turnErr == nil || !strings.Contains(turnErr.Error(), "provider failed") {
		t.Fatalf("provider error = %v, want provider failure", turnErr)
	}
	job := <-jobs
	job()
	if scheduledRuns != 1 || scheduledErr != nil {
		t.Fatalf("scheduled runs = %d, error = %v", scheduledRuns, scheduledErr)
	}
	if client.calls != 3 {
		t.Fatalf("wire calls = %d, want provider error plus two delivery requests", client.calls)
	}
	lastWire := joinedRequestUserText(client.requests[len(client.requests)-1])
	if strings.Count(lastWire, "error-path steer") != 1 || strings.Count(lastWire, "error-path follow-up") != 1 {
		t.Fatalf("error-path messages were not projected exactly once: %q", lastWire)
	}
	loader, err := session.LoadWithOptions(sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := projectModelHistoryWithIDs(loader); err != nil {
		t.Fatalf("project error-path history: %v", err)
	}
	host.shutdown()
}

func TestEndTurnDropsLateSDKDeliveriesAfterCancelSessionChangeAndShutdown(t *testing.T) {
	for _, scenario := range []string{"cancel", "session-change", "shutdown"} {
		t.Run(scenario, func(t *testing.T) {
			cwd := t.TempDir()
			store, err := session.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			first, err := store.Create(cwd)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			second, err := store.Create(cwd)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
			host.bindSession(first, &sessionRecorder{first}, first.ID(), first.Path(), cwd, "")
			jobs := make(chan func(), 4)
			var scheduledRuns int
			lifecycle := hostLifecycle{
				dispatch: func(job func()) bool {
					select {
					case jobs <- job:
						return true
					default:
						return false
					}
				},
				runScheduled: func(hostScheduledTurn) { scheduledRuns++ },
			}
			if scenario != "cancel" {
				host.setLifecycle(lifecycle)
			}
			api := extensions.NewAPI(extensions.APIOptions{Host: host.hostOptions()})
			host.bindAPI(api)
			if err := api.SendMessage(sdk.CustomMessage{Type: "initial-invalidated", Content: "must not persist"}, sdk.SendOptions{TriggerTurn: true}); err != nil {
				t.Fatal(err)
			}
			turnSignal, cancelTurn := context.WithCancel(context.Background())
			defer cancelTurn()
			hookContext := host.context().(*hostHandlerContext).WithSignal(turnSignal).(*hostHandlerContext)
			host.beginTurn()
			if err := hookContext.SendMessage(sdk.CustomMessage{Type: "next", Content: "next external"}, sdk.SendOptions{DeliverAs: sdk.DeliveryNextTurn, TriggerTurn: true}); err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			type sendResult struct {
				customErr error
				userErr   error
			}
			sendDone := make(chan sendResult, 1)
			endDone := make(chan error, 1)
			go func() {
				<-start
				customErr := hookContext.SendMessage(sdk.CustomMessage{Type: "late", Content: "must not persist"}, sdk.SendOptions{TriggerTurn: true})
				userErr := hookContext.SendUserMessage("must not persist", sdk.SendOptions{DeliverAs: sdk.DeliveryFollowUp})
				sendDone <- sendResult{customErr: customErr, userErr: userErr}
			}()
			go func() {
				<-start
				var err error
				switch scenario {
				case "cancel":
					cancelTurn()
					err = host.endTurn(true)
				case "session-change":
					host.bindSession(second, &sessionRecorder{second}, second.ID(), second.Path(), cwd, "")
					err = host.endTurn(false)
				case "shutdown":
					host.shutdown()
					err = host.endTurn(false)
				}
				endDone <- err
			}()
			close(start)
			sends := <-sendDone
			if err := <-endDone; err != nil {
				t.Fatal(err)
			}
			if scenario == "cancel" {
				host.setLifecycle(lifecycle)
			}
			for _, err := range []error{sends.customErr, sends.userErr} {
				if err == nil {
					continue
				}
				switch scenario {
				case "cancel":
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled send error = %v, want context.Canceled", err)
					}
				case "session-change":
					if err != errHostStaleSession {
						t.Fatalf("session-change send error = %v, want %v", err, errHostStaleSession)
					}
				case "shutdown":
					if err != errHostClosed {
						t.Fatalf("shutdown send error = %v, want %v", err, errHostClosed)
					}
				}
			}
			host.mailboxMu.Lock()
			steerCount := len(host.mailbox.steer)
			followUpCount := len(host.mailbox.followUp)
			nextTurnCount := len(host.mailbox.nextTurn)
			continuationQueued := host.mailbox.continuationQueued
			host.mailboxMu.Unlock()
			if steerCount != 0 || followUpCount != 0 || continuationQueued {
				t.Fatalf("mailbox retained steer=%d follow-up=%d continuation=%v", steerCount, followUpCount, continuationQueued)
			}
			if scenario == "cancel" && nextTurnCount != 1 {
				t.Fatalf("next-turn messages after normal abort = %d, want 1", nextTurnCount)
			}
			if scenario != "cancel" && nextTurnCount != 0 {
				t.Fatalf("next-turn messages after %s = %d, want 0", scenario, nextTurnCount)
			}
			var staleJob func()
			select {
			case staleJob = <-jobs:
				if scenario == "cancel" {
					t.Fatal("canceled continuation was dispatched after its lifecycle was installed")
				}
			default:
				if scenario != "cancel" {
					t.Fatal("the original continuation job disappeared")
				}
			}
			if staleJob != nil {
				staleJob()
			}
			if scheduledRuns != 0 {
				t.Fatalf("invalidated continuation ran %d scheduled turns", scheduledRuns)
			}
			select {
			case <-jobs:
				t.Fatal("invalidated late messages scheduled another continuation")
			default:
			}
			for _, path := range []string{first.Path(), second.Path()} {
				if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
					continue
				} else if err != nil {
					t.Fatal(err)
				}
				loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range loader.Entries() {
					if custom, ok := entry.(*session.CustomMessageEntry); ok && (custom.CustomType == "initial-invalidated" || custom.CustomType == "late" || custom.CustomType == "next") {
						t.Fatalf("late custom message %q was persisted in %s", custom.CustomType, path)
					}
					if message, ok := entry.(*session.MessageEntry); ok {
						decoded, decodeErr := message.DecodeMessage()
						if decodeErr != nil {
							t.Fatal(decodeErr)
						}
						if decoded.User != nil && strings.Contains(userMessageText(decoded.User.Content), "must not persist") {
							t.Fatalf("late user message was persisted in %s", path)
						}
					}
				}
			}
			host.shutdown()
		})
	}
}

func TestHostMessageExpansionAndCanceledRunErrors(t *testing.T) {
	cwd := t.TempDir()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	api := extensions.NewAPI(extensions.APIOptions{Host: host.hostOptions()})
	if err := api.SendMessage(sdk.CustomMessage{Type: "expand"}, sdk.SendOptions{ExpandPromptTemplates: true}); err != errHostPromptUnavailable {
		t.Fatalf("missing prompt expander error = %v", err)
	}
	host.setPromptExpander(func(string) (string, error) { return "", errors.New("prompt lookup failed") })
	if err := api.SendUserMessage("/prompt absent", sdk.SendOptions{ExpandPromptTemplates: true}); err == nil || err.Error() != "prompt lookup failed" {
		t.Fatalf("prompt expansion error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	host.bindRunContext(canceled, cancel)
	cancel()
	if err := api.SendMessage(sdk.CustomMessage{Type: "canceled"}, sdk.SendOptions{}); err == nil || !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), errHostMessageCanceled.Error()) {
		t.Fatalf("canceled run SendMessage error = %v", err)
	}
	if _, err := host.mailboxBoundary(canceled, nil, false, false); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled mailbox boundary error = %v", err)
	}
	host.shutdown()
}

func TestHostOnlyDeliversVisibleCustomMessagesToRenderer(t *testing.T) {
	cwd := t.TempDir()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	jobs := make(chan func(), 3)
	var rendered []*session.CustomMessageEntry
	var scheduled []hostScheduledTurn
	host.setLifecycle(hostLifecycle{
		dispatch: func(job func()) bool {
			jobs <- job
			return true
		},
		message: func(entry *session.CustomMessageEntry) {
			rendered = append(rendered, entry)
		},
		runScheduled: func(turn hostScheduledTurn) {
			scheduled = append(scheduled, turn)
		},
	})
	api := extensions.NewAPI(extensions.APIOptions{Host: host.hostOptions()})
	if err := api.SendMessage(sdk.CustomMessage{Type: "hidden", Content: "model only"}, sdk.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-jobs:
		job()
		t.Fatal("Display=false message was sent to the renderer")
	default:
	}
	if err := api.SendMessage(sdk.CustomMessage{Type: "visible", Content: "show me", Display: true}, sdk.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-jobs:
		job()
	default:
		t.Fatal("Display=true message was not sent to the renderer")
	}
	if len(rendered) != 1 || rendered[0].CustomType != "visible" {
		t.Fatalf("rendered messages = %+v", rendered)
	}
	if err := api.SendUserMessage("api user turn", sdk.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-jobs:
		job()
	default:
		t.Fatal("API SendUserMessage did not schedule a user turn")
	}
	if len(scheduled) != 1 || !scheduled[0].external || scheduled[0].text != "api user turn" {
		t.Fatalf("scheduled turns = %+v", scheduled)
	}
	host.shutdown()
}

type editorFollowUpClient struct {
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
	reqs    []*agent.TurnRequest
}

func (c *editorFollowUpClient) StreamTurn(ctx context.Context, req *agent.TurnRequest, onText func(string), onThinking func(string)) (*agent.AssistantMessage, error) {
	c.mu.Lock()
	call := c.calls
	c.calls++
	c.reqs = append(c.reqs, req)
	c.mu.Unlock()
	if call == 0 {
		close(c.entered)
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return textStop("initial answer"), nil
	}
	return textStop("follow-up answer"), nil
}

func TestTUIHostScheduledContinuationAndUserTurnUseOwnedWorker(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	host := newHostRuntime(context.Background(), fixture.cwd, nil, fixture.bridge.rd.catalog)
	host.bindSession(fixture.sess, &sessionRecorder{fixture.sess}, fixture.sess.ID(), fixture.sess.Path(), fixture.cwd, "")
	fixture.bridge.rd.host = host
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("initial"), textStop("continuation"), textStop("user turn")}}
	fixture.bridge.rd.client = client
	host.setLifecycle(fixture.bridge.tuiHostLifecycle(fixture.runner))
	api := extensions.NewAPI(extensions.APIOptions{Host: host.hostOptions()})
	fixture.bridge.rd.host.bindAPI(api)
	t.Cleanup(func() {
		fixture.bridge.shutdown()
		fixture.bridge.wait()
	})
	fixture.bridge.submit("initial prompt")
	waitBridgeQueue(t, fixture.bridge)
	if client.calls != 1 {
		t.Fatalf("initial model calls = %d, want 1", client.calls)
	}
	if err := api.SendMessage(sdk.CustomMessage{Type: "visible", Content: "scheduled custom", Display: true}, sdk.SendOptions{TriggerTurn: true}); err != nil {
		t.Fatal(err)
	}
	waitBridgeQueue(t, fixture.bridge)
	if client.calls != 2 || !strings.Contains(joinedRequestUserText(client.reqs[1]), "scheduled custom") {
		t.Fatalf("continuation calls = %d, history = %q", client.calls, joinedRequestUserText(client.reqs[1]))
	}
	if err := api.SendUserMessage("scheduled user", sdk.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	waitBridgeQueue(t, fixture.bridge)
	if client.calls != 3 || !strings.Contains(joinedRequestUserText(client.reqs[2]), "scheduled user") {
		t.Fatalf("user turn calls = %d, history = %q", client.calls, joinedRequestUserText(client.reqs[2]))
	}
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, "visible") || !strings.Contains(frame, "scheduled user") {
		t.Fatalf("scheduled messages missing from TUI:\n%s", frame)
	}
}

func waitBridgeQueue(t *testing.T, bridge *tuiBridge) {
	t.Helper()
	done := make(chan struct{})
	if !bridge.lifecycle.enqueue(func() { close(done) }) {
		t.Fatal("bridge lifecycle rejected a completion barrier")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bridge lifecycle did not process its completion barrier")
	}
}

type failedContinuationClient struct{}

func (failedContinuationClient) StreamTurn(context.Context, *agent.TurnRequest, func(string), func(string)) (*agent.AssistantMessage, error) {
	return nil, errors.New("scheduled provider failed")
}

func TestScheduledContinuationReportsProviderFailure(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	host := newHostRuntime(context.Background(), fixture.cwd, nil, fixture.bridge.rd.catalog)
	host.bindSession(fixture.sess, &sessionRecorder{fixture.sess}, fixture.sess.ID(), fixture.sess.Path(), fixture.cwd, "")
	fixture.bridge.rd.host = host
	fixture.bridge.rd.client = failedContinuationClient{}
	t.Cleanup(func() {
		fixture.bridge.shutdown()
		fixture.bridge.wait()
	})
	fixture.bridge.runScheduledContinuation(hostScheduledTurn{generation: host.generation})
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, "scheduled provider failed") {
		t.Fatalf("scheduled failure missing from TUI:\n%s", frame)
	}
}

func TestCanceledScheduledContinuationSettlesAsInterrupted(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	host := newHostRuntime(context.Background(), fixture.cwd, nil, fixture.bridge.rd.catalog)
	host.bindSession(fixture.sess, &sessionRecorder{fixture.sess}, fixture.sess.ID(), fixture.sess.Path(), fixture.cwd, "")
	fixture.bridge.rd.host = host
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	fixture.bridge.ctx = canceled
	t.Cleanup(func() {
		fixture.bridge.shutdown()
		fixture.bridge.wait()
	})
	fixture.bridge.runScheduledContinuation(hostScheduledTurn{generation: host.generation})
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, "interrupted") {
		t.Fatalf("canceled scheduled continuation missing from TUI:\n%s", frame)
	}
}

func TestBridgeEditorFollowUpSchedulerSettlesClosedHost(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	host := newHostRuntime(context.Background(), fixture.cwd, nil, fixture.bridge.rd.catalog)
	host.bindSession(fixture.sess, &sessionRecorder{fixture.sess}, fixture.sess.ID(), fixture.sess.Path(), fixture.cwd, "")
	fixture.bridge.rd.host = host
	host.shutdown()
	t.Cleanup(func() {
		fixture.bridge.shutdown()
		fixture.bridge.wait()
	})
	fixture.runner.Surface().Editor().SetText("queued follow-up")
	fixture.runner.Surface().Editor().HandleInput("\x1b\r")
	fixture.bridge.scheduleEditorFollowUps()
	waitBridgeQueue(t, fixture.bridge)
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, errHostClosed.Error()) {
		t.Fatalf("closed-host warning missing:\n%s", frame)
	}
}

func TestBridgeAltEnterFollowUpDeliveredThroughMailbox(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	host := newHostRuntime(context.Background(), fixture.cwd, nil, fixture.bridge.rd.catalog)
	host.bindSession(fixture.sess, &sessionRecorder{fixture.sess}, fixture.sess.ID(), fixture.sess.Path(), fixture.cwd, "")
	host.setLifecycle(hostLifecycle{userMessage: func(text string) { fixture.runner.Surface().AddUserMessage(text) }})
	fixture.bridge.rd.host = host
	client := &editorFollowUpClient{entered: make(chan struct{}), release: make(chan struct{})}
	fixture.bridge.rd.client = client
	turns := make(chan struct{}, 1)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	t.Cleanup(func() {
		fixture.bridge.shutdown()
		fixture.bridge.wait()
	})
	fixture.bridge.submit("initial prompt")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("initial turn did not start")
	}
	fixture.runner.Surface().Editor().SetText("queued follow-up")
	fixture.runner.Surface().Editor().HandleInput("\x1b\r")
	if fixture.runner.Surface().Editor().QueuedCount() != 1 {
		t.Fatal("Alt+Enter follow-up was not queued")
	}
	close(client.release)
	select {
	case <-turns:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
	if client.calls != 2 {
		t.Fatalf("model calls = %d, want initial and queued follow-up", client.calls)
	}
	if got := joinedRequestUserText(client.reqs[1]); !strings.Contains(got, "queued follow-up") {
		t.Fatalf("follow-up request history = %q", got)
	}
	loader, err := session.LoadWithOptions(fixture.sess.Path(), session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	history, _, err := projectModelHistory(loader)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.Join(messageUserTexts(history), "\n"), "queued follow-up") != 1 {
		t.Fatalf("projected history = %q", strings.Join(messageUserTexts(history), "\n"))
	}
}

func TestBridgeSessionSwitchStillDropsQueuedEditorFollowUp(t *testing.T) {
	fixture := newSessionBridgeFixture(t)
	host := newHostRuntime(context.Background(), fixture.cwd, fixture.bridge.sessions, fixture.bridge.rd.catalog)
	current := fixture.bridge.sessions.Current()
	host.bindSession(current.sess, current.recorder, current.sess.ID(), current.path, fixture.cwd, current.name)
	fixture.bridge.rd.host = host
	host.setLifecycle(fixture.bridge.tuiHostLifecycle(fixture.runner))
	other, err := fixture.store.Create(fixture.cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.AppendUser(&agent.UserMessage{Role: string(agent.RoleUser), Content: json.RawMessage(`"prior question"`), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if err := other.AppendAssistant(&agent.AssistantMessage{Role: string(agent.RoleAssistant), Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: "prior answer"}}, StopReason: "stop", Timestamp: 2}); err != nil {
		t.Fatal(err)
	}
	otherPath := other.Path()
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	client := &editorFollowUpClient{entered: make(chan struct{}), release: make(chan struct{})}
	fixture.bridge.rd.client = client
	turns := make(chan struct{}, 2)
	fixture.bridge.afterTurn = func() { turns <- struct{}{} }
	t.Cleanup(func() {
		fixture.bridge.shutdown()
		fixture.bridge.wait()
	})
	fixture.bridge.submit("current session prompt")
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("current-session turn did not start")
	}
	fixture.runner.Surface().Editor().SetText("queued follow-up")
	fixture.runner.Surface().Editor().HandleInput("\x1b\r")
	fixture.bridge.submit("/resume " + otherPath)
	close(client.release)
	for i := 0; i < 2; i++ {
		select {
		case <-turns:
		case <-time.After(5 * time.Second):
			t.Fatal("session-switch queue did not finish")
		}
	}
	if fixture.bridge.rd.sessionPath != otherPath {
		t.Fatalf("active path = %q, want %q", fixture.bridge.rd.sessionPath, otherPath)
	}
	if client.calls != 1 {
		t.Fatalf("model calls = %d, want no follow-up call after session switch", client.calls)
	}
	if fixture.runner.Surface().Editor().QueuedCount() != 0 {
		t.Fatal("queued follow-up survived the session switch")
	}
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, "dropped 1 queued follow-up message(s) on session switch") {
		t.Fatalf("session-switch drop warning missing:\n%s", frame)
	}
	fixture.bridge.shutdown()
	fixture.bridge.wait()
	fixture.bridge.submit("/resume " + otherPath)
	if fixture.bridge.lifecycle.sessionChangePending() {
		t.Fatal("shutdown accepted a queued session change")
	}
}

func joinedRequestUserText(req *agent.TurnRequest) string {
	return strings.Join(messageUserTexts(req.Messages), "\n")
}

func messageUserTexts(history []*agent.Message) []string {
	var texts []string
	for _, message := range history {
		if message != nil && message.User != nil {
			texts = append(texts, userMessageText(message.User.Content))
		}
	}
	return texts
}

func userMessageText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return string(raw)
}
