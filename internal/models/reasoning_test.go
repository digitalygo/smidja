package models

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestParseReasoningMetadataVariants(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		parameters []string
		want       ReasoningInfo
	}{
		{"absent", "", nil, ReasoningInfo{}},
		{"null without parameters", "null", nil, ReasoningInfo{}},
		{"parameter only", "", []string{"temperature", "reasoning"}, ReasoningInfo{Known: true, Supported: true}},
		{"true bool", "true", nil, ReasoningInfo{Known: true, Supported: true}},
		{"false bool", "false", nil, ReasoningInfo{Known: true}},
		{"malformed string", `"yes"`, nil, ReasoningInfo{}},
		{"malformed number", "3", nil, ReasoningInfo{}},
		{"malformed array", "[]", nil, ReasoningInfo{}},
		{"empty object", "{}", nil, ReasoningInfo{Known: true, Supported: true}},
		{"efforts null", `{"supported_efforts":null}`, nil, ReasoningInfo{Known: true, Supported: true, EffortSelection: true}},
		{"efforts list", `{"supported_efforts":["high","low"]}`, nil, ReasoningInfo{Known: true, Supported: true, EffortSelection: true, Allowlist: true, Efforts: "high,low"}},
		{"efforts upper", `{"supported_efforts":["HIGH"," Low "]}`, nil, ReasoningInfo{Known: true, Supported: true, EffortSelection: true, Allowlist: true, Efforts: "high,low"}},
		{"mandatory", `{"mandatory":true,"supported_efforts":["high"]}`, nil, ReasoningInfo{Known: true, Supported: true, Mandatory: true, EffortSelection: true, Allowlist: true, Efforts: "high"}},
		{"mandatory malformed", `{"mandatory":"yes"}`, nil, ReasoningInfo{Known: true, Supported: true}},
		{"pi efforts key", `{"efforts":["minimal"]}`, nil, ReasoningInfo{Known: true, Supported: true, EffortSelection: true, Allowlist: true, Efforts: "minimal"}},
		{"values key", `{"values":["max"]}`, nil, ReasoningInfo{Known: true, Supported: true, EffortSelection: true, Allowlist: true, Efforts: "max"}},
		{"efforts wrong type", `{"supported_efforts":"high"}`, nil, ReasoningInfo{Known: true, Supported: true}},
	}
	for _, tc := range cases {
		got := parseReasoningMetadata(json.RawMessage(tc.raw), tc.parameters)
		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestReasoningInfoAllowsEffort(t *testing.T) {
	all := ReasoningInfo{Known: true, Supported: true, EffortSelection: true}
	if !all.AllowsEffort("max") || !all.AllowsEffort("minimal") {
		t.Fatal("null effort list must accept every gateway effort")
	}
	limited := ReasoningInfo{Known: true, Supported: true, EffortSelection: true, Allowlist: true, Efforts: "high,low"}
	if !limited.AllowsEffort("high") || !limited.AllowsEffort("low") {
		t.Fatal("allowlisted efforts must be accepted")
	}
	if limited.AllowsEffort("max") || limited.AllowsEffort("minimal") {
		t.Fatal("non-allowlisted efforts must be rejected without clamping")
	}
	noSelection := ReasoningInfo{Known: true, Supported: true}
	if noSelection.AllowsEffort("high") {
		t.Fatal("models without effort selection must reject efforts")
	}
	unsupported := ReasoningInfo{Known: true}
	if unsupported.AllowsEffort("high") {
		t.Fatal("unsupported reasoning must reject efforts")
	}
	if (ReasoningInfo{}).AllowsEffort("high") {
		t.Fatal("unknown metadata must not invent support")
	}
}

func TestCatalogRecordInfoCarriesReasoning(t *testing.T) {
	var rec CatalogRecord
	raw := `{"id":"vendor/reasoner","provider":"vendor","contextWindow":1000,"reasoning":{"mandatory":true,"supported_efforts":["high","low"]}}`
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatal(err)
	}
	info := rec.Info()
	if !info.Reasoning.Known || !info.Reasoning.Supported || !info.Reasoning.Mandatory {
		t.Fatalf("reasoning = %+v", info.Reasoning)
	}
	if !info.Reasoning.AllowsEffort("high") || info.Reasoning.AllowsEffort("max") {
		t.Fatalf("efforts = %+v", info.Reasoning)
	}
	var boolRec CatalogRecord
	if err := json.Unmarshal([]byte(`{"id":"m","contextWindow":1,"reasoning":true}`), &boolRec); err != nil {
		t.Fatal(err)
	}
	if got := boolRec.Info().Reasoning; !got.Known || !got.Supported || got.EffortSelection {
		t.Fatalf("bool reasoning = %+v", got)
	}
}

func TestFetchOpenRouterModelsCarriesReasoning(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[
			{"id":"vendor/reasoner","context_length":1000,"reasoning":{"mandatory":true,"supported_efforts":["high","low"]}},
			{"id":"vendor/parameter-only","context_length":1000,"supported_parameters":["tools","reasoning"]},
			{"id":"vendor/plain","context_length":1000,"supported_parameters":["tools"]}
		]}`
		return jsonResponse(http.StatusOK, body), nil
	})}
	infos, err := FetchOpenRouterModels(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 {
		t.Fatalf("infos = %+v", infos)
	}
	if !infos[0].Reasoning.Mandatory || !infos[0].Reasoning.AllowsEffort("low") || infos[0].Reasoning.AllowsEffort("max") {
		t.Fatalf("reasoner metadata = %+v", infos[0].Reasoning)
	}
	if !infos[1].Reasoning.Known || !infos[1].Reasoning.Supported || infos[1].Reasoning.EffortSelection {
		t.Fatalf("parameter metadata = %+v", infos[1].Reasoning)
	}
	if infos[2].Reasoning.Known {
		t.Fatalf("plain metadata must stay unknown: %+v", infos[2].Reasoning)
	}
}

func TestRegistryMergePreservesKnownReasoning(t *testing.T) {
	reg := NewRegistry()
	reg.Merge([]ModelInfo{{ID: "vendor/reasoner", ContextWindow: 1000, Reasoning: ReasoningInfo{Known: true, Supported: true, EffortSelection: true, Allowlist: true, Efforts: "high"}}})
	reg.Merge([]ModelInfo{{ID: "vendor/reasoner", ContextWindow: 1000}})
	info, ok := reg.Get("vendor/reasoner")
	if !ok || !info.Reasoning.Known {
		t.Fatalf("merge dropped reasoning: %+v", info)
	}
	reg.Merge([]ModelInfo{{ID: "vendor/reasoner", ContextWindow: 1000, Reasoning: ReasoningInfo{Known: true}}})
	info, _ = reg.Get("vendor/reasoner")
	if !info.Reasoning.Known || info.Reasoning.Supported {
		t.Fatalf("known unsupported metadata must win: %+v", info.Reasoning)
	}
}

func TestGatewayReasoningEffortsAreTheDocumentedSet(t *testing.T) {
	if strings.Join(GatewayReasoningEfforts, ",") != "minimal,low,medium,high,xhigh,max" {
		t.Fatalf("efforts = %v", GatewayReasoningEfforts)
	}
}
