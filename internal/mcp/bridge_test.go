package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/digitalygo/smidja/internal/agent"
	"strings"
	"testing"
)

func TestToAgentToolsNames(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	tools, err := ToAgentTools(context.Background(), c, "mcp")
	if err != nil {
		t.Fatalf("ToAgentTools: %v", err)
	}
	if len(tools) != 6 {
		t.Fatalf("tools = %d, want 6", len(tools))
	}
	wantNames := []string{"mcp_echo", "mcp_fail", "mcp_rich", "mcp_jsonify", "mcp_mixed", "mcp_slow"}
	for i, tl := range tools {
		if tl.Name() != wantNames[i] {
			t.Fatalf("tool %d name = %q, want %q", i, tl.Name(), wantNames[i])
		}
	}
	if tools[0].Description() != "echo arguments" {
		t.Fatalf("description = %q", tools[0].Description())
	}
	if !json.Valid(tools[0].Schema()) {
		t.Fatalf("schema not valid json")
	}
}

func TestBridgeCallSuccess(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	tools, err := ToAgentTools(context.Background(), c, "mcp")
	if err != nil {
		t.Fatalf("ToAgentTools: %v", err)
	}
	var echo agent.Tool
	for _, tl := range tools {
		if tl.Name() == "mcp_echo" {
			echo = tl
			break
		}
	}
	if echo == nil {
		t.Fatal("mcp_echo not found")
	}
	res := echo.Exec(context.Background(), json.RawMessage(`{"value":"bridge"}`))
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if len(res.Content) != 1 || !strings.Contains(res.Content[0].Text, "bridge") {
		t.Fatalf("content = %+v", res.Content)
	}
}

func TestBridgeIsErrorMapping(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	tools, err := ToAgentTools(context.Background(), c, "mcp")
	if err != nil {
		t.Fatalf("ToAgentTools: %v", err)
	}
	var fail agent.Tool
	for _, tl := range tools {
		if tl.Name() == "mcp_fail" {
			fail = tl
			break
		}
	}
	res := fail.Exec(context.Background(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Fatal("fail tool did not map isError")
	}
	if len(res.Content) != 1 || res.Content[0].Text != "boom" {
		t.Fatalf("content = %+v", res.Content)
	}
}

func TestBridgeUnsupportedContentType(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	tools, err := ToAgentTools(context.Background(), c, "mcp")
	if err != nil {
		t.Fatalf("ToAgentTools: %v", err)
	}
	var rich agent.Tool
	for _, tl := range tools {
		if tl.Name() == "mcp_rich" {
			rich = tl
			break
		}
	}
	res := rich.Exec(context.Background(), json.RawMessage(`{}`))
	if !res.IsError {
		t.Fatal("rich content did not produce error result")
	}
	if !strings.Contains(res.Content[0].Text, "unsupported content type") {
		t.Fatalf("error text = %q", res.Content[0].Text)
	}
}

func TestBridgeJSONContentPreserved(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	tools, err := ToAgentTools(context.Background(), c, "mcp")
	if err != nil {
		t.Fatalf("ToAgentTools: %v", err)
	}
	byName := map[string]agent.Tool{}
	for _, tl := range tools {
		byName[tl.Name()] = tl
	}
	res := byName["mcp_jsonify"].Exec(context.Background(), json.RawMessage(`{}`))
	if res.IsError || len(res.Content) != 1 {
		t.Fatalf("jsonify result = %+v", res)
	}
	if !strings.Contains(res.Content[0].Text, `"k":"v"`) {
		t.Fatalf("json content = %q", res.Content[0].Text)
	}
	mixed := byName["mcp_mixed"].Exec(context.Background(), json.RawMessage(`{}`))
	if mixed.IsError || len(mixed.Content) != 2 {
		t.Fatalf("mixed result = %+v", mixed)
	}
	if mixed.Content[0].Text != "first" || !strings.Contains(mixed.Content[1].Text, `"n":1`) {
		t.Fatalf("mixed content = %+v", mixed.Content)
	}
}

func TestBridgeCallErrorResult(t *testing.T) {
	c, _ := spawnTestClient(t, "echo", "auto", "ndjson")
	_ = c.Close()
	_, err := ToAgentTools(context.Background(), c, "mcp")
	if err == nil {
		t.Fatal("ToAgentTools succeeded on closed client")
	}
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("error = %v, want ErrClosed", err)
	}
}

