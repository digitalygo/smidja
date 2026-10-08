package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/openrouter"
	"github.com/digitalygo/smidja/internal/session"
	"github.com/digitalygo/smidja/sdk"
)

type r3HTTPRecord struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

type r3HTTPCapture struct {
	mu      sync.Mutex
	records []r3HTTPRecord
}

func (c *r3HTTPCapture) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.records)
}

func (c *r3HTTPCapture) At(index int) r3HTTPRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.records[index]
}

func (c *r3HTTPCapture) Last() r3HTTPRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.records) == 0 {
		return r3HTTPRecord{}
	}
	return c.records[len(c.records)-1]
}

func newR3HTTPCaptureServer(t *testing.T) (*httptest.Server, *r3HTTPCapture) {
	t.Helper()
	capture := &r3HTTPCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		capture.mu.Lock()
		capture.records = append(capture.records, r3HTTPRecord{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Body:   body,
		})
		capture.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		events := []string{
			`{"id":"gen_1","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
			`{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"id":"gen_1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
			`[DONE]`,
		}
		for _, event := range events {
			fmt.Fprintf(w, "data: %s\n\n", event)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func r3ReasoningField(t *testing.T, body []byte) (json.RawMessage, bool) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatalf("decode request body %s: %v", body, err)
	}
	raw, ok := object["reasoning"]
	return raw, ok
}

func r3RequestModel(t *testing.T, body []byte) string {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatalf("decode request body %s: %v", body, err)
	}
	var model string
	if err := json.Unmarshal(object["model"], &model); err != nil {
		t.Fatalf("decode request model: %v", err)
	}
	return model
}

func r3ModelRegistry(t *testing.T, infos ...models.ModelInfo) *models.Registry {
	t.Helper()
	reg := models.NewRegistry()
	for _, info := range infos {
		reg.Register(info.ID, info)
	}
	return reg
}

func r3ReplDeps(t *testing.T, cwd string, client agent.Client, registry *models.Registry, input string, ext sdk.Extension) *Deps {
	t.Helper()
	home := t.TempDir()
	deps := wiringTestDeps(t.TempDir())
	deps.Getwd = func() (string, error) { return cwd, nil }
	deps.Home = func() string { return home }
	deps.Client = client
	deps.Config = testConfig(t, cwd)
	deps.Config.Model = "test/model"
	deps.Config.RetryEnabled = false
	deps.Store = wiringStore(t)
	deps.ModelRegistry = registry
	deps.Stdin = strings.NewReader(input)
	var stdout, stderr strings.Builder
	deps.Stdout = &stdout
	deps.Stderr = &stderr
	if ext != nil {
		registry := extensions.NewRegistry()
		if err := registry.Register(ext); err != nil {
			t.Fatal(err)
		}
		deps.ExtensionRuntime = extensions.NewRuntime(registry)
	}
	return deps
}

func r3SessionEntries(t *testing.T, root string) []session.Entry {
	t.Helper()
	path := firstSessionPath(t, root)
	loader, err := session.LoadWithOptions(path, session.LoadOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	return loader.Entries()
}

func TestComposedSetModelAppliesAtNextTurn(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("one"), textStop("two")}}
	registry := r3ModelRegistry(t,
		models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"},
		models.ModelInfo{ID: "vendor/alpha", ContextWindow: 8192, Provider: "openrouter"},
	)
	var seen []string
	ext := &hostHookExtension{
		id: "model-hooks",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call == 0 {
				return nil, ctx.SetModel(sdk.Model{ID: "vendor/alpha"})
			}
			seen = append(seen, ctx.Model().ID, ctx.ModelRegistry().Model().ID, fmt.Sprint(ctx.ContextUsage().ContextWindow))
			return nil, nil
		},
	}
	deps := r3ReplDeps(t, cwd, client, registry, "first\nsecond\n/quit\n", ext)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.reqs))
	}
	if client.reqs[0].Model != "test/model" || client.reqs[1].Model != "vendor/alpha" {
		t.Fatalf("request models = %q, %q", client.reqs[0].Model, client.reqs[1].Model)
	}
	if len(client.reqs[1].Messages) < 2 {
		t.Fatalf("history was dropped across the model change: %d messages", len(client.reqs[1].Messages))
	}
	if strings.Join(seen, ",") != "vendor/alpha,vendor/alpha,8192" {
		t.Fatalf("getters on next dispatch = %v", seen)
	}
	entries := r3SessionEntries(t, deps.Store.Root())
	profile, ok := sessionProfileFromEntries(entries)
	if !ok || profile.ModelID != "vendor/alpha" || profile.ProviderID != "openrouter" {
		t.Fatalf("persisted profile = %+v ok=%v", profile, ok)
	}
}

