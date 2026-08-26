package manifest

import (
	"sort"
	"testing"

	"github.com/digitalygo/smidja/internal/providers"
	"github.com/digitalygo/smidja/internal/providers/gemini"
	"github.com/digitalygo/smidja/internal/providers/responses"
)

// TestWireSkippedUnconfigured verifies that Wire registers every spec
// whose mandatory build-time environment is present, skipping only the
// cloudflare and azure specs, and reports them without failing the
// wiring. Credential resolution stays lazy: specs without a key are
// still registered and fail the turn with a clear error.
func TestWireSkippedUnconfigured(t *testing.T) {
	reg := providers.NewRegistry()
	skipped, err := Wire(reg, Deps{Env: envOf(map[string]string{
		"DEEPSEEK_API_KEY":  "sk-deepseek",
		"ANTHROPIC_API_KEY": "sk-ant",
	})})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	wantSkipped := []string{"cloudflare-ai-gateway", "cloudflare-workers-ai", "azure-openai-responses"}
	if len(skipped) != len(wantSkipped) {
		t.Fatalf("skipped = %v, want %v", skipped, wantSkipped)
	}
	for i := range wantSkipped {
		if skipped[i] != wantSkipped[i] {
			t.Fatalf("skipped = %v, want %v", skipped, wantSkipped)
		}
	}

	got := reg.List()
	want := make([]string, 0, len(All)-len(wantSkipped))
	for _, spec := range All {
		skip := false
		for _, id := range wantSkipped {
			if spec.ID == id {
				skip = true
			}
		}
		if !skip {
			want = append(want, spec.ID)
		}
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("List() has %d ids, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List() = %v, want %v", got, want)
		}
	}
}

// TestWireFullEnv verifies that Wire registers all 32 specs when the
// cloudflare and azure env is present, and that the registered drivers
// carry their provider id.
func TestWireFullEnv(t *testing.T) {
	env := map[string]string{}
	for _, spec := range All {
		env[spec.EnvVar] = "sk-" + spec.ID
	}
	env["CLOUDFLARE_ACCOUNT_ID"] = "acct-123"
	env["CLOUDFLARE_GATEWAY_ID"] = "gw-456"
	env["AZURE_OPENAI_BASE_URL"] = "https://my-resource.openai.azure.com"

	reg := providers.NewRegistry()
	skipped, err := Wire(reg, Deps{Env: envOf(env)})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
	got := reg.List()
	if len(got) != len(All) {
		t.Fatalf("registered %d drivers, want %d", len(got), len(All))
	}
	want := make([]string, len(All))
	for i, spec := range All {
		want[i] = spec.ID
	}
	sort.Strings(want)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("List()[%d] = %q, want %q (sorted)", i, got[i], want[i])
		}
	}
	for _, spec := range All {
		if _, ok := reg.Get(spec.ID); !ok {
			t.Errorf("no driver registered for %s", spec.ID)
		}
	}
}

// TestWireNilRegistry covers the nil registry guard.
func TestWireNilRegistry(t *testing.T) {
	if _, err := Wire(nil, Deps{}); err == nil {
		t.Fatal("Wire with a nil registry returned no error")
	}
}

// TestWireRegisteredDriverTypes verifies the wired drivers are the
// concrete drivers of their dialect, one per dialect sample.
func TestWireRegisteredDriverTypes(t *testing.T) {
	env := map[string]string{
		"DEEPSEEK_API_KEY":  "sk-deepseek",
		"ANTHROPIC_API_KEY": "sk-ant",
		"GEMINI_API_KEY":    "sk-google",
		"OPENAI_API_KEY":    "sk-openai",
	}
	reg := providers.NewRegistry()
	skipped, err := Wire(reg, Deps{Env: envOf(env)})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if len(skipped) != 3 {
		t.Fatalf("skipped = %d, want 3 (the cloudflare and azure specs)", len(skipped))
	}

	wantType := map[string]func(providers.Driver) bool{
		"deepseek":  func(d providers.Driver) bool { _, ok := d.(*providers.OpenAICompletions); return ok },
		"anthropic": func(d providers.Driver) bool { _, ok := d.(*providers.Anthropic); return ok },
		"google":    func(d providers.Driver) bool { _, ok := d.(*gemini.Gemini); return ok },
		"openai":    func(d providers.Driver) bool { _, ok := d.(*responses.Responses); return ok },
	}
	for id, matches := range wantType {
		d, ok := reg.Get(id)
		if !ok {
			t.Errorf("no driver registered for %s", id)
			continue
		}
		if !matches(d) {
			t.Errorf("%s driver = %T, want its dialect driver", id, d)
		}
	}
}
