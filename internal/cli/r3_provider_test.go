package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/extensions"
	"github.com/digitalygo/smidja/internal/models"
	"github.com/digitalygo/smidja/sdk"
)

const providerTestKey = "sk-proxy-secret"

func TestComposedCustomProviderRoutesNextTurn(t *testing.T) {
	cwd := t.TempDir()
	srv, capture := newR3HTTPCaptureServer(t)
	registry := r3ModelRegistry(t, models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"})
	base := &capturingClient{script: []*agent.AssistantMessage{textStop("one")}}
	var apiRef sdk.API
	var available []string
	var system string
	ext := &hostHookExtension{
		id: "provider-hooks",
		setupFn: func(api sdk.API) error {
			apiRef = api
			return api.RegisterProvider("proxy", sdk.ProviderConfig{
				BaseURL: srv.URL + "/v1",
				APIKey:  providerTestKey,
				API:     "openai-completions",
				Models:  []sdk.Model{{ID: "proxy/alpha"}, {ID: "proxy/beta"}},
			})
		},
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call == 0 {
				for _, model := range ctx.ModelRegistry().Available() {
					available = append(available, model.ID)
				}
				if found, ok := ctx.ModelRegistry().Find("proxy", "proxy/alpha"); !ok || found.ID != "proxy/alpha" {
					t.Errorf("Find(proxy, proxy/alpha) = %+v ok=%v", found, ok)
				}
				return nil, ctx.SetModel(sdk.Model{ID: "proxy/alpha"})
			}
			model := ctx.Model()
			if model == nil || model.ID != "proxy/alpha" || model.Provider != "proxy" {
				t.Errorf("next dispatch model = %+v", model)
			}
			system = ctx.SystemPrompt()
			return nil, nil
		},
	}
	deps := r3ReplDeps(t, cwd, base, registry, "first\nsecond\n/quit\n", ext)
	home := deps.Home()
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if capture.Len() != 1 {
		t.Fatalf("provider requests = %d, want 1", capture.Len())
	}
	record := capture.At(0)
	if record.Method != "POST" || record.Path != "/v1/chat/completions" {
		t.Fatalf("provider request = %s %s", record.Method, record.Path)
	}
	if got := record.Header.Get("Authorization"); got != "Bearer "+providerTestKey {
		t.Fatalf("authorization = %q", got)
	}
	if got := r3RequestModel(t, record.Body); got != "proxy/alpha" {
		t.Fatalf("provider request model = %q", got)
	}
	if !strings.Contains(strings.Join(available, ","), "proxy/alpha") {
		t.Fatalf("available models = %v, want the custom model", available)
	}
	entries := r3SessionEntries(t, deps.Store.Root())
	profile, ok := sessionProfileFromEntries(entries)
	if !ok || profile.ModelID != "proxy/alpha" || profile.ProviderID != "proxy" {
		t.Fatalf("profile = %+v ok=%v", profile, ok)
	}
	if raw := readOnlySession(t, deps.Store.Root()); strings.Contains(raw, providerTestKey) {
		t.Fatal("provider key leaked into the session")
	}
	if strings.Contains(system, providerTestKey) {
		t.Fatalf("provider key leaked into the system prompt: %q", system)
	}
	if stderr, ok := deps.Stderr.(*strings.Builder); ok && strings.Contains(stderr.String(), providerTestKey) {
		t.Fatalf("provider key leaked into stderr: %s", stderr.String())
	}
	if apiRef == nil {
		t.Fatal("setup did not capture the API")
	}
	for key, value := range apiRef.Flags() {
		if strings.Contains(fmt.Sprint(value), providerTestKey) {
			t.Fatalf("provider key leaked into flag %s", key)
		}
	}
	authPath := filepath.Join(home, ".smidja", "auth.json")
	if _, err := os.Stat(authPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("provider registration persisted credentials at %s (err=%v)", authPath, err)
	}
}