func sessionProfileFromEntries(entries []session.Entry) (session.RuntimeProfile, bool) {
	var found session.RuntimeProfile
	var ok bool
	for _, entry := range entries {
		custom, isCustom := entry.(*session.CustomEntry)
		if !isCustom || custom.CustomType != session.RuntimeProfileCustomType {
			continue
		}
		var profile session.RuntimeProfile
		if err := json.Unmarshal(custom.Data, &profile); err != nil {
			continue
		}
		found = profile
		ok = true
	}
	return found, ok
}

func TestComposedSetModelStaleContextRejected(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessA.Close()
	sessB, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()
	registry := r3ModelRegistry(t,
		models.ModelInfo{ID: "model-a", ContextWindow: 4096, Provider: "openrouter"},
		models.ModelInfo{ID: "model-b", ContextWindow: 4096, Provider: "openrouter"},
	)
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.setModelRegistry(registry)
	host.bindSession(sessA, &sessionRecorder{sessA}, sessA.ID(), sessA.Path(), cwd, "")
	host.setModel(registry, "model-a", "model-a", "openrouter")
	ctxA := host.context()
	host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), cwd, "")
	host.setModel(registry, "model-b", "model-b", "openrouter")
	if err := ctxA.SetModel(sdk.Model{ID: "model-a"}); !errIsStale(err) {
		t.Fatalf("stale SetModel error = %v, want errHostStaleSession", err)
	}
	if err := ctxA.SetThinkingLevel(sdk.ThinkingHigh); !errIsStale(err) {
		t.Fatalf("stale SetThinkingLevel error = %v, want errHostStaleSession", err)
	}
	if model := host.currentModel(); model == nil || model.ID != "model-b" {
		t.Fatalf("stale change mutated the new session: %+v", model)
	}
	if intent := host.takePendingModel(); intent != nil {
		t.Fatalf("stale change queued an intent: %+v", intent)
	}
}

func errIsStale(err error) bool {
	return err == errHostStaleSession
}

func TestComposedPendingModelDroppedOnSessionSwitch(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sessA, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessA.Close()
	sessB, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sessB.Close()
	registry := r3ModelRegistry(t,
		models.ModelInfo{ID: "model-a", ContextWindow: 4096, Provider: "openrouter"},
		models.ModelInfo{ID: "model-b", ContextWindow: 8192, Provider: "openrouter"},
	)
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.setModelRegistry(registry)
	host.bindSession(sessA, &sessionRecorder{sessA}, sessA.ID(), sessA.Path(), cwd, "")
	host.setModel(registry, "model-a", "model-a", "openrouter")
	host.bindModelBindings(hostModelBindings{
		resolveWire: func(model string) (string, string, bool) { return model, "openrouter", true },
		buildPreparer: func(model, wire string) (*contextPreparerAdapter, error) {
			return testPreparer(t), nil
		},
		persist: func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
			adopt(handle)
			return nil
		},
	})
	ctx := host.context()
	if err := ctx.SetModel(sdk.Model{ID: "model-b"}); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if host.takePendingModel() == nil {
		t.Fatal("expected a queued intent before the session switch")
	}
	if err := ctx.SetModel(sdk.Model{ID: "model-b"}); err != nil {
		t.Fatalf("second SetModel: %v", err)
	}
	host.bindSession(sessB, &sessionRecorder{sessB}, sessB.ID(), sessB.Path(), cwd, "")
	host.setModel(registry, "model-a", "model-a", "openrouter")
	if intent := host.takePendingModel(); intent != nil {
		t.Fatalf("session switch kept a stale model intent: %+v", intent)
	}
	if model := host.currentModel(); model == nil || model.ID != "model-a" {
		t.Fatalf("session switch model state = %+v, want model-a", model)
	}
}

