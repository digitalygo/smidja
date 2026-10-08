package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/config"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/internal/providers"
	"github.com/digitalygo/smidja/internal/providers/manifest"
	"github.com/digitalygo/smidja/internal/providers/responses"
	"github.com/digitalygo/smidja/internal/subagent"
)

func TestTransportAllowsModelSelection(t *testing.T) {
	cases := []struct {
		transport string
		want      bool
	}{
		{"", true},
		{"openrouter", true},
		{"openrouter-oauth", true},
		{"anthropic", true},
		{"anthropic-oauth", true},
		{"codex", true},
		{"openai", true},
		{"xai", true},
		{"xai-subscription", true},
		{"kimi-coding", true},
		{"kimi-coding-oauth", true},
		{"google", true},
		{"gemini", true},
		{"azure-openai-responses", false},
		{" azure-openai-responses ", false},
	}
	for _, tc := range cases {
		if got := transportAllowsModelSelection(tc.transport); got != tc.want {
			t.Errorf("transportAllowsModelSelection(%q) = %v, want %v", tc.transport, got, tc.want)
		}
	}
}

func TestAnthropicWireAliasesMatchCatalogAndManifest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "models", "testdata", "pi-models-sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	infos, err := models.ParseOverrides(data)
	if err != nil {
		t.Fatal(err)
	}
	native := make(map[string]struct{})
	for _, info := range infos {
		if info.Provider != "anthropic" {
			continue
		}
		native[strings.TrimPrefix(info.ID, "anthropic/")] = struct{}{}
	}
	aliases := verifiedNativeWireModels["anthropic"]
	for alias, wire := range aliases {
		if _, ok := native[wire]; !ok {
			t.Errorf("anthropic alias %q maps to %q, absent from the catalog contract", alias, wire)
		}
	}
	spec, ok := manifest.Lookup("anthropic")
	if !ok {
		t.Fatal("anthropic manifest spec missing")
	}
	if _, ok := aliases[spec.DefaultModel]; !ok {
		t.Errorf("manifest default model %q is absent from the anthropic alias table", spec.DefaultModel)
	}
}