func TestComposedProviderReplacementRoutesToNewEndpoint(t *testing.T) {
	cwd := t.TempDir()
	first, firstCapture := newR3HTTPCaptureServer(t)
	second, secondCapture := newR3HTTPCaptureServer(t)
	registry := r3ModelRegistry(t, models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"})
	base := &capturingClient{script: []*agent.AssistantMessage{textStop("one"), textStop("three")}}
	ext := &hostHookExtension{
		id: "provider-replacement",
		setupFn: func(api sdk.API) error {
			return api.RegisterProvider("proxy", sdk.ProviderConfig{
				BaseURL: first.URL,
				APIKey:  providerTestKey,
				Models:  []sdk.Model{{ID: "proxy/alpha"}},
			})
		},
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			switch call {
			case 0:
				return nil, ctx.SetModel(sdk.Model{ID: "proxy/alpha"})
			case 1:
				if err := ctx.RegisterProvider("proxy", sdk.ProviderConfig{
					BaseURL: second.URL,
					APIKey:  providerTestKey,
					Models:  []sdk.Model{{ID: "proxy/alpha"}},
				}); err != nil {
					return nil, err
				}
				return nil, ctx.SetModel(sdk.Model{ID: "test/model"})
			case 2:
				return nil, ctx.SetModel(sdk.Model{ID: "proxy/alpha"})
			}
			return nil, nil
		},
	}
	deps := r3ReplDeps(t, cwd, base, registry, "first\nsecond\nthird\nfourth\n/quit\n", ext)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if firstCapture.Len() != 1 || secondCapture.Len() != 1 {
		t.Fatalf("endpoint hits = %d, %d; want one each", firstCapture.Len(), secondCapture.Len())
	}
	if got := firstCapture.At(0).Path; got != "/chat/completions" {
		t.Fatalf("first endpoint path = %q", got)
	}
	if got := secondCapture.At(0).Path; got != "/chat/completions" {
		t.Fatalf("second endpoint path = %q", got)
	}
}

func TestComposedProviderRemovalIsTransactionSafe(t *testing.T) {
	cwd := t.TempDir()
	srv, _ := newR3HTTPCaptureServer(t)
	registry := r3ModelRegistry(t, models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"})
	base := &capturingClient{script: []*agent.AssistantMessage{textStop("one"), textStop("three")}}
	var (
		activeErr  error
		removedErr error
		ghostErr   error
		apiRef     sdk.API
	)
	ext := &hostHookExtension{
		id: "provider-removal",
		setupFn: func(api sdk.API) error {
			apiRef = api
			return api.RegisterProvider("proxy", sdk.ProviderConfig{
				BaseURL: srv.URL,
				APIKey:  providerTestKey,
				Models:  []sdk.Model{{ID: "proxy/alpha"}},
			})
		},
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			switch call {
			case 0:
				return nil, ctx.SetModel(sdk.Model{ID: "proxy/alpha"})
			case 1:
				activeErr = ctx.RemoveProvider("proxy")
				if err := ctx.SetModel(sdk.Model{ID: "test/model"}); err != nil {
					return nil, err
				}
				removedErr = ctx.RemoveProvider("proxy")
				ghostErr = ctx.SetModel(sdk.Model{ID: "proxy/alpha"})
			}
			return nil, nil
		},
	}
	deps := r3ReplDeps(t, cwd, base, registry, "first\nsecond\nthird\n/quit\n", ext)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	if activeErr == nil || !strings.Contains(activeErr.Error(), "provider is active") {
		t.Fatalf("active removal error = %v", activeErr)
	}
	if removedErr != nil {
		t.Fatalf("inactive removal error = %v", removedErr)
	}
	if ghostErr == nil || !strings.Contains(ghostErr.Error(), errHostModelUnknown.Error()) {
		t.Fatalf("removed provider model error = %v", ghostErr)
	}
	if apiRef == nil {
		t.Fatal("setup did not capture the API")
	}
	if err := apiRef.RemoveProvider("proxy"); !errors.Is(err, extensions.ErrProviderNotFound) {
		t.Fatalf("post-run removal error = %v", err)
	}
}

