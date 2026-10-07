package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/agents"
	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/openrouter"
	"github.com/digitalygo/smidja/internal/retry"
	"github.com/digitalygo/smidja/sdk"
)

func reasoningModelRegistry(t *testing.T, infos ...models.ModelInfo) *models.Registry {
	t.Helper()
	registry := models.NewRegistry()
	for _, info := range infos {
		registry.Register(info.ID, info)
	}
	return registry
}

func plainParent() agents.Parent {
	return agents.Parent{Model: "test/model", WireModel: "test/model", Provider: "openrouter", SessionID: "parent", System: "parent system"}
}

func reasoningParent() agents.Parent {
	parent := plainParent()
	parent.Thinking = sdk.ThinkingHigh
	parent.ThinkingSet = true
	return parent
}

func streamChild(t *testing.T, client agents.Client) *agent.AssistantMessage {
	t.Helper()
	content, err := json.Marshal("hello")
	if err != nil {
		t.Fatal(err)
	}
	message, err := client.Client.StreamTurn(context.Background(), &agent.TurnRequest{
		Model: client.Model,
		Messages: []*agent.Message{{User: &agent.UserMessage{
			Role:    string(agent.RoleUser),
			Content: content,
		}}},
	}, nil, nil)
	if err != nil {
		t.Fatalf("child StreamTurn: %v", err)
	}
	return message
}