func TestResolveWireModelTransportModes(t *testing.T) {
	cases := []struct {
		transport  string
		registryID string
		want       string
		wantOK     bool
	}{
		{"", "anthropic/claude-sonnet-4.5", "anthropic/claude-sonnet-4.5", true},
		{"openrouter", "anthropic/claude-sonnet-4.5", "anthropic/claude-sonnet-4.5", true},
		{"openrouter-oauth", "openai/gpt-5", "openai/gpt-5", true},
		{"anthropic", "anthropic/claude-fable-5", "claude-fable-5", true},
		{"anthropic-oauth", "anthropic/claude-opus-4.6", "claude-opus-4-6", true},
		{"anthropic", "anthropic/claude-sonnet-4.6", "claude-sonnet-4-6", true},
		{"codex", "openai/gpt-5.4", "gpt-5.4", true},
		{"openai", "openai/gpt-5.2", "gpt-5.2", true},
		{"xai", "xai/grok-4.6", "grok-4.6", true},
		{"xai-subscription", "xai/grok-4.6", "grok-4.6", true},
		{"kimi-coding", "kimi/kimi-for-coding", "kimi-for-coding", true},
		{"kimi-coding-oauth", "kimi/kimi-for-coding", "kimi-for-coding", true},
		{"gemini", "google/gemini-2.5-pro", "gemini-2.5-pro", true},
		{"google", "google/gemini-3-pro", "gemini-3-pro", true},
		{"deepseek", "deepseek/deepseek-v4-pro", "deepseek-v4-pro", true},
		{"deepseek", "deepseek/deepseek-r1", "deepseek-reasoner", true},
		{"deepseek", "deepseek/deepseek-chat", "deepseek-chat", true},
		{"deepseek", "deepseek/deepseek-v3.2", "", false},
		{"anthropic", "claude-fable-5", "claude-fable-5", true},
		{"openai", "gpt-5.2", "gpt-5.2", true},
		{"nvidia", "meta/llama-3.3-70b-instruct", "meta/llama-3.3-70b-instruct", true},
		{"fireworks", "accounts/fireworks/models/glm-5p2", "accounts/fireworks/models/glm-5p2", true},
		{"cloudflare-workers-ai", "@cf/moonshotai/kimi-k2.6", "@cf/moonshotai/kimi-k2.6", true},
		{"anthropic", "anthropic/", "", false},
		{"anthropic", "anthropic/claude-3.5-sonnet", "", false},
		{"anthropic", "anthropic/claude-3.7-sonnet", "", false},
		{"anthropic", "anthropic/claude-sonnet-4", "", false},
		{"anthropic", "anthropic/claude-opus-4.1", "", false},
		{"anthropic", "provider/keep", "", false},
		{"anthropic", "", "", false},
		{"openai", "openai/", "", false},
		{"acme", "acme/model", "", false},
		{"azure-openai-responses", "openai/gpt-5.2", "", false},
	}
	for _, tc := range cases {
		got, ok := resolveWireModel(tc.transport, tc.registryID)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("resolveWireModel(%q, %q) = %q, %v; want %q, %v", tc.transport, tc.registryID, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestResolveWireModelFallbackEntriesAcrossManifestTransports(t *testing.T) {
	identity := func(ids ...string) map[string]string {
		out := make(map[string]string, len(ids))
		for _, id := range ids {
			out[id] = id
		}
		return out
	}
	rejected := func(ids ...string) map[string]struct{} {
		out := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			out[id] = struct{}{}
		}
		return out
	}
	type providerContract struct {
		mapped     map[string]string
		rejected   map[string]struct{}
		transports []string
	}
	contracts := map[string]providerContract{
		"anthropic": {
			mapped: map[string]string{
				"claude-haiku-4.5":  "claude-haiku-4-5",
				"claude-opus-4.5":   "claude-opus-4-5",
				"claude-opus-4.6":   "claude-opus-4-6",
				"claude-opus-4.7":   "claude-opus-4-7",
				"claude-opus-4.8":   "claude-opus-4-8",
				"claude-opus-5":     "claude-opus-5",
				"claude-sonnet-4.5": "claude-sonnet-4-5",
				"claude-sonnet-4.6": "claude-sonnet-4-6",
				"claude-sonnet-5":   "claude-sonnet-5",
			},
			rejected: rejected(
				"claude-sonnet-4",
				"claude-opus-4",
				"claude-opus-4.1",
				"claude-3.7-sonnet",
				"claude-3.5-sonnet",
				"claude-3.5-haiku",
				"claude-3-haiku",
				"claude-3-opus",
				"claude-hx-1",
				"claude-hx-1.5",
			),
			transports: []string{"anthropic", "anthropic-oauth"},
		},
		"openai": {
			mapped: identity(
				"gpt-5",
				"gpt-5-mini",
				"gpt-5-nano",
				"gpt-5-pro",
				"gpt-5.1",
				"gpt-5.2",
				"gpt-5.2-pro",
				"gpt-5.4",
				"gpt-5.4-mini",
				"gpt-5.4-pro",
				"gpt-4.1",
				"gpt-4o",
				"gpt-4o-mini",
			),
			transports: []string{"openai", "codex"},
		},
		"google": {
			mapped: identity(
				"gemini-2.5-pro",
				"gemini-2.5-flash",
				"gemini-2.5-flash-lite",
				"gemini-3-pro",
				"gemini-3-pro-preview",
			),
			transports: []string{"google", "gemini"},
		},
		"deepseek": {
			mapped: map[string]string{
				"deepseek-chat": "deepseek-chat",
				"deepseek-r1":   "deepseek-reasoner",
			},
			rejected:   rejected("deepseek-v3.2"),
			transports: []string{"deepseek"},
		},
		"qwen": {
			rejected: rejected(
				"qwen3-max",
				"qwen3-235b-a22b",
				"qwen2.5-coder-32b-instruct",
			),
		},
	}
	registry := models.NewRegistry()
	covered := make(map[string]int, len(contracts))
	for _, key := range registry.Keys() {
		info, ok := registry.GetByKey(key)
		if !ok || info.ID == "" {
			continue
		}
		contract, ok := contracts[info.Provider]
		if !ok {
			continue
		}
		covered[info.Provider]++
		bare := strings.TrimPrefix(info.ID, info.Provider+"/")
		expect := func(transport string) (string, bool) {
			if canonicalTransportProvider(transport) != info.Provider {
				return "", false
			}
			if _, skip := contract.rejected[bare]; skip {
				return "", false
			}
			want, wantOK := contract.mapped[bare]
			return want, wantOK
		}
		check := func(transport string) {
			want, wantOK := expect(transport)
			got, ok := resolveWireModel(transport, info.ID)
			if ok != wantOK || got != want {
				t.Errorf("resolveWireModel(%q, %q) = %q, %v; want %q, %v", transport, info.ID, got, ok, want, wantOK)
			}
		}
		for _, spec := range manifest.All {
			if _, fixed := fixedDeploymentTransports[spec.ID]; fixed {
				continue
			}
			if canonicalTransportProvider(spec.ID) == multiModelTransport {
				continue
			}
			check(spec.ID)
		}
		for _, transport := range contract.transports {
			check(transport)
		}
	}
	for provider, contract := range contracts {
		if covered[provider] != len(contract.mapped)+len(contract.rejected) {
			t.Errorf("provider %s covers %d fallback entries, want %d", provider, covered[provider], len(contract.mapped)+len(contract.rejected))
		}
	}
	for _, spec := range manifest.All {
		if _, fixed := fixedDeploymentTransports[spec.ID]; fixed {
			continue
		}
		got, ok := resolveWireModel(spec.ID, spec.DefaultModel)
		if !ok || got != spec.DefaultModel {
			t.Errorf("resolveWireModel(%q, %q) = %q, %v; want the manifest default", spec.ID, spec.DefaultModel, got, ok)
		}
	}
	if _, ok := resolveWireModel("openrouter", "anthropic/claude-sonnet-4.6"); !ok {
		t.Fatal("openrouter must keep qualified registry IDs")
	}
}

func TestModelWindowLooksUpRegistryIdentity(t *testing.T) {
	registry := models.NewRegistry()
	registry.Register("anthropic/claude-fable-5", models.ModelInfo{
		ID:            "anthropic/claude-fable-5",
		Provider:      "anthropic",
		ContextWindow: 987_654,
	})
	if got := modelWindow(registry, "anthropic/claude-fable-5"); got != 987_654 {
		t.Fatalf("modelWindow by registry identity = %d, want 987654", got)
	}
	if got := modelWindow(registry, "claude-fable-5"); got != models.DefaultModelContextWindow {
		t.Fatalf("modelWindow by bare wire ID = %d, want the default %d", got, models.DefaultModelContextWindow)
	}
}

func TestBridgeNativeModelSelectionKeepsRegistryIdentityAndMapsWire(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	registry := models.NewRegistry()
	registry.Register("anthropic/claude-fable-5", models.ModelInfo{
		ID:            "anthropic/claude-fable-5",
		Provider:      "anthropic",
		ContextWindow: 7777,
	})
	fixture.bridge.rd.modelRegistry = registry
	fixture.bridge.rd.provider = "anthropic-oauth"
	fixture.bridge.rd.preparer = testPreparer(t)

	cfg := config.Config{Model: "anthropic/claude-fable-5", ContextEnabled: true}
	selected, err := newModelPreparer(cfg, registry, "anthropic/claude-fable-5", "claude-fable-5", nil)
	if err != nil {
		t.Fatalf("newModelPreparer: %v", err)
	}
	fixture.bridge.rd.reprepare = func(model, wireModel string) (*contextPreparerAdapter, error) {
		if model != "anthropic/claude-fable-5" {
			t.Fatalf("reprepare model = %q, want the registry identity", model)
		}
		if wireModel != "claude-fable-5" {
			t.Fatalf("reprepare wire model = %q, want the native wire ID", wireModel)
		}
		return selected, nil
	}

	fixture.bridge.applyModel("anthropic/claude-fable-5")
	if fixture.bridge.rd.model != "anthropic/claude-fable-5" {
		t.Fatalf("rd.model = %q, want the registry identity", fixture.bridge.rd.model)
	}
	if fixture.bridge.rd.wireModel != "claude-fable-5" {
		t.Fatalf("rd.wireModel = %q, want the bare driver model", fixture.bridge.rd.wireModel)
	}
	if fixture.bridge.rd.preparer != selected {
		t.Fatal("applyModel did not install the registry-window preparer")
	}
	if selected.selectorModel != "claude-fable-5" || selected.contextWindow != 7777 {
		t.Fatalf("preparer selector/window = %q/%d, want the native wire model and 7777", selected.selectorModel, selected.contextWindow)
	}
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, "anthropic/claude-fable-5") {
		t.Fatalf("footer missing the registry identity:\n%s", frame)
	}
}

func TestBridgeNativeModelSelectionSendsWireIDOnNextTurn(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{textStop("done")}, nil)
	registry := models.NewRegistry()
	registry.Register("openai/gpt-5.4", models.ModelInfo{ID: "openai/gpt-5.4", Provider: "openai", ContextWindow: 400_000})
	fixture.bridge.rd.modelRegistry = registry
	fixture.bridge.rd.provider = "codex"

	fixture.bridge.applyModel("openai/gpt-5.4")
	turned := make(chan struct{})
	fixture.bridge.afterTurn = func() { close(turned) }
	fixture.bridge.submit("hello")
	select {
	case <-turned:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
	if len(fixture.client.reqs) == 0 {
		t.Fatal("no turn request was captured")
	}
	if got := fixture.client.reqs[len(fixture.client.reqs)-1].Model; got != "gpt-5.4" {
		t.Fatalf("TurnRequest.Model = %q, want the wire model gpt-5.4", got)
	}
}

func TestBridgeFixedDeploymentRejectsModelSelection(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	registry := models.NewRegistry()
	registry.Register("openai/gpt-5.2", models.ModelInfo{ID: "openai/gpt-5.2", Provider: "openai", ContextWindow: 400_000})
	fixture.bridge.rd.modelRegistry = registry
	fixture.bridge.rd.provider = "azure-openai-responses"
	fixture.bridge.rd.model = "openai/gpt-5"
	fixture.bridge.rd.wireModel = "gpt-5"

	fixture.bridge.applyModel("openai/gpt-5.2")
	if fixture.bridge.rd.model != "openai/gpt-5" || fixture.bridge.rd.wireModel != "gpt-5" {
		t.Fatalf("fixed-deployment switch mutated rd: model=%q wire=%q", fixture.bridge.rd.model, fixture.bridge.rd.wireModel)
	}
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, "fixed deployment") {
		t.Fatalf("missing fixed-deployment warning:\n%s", frame)
	}

	choices := fixture.bridge.modelChoices()
	if len(choices) != 1 || choices[0].ID != "openai/gpt-5" || choices[0].Provider != currentModelProviderLabel {
		t.Fatalf("fixed-deployment choices = %+v, want only the current model", choices)
	}

	fixture.bridge.selectModel()
	if frame := bridgeFrameText(t, fixture); !strings.Contains(frame, "fixed deployment") {
		t.Fatalf("selectModel did not refuse the fixed deployment:\n%s", frame)
	}
}