func TestComposedSetModelPreciseErrors(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	registry := r3ModelRegistry(t, models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"})

	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.setModelRegistry(registry)
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	host.setModel(registry, "test/model", "test/model", "openrouter")
	ctx := host.context()
	if err := ctx.SetModel(sdk.Model{ID: "test/model"}); err != errHostModelUnavailable {
		t.Fatalf("missing bindings error = %v", err)
	}

	host.bindModelBindings(hostModelBindings{
		resolveWire: func(model string) (string, string, bool) { return model, "openrouter", true },
		buildPreparer: func(model, wire string) (*contextPreparerAdapter, error) {
			return testPreparer(t), nil
		},
		persist: func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
			adopt(handle)
			return nil
		},
	})
	if err := ctx.SetModel(sdk.Model{ID: "unknown/model"}); !strings.Contains(err.Error(), errHostModelUnknown.Error()) {
		t.Fatalf("unknown model error = %v", err)
	}
	if err := ctx.SetModel(sdk.Model{}); err != errHostModelEmpty {
		t.Fatalf("empty model error = %v", err)
	}
	host.bindModelBindings(hostModelBindings{
		resolveWire: func(model string) (string, string, bool) { return "", "", false },
		buildPreparer: func(model, wire string) (*contextPreparerAdapter, error) {
			return testPreparer(t), nil
		},
		persist: func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
			adopt(handle)
			return nil
		},
	})
	if err := ctx.SetModel(sdk.Model{ID: "test/model"}); !strings.Contains(err.Error(), errHostModelWire.Error()) {
		t.Fatalf("wire model error = %v", err)
	}
	if model := host.currentModel(); model == nil || model.ID != "test/model" {
		t.Fatalf("failed change mutated the host: %+v", model)
	}
	if intent := host.takePendingModel(); intent != nil {
		t.Fatalf("failed change queued an intent: %+v", intent)
	}

	persistErr := errors.New("profile write failed")
	host.bindModelBindings(hostModelBindings{
		resolveWire: func(model string) (string, string, bool) { return "alias-wire", "openrouter", true },
		buildPreparer: func(model, wire string) (*contextPreparerAdapter, error) {
			return testPreparer(t), nil
		},
		persist: func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
			return persistErr
		},
	})
	if err := ctx.SetModel(sdk.Model{ID: "test/model"}); !errors.Is(err, persistErr) {
		t.Fatalf("persist failure error = %v", err)
	}
	if intent := host.takePendingModel(); intent != nil {
		t.Fatalf("persist failure queued an intent: %+v", intent)
	}
	if model := host.currentModel(); model.ID != "test/model" {
		t.Fatalf("persist failure mutated the host: %+v", model)
	}

	host.bindModelBindings(hostModelBindings{
		resolveWire: func(model string) (string, string, bool) { return "alias-wire", "openrouter", true },
		buildPreparer: func(model, wire string) (*contextPreparerAdapter, error) {
			return testPreparer(t), nil
		},
		persist: func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
			adopt(handle)
			return nil
		},
	})
	if err := ctx.SetModel(sdk.Model{ID: "test/model"}); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	rd := &runDeps{host: host}
	rd.applyPendingModel()
	if rd.wireModelID() != "alias-wire" || rd.preparer == nil {
		t.Fatalf("applied model state = %q preparer=%v", rd.wireModelID(), rd.preparer != nil)
	}
	if model := host.currentModel(); model == nil || model.ID != "test/model" {
		t.Fatalf("host state = %+v", model)
	}
}