func TestToAgentToolsNameCollision(t *testing.T) {
	c, _ := spawnTestClient(t, "collide", "auto", "ndjson")
	if _, err := ToAgentTools(context.Background(), c, "mcp"); err == nil {
		t.Fatal("ToAgentTools accepted colliding names")
	} else if !errors.Is(err, ErrNameCollision) {
		t.Fatalf("error = %v, want ErrNameCollision", err)
	}
}

func TestToAgentToolsInvalidSchema(t *testing.T) {
	for _, behavior := range []string{"badschema", "noschema"} {
		c, _ := spawnTestClient(t, behavior, "auto", "ndjson")
		if _, err := ToAgentTools(context.Background(), c, "mcp"); err == nil {
			t.Fatalf("ToAgentTools accepted %s", behavior)
		} else if !errors.Is(err, ErrSchemaInvalid) {
			t.Fatalf("%s error = %v, want ErrSchemaInvalid", behavior, err)
		}
	}
}

func TestToAgentToolsSchemaTooLarge(t *testing.T) {
	c, _ := spawnTestClient(t, "bigschema", "auto", "ndjson")
	if _, err := ToAgentTools(context.Background(), c, "mcp"); err == nil {
		t.Fatal("ToAgentTools accepted oversized schema")
	} else if !errors.Is(err, ErrSchemaTooLarge) {
		t.Fatalf("error = %v, want ErrSchemaTooLarge", err)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		prefix string
		name   string
		want   string
	}{
		{"mcp", "echo", "mcp_echo"},
		{"cdp", "navigate_page", "cdp_navigate_page"},
		{"mcp", "a.b c", "mcp_a_b_c"},
		{"mcp", "ümlaut", "mcp__mlaut"},
		{"mcp", "a", "mcp_a"},
	}
	for _, tc := range cases {
		got, err := SanitizeName(tc.prefix, tc.name)
		if err != nil {
			t.Fatalf("SanitizeName(%q,%q): %v", tc.prefix, tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("SanitizeName(%q,%q) = %q, want %q", tc.prefix, tc.name, got, tc.want)
		}
	}
}

func TestSanitizeNameRejectsTooLong(t *testing.T) {
	long := strings.Repeat("x", 70)
	if _, err := SanitizeName("mcp", long); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("error = %v, want ErrInvalidName", err)
	}
	edge := strings.Repeat("x", 60)
	got, err := SanitizeName("mcp", edge)
	if err != nil {
		t.Fatalf("SanitizeName at 64 chars: %v", err)
	}
	if len(got) != 64 {
		t.Fatalf("name len = %d, want 64", len(got))
	}
}

func TestValidateInputSchema(t *testing.T) {
	if err := validateInputSchema(json.RawMessage(`{"type":"object","properties":{}}`)); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
	if err := validateInputSchema(json.RawMessage(`{}`)); err != nil {
		t.Fatalf("empty object schema rejected: %v", err)
	}
	if err := validateInputSchema(json.RawMessage(`null`)); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("null schema error = %v", err)
	}
	if err := validateInputSchema(json.RawMessage(`[]`)); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("array schema error = %v", err)
	}
	if err := validateInputSchema(json.RawMessage(`{oops`)); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("malformed schema error = %v", err)
	}
	big := json.RawMessage(`{"type":"object","pad":"` + strings.Repeat("a", maxSchemaBytes) + `"}`)
	if err := validateInputSchema(big); !errors.Is(err, ErrSchemaTooLarge) {
		t.Fatalf("oversized schema error = %v", err)
	}
}