func TestBridgeApplyModelRollsBackWireIDAndPreparer(t *testing.T) {
	fixture := newBridgeFixture(t, nil, nil)
	registry := models.NewRegistry()
	registry.Register("anthropic/claude-fable-5", models.ModelInfo{ID: "anthropic/claude-fable-5", Provider: "anthropic", ContextWindow: 4096})
	fixture.bridge.rd.modelRegistry = registry
	fixture.bridge.rd.provider = "anthropic"

	previous := testPreparer(t)
	fixture.bridge.rd.model = "anthropic/keep"
	fixture.bridge.rd.wireModel = "keep"
	fixture.bridge.rd.preparer = previous

	fixture.bridge.rd.reprepare = func(string, string) (*contextPreparerAdapter, error) {
		return nil, fmt.Errorf("window unavailable")
	}
	fixture.bridge.applyModel("anthropic/claude-fable-5")
	assertSelectionRolledBack(t, fixture, previous)

	built := testPreparer(t)
	fixture.bridge.rd.reprepare = func(string, string) (*contextPreparerAdapter, error) { return built, nil }
	fixture.bridge.rd.persistModel = func(string) error { return fmt.Errorf("profile write failed") }
	fixture.bridge.applyModel("anthropic/claude-fable-5")
	assertSelectionRolledBack(t, fixture, previous)

	fixture.bridge.rd.persistModel = nil
	fixture.bridge.applyModel("anthropic/claude-fable-5")
	if fixture.bridge.rd.model != "anthropic/claude-fable-5" || fixture.bridge.rd.wireModel != "claude-fable-5" {
		t.Fatalf("successful apply left model=%q wire=%q", fixture.bridge.rd.model, fixture.bridge.rd.wireModel)
	}
	if fixture.bridge.rd.preparer != built {
		t.Fatal("successful apply did not install the rebuilt preparer")
	}
}