func TestComposedSetModelRejectsIncompatibleThinking(t *testing.T) {
	cwd := t.TempDir()
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	registry := r3ModelRegistry(t,
		models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true}},
		models.ModelInfo{ID: "plain/model", ContextWindow: 4096, Provider: "openrouter"},
	)
	host := newHostRuntime(context.Background(), cwd, nil, extensions.NewToolCatalog())
	host.setModelRegistry(registry)
	host.setReasoningSeam(true)
	host.bindSession(sess, &sessionRecorder{sess}, sess.ID(), sess.Path(), cwd, "")
	host.setModel(registry, "test/model", "test/model", "openrouter")
	host.bindModelBindings(hostModelBindings{
		resolveWire: func(model string) (string, string, bool) { return model, "openrouter", true },
		buildPreparer: func(model, wire string) (*contextPreparerAdapter, error) {
			return testPreparer(t), nil
		},
		persist: func(handle *hostSessionHandle, intent *hostModelIntent, adopt func(*hostSessionHandle)) error {
			adopt(handle)
			return nil
		},
	})
	ctx := host.context()
	if err := ctx.SetThinkingLevel(sdk.ThinkingHigh); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}
	if err := ctx.SetModel(sdk.Model{ID: "plain/model"}); err == nil || !strings.Contains(err.Error(), errHostModelEffort.Error()) {
		t.Fatalf("incompatible model error = %v", err)
	}
	if model := host.currentModel(); model.ID != "test/model" {
		t.Fatalf("rejected model changed host state: %+v", model)
	}
	if intent := host.takePendingModel(); intent != nil {
		t.Fatalf("rejected model queued an intent: %+v", intent)
	}
}

func TestComposedThinkingReachesOpenRouterWire(t *testing.T) {
	cwd := t.TempDir()
	srv, capture := newR3HTTPCaptureServer(t)
	client := openrouter.New(srv.URL, "sk-test", nil)
	registry := r3ModelRegistry(t, models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	var seen []sdk.ThinkingLevel
	ext := &hostHookExtension{
		id: "thinking-hooks",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			switch call {
			case 0:
				seen = append(seen, ctx.ThinkingLevel())
			case 1:
				return nil, ctx.SetThinkingLevel(sdk.ThinkingHigh)
			case 2:
				seen = append(seen, ctx.ThinkingLevel())
			}
			return nil, nil
		},
	}
	deps := r3ReplDeps(t, cwd, client, registry, "first\nsecond\nthird\n/quit\n", ext)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if capture.Len() != 3 {
		t.Fatalf("http requests = %d, want 3", capture.Len())
	}
	if raw, ok := r3ReasoningField(t, capture.At(0).Body); ok {
		t.Fatalf("first request carried reasoning before it was set: %s", raw)
	}
	for index := 1; index < capture.Len(); index++ {
		raw, ok := r3ReasoningField(t, capture.At(index).Body)
		if !ok || string(raw) != `{"effort":"high"}` {
			t.Fatalf("request %d reasoning = %s ok=%v", index, raw, ok)
		}
	}
	if len(seen) != 2 || seen[0] != sdk.ThinkingDefault || seen[1] != sdk.ThinkingHigh {
		t.Fatalf("next dispatch thinking = %v", seen)
	}
	entries := r3SessionEntries(t, deps.Store.Root())
	found := false
	for _, entry := range entries {
		if typed, ok := entry.(*session.ThinkingLevelChangeEntry); ok && typed.ThinkingLevel == "high" {
			found = true
		}
	}
	if !found {
		t.Fatal("thinking level change was not persisted")
	}
}

