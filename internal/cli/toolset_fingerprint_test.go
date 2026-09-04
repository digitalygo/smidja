package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/session"
)

type fingerprintTool struct {
	name string
}

func (t fingerprintTool) Name() string            { return t.name }
func (t fingerprintTool) Description() string     { return "fingerprint probe " + t.name }
func (t fingerprintTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t fingerprintTool) Exec(context.Context, json.RawMessage) agent.Result {
	return agent.Result{}
}

type fingerprintCatalog struct {
	tools []agent.Tool
}

func (c *fingerprintCatalog) Tools() []agent.Tool { return c.tools }

func (c *fingerprintCatalog) Get(name string) (agent.Tool, bool) {
	for _, t := range c.tools {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

func TestToolsetFingerprintPreservesSliceOrder(t *testing.T) {
	ordered := []agent.Tool{
		fingerprintTool{name: "zeta"},
		fingerprintTool{name: "alpha"},
		fingerprintTool{name: "mid"},
	}
	reordered := []agent.Tool{
		fingerprintTool{name: "alpha"},
		fingerprintTool{name: "zeta"},
		fingerprintTool{name: "mid"},
	}
	first := toolsetFingerprint(nil, ordered)
	second := toolsetFingerprint(nil, ordered)
	if first == "" {
		t.Fatal("fingerprint of an ordered tool slice must be non-empty")
	}
	if first != second {
		t.Fatalf("same ordered list must produce a stable fingerprint: %q vs %q", first, second)
	}
	if reorderedFp := toolsetFingerprint(nil, reordered); reorderedFp == first {
		t.Fatal("reordering tools must change the fingerprint")
	}
}

func TestToolsetFingerprintPreservesCatalogOrder(t *testing.T) {
	catalogA := &fingerprintCatalog{tools: []agent.Tool{
		fingerprintTool{name: "zeta"},
		fingerprintTool{name: "alpha"},
	}}
	catalogB := &fingerprintCatalog{tools: []agent.Tool{
		fingerprintTool{name: "alpha"},
		fingerprintTool{name: "zeta"},
	}}
	fpA := toolsetFingerprint(catalogA, nil)
	if fpA == "" {
		t.Fatal("catalog fingerprint must be non-empty")
	}
	if again := toolsetFingerprint(catalogA, nil); again != fpA {
		t.Fatalf("same catalog order must be stable: %q vs %q", fpA, again)
	}
	if fpB := toolsetFingerprint(catalogB, nil); fpB == fpA {
		t.Fatal("reordered catalog tools must change the fingerprint")
	}
	if sliceFp := toolsetFingerprint(nil, catalogA.Tools()); sliceFp != fpA {
		t.Fatalf("catalog fingerprint %q must match the same ordered slice %q", fpA, sliceFp)
	}
}

func TestToolsetFingerprintReorderResetsRuntimeProfile(t *testing.T) {
	ordered := []agent.Tool{
		fingerprintTool{name: "zeta"},
		fingerprintTool{name: "alpha"},
	}
	reordered := []agent.Tool{
		fingerprintTool{name: "alpha"},
		fingerprintTool{name: "zeta"},
	}
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := session.CurrentProfile{
		ProviderID:                     "openrouter",
		ModelID:                        "replay-model",
		SystemPromptSHA256:             "sp-stable",
		ToolSchemasCanonicalJSONSHA256: toolsetFingerprint(nil, ordered),
		OrderingVersion:                runtimeOrderingVersion,
		AffinityKey:                    "workspace:/tmp/replay",
	}
	reset, err := syncRuntimeProfile(sess, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reset {
		t.Fatal("first sync must persist, not reset")
	}
	reorderedProfile := base
	reorderedProfile.ToolSchemasCanonicalJSONSHA256 = toolsetFingerprint(nil, reordered)
	reset, err = syncRuntimeProfile(sess, reorderedProfile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reset {
		t.Fatal("reordered tools must change the fingerprint enough to reset the runtime profile")
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
}