func assertSelectionRolledBack(t *testing.T, fixture *bridgeFixture, preparer *contextPreparerAdapter) {
	t.Helper()
	if fixture.bridge.rd.model != "anthropic/keep" {
		t.Fatalf("rd.model = %q, want the previous identity", fixture.bridge.rd.model)
	}
	if fixture.bridge.rd.wireModel != "keep" {
		t.Fatalf("rd.wireModel = %q, want the previous wire model", fixture.bridge.rd.wireModel)
	}
	if fixture.bridge.rd.preparer != preparer {
		t.Fatal("failed apply replaced the preparer")
	}
}

type wireCapture struct {
	srv      *httptest.Server
	mu       sync.Mutex
	wire     string
	models   []string
	requests int
}

func newWireCapture(t *testing.T, write func(w http.ResponseWriter)) *wireCapture {
	t.Helper()
	c := &wireCapture{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		var envelope struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Errorf("decode request body %q: %v", body, err)
		}
		c.mu.Lock()
		c.wire = envelope.Model
		c.models = append(c.models, envelope.Model)
		c.requests++
		c.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if write != nil {
			write(w)
		}
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *wireCapture) model() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wire
}

func (c *wireCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

func (c *wireCapture) modelList() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.models))
	copy(out, c.models)
	return out
}