func TestComposedProviderValidationErrorsStayPrecise(t *testing.T) {
	cwd := t.TempDir()
	registry := r3ModelRegistry(t, models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"})
	base := &capturingClient{script: []*agent.AssistantMessage{textStop("one")}}
	var (
		dialectErr error
		urlErr     error
		nameErr    error
		modelErr   error
	)
	ext := &hostHookExtension{
		id: "provider-validation",
		setupFn: func(api sdk.API) error {
			dialectErr = api.RegisterProvider("proxy", sdk.ProviderConfig{
				BaseURL: "https://example.com",
				APIKey:  providerTestKey,
				API:     "anthropic-messages",
			})
			urlErr = api.RegisterProvider("proxy", sdk.ProviderConfig{
				BaseURL: "https://user:pass@example.com",
				APIKey:  providerTestKey,
			})
			nameErr = api.RegisterProvider("openrouter", sdk.ProviderConfig{
				BaseURL: "https://example.com",
				APIKey:  providerTestKey,
			})
			modelErr = api.RegisterProvider("proxy", sdk.ProviderConfig{
				BaseURL: "https://example.com",
				APIKey:  providerTestKey,
				Models:  []sdk.Model{{ID: " "}},
			})
			return nil
		},
	}
	deps := r3ReplDeps(t, cwd, base, registry, "first\n/quit\n", ext)
	if err := RunWithDeps(nil, deps); err != nil {
		t.Fatalf("RunWithDeps: %v", err)
	}
	for name, err := range map[string]error{
		"dialect": dialectErr,
		"url":     urlErr,
		"name":    nameErr,
		"model":   modelErr,
	} {
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if strings.Contains(err.Error(), providerTestKey) {
			t.Errorf("%s: error leaked the provider key: %v", name, err)
		}
	}
	if !strings.Contains(dialectErr.Error(), "unsupported provider completion dialect") {
		t.Errorf("dialect error = %v", dialectErr)
	}
	if !strings.Contains(urlErr.Error(), "without user info") {
		t.Errorf("url error = %v", urlErr)
	}
	if !strings.Contains(nameErr.Error(), "collides with a built-in transport") {
		t.Errorf("name error = %v", nameErr)
	}
	if !strings.Contains(modelErr.Error(), "non-empty ids") {
		t.Errorf("model error = %v", modelErr)
	}
}

func TestComposedProviderFailureDoesNotLeakKey(t *testing.T) {
	cwd := t.TempDir()
	registry := r3ModelRegistry(t, models.ModelInfo{ID: "test/model", ContextWindow: 4096, Provider: "openrouter"})
	base := &capturingClient{script: []*agent.AssistantMessage{textStop("one"), textStop("two")}}
	ext := &hostHookExtension{
		id: "provider-failure",
		setupFn: func(api sdk.API) error {
			return api.RegisterProvider("proxy", sdk.ProviderConfig{
				BaseURL: "http://127.0.0.1:1/v1",
				APIKey:  providerTestKey,
				Models:  []sdk.Model{{ID: "proxy/alpha"}},
			})
		},
		contextFn: func(call int, ctx sdk.HandlerContext) (*sdk.ContextEventResult, error) {
			if call == 0 {
				return nil, ctx.SetModel(sdk.Model{ID: "proxy/alpha"})
			}
			return nil, nil
		},
	}
	deps := r3ReplDeps(t, cwd, base, registry, "first\nsecond\n/quit\n", ext)
	err := RunWithDeps(nil, deps)
	if err == nil {
		t.Fatal("a dead provider endpoint must surface an error")
	}
	if strings.Contains(err.Error(), providerTestKey) {
		t.Fatalf("provider failure leaked the key: %v", err)
	}
	stderr, _ := deps.Stderr.(*strings.Builder)
	if stderr != nil && strings.Contains(stderr.String(), providerTestKey) {
		t.Fatalf("provider failure leaked the key to stderr: %s", stderr.String())
	}
}