func TestComposedThinkingOffAndMaxReachWire(t *testing.T) {
	cwd := t.TempDir()
	srv, capture := newR3HTTPCaptureServer(t)
	client := openrouter.New(srv.URL, "sk-test", nil)
	registry := r3ModelRegistry(t, models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	ext := &hostHookExtension{
		id: "thinking-sequence",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			switch call {
			case 1:
				return nil, ctx.SetThinkingLevel(sdk.ThinkingOff)
			case 2:
				return nil, ctx.SetThinkingLevel(sdk.ThinkingMax)
			}
			return nil, nil
		},
	}
	deps := r3ReplDeps(t, cwd, client, registry, "first\nsecond\nthird\n/quit\n", ext)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if capture.Len() != 3 {
		t.Fatalf("http requests = %d, want 3", capture.Len())
	}
	if raw, ok := r3ReasoningField(t, capture.At(0).Body); ok {
		t.Fatalf("first request carried reasoning: %s", raw)
	}
	raw, ok := r3ReasoningField(t, capture.At(1).Body)
	if !ok || string(raw) != `{"enabled":false,"effort":"none"}` {
		t.Fatalf("off reasoning = %s ok=%v", raw, ok)
	}
	raw, ok = r3ReasoningField(t, capture.At(2).Body)
	if !ok || string(raw) != `{"effort":"max"}` {
		t.Fatalf("max reasoning = %s ok=%v", raw, ok)
	}
}

func TestComposedThinkingRejectsUnsupportedAndMandatory(t *testing.T) {
	cases := []struct {
		name       string
		reasoning  models.ReasoningInfo
		level      sdk.ThinkingLevel
		wantSubstr string
	}{
		{"unknown metadata", models.ReasoningInfo{}, sdk.ThinkingHigh, "does not support reasoning effort"},
		{"no effort selection", models.ReasoningInfo{Known: true, Supported: true}, sdk.ThinkingHigh, "does not expose reasoning effort"},
		{"allowlist excludes", models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true, Allowlist: true, Efforts: "low"}, sdk.ThinkingHigh, "does not allow effort"},
		{"mandatory off", models.ReasoningInfo{Known: true, Supported: true, Mandatory: true, EffortSelection: true}, sdk.ThinkingOff, "requires reasoning"},
		{"unknown level", models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true}, sdk.ThinkingLevel("extreme"), "unknown thinking level"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			srv, capture := newR3HTTPCaptureServer(t)
			client := openrouter.New(srv.URL, "sk-test", nil)
			registry := r3ModelRegistry(t, models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter", Reasoning: tc.reasoning})
			var hookErr error
			ext := &hostHookExtension{
				id: "thinking-negative",
				contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
					if call == 0 {
						hookErr = ctx.SetThinkingLevel(tc.level)
					}
					return nil, nil
				},
			}
			deps := r3ReplDeps(t, cwd, client, registry, "first\nsecond\n/quit\n", ext)
			if err := RunWithDeps(nil, deps); err != nil {
				t.Fatalf("RunWithDeps: %v", err)
			}
			if hookErr == nil || !strings.Contains(hookErr.Error(), tc.wantSubstr) {
				t.Fatalf("SetThinkingLevel error = %v, want %q", hookErr, tc.wantSubstr)
			}
			for index := 0; index < capture.Len(); index++ {
				if raw, ok := r3ReasoningField(t, capture.At(index).Body); ok {
					t.Fatalf("rejected level changed the wire: %s", raw)
				}
			}
			for _, entry := range r3SessionEntries(t, deps.Store.Root()) {
				if _, ok := entry.(*session.ThinkingLevelChangeEntry); ok {
					t.Fatal("rejected level was persisted")
				}
			}
		})
	}
}