func writeSSE(w http.ResponseWriter, lines ...string) {
	flusher, _ := w.(http.Flusher)
	for _, line := range lines {
		fmt.Fprintf(w, "%s\n\n", line)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func writeOpenAIChatEvents(w http.ResponseWriter) {
	writeSSE(w,
		`data: {"id":"gen_1","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`data: {"id":"gen_1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	)
}

func writeResponsesEvents(w http.ResponseWriter) {
	writeSSE(w,
		`data: {"type":"response.created","response":{"id":"resp_1"}}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"","annotations":[]}],"status":"completed"}}`,
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"ok"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"ok","annotations":[]}],"status":"completed"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2},"output":[]}}`,
	)
}

func writeAnthropicEvents(w http.ResponseWriter) {
	writeSSE(w,
		`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		`event: content_block_stop
data: {"type":"content_block_stop","index":0}`,
		`event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
		`event: message_stop
data: {"type":"message_stop"}`,
	)
}

func openRouterTestDriver(baseURL string) agent.Client {
	return providers.NewOpenAICompletions(providers.Config{
		BaseURL:    baseURL,
		ProviderID: "openrouter",
		API:        "openai-completions",
		Auth:       func(context.Context) (string, error) { return "sk-test", nil },
	}, nil)
}

func xaiTestDriver(baseURL string) agent.Client {
	return providers.NewOpenAICompletions(providers.Config{
		BaseURL:    baseURL,
		ProviderID: "xai",
		API:        "openai-completions",
		Auth:       func(context.Context) (string, error) { return "sk-test", nil },
	}, nil)
}

func anthropicTestDriver(baseURL string) agent.Client {
	return providers.NewAnthropic(providers.AnthropicConfig{
		BaseURL:    baseURL,
		ProviderID: "anthropic",
		APIKey:     func(context.Context) (string, error) { return "sk-ant-test", nil },
	}, nil)
}

func kimiTestDriver(baseURL string) agent.Client {
	return providers.NewAnthropic(providers.AnthropicConfig{
		BaseURL:    baseURL,
		ProviderID: "kimi",
		APIKey:     func(context.Context) (string, error) { return "sk-ant-test", nil },
	}, nil)
}

func deepseekTestDriver(baseURL string) agent.Client {
	return providers.NewOpenAICompletions(providers.Config{
		BaseURL:    baseURL,
		ProviderID: "deepseek",
		API:        "openai-completions",
		Auth:       func(context.Context) (string, error) { return "sk-test", nil },
	}, nil)
}

func openAIResponsesTestDriver(baseURL string) agent.Client {
	return responses.New(responses.Config{
		BaseURL:    baseURL,
		ProviderID: "openai",
		API:        "openai-responses",
		Auth:       func(context.Context) (string, error) { return "sk-test", nil },
	}, nil)
}

func codexTestDriver(baseURL string) agent.Client {
	return responses.New(responses.Config{
		BaseURL:    baseURL,
		ProviderID: "openai",
		API:        "openai-codex-responses",
		Codex:      true,
		Auth:       func(context.Context) (string, error) { return codexTestToken("acct-1"), nil },
	}, nil)
}

func codexTestToken(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		fmt.Sprintf(`{"https://api.openai.com/auth":{"chatgpt_account_id":%q}}`, accountID)))
	return header + "." + payload + ".signature"
}

func captureWireTurn(t *testing.T, transport, registryID, provider string, client agent.Client, capture *wireCapture, wantWire string) {
	t.Helper()
	fixture := newBridgeFixture(t, nil, nil)
	registry := models.NewRegistry()
	registry.Register(registryID, models.ModelInfo{ID: registryID, Provider: provider, ContextWindow: 100_000})
	fixture.bridge.rd.modelRegistry = registry
	fixture.bridge.rd.provider = transport
	fixture.bridge.rd.client = client

	fixture.bridge.applyModel(registryID)
	if fixture.bridge.rd.model != registryID {
		t.Fatalf("rd.model = %q, want registry identity %q", fixture.bridge.rd.model, registryID)
	}
	if fixture.bridge.rd.wireModel != wantWire {
		t.Fatalf("rd.wireModel = %q, want %q", fixture.bridge.rd.wireModel, wantWire)
	}

	turned := make(chan struct{})
	fixture.bridge.afterTurn = func() { close(turned) }
	fixture.bridge.submit("hello")
	select {
	case <-turned:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
	if capture.count() == 0 {
		t.Fatal("no HTTP request was captured")
	}
	if got := capture.model(); got != wantWire {
		t.Fatalf("serialized wire model = %q, want %q", got, wantWire)
	}
}

func TestWireModelSerializedForOpenRouterTransport(t *testing.T) {
	capture := newWireCapture(t, writeOpenAIChatEvents)
	captureWireTurn(t, "openrouter-oauth", "anthropic/claude-sonnet-4.5", "anthropic",
		openRouterTestDriver(capture.srv.URL), capture, "anthropic/claude-sonnet-4.5")
}

func TestWireModelSerializedForOpenAIResponsesTransport(t *testing.T) {
	capture := newWireCapture(t, writeResponsesEvents)
	captureWireTurn(t, "openai", "openai/gpt-5.2", "openai",
		openAIResponsesTestDriver(capture.srv.URL), capture, "gpt-5.2")
}

func TestWireModelSerializedForCodexTransport(t *testing.T) {
	capture := newWireCapture(t, writeResponsesEvents)
	captureWireTurn(t, "codex", "openai/gpt-5.4", "openai",
		codexTestDriver(capture.srv.URL), capture, "gpt-5.4")
}

func TestWireModelSerializedForAnthropicTransport(t *testing.T) {
	capture := newWireCapture(t, writeAnthropicEvents)
	captureWireTurn(t, "anthropic-oauth", "anthropic/claude-fable-5", "anthropic",
		anthropicTestDriver(capture.srv.URL), capture, "claude-fable-5")
}

func TestWireModelSerializedForXaiTransport(t *testing.T) {
	capture := newWireCapture(t, writeOpenAIChatEvents)
	captureWireTurn(t, "xai-subscription", "xai/grok-4.6", "xai",
		xaiTestDriver(capture.srv.URL), capture, "grok-4.6")
}

func TestWireModelSerializedForKimiTransport(t *testing.T) {
	capture := newWireCapture(t, writeAnthropicEvents)
	captureWireTurn(t, "kimi-coding-oauth", "kimi/kimi-for-coding", "kimi",
		kimiTestDriver(capture.srv.URL), capture, "kimi-for-coding")
}

func TestWireModelSerializedForDeepSeekReasoner(t *testing.T) {
	capture := newWireCapture(t, writeOpenAIChatEvents)
	captureWireTurn(t, "deepseek", "deepseek/deepseek-r1", "deepseek",
		deepseekTestDriver(capture.srv.URL), capture, "deepseek-reasoner")
}

func captureSelectedWireRequests(t *testing.T, transport, registryID, provider, wire string, client agent.Client, capture *wireCapture) {
	t.Helper()
	fixture := newBridgeFixture(t, nil, nil)
	registry := models.NewRegistry()
	registry.Register(registryID, models.ModelInfo{ID: registryID, Provider: provider, ContextWindow: 100_000})
	fixture.bridge.rd.modelRegistry = registry
	fixture.bridge.rd.provider = transport
	fixture.bridge.rd.client = client

	cfg := config.Config{Model: registryID, ContextEnabled: true, ContextWindowTokens: 10_000}
	selector := subagent.NewOpenRouterSelector(client)
	fixture.bridge.rd.reprepare = func(model, wireModel string) (*contextPreparerAdapter, error) {
		return newModelPreparer(cfg, registry, model, wireModel, selector)
	}

	fixture.bridge.applyModel(registryID)
	if fixture.bridge.rd.model != registryID {
		t.Fatalf("rd.model = %q, want registry identity %q", fixture.bridge.rd.model, registryID)
	}
	if fixture.bridge.rd.wireModel != wire {
		t.Fatalf("rd.wireModel = %q, want %q", fixture.bridge.rd.wireModel, wire)
	}
	preparer := fixture.bridge.rd.preparer
	if preparer == nil {
		t.Fatal("applyModel did not install a preparer")
	}
	if preparer.selectorModel != wire {
		t.Fatalf("selectorModel = %q, want the wire model %q", preparer.selectorModel, wire)
	}

	turned := make(chan struct{})
	fixture.bridge.afterTurn = func() { close(turned) }
	fixture.bridge.submit("hello")
	select {
	case <-turned:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}

	msgs := make([]*agent.Message, 12)
	for i := range msgs {
		content := strings.Repeat("x", 8000)
		if i%2 == 0 {
			msgs[i] = &agent.Message{User: &agent.UserMessage{
				Role: string(agent.RoleUser), Content: json.RawMessage(strconv.Quote(content)), Timestamp: int64(i),
			}}
		} else {
			msgs[i] = &agent.Message{Assistant: &agent.AssistantMessage{
				Role: string(agent.RoleAssistant), Content: []agent.ContentBlock{{Type: agent.BlockTypeText, Text: content}}, Timestamp: int64(i),
			}}
		}
	}
	if _, err := preparer.Prepare(context.Background(), agent.ContextRequest{Messages: msgs, LastUsageInput: 500_000}); err != nil {
		t.Fatalf("compaction prepare: %v", err)
	}

	captured := capture.modelList()
	if len(captured) < 2 {
		t.Fatalf("captured %d requests, want the ordinary turn and the compaction selector", len(captured))
	}
	for _, got := range captured {
		if got != wire || strings.Contains(got, "/") {
			t.Fatalf("captured wire model = %q, want %q with no qualifier", got, wire)
		}
	}
}

func TestSelectedWireModelUsedForOrdinaryAndCompactionCodex(t *testing.T) {
	capture := newWireCapture(t, writeResponsesEvents)
	captureSelectedWireRequests(t, "codex", "openai/gpt-5.4", "openai", "gpt-5.4",
		codexTestDriver(capture.srv.URL), capture)
}

func TestSelectedWireModelUsedForOrdinaryAndCompactionAnthropic(t *testing.T) {
	capture := newWireCapture(t, writeAnthropicEvents)
	captureSelectedWireRequests(t, "anthropic-oauth", "anthropic/claude-sonnet-4.6", "anthropic", "claude-sonnet-4-6",
		anthropicTestDriver(capture.srv.URL), capture)
}

func TestInitialModelWirePreservesConfiguredValue(t *testing.T) {
	fixture := newBridgeFixture(t, []*agent.AssistantMessage{textStop("done")}, nil)
	fixture.bridge.rd.provider = "anthropic"
	fixture.bridge.rd.model = "anthropic/claude-sonnet-4.5"
	fixture.bridge.rd.wireModel = "anthropic/claude-sonnet-4.5"

	turned := make(chan struct{})
	fixture.bridge.afterTurn = func() { close(turned) }
	fixture.bridge.submit("hello")
	select {
	case <-turned:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
	if len(fixture.client.reqs) == 0 {
		t.Fatal("no turn request was captured")
	}
	if got := fixture.client.reqs[len(fixture.client.reqs)-1].Model; got != "anthropic/claude-sonnet-4.5" {
		t.Fatalf("initial TurnRequest.Model = %q, want the configured value unchanged", got)
	}
}

func TestWireModelIDFallsBackToRegistryIdentity(t *testing.T) {
	d := &runDeps{model: "fallback/model"}
	if got := d.wireModelID(); got != "fallback/model" {
		t.Fatalf("wireModelID() = %q, want the registry identity fallback", got)
	}
	d.wireModel = "bare-model"
	if got := d.wireModelID(); got != "bare-model" {
		t.Fatalf("wireModelID() = %q, want the explicit wire model", got)
	}
	d.wireModel = "  "
	if got := d.wireModelID(); got != "fallback/model" {
		t.Fatalf("wireModelID() with blank wire = %q, want the registry identity fallback", got)
	}
}