func TestChildClientThinkingReachesWire(t *testing.T) {
	srv, capture := newR3HTTPCaptureServer(t)
	base := openrouter.New(srv.URL, "sk-test", nil)
	registry := reasoningModelRegistry(t, models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	definition := agents.Definition{Name: "reader", Body: "body", Thinking: sdk.ThinkingHigh, ThinkingSet: true}
	client, err := childClientFor(nil, nil, registry, nil, base, true, "openrouter", definition, plainParent())
	if err != nil {
		t.Fatalf("childClientFor: %v", err)
	}
	if client.Thinking != sdk.ThinkingHigh || !client.ThinkingSet {
		t.Fatalf("client thinking = %q set=%v", client.Thinking, client.ThinkingSet)
	}
	streamChild(t, client)
	if capture.Len() != 1 {
		t.Fatalf("requests = %d, want 1", capture.Len())
	}
	raw, ok := r3ReasoningField(t, capture.At(0).Body)
	if !ok || string(raw) != `{"effort":"high"}` {
		t.Fatalf("reasoning = %s ok=%v", raw, ok)
	}
}

func TestChildClientInheritsParentThinking(t *testing.T) {
	srv, capture := newR3HTTPCaptureServer(t)
	base := openrouter.New(srv.URL, "sk-test", nil)
	registry := reasoningModelRegistry(t, models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	client, err := childClientFor(nil, nil, registry, nil, base, true, "openrouter", agents.Definition{Name: "reader", Body: "body"}, reasoningParent())
	if err != nil {
		t.Fatalf("childClientFor: %v", err)
	}
	if client.Thinking != sdk.ThinkingHigh || !client.ThinkingSet {
		t.Fatalf("inherited thinking = %q set=%v", client.Thinking, client.ThinkingSet)
	}
	streamChild(t, client)
	raw, ok := r3ReasoningField(t, capture.At(0).Body)
	if !ok || string(raw) != `{"effort":"high"}` {
		t.Fatalf("reasoning = %s ok=%v", raw, ok)
	}
}

func TestChildClientDropsIncompatibleInheritedThinking(t *testing.T) {
	srv, capture := newR3HTTPCaptureServer(t)
	base := openrouter.New(srv.URL, "sk-test", nil)
	registry := reasoningModelRegistry(t, models.ModelInfo{ID: "plain/model", ContextWindow: 4096, Provider: "openrouter"})
	parent := plainParent()
	parent.Model = "plain/model"
	parent.WireModel = "plain/model"
	parent.Thinking = sdk.ThinkingHigh
	parent.ThinkingSet = true
	client, err := childClientFor(nil, nil, registry, nil, base, true, "openrouter", agents.Definition{Name: "reader", Body: "body"}, parent)
	if err != nil {
		t.Fatalf("childClientFor: %v", err)
	}
	if client.ThinkingSet {
		t.Fatalf("incompatible inherited thinking was kept: %+v", client)
	}
	streamChild(t, client)
	if raw, ok := r3ReasoningField(t, capture.At(0).Body); ok {
		t.Fatalf("wire carried a false effort: %s", raw)
	}
}

func TestChildClientThinkingRejections(t *testing.T) {
	reasoning := models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	}
	mandatory := reasoning
	mandatory.ID = "mandatory/model"
	mandatory.Reasoning.Mandatory = true
	allowlisted := reasoning
	allowlisted.ID = "allowlisted/model"
	allowlisted.Reasoning.Allowlist = true
	allowlisted.Reasoning.Efforts = "low"
	cases := []struct {
		name         string
		def          agents.Definition
		parent       agents.Parent
		models       []models.ModelInfo
		seam         bool
		rootProvider string
		match        string
	}{
		{"unknown model", agents.Definition{Name: "reader", Body: "body", Model: "missing/model"}, plainParent(), []models.ModelInfo{reasoning}, true, "openrouter", "unknown model"},
		{"unverified wire", agents.Definition{Name: "reader", Body: "body", Model: "anthropic/claude-sonnet-4"}, agents.Parent{Model: "test/model", Provider: "anthropic"}, []models.ModelInfo{reasoning, {ID: "anthropic/claude-sonnet-4", ContextWindow: 4096, Provider: "anthropic"}}, true, "anthropic", "no verified wire model"},
		{"unsupported effort", agents.Definition{Name: "reader", Body: "body", Thinking: sdk.ThinkingHigh, ThinkingSet: true}, agents.Parent{Model: "plain/model", WireModel: "plain/model", Provider: "openrouter"}, []models.ModelInfo{{ID: "plain/model", ContextWindow: 4096, Provider: "openrouter"}}, true, "openrouter", "does not support reasoning effort"},
		{"no effort selection", agents.Definition{Name: "reader", Body: "body", Thinking: sdk.ThinkingHigh, ThinkingSet: true}, agents.Parent{Model: "test/model", WireModel: "test/model", Provider: "openrouter"}, []models.ModelInfo{{ID: "test/model", ContextWindow: 4096, Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true}}}, true, "openrouter", "does not expose reasoning effort"},
		{"effort not allowed", agents.Definition{Name: "reader", Body: "body", Thinking: sdk.ThinkingHigh, ThinkingSet: true}, agents.Parent{Model: "allowlisted/model", WireModel: "allowlisted/model", Provider: "openrouter"}, []models.ModelInfo{allowlisted}, true, "openrouter", "does not allow effort"},
		{"mandatory off", agents.Definition{Name: "reader", Body: "body", Thinking: sdk.ThinkingOff, ThinkingSet: true}, agents.Parent{Model: "mandatory/model", WireModel: "mandatory/model", Provider: "openrouter"}, []models.ModelInfo{mandatory}, true, "openrouter", "requires reasoning"},
		{"no seam", agents.Definition{Name: "reader", Body: "body", Thinking: sdk.ThinkingHigh, ThinkingSet: true}, plainParent(), []models.ModelInfo{reasoning}, false, "openrouter", "no request-side reasoning seam"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := reasoningModelRegistry(t, tc.models...)
			base := &capturingClient{}
			_, err := childClientFor(nil, nil, registry, nil, base, tc.seam, tc.rootProvider, tc.def, tc.parent)
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("error = %v, want %q", err, tc.match)
			}
		})
	}
}

