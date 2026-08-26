package providers

import (
	"fmt"
	"sync"
	"testing"
)

// TestRegistryRegisterGetList covers the basic registry contract.
func TestRegistryRegisterGetList(t *testing.T) {
	r := NewRegistry()
	if got := r.List(); len(got) != 0 {
		t.Errorf("fresh registry lists %v, want empty", got)
	}

	d1 := testDriver(t, "https://one.example.com")
	d2 := testDriver(t, "https://two.example.com")
	r.Register("deepseek", d1)
	r.Register("openai", d2)

	got, ok := r.Get("deepseek")
	if !ok || got != Driver(d1) {
		t.Errorf("Get(deepseek) = %v, %v; want registered driver", got, ok)
	}
	if _, ok := r.Get("unknown"); ok {
		t.Error("Get on an unknown provider returned ok=true")
	}
	if got := r.List(); len(got) != 2 || got[0] != "deepseek" || got[1] != "openai" {
		t.Errorf("List() = %v, want sorted [deepseek openai]", got)
	}
}

// TestRegistryRegisterReplaces verifies Register replaces an existing
// driver.
func TestRegistryRegisterReplaces(t *testing.T) {
	r := NewRegistry()
	old := testDriver(t, "https://old.example.com")
	newD := testDriver(t, "https://new.example.com")
	r.Register("p", old)
	r.Register("p", newD)
	got, ok := r.Get("p")
	if !ok || got != Driver(newD) {
		t.Errorf("Get(p) = %v, %v; want the replacement driver", got, ok)
	}
}

// TestRegistryIgnoresEmptyAndNil guards the Register contract for empty
// provider ids and nil drivers.
func TestRegistryIgnoresEmptyAndNil(t *testing.T) {
	r := NewRegistry()
	r.Register("", testDriver(t, "https://example.com"))
	r.Register("p", nil)
	if got := r.List(); len(got) != 0 {
		t.Errorf("List() = %v, want empty after invalid registers", got)
	}
}

// TestRegistryConcurrent exercises concurrent registration and lookup
// under the race detector.
func TestRegistryConcurrent(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("provider-%d", n)
			d := testDriver(t, "https://example.com")
			r.Register(id, d)
			for j := 0; j < 20; j++ {
				if got, ok := r.Get(id); !ok || got == nil {
					t.Errorf("Get(%q) miss after register", id)
				}
				_ = r.List()
			}
		}(i)
	}
	wg.Wait()
	if got := len(r.List()); got != 8 {
		t.Errorf("listed providers = %d, want 8", got)
	}
}

// TestRefShape pins the canonical identity fields.
func TestRefShape(t *testing.T) {
	ref := Ref{ProviderID: "deepseek", ModelID: "deepseek-chat", Dialect: "openai-completions"}
	if ref.ProviderID != "deepseek" || ref.ModelID != "deepseek-chat" || ref.Dialect != "openai-completions" {
		t.Errorf("ref = %+v, want canonical identity fields", ref)
	}
}
