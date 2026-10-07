package agents

import (
	"errors"
	"strings"
	"testing"

	"github.com/digitalygo/smidja/internal/content"
	"github.com/digitalygo/smidja/sdk"
)

func testRef(name, body string) content.AgentRef {
	return content.AgentRef{Name: name, Content: body, Tier: content.TierBundle, Package: "test", Path: name + ".md", Origin: "bundle:agents"}
}

func TestParseDefinitionDocumentedSubset(t *testing.T) {
	ref := testRef("worker", `---
name: Worker
description: Does the work
model: vendor/model
tools: [read, exec]
thinking: high
---
You are the worker.

Do the task.`)
	def, err := parseDefinition(ref)
	if err != nil {
		t.Fatalf("parseDefinition: %v", err)
	}
	if def.Name != "worker" || def.DisplayName != "Worker" {
		t.Fatalf("identity = %q %q", def.Name, def.DisplayName)
	}
	if def.Description != "Does the work" {
		t.Fatalf("description = %q", def.Description)
	}
	if def.Model != "vendor/model" {
		t.Fatalf("model = %q", def.Model)
	}
	if !def.ToolsSet || strings.Join(def.Tools, ",") != "read,exec" {
		t.Fatalf("tools = %v set=%v", def.Tools, def.ToolsSet)
	}
	if !def.ThinkingSet || def.Thinking != sdk.ThinkingHigh {
		t.Fatalf("thinking = %q set=%v", def.Thinking, def.ThinkingSet)
	}
	if !strings.HasPrefix(def.Body, "You are the worker.") || !strings.Contains(def.Body, "Do the task.") {
		t.Fatalf("body = %q", def.Body)
	}
}

func TestParseDefinitionToolFormsAndDefaults(t *testing.T) {
	cases := []struct {
		name  string
		head  string
		tools string
	}{
		{"comma", "tools: read, exec", "read,exec"},
		{"inline list", "tools: [read, exec]", "read,exec"},
		{"block list", "tools:\n  - read\n  - exec", "read,exec"},
		{"quoted", `tools: ["read", 'exec']`, "read,exec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def, err := parseDefinition(testRef("worker", "---\n"+tc.head+"\n---\nbody"))
			if err != nil {
				t.Fatalf("parseDefinition: %v", err)
			}
			if !def.ToolsSet || strings.Join(def.Tools, ",") != tc.tools {
				t.Fatalf("tools = %v", def.Tools)
			}
		})
	}
	def, err := parseDefinition(testRef("worker", "no frontmatter body line"))
	if err != nil {
		t.Fatalf("parseDefinition: %v", err)
	}
	if def.DisplayName != "worker" || def.ToolsSet || def.ThinkingSet || def.Model != "" {
		t.Fatalf("defaults = %+v", def)
	}
	if def.Description != "no frontmatter body line" {
		t.Fatalf("fallback description = %q", def.Description)
	}
}

func TestParseDefinitionRejections(t *testing.T) {
	cases := []struct {
		name string
		body string
		want error
	}{
		{"unterminated", "---\nname: x\nbody", errAgentFrontmatter},
		{"empty body", "---\nname: x\n---\n   \n", errAgentBody},
		{"empty body without frontmatter", "  \n\t", errAgentBody},
		{"duplicate name", "---\nname: a\nname: b\n---\nbody", errAgentDuplicate},
		{"duplicate tools", "---\ntools: read\ntools: exec\n---\nbody", errAgentDuplicate},
		{"duplicate unknown", "---\nmeta: a\nmeta: b\n---\nbody", errAgentDuplicate},
		{"scalar as list", "---\nname:\n  - a\n---\nbody", errAgentType},
		{"scalar as inline list", "---\nmodel: [a]\n---\nbody", errAgentType},
		{"tools as mapping", "---\ntools: {read: yes}\n---\nbody", errAgentType},
		{"unknown mapping", "---\nmeta: {a: b}\n---\nbody", errAgentType},
		{"unknown block list", "---\nmeta:\n  - a\n---\nbody", errAgentType},
		{"unknown scalar", "---\nmeta: value\n---\nbody", nil},
		{"unknown inline list", "---\nmeta: [a, b]\n---\nbody", nil},
		{"bad key", "---\nbad key: value\n---\nbody", errAgentMetadata},
		{"no colon", "---\njust text\n---\nbody", errAgentMetadata},
		{"unknown thinking", "---\nthinking: extreme\n---\nbody", errAgentThinking},
		{"control value", "---\nmodel: a\x00b\n---\nbody", errAgentMetadata},
		{"empty tool item", "---\ntools:\n  -\n---\nbody", errAgentMetadata},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseDefinition(testRef("worker", tc.body))
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if tc.want == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	if _, err := parseDefinition(testRef("worker", "---\nname: a\nname: b\n---\nbody")); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := parseDefinition(testRef("worker", "---\nthinking: extreme\n---\nbody")); err == nil || !strings.Contains(err.Error(), "thinking level") {
		t.Fatalf("thinking error = %v", err)
	}
	if _, err := parseDefinition(testRef("worker", "---\ntools:\n  -\n---\nbody")); err == nil || !strings.Contains(err.Error(), "unsupported frontmatter") {
		t.Fatalf("empty item error = %v", err)
	}
}

func TestParseDefinitionCRLFAndDuplicates(t *testing.T) {
	def, err := parseDefinition(testRef("worker", "---\r\nname: Worker\r\n---\r\nBody line\r\nsecond line"))
	if err != nil {
		t.Fatalf("parseDefinition: %v", err)
	}
	if def.DisplayName != "Worker" || !strings.Contains(def.Body, "Body line") {
		t.Fatalf("crlf parse = %+v", def)
	}
}

func TestParseDefinitionBoundsErrors(t *testing.T) {
	long := strings.Repeat("x", 500)
	if _, err := parseDefinition(testRef("worker", "---\nthinking: "+long+"\n---\nbody")); err == nil {
		t.Fatal("giant thinking value accepted")
	} else if strings.Contains(err.Error(), long) || len([]rune(err.Error())) > 300 {
		t.Fatalf("giant thinking error is not bounded: %d runes", len([]rune(err.Error())))
	}
	if _, err := parseDefinition(testRef("worker", "---\ntools: [\"bad\x01name\"]\n---\nbody")); err == nil {
		t.Fatal("control tool name accepted")
	} else if len([]rune(err.Error())) > 300 || strings.ContainsAny(err.Error(), "\x01") {
		t.Fatalf("control tool name error = %q", err.Error())
	}
	if _, err := parseDefinition(testRef("worker", "---\nmodel: a\x00b\n---\nbody")); err == nil {
		t.Fatal("control model value accepted")
	}
}

func TestSafeAgentNameAndBounds(t *testing.T) {
	valid := []string{"worker", "team/reader", "a-b_c.1"}
	for _, name := range valid {
		if !safeAgentName(name) {
			t.Errorf("safeAgentName(%q) = false", name)
		}
	}
	invalid := []string{"", "a b", "a\tb", "a\u200bb", "../escape", "a/../b", ".hidden", "a//b", `a\b`}
	for _, name := range invalid {
		if safeAgentName(name) {
			t.Errorf("safeAgentName(%q) = true", name)
		}
	}
	if got := bounded(strings.Repeat("x", 500)); len([]rune(got)) != errorRunes {
		t.Fatalf("bounded length = %d", len([]rune(got)))
	}
}