func TestChildClientOffAndNoSeamInheritance(t *testing.T) {
	srv, capture := newR3HTTPCaptureServer(t)
	base := openrouter.New(srv.URL, "sk-test", nil)
	registry := reasoningModelRegistry(t, models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	definition := agents.Definition{Name: "reader", Body: "body", Thinking: sdk.ThinkingOff, ThinkingSet: true}
	client, err := childClientFor(nil, nil, registry, nil, base, true, "openrouter", definition, plainParent())
	if err != nil {
		t.Fatalf("childClientFor: %v", err)
	}
	streamChild(t, client)
	raw, ok := r3ReasoningField(t, capture.At(0).Body)
	if !ok || string(raw) != `{"enabled":false,"effort":"none"}` {
		t.Fatalf("off reasoning = %s ok=%v", raw, ok)
	}
	noSeam, err := childClientFor(nil, nil, registry, nil, &capturingClient{}, false, "openrouter", agents.Definition{Name: "reader", Body: "body"}, reasoningParent())
	if err != nil {
		t.Fatalf("no-seam inherit: %v", err)
	}
	if noSeam.ThinkingSet {
		t.Fatalf("no-seam parent thinking leaked: %+v", noSeam)
	}
	unknownCaps, err := childClientFor(nil, nil, models.NewRegistry(), nil, &capturingClient{}, true, "openrouter", agents.Definition{Name: "reader", Body: "body", Thinking: sdk.ThinkingOff, ThinkingSet: true}, plainParent())
	if err != nil {
		t.Fatalf("unknown-caps off: %v", err)
	}
	if unknownCaps.ThinkingSet || unknownCaps.Thinking != sdk.ThinkingDefault {
		t.Fatalf("unknown reasoning caps reported a fake state: %+v", unknownCaps)
	}
}

func TestChildClientModelOverrideUsesCurrentTransport(t *testing.T) {
	registry := reasoningModelRegistry(t, models.ModelInfo{ID: "anthropic/claude-sonnet-4.5", ContextWindow: 4096, Provider: "anthropic"})
	parent := plainParent()
	parent.Provider = "anthropic"
	parent.Model = "anthropic/model"
	parent.WireModel = "anthropic/model"
	definition := agents.Definition{Name: "reader", Body: "body", Model: "anthropic/claude-sonnet-4.5"}
	client, err := childClientFor(nil, nil, registry, nil, &capturingClient{}, true, "openrouter", definition, parent)
	if err != nil {
		t.Fatalf("childClientFor: %v", err)
	}
	if client.Wire != "claude-sonnet-4-5" || client.Provider != "anthropic" {
		t.Fatalf("override client = %+v", client)
	}
}

func TestChildClientInheritsCurrentHostTransportSnapshot(t *testing.T) {
	base := &capturingClient{}
	host := newHostRuntime(context.Background(), t.TempDir(), nil, nil)
	host.setModel(models.NewRegistry(), "claude-sonnet-4.5", "claude-sonnet-4.5", "anthropic")
	host.reasoningSeam = true
	host.thinking = sdk.ThinkingHigh
	host.thinkingSet = true
	parent, handle := host.agentParentSnapshot()
	if handle != nil {
		t.Fatal("host without a bound session returned a handle")
	}
	if parent.Model != "claude-sonnet-4.5" || parent.WireModel != "claude-sonnet-4.5" || parent.Provider != "anthropic" {
		t.Fatalf("parent snapshot = %+v", parent)
	}
	if !parent.ReasoningSeam || !parent.ThinkingSet || parent.Thinking != sdk.ThinkingHigh {
		t.Fatalf("parent thinking snapshot = %+v", parent)
	}
	client, err := childClientFor(nil, host, reasoningModelRegistry(t, models.ModelInfo{
		ID: "claude-sonnet-4.5", ContextWindow: 4096, Provider: "anthropic",
		Reasoning: models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	}), nil, base, false, "anthropic", agents.Definition{Name: "reader", Body: "body"}, parent)
	if err != nil {
		t.Fatalf("childClientFor: %v", err)
	}
	if client.Model != "claude-sonnet-4.5" || client.Wire != "claude-sonnet-4-5" || client.Provider != "anthropic" {
		t.Fatalf("inherited client = %+v", client)
	}
	if client.Thinking != sdk.ThinkingHigh || !client.ThinkingSet {
		t.Fatalf("inherited thinking = %q set=%v", client.Thinking, client.ThinkingSet)
	}
	if client.Client == nil {
		t.Fatal("inherited client is nil")
	}
}

func TestChildClientProviderRemovalFailsExplicitly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("removed provider endpoint was contacted: %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	providers := extensions.NewProviderRegistry()
	if err := providers.Register("vendor", sdk.ProviderConfig{
		BaseURL: srv.URL,
		APIKey:  "vendor-key",
		Models:  []sdk.Model{{ID: "vendor/model", Name: "vendor/model"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := providers.Remove("vendor"); err != nil {
		t.Fatal(err)
	}
	parent := plainParent()
	parent.Provider = "vendor"
	parent.Model = "vendor/model"
	parent.WireModel = "vendor/model"
	definition := agents.Definition{Name: "reader", Body: "body"}
	_, err := childClientFor(&Deps{}, nil, nil, providers, &capturingClient{}, true, "vendor", definition, parent)
	if err == nil || !strings.Contains(err.Error(), "no verified wire model") {
		t.Fatalf("removed provider error = %v", err)
	}
}

func streamChildWire(t *testing.T, client agents.Client) *agent.AssistantMessage {
	t.Helper()
	content, err := json.Marshal("hello")
	if err != nil {
		t.Fatal(err)
	}
	message, err := client.Client.StreamTurn(context.Background(), &agent.TurnRequest{
		Model: client.Wire,
		Messages: []*agent.Message{{User: &agent.UserMessage{
			Role:    string(agent.RoleUser),
			Content: content,
		}}},
	}, nil, nil)
	if err != nil {
		t.Fatalf("child StreamTurn: %v", err)
	}
	return message
}

func TestChildTransportAfterProviderSwitch(t *testing.T) {
	nativeSrv, nativeCapture := newR3HTTPCaptureServer(t)
	vendorSrv, vendorCapture := newR3HTTPCaptureServer(t)
	providers := extensions.NewProviderRegistry()
	if err := providers.Register("vendor", sdk.ProviderConfig{
		BaseURL: vendorSrv.URL,
		APIKey:  "vendor-key",
		Models:  []sdk.Model{{ID: "vendor/model", Name: "vendor/model"}},
	}); err != nil {
		t.Fatal(err)
	}
	registry := reasoningModelRegistry(t, models.ModelInfo{
		ID: "test/model", ContextWindow: 4096, Provider: "openrouter",
		Reasoning: models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	base := openrouter.New(nativeSrv.URL, "sk-native", nil)
	host := newHostRuntime(context.Background(), t.TempDir(), nil, nil)
	host.setModel(registry, "vendor/model", "vendor/model", "vendor")
	host.thinking = sdk.ThinkingHigh
	host.thinkingSet = true
	host.reasoningSeam = false
	parent, _ := host.agentParentSnapshot()
	vendorDef := agents.Definition{Name: "reader", Body: "body", Model: "vendor/model"}
	vendorClient, err := childClientFor(&Deps{HTTPClient: vendorSrv.Client()}, host, registry, providers, base, true, "openrouter", vendorDef, parent)
	if err != nil {
		t.Fatalf("vendor childClientFor: %v", err)
	}
	if vendorClient.Provider != "vendor" || vendorClient.Wire != "vendor/model" || vendorClient.Model != "vendor/model" {
		t.Fatalf("vendor child = %+v", vendorClient)
	}
	streamChildWire(t, vendorClient)
	if vendorCapture.Len() != 1 {
		t.Fatalf("vendor requests = %d, want 1", vendorCapture.Len())
	}
	if auth := vendorCapture.At(0).Header.Get("Authorization"); auth != "Bearer vendor-key" {
		t.Fatalf("vendor authorization = %q", auth)
	}
	if model := r3RequestModel(t, vendorCapture.At(0).Body); model != "vendor/model" {
		t.Fatalf("vendor model = %q", model)
	}
	if nativeCapture.Len() != 0 {
		t.Fatalf("native endpoint was used for the vendor model: %d requests", nativeCapture.Len())
	}

	host.setModel(registry, "test/model", "test/wire", "openrouter")
	host.reasoningSeam = true
	parent, _ = host.agentParentSnapshot()
	nativeClient, err := childClientFor(&Deps{HTTPClient: nativeSrv.Client()}, host, registry, providers, base, false, "openrouter", agents.Definition{Name: "reader", Body: "body"}, parent)
	if err != nil {
		t.Fatalf("native childClientFor: %v", err)
	}
	if nativeClient.Provider != "openrouter" || nativeClient.Wire != "test/model" {
		t.Fatalf("native child = %+v", nativeClient)
	}
	streamChildWire(t, nativeClient)
	if nativeCapture.Len() != 1 {
		t.Fatalf("native requests = %d, want 1", nativeCapture.Len())
	}
	if auth := nativeCapture.At(0).Header.Get("Authorization"); auth != "Bearer sk-native" {
		t.Fatalf("native authorization = %q", auth)
	}
	if model := r3RequestModel(t, nativeCapture.At(0).Body); model != "test/model" {
		t.Fatalf("native model = %q", model)
	}
	if raw, ok := r3ReasoningField(t, nativeCapture.At(0).Body); !ok || string(raw) != `{"effort":"high"}` {
		t.Fatalf("native reasoning = %s ok=%v", raw, ok)
	}
	if vendorCapture.Len() != 1 {
		t.Fatalf("vendor endpoint was reused after the switch: %d requests", vendorCapture.Len())
	}
}

func TestChildClientProviderWithoutFactoryFailsExplicitly(t *testing.T) {
	providers := extensions.NewProviderRegistry()
	if err := providers.Register("vendor", sdk.ProviderConfig{
		BaseURL: "https://vendor.invalid/v1",
		APIKey:  "vendor-key",
		Models:  []sdk.Model{{ID: "vendor/model", Name: "vendor/model"}},
	}); err != nil {
		t.Fatal(err)
	}
	definition := agents.Definition{Name: "reader", Body: "body", Model: "vendor/model"}
	_, err := childClientFor(nil, nil, nil, providers, &capturingClient{}, true, "openrouter", definition, plainParent())
	if err == nil || !strings.Contains(err.Error(), "no longer registered") {
		t.Fatalf("missing factory error = %v", err)
	}
}

func TestChildClientProviderOverrideUsesPrivateCredential(t *testing.T) {
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `data: {"id":"gen_1","choices":[{"index":0,"delta":{"content":"provider ok"}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	providers := extensions.NewProviderRegistry()
	if err := providers.Register("vendor", sdk.ProviderConfig{
		BaseURL: srv.URL,
		APIKey:  "vendor-key",
		Models:  []sdk.Model{{ID: "vendor/model", Name: "vendor/model"}},
	}); err != nil {
		t.Fatal(err)
	}
	deps := &Deps{HTTPClient: srv.Client()}
	definition := agents.Definition{Name: "reader", Body: "body", Model: "vendor/model"}
	client, err := childClientFor(deps, nil, nil, providers, &capturingClient{}, true, "openrouter", definition, plainParent())
	if err != nil {
		t.Fatalf("childClientFor: %v", err)
	}
	if client.Model != "vendor/model" || client.Provider != "vendor" {
		t.Fatalf("provider client = %+v", client)
	}
	message := streamChild(t, client)
	if message == nil || !strings.Contains(blockText(message.Content), "provider ok") {
		t.Fatalf("provider response = %+v", message)
	}
	if authHeader != "Bearer vendor-key" {
		t.Fatalf("authorization = %q, want the provider credential", authHeader)
	}
}

func TestChildRunUsesNativeWireModelAndOverflowContinuation(t *testing.T) {
	var (
		mu       sync.Mutex
		requests int
		bodies   [][]byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read child request body: %v", err)
			return
		}
		mu.Lock()
		requests++
		current := requests
		bodies = append(bodies, body)
		mu.Unlock()
		if current == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"prompt is too long"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, event := range []string{
			`{"id":"gen_1","choices":[{"index":0,"delta":{"content":"wire answer"}}]}`,
			`{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", event)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	base := openrouter.New(srv.URL, "sk-test", nil)
	preparer := &cliChildPreparer{}
	exec, err := agents.NewExecutor(agents.Dependencies{
		Catalog:     func() agents.Catalog { return testDefinitionCatalog() },
		ParentTools: func() agent.ToolCatalog { return extensions.NewToolCatalog() },
		Client: func(agents.Definition, agents.Parent) (agents.Client, error) {
			return agents.Client{Client: base, Model: "display/model", Wire: "wire/model", Provider: "openrouter"}, nil
		},
		Preparer:          func(string, string, agent.Client) (agents.Preparer, error) { return preparer, nil },
		IsContextOverflow: retry.IsContextOverflow,
		SessionsRoot:      t.TempDir(),
		Cwd:               t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	res, err := exec.Run(context.Background(), agents.Request{
		Name: "reader",
		Task: "do it",
		Parent: agents.Parent{
			SessionID: "parent",
			Model:     "display/model",
			WireModel: "wire/model",
			Provider:  "openrouter",
			System:    "parent system",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !preparer.forced {
		t.Fatal("overflow did not force child compaction")
	}
	if res.Model != "display/model" || res.Wire != "wire/model" {
		t.Fatalf("result display/wire = %+v", res)
	}
	if res.Answer != "wire answer" {
		t.Fatalf("answer = %q", res.Answer)
	}
	mu.Lock()
	count := requests
	captured := append([][]byte(nil), bodies...)
	mu.Unlock()
	if count != 2 {
		t.Fatalf("child requests = %d, want overflow plus continuation", count)
	}
	for i, body := range captured {
		if model := r3RequestModel(t, body); model != "wire/model" {
			t.Fatalf("request %d model = %q, want the native wire model", i, model)
		}
	}
}

func nestedReasoningCatalog(midContent, deepContent string) agents.Catalog {
	return agents.NewCatalog(content.Snapshot{Agents: map[string]content.AgentRef{
		"mid":  {Name: "mid", Content: midContent, Tier: content.TierBundle, Package: "test", Path: "mid.md", Origin: "bundle:agents"},
		"deep": {Name: "deep", Content: deepContent, Tier: content.TierBundle, Package: "test", Path: "deep.md", Origin: "bundle:agents"},
	}})
}

func newNestedReasoningServer(t *testing.T) (*httptest.Server, *r3HTTPCapture) {
	t.Helper()
	capture := &r3HTTPCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read nested request body: %v", err)
			return
		}
		capture.mu.Lock()
		capture.records = append(capture.records, r3HTTPRecord{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
		capture.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		var events []string
		switch {
		case strings.Contains(string(body), "deep body"):
			events = []string{
				`{"id":"gen_1","choices":[{"index":0,"delta":{"content":"deep answer"}}]}`,
				`{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`[DONE]`,
			}
		case strings.Contains(string(body), "call_sub"):
			events = []string{
				`{"id":"gen_1","choices":[{"index":0,"delta":{"content":"mid final"}}]}`,
				`{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`[DONE]`,
			}
		default:
			arguments := `{"name":"deep","task":"go"}`
			events = []string{
				fmt.Sprintf(`{"id":"gen_1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_sub","type":"function","function":{"name":"subagent","arguments":%q}}]}}]}`, arguments),
				`{"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
				`[DONE]`,
			}
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

func newNestedReasoningExecutor(t *testing.T, d *Deps, host *hostRuntime, registry *models.Registry, providers *extensions.ProviderRegistry, base agent.Client, catalog agents.Catalog) *agents.Executor {
	t.Helper()
	tools := extensions.NewToolCatalog()
	if err := tools.Register(agents.NewTool(nil)); err != nil {
		t.Fatal(err)
	}
	exec, err := agents.NewExecutor(agents.Dependencies{
		Catalog:      func() agents.Catalog { return catalog },
		ParentTools:  func() agent.ToolCatalog { return tools },
		Client:       childClientBuilder(d, host, registry, providers, base, false, "openrouter"),
		Preparer:     func(string, string, agent.Client) (agents.Preparer, error) { return &cliChildPreparer{}, nil },
		SessionsRoot: t.TempDir(),
		Cwd:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return exec
}

func nestedReasoningHost(t *testing.T, registry *models.Registry) *hostRuntime {
	t.Helper()
	host := newHostRuntime(context.Background(), t.TempDir(), nil, nil)
	host.setModel(registry, "test/model", "test/model", "openrouter")
	host.setReasoningSeam(true)
	host.thinking = sdk.ThinkingHigh
	host.thinkingSet = true
	return host
}

func TestNestedChildSeamReachesTwoLevelWire(t *testing.T) {
	srv, capture := newNestedReasoningServer(t)
	base := openrouter.New(srv.URL, "sk-test", srv.Client())
	registry := reasoningModelRegistry(t, models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	host := nestedReasoningHost(t, registry)
	parent, _ := host.agentParentSnapshot()
	if !parent.ReasoningSeam || !parent.ThinkingSet || parent.Thinking != sdk.ThinkingHigh {
		t.Fatalf("parent snapshot = %+v", parent)
	}
	exec := newNestedReasoningExecutor(t, &Deps{HTTPClient: srv.Client()}, host, registry, nil, base, nestedReasoningCatalog("---\ntools: [subagent]\n---\nmid body", "deep body"))
	res, err := exec.Run(context.Background(), agents.Request{Name: "mid", Task: "go", Parent: parent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "mid final" {
		t.Fatalf("answer = %q", res.Answer)
	}
	if capture.Len() != 3 {
		t.Fatalf("requests = %d, want the mid tool call, the deep answer and the mid final", capture.Len())
	}
	for i := 0; i < capture.Len(); i++ {
		raw, ok := r3ReasoningField(t, capture.At(i).Body)
		if !ok || string(raw) != `{"effort":"high"}` {
			t.Fatalf("request %d reasoning = %s ok=%v body=%s", i, raw, ok, capture.At(i).Body)
		}
	}
}

func TestNestedChildCustomProviderKeepsTruthfulSeam(t *testing.T) {
	vendorSrv, vendorCapture := newNestedReasoningServer(t)
	nativeSrv, nativeCapture := newNestedReasoningServer(t)
	providers := extensions.NewProviderRegistry()
	if err := providers.Register("vendor", sdk.ProviderConfig{
		BaseURL: vendorSrv.URL,
		APIKey:  "vendor-key",
		Models:  []sdk.Model{{ID: "vendor/model", Name: "vendor/model"}},
	}); err != nil {
		t.Fatal(err)
	}
	base := openrouter.New(nativeSrv.URL, "sk-native", nativeSrv.Client())
	registry := reasoningModelRegistry(t, models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	host := nestedReasoningHost(t, registry)
	parent, _ := host.agentParentSnapshot()
	midContent := "---\nmodel: vendor/model\ntools: [subagent]\n---\nmid body"
	exec := newNestedReasoningExecutor(t, &Deps{HTTPClient: vendorSrv.Client()}, host, registry, providers, base, nestedReasoningCatalog(midContent, "deep body"))
	res, err := exec.Run(context.Background(), agents.Request{Name: "mid", Task: "go", Parent: parent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "mid final" {
		t.Fatalf("answer = %q", res.Answer)
	}
	if nativeCapture.Len() != 0 {
		t.Fatalf("native endpoint received %d requests for a vendor child", nativeCapture.Len())
	}
	if vendorCapture.Len() != 3 {
		t.Fatalf("vendor requests = %d, want the mid tool call, the deep answer and the mid final", vendorCapture.Len())
	}
	for i := 0; i < vendorCapture.Len(); i++ {
		if raw, ok := r3ReasoningField(t, vendorCapture.At(i).Body); ok {
			t.Fatalf("vendor request %d carried a false reasoning field: %s", i, raw)
		}
	}
}

func TestNestedChildExplicitThinkingWithoutSeamFails(t *testing.T) {
	vendorSrv, vendorCapture := newNestedReasoningServer(t)
	nativeSrv, nativeCapture := newNestedReasoningServer(t)
	providers := extensions.NewProviderRegistry()
	if err := providers.Register("vendor", sdk.ProviderConfig{
		BaseURL: vendorSrv.URL,
		APIKey:  "vendor-key",
		Models:  []sdk.Model{{ID: "vendor/model", Name: "vendor/model"}},
	}); err != nil {
		t.Fatal(err)
	}
	base := openrouter.New(nativeSrv.URL, "sk-native", nativeSrv.Client())
	registry := reasoningModelRegistry(t, models.ModelInfo{
		ID:            "test/model",
		ContextWindow: 4096,
		Provider:      "openrouter",
		Reasoning:     models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true},
	})
	host := nestedReasoningHost(t, registry)
	parent, _ := host.agentParentSnapshot()
	midContent := "---\nmodel: vendor/model\ntools: [subagent]\n---\nmid body"
	exec := newNestedReasoningExecutor(t, &Deps{HTTPClient: vendorSrv.Client()}, host, registry, providers, base, nestedReasoningCatalog(midContent, "---\nthinking: high\n---\ndeep body"))
	res, err := exec.Run(context.Background(), agents.Request{Name: "mid", Task: "go", Parent: parent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "mid final" {
		t.Fatalf("answer = %q", res.Answer)
	}
	if nativeCapture.Len() != 0 {
		t.Fatalf("native endpoint received %d requests", nativeCapture.Len())
	}
	if vendorCapture.Len() != 2 {
		t.Fatalf("vendor requests = %d, want the mid tool call and the mid final without a deep request", vendorCapture.Len())
	}
	second := string(vendorCapture.At(1).Body)
	if !strings.Contains(second, "no request-side reasoning seam") {
		t.Fatalf("mid model did not receive the unsupported-seam result: %s", second)
	}
	for i := 0; i < vendorCapture.Len(); i++ {
		if strings.Contains(string(vendorCapture.At(i).Body), "deep body") {
			t.Fatalf("request %d reached the explicitly unsupported deep child", i)
		}
		if raw, ok := r3ReasoningField(t, vendorCapture.At(i).Body); ok {
			t.Fatalf("vendor request %d carried a false reasoning field: %s", i, raw)
		}
	}
}

func TestNestedChildModelOverrideKeepsSeam(t *testing.T) {
	srv, capture := newNestedReasoningServer(t)
	base := openrouter.New(srv.URL, "sk-test", srv.Client())
	registry := reasoningModelRegistry(t,
		models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true}},
		models.ModelInfo{ID: "alt/model", ContextWindow: 4096, Provider: "openrouter", Reasoning: models.ReasoningInfo{Known: true, Supported: true, EffortSelection: true}},
	)
	host := nestedReasoningHost(t, registry)
	parent, _ := host.agentParentSnapshot()
	midContent := "---\nmodel: alt/model\ntools: [subagent]\n---\nmid body"
	exec := newNestedReasoningExecutor(t, &Deps{HTTPClient: srv.Client()}, host, registry, nil, base, nestedReasoningCatalog(midContent, "deep body"))
	res, err := exec.Run(context.Background(), agents.Request{Name: "mid", Task: "go", Parent: parent})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Answer != "mid final" {
		t.Fatalf("answer = %q", res.Answer)
	}
	if capture.Len() != 3 {
		t.Fatalf("requests = %d, want the mid tool call, the deep answer and the mid final", capture.Len())
	}
	for i := 0; i < capture.Len(); i++ {
		if model := r3RequestModel(t, capture.At(i).Body); model != "alt/model" {
			t.Fatalf("request %d model = %q, want the compatible override", i, model)
		}
		raw, ok := r3ReasoningField(t, capture.At(i).Body)
		if !ok || string(raw) != `{"effort":"high"}` {
			t.Fatalf("request %d reasoning = %s ok=%v", i, raw, ok)
		}
	}
}

func blockText(blocks []agent.ContentBlock) string {
	var out strings.Builder
	for _, block := range blocks {
		out.WriteString(block.Text)
	}
	return out.String()
}