func TestComposedThinkingUnsupportedTransportIsTyped(t *testing.T) {
	cwd := t.TempDir()
	client := &capturingClient{script: []*agent.AssistantMessage{textStop("ok")}}
	registry := r3ModelRegistry(t, models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	var hookErr error
	ext := &hostHookExtension{
		id: "thinking-transport",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			hookErr = ctx.SetThinkingLevel(sdk.ThinkingHigh)
			return nil, nil
		},
	}
	deps := r3ReplDeps(t, cwd, client, registry, "first\n/quit\n", ext)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if hookErr == nil || !strings.Contains(hookErr.Error(), sdk.ErrUnsupported.Error()) {
		t.Fatalf("non-OpenRouter error = %v, want typed unsupported", hookErr)
	}
}

func TestComposedSetModelResetsMandatoryOff(t *testing.T) {
	cwd := t.TempDir()
	srv, capture := newR3HTTPCaptureServer(t)
	client := openrouter.New(srv.URL, "sk-test", nil)
	registry := r3ModelRegistry(t,
		models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true}},
		models.ModelInfo{ID: "mandatory/model", ContextWindow: 4096, Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true, Mandatory: true, EffortSelection: true, Allowlist: true, Efforts: "high"}},
	)
	var seen []sdk.ThinkingLevel
	ext := &hostHookExtension{
		id: "mandatory-reset",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call != 0 {
				seen = append(seen, ctx.ThinkingLevel())
				return nil, nil
			}
			if err := ctx.SetThinkingLevel(sdk.ThinkingOff); err != nil {
				return nil, err
			}
			return nil, ctx.SetModel(sdk.Model{ID: "mandatory/model"})
		},
	}
	deps := r3ReplDeps(t, cwd, client, registry, "first\nsecond\n/quit\n", ext)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if capture.Len() != 2 {
		t.Fatalf("http requests = %d, want 2", capture.Len())
	}
	if raw, ok := r3ReasoningField(t, capture.At(1).Body); ok {
		t.Fatalf("mandatory model inherited a rejected disable: %s", raw)
	}
	if got := r3RequestModel(t, capture.At(1).Body); got != "mandatory/model" {
		t.Fatalf("second request model = %q", got)
	}
	if len(seen) != 1 || seen[0] != sdk.ThinkingDefault {
		t.Fatalf("post-reset thinking getter = %v, want provider default", seen)
	}
	last := sdk.ThinkingLevel("")
	for _, entry := range r3SessionEntries(t, deps.Store.Root()) {
		if typed, ok := entry.(*session.ThinkingLevelChangeEntry); ok {
			last = sdk.ThinkingLevel(typed.ThinkingLevel)
		}
	}
	if last != sdk.ThinkingDefault {
		t.Fatalf("last persisted thinking entry = %q, want %q", last, sdk.ThinkingDefault)
	}
}

func TestBridgeApplyModelRoutesCustomProviderClient(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	registry := models.NewRegistry()
	registry.Register("vendor/native", models.ModelInfo{ID: "vendor/native", Provider: "openrouter", ContextWindow: 4096})
	fixture.bridge.rd.modelRegistry = registry
	providers := extensions.NewProviderRegistry()
	if err := providers.Register("proxy", sdk.ProviderConfig{
		BaseURL: "https://example.com/v1",
		APIKey:  "sk-proxy",
		Models:  []sdk.Model{{ID: "proxy/model"}},
	}); err != nil {
		t.Fatal(err)
	}
	fixture.bridge.rd.providers = providers
	base := &capturingClient{}
	custom := &capturingClient{}
	fixture.bridge.rd.baseClient = base
	fixture.bridge.rd.baseSeam = true
	fixture.bridge.rd.providerClient = func(model string) (agent.Client, bool) {
		if model == "proxy/model" {
			return custom, true
		}
		return nil, false
	}
	if choices := fixture.bridge.modelChoices(); !hasModelChoice(choices, "proxy/model") {
		t.Fatalf("modelChoices dropped the custom provider model: %v", choices)
	}
	if !fixture.bridge.modelSelectable("proxy/model") {
		t.Fatal("the custom provider model must be selectable")
	}
	fixture.bridge.applyModel("proxy/model")
	if fixture.bridge.rd.model != "proxy/model" || fixture.bridge.rd.wireModel != "proxy/model" {
		t.Fatalf("applied model = %q/%q", fixture.bridge.rd.model, fixture.bridge.rd.wireModel)
	}
	if fixture.bridge.rd.client != custom {
		t.Fatalf("provider client = %T, want the custom endpoint client", fixture.bridge.rd.client)
	}
	frame := bridgeFrameText(t, fixture)
	if !strings.Contains(frame, "proxy/model") {
		t.Fatalf("footer missing the custom model:\n%s", frame)
	}
}

