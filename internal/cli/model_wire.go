package cli

import (
	"strings"

	"github.com/digitalygo/smidja/internal/providers/manifest"
)

func identityWireModels(nativeIDs ...string) map[string]string {
	models := make(map[string]string, len(nativeIDs))
	for _, id := range nativeIDs {
		models[id] = id
	}
	return models
}

func aliasedWireModels(aliases map[string]string) map[string]string {
	models := make(map[string]string, len(aliases)*2)
	for registryID, nativeID := range aliases {
		models[registryID] = nativeID
		models[nativeID] = nativeID
	}
	return models
}

var verifiedNativeWireModels = map[string]map[string]string{
	"anthropic": aliasedWireModels(map[string]string{
		"claude-fable-5":    "claude-fable-5",
		"claude-haiku-4.5":  "claude-haiku-4-5",
		"claude-opus-4.5":   "claude-opus-4-5",
		"claude-opus-4.6":   "claude-opus-4-6",
		"claude-opus-4.7":   "claude-opus-4-7",
		"claude-opus-4.8":   "claude-opus-4-8",
		"claude-opus-5":     "claude-opus-5",
		"claude-sonnet-4.5": "claude-sonnet-4-5",
		"claude-sonnet-4.6": "claude-sonnet-4-6",
		"claude-sonnet-5":   "claude-sonnet-5",
	}),
	"openai": identityWireModels(
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
	"google": identityWireModels(
		"gemini-2.5-pro",
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
		"gemini-3-pro",
		"gemini-3-pro-preview",
	),
	"deepseek": aliasedWireModels(map[string]string{
		"deepseek-chat": "deepseek-chat",
		"deepseek-r1":   "deepseek-reasoner",
	}),
}

func resolveWireModel(transport, registryID string) (string, bool) {
	id := strings.TrimSpace(registryID)
	if id == "" {
		return "", false
	}
	if _, fixed := fixedDeploymentTransports[strings.TrimSpace(transport)]; fixed {
		return "", false
	}
	canonical := canonicalTransportProvider(transport)
	if canonical == "" || canonical == multiModelTransport {
		return id, true
	}
	bare := bareRegistryModelID(canonical, id)
	if wire, ok := verifiedNativeWireModels[canonical][bare]; ok {
		return wire, true
	}
	if !isKnownNativeProvider(canonical) {
		return "", false
	}
	if defaultModel := transportDefaultModel(transport); defaultModel != "" && bare == defaultModel {
		return bare, true
	}
	return "", false
}

func bareRegistryModelID(provider, id string) string {
	if bare, ok := strings.CutPrefix(id, provider+"/"); ok {
		return bare
	}
	return id
}

func transportDefaultModel(transport string) string {
	trimmed := strings.TrimSpace(transport)
	if spec, ok := manifest.Lookup(trimmed); ok {
		return spec.DefaultModel
	}
	canonical := canonicalTransportProvider(trimmed)
	if spec, ok := manifest.Lookup(canonical); ok {
		return spec.DefaultModel
	}
	provider, _ := oauthProviderByID(canonical)
	return provider.model
}

func isKnownNativeProvider(provider string) bool {
	if _, ok := manifest.Lookup(provider); ok {
		return true
	}
	_, ok := oauthProviderByID(provider)
	return ok
}