func TestComposedThinkingResumeStaysProviderDefaultAndEphemeral(t *testing.T) {
	cwd := t.TempDir()
	srv, capture := newR3HTTPCaptureServer(t)
	registry := r3ModelRegistry(t, models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	store := wiringStore(t)
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendEntry(&session.ThinkingLevelChangeEntry{ThinkingLevel: string(sdk.ThinkingHigh)}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}

	var seen []sdk.ThinkingLevel
	ext := &hostHookExtension{
		id: "resume-thinking",
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			seen = append(seen, ctx.ThinkingLevel())
			return nil, nil
		},
	}
	for run := 0; run < 2; run++ {
		client := openrouter.New(srv.URL, "sk-test", nil)
		deps := r3ReplDeps(t, cwd, client, registry, "", ext)
		deps.Store = store
		if err := RunWithDeps([]string{"-continue", path, "-p", "resumed"}, deps); err != nil {
			t.Fatalf("resume run %d: %v", run, err)
		}
	}
	if capture.Len() != 2 {
		t.Fatalf("http requests = %d, want 2", capture.Len())
	}
	for index := 0; index < capture.Len(); index++ {
		if raw, ok := r3ReasoningField(t, capture.At(index).Body); ok {
			t.Fatalf("resume %d silently restored the persisted reasoning level: %s", index, raw)
		}
	}
	if len(seen) != 2 || seen[0] != sdk.ThinkingDefault || seen[1] != sdk.ThinkingDefault {
		t.Fatalf("resumed thinking getter = %v, want provider default", seen)
	}
	last := sdk.ThinkingLevel("")
	profiles := 0
	for _, entry := range sessionEntriesAt(t, path) {
		switch typed := entry.(type) {
		case *session.ThinkingLevelChangeEntry:
			last = sdk.ThinkingLevel(typed.ThinkingLevel)
		case *session.CustomEntry:
			if typed.CustomType == session.RuntimeProfileCustomType {
				profiles++
			}
		}
	}
	if last != sdk.ThinkingHigh {
		t.Fatalf("resume rewrote the persisted thinking entry: %q", last)
	}
	if profiles != 1 {
		t.Fatalf("runtime profile entries = %d, want the single persisted profile", profiles)
	}
}

func TestModelProviderForResolvesRegisteredProviders(t *testing.T) {
	providers := extensions.NewProviderRegistry()
	if err := providers.Register("proxy", sdk.ProviderConfig{BaseURL: "https://example.com", Models: []sdk.Model{{ID: "proxy/model"}}}); err != nil {
		t.Fatal(err)
	}
	if got := modelProviderFor(providers, "openrouter", "proxy/model"); got != "proxy" {
		t.Fatalf("provider = %q, want proxy", got)
	}
	if got := modelProviderFor(providers, "openrouter", "test/model"); got != "openrouter" {
		t.Fatalf("fallback provider = %q, want openrouter", got)
	}
	if got := modelProviderFor(nil, "openrouter", "proxy/model"); got != "openrouter" {
		t.Fatalf("nil registry provider = %q", got)
	}
}
