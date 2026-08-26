// Package manifest is the frozen catalogue of API-key providers of the
// smidja harness. All holds one Spec per provider, mirroring the
// API-key rows of the pi providers table (docs/providers-manifest.md)
// minus the excluded providers (bedrock, copilot, radius) and the
// OAuth-only variants, which the OAuth block wires separately. Build
// constructs the driver for one spec, resolving the credential through
// the auth store with environment precedence, and Wire registers every
// constructible spec on a providers.Registry.
package manifest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/digitalygo/smidja/internal/authstore"
	"github.com/digitalygo/smidja/internal/providers"
	"github.com/digitalygo/smidja/internal/providers/gemini"
	"github.com/digitalygo/smidja/internal/providers/responses"
)

// Dialect identifiers of the manifest. They mirror the wire protocols
// the smidja drivers speak; the gemini dialect maps to the
// google-generative-ai protocol of pi.
const (
	DialectOpenAICompletions = "openai-completions"
	DialectAnthropicMessages = "anthropic-messages"
	DialectGemini            = "gemini"
	DialectOpenAIResponses   = "openai-responses"
)

// Spec freezes one API-key provider of the manifest: the canonical id,
// the environment variable that carries its API key, the wire protocol
// dialect, the provider base URL from pi-ai, any static extra headers,
// and the default model id.
type Spec struct {
	// ID is the canonical provider identifier, for example "deepseek".
	// It is also the auth.json key and the registry key.
	ID string

	// EnvVar is the environment variable that carries the API key, for
	// example "DEEPSEEK_API_KEY".
	EnvVar string

	// Dialect is the wire protocol dialect: DialectOpenAICompletions,
	// DialectAnthropicMessages, DialectGemini, or DialectOpenAIResponses.
	Dialect string

	// BaseURL is the provider base URL copied from the pi-ai provider
	// source, for example "https://api.deepseek.com". Build derives the
	// concrete endpoint by appending the dialect path. The
	// azure-openai-responses spec carries an empty BaseURL: its endpoint
	// is env-driven (AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME).
	BaseURL string

	// ExtraHeaders are static headers sent on every request, for
	// example gateway identity headers. Dynamic credentials never land
	// here; they flow through the driver auth closure.
	ExtraHeaders map[string]string

	// DefaultModel is the model id used when the caller does not pick
	// one, taken from the pi-ai model catalogue of the provider.
	DefaultModel string
}

// All is the frozen provider list of the manifest. It mirrors the
// API-key rows of the pi providers table, excluding bedrock, copilot,
// and radius, and excluding the OAuth-only variants (openrouter-oauth,
// anthropic-oauth, codex, xai-subscription, kimi-coding-oauth), which
// the OAuth block handles separately. Base URLs and default models are
// copied from the pi-ai dist sources; the citation per row lives in
// docs/providers-manifest.md.
var All = []Spec{
	{ID: "anthropic", EnvVar: "ANTHROPIC_API_KEY", Dialect: DialectAnthropicMessages, BaseURL: "https://api.anthropic.com", DefaultModel: "claude-sonnet-4-6"},
	{ID: "openai", EnvVar: "OPENAI_API_KEY", Dialect: DialectOpenAIResponses, BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-5.2"},
	{ID: "deepseek", EnvVar: "DEEPSEEK_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-v4-pro"},
	{ID: "nvidia", EnvVar: "NVIDIA_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://integrate.api.nvidia.com/v1", DefaultModel: "meta/llama-3.3-70b-instruct"},
	{ID: "google", EnvVar: "GEMINI_API_KEY", Dialect: DialectGemini, BaseURL: "https://generativelanguage.googleapis.com/v1beta", DefaultModel: "gemini-2.5-pro"},
	{ID: "mistral", EnvVar: "MISTRAL_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://api.mistral.ai", DefaultModel: "codestral-latest"},
	{ID: "groq", EnvVar: "GROQ_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://api.groq.com/openai/v1", DefaultModel: "llama-3.3-70b-versatile"},
	{ID: "cerebras", EnvVar: "CEREBRAS_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://api.cerebras.ai/v1", DefaultModel: "gpt-oss-120b"},
	{ID: "cloudflare-ai-gateway", EnvVar: "CLOUDFLARE_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat", DefaultModel: "workers-ai/@cf/moonshotai/kimi-k2.6"},
	{ID: "cloudflare-workers-ai", EnvVar: "CLOUDFLARE_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1", DefaultModel: "@cf/moonshotai/kimi-k2.6"},
	{ID: "xai", EnvVar: "XAI_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://api.x.ai/v1", DefaultModel: "grok-4.6"},
	{ID: "openrouter", EnvVar: "OPENROUTER_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "anthropic/claude-sonnet-4.5"},
	{ID: "vercel-ai-gateway", EnvVar: "AI_GATEWAY_API_KEY", Dialect: DialectAnthropicMessages, BaseURL: "https://ai-gateway.vercel.sh", DefaultModel: "alibaba/qwen3-max"},
	{ID: "zai", EnvVar: "ZAI_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://api.z.ai/api/coding/paas/v4", DefaultModel: "glm-5.2"},
	{ID: "zai-coding-cn", EnvVar: "ZAI_CODING_CN_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4", DefaultModel: "glm-5.2"},
	{ID: "ant-ling", EnvVar: "ANT_LING_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://api.ant-ling.com/v1", DefaultModel: "Ling-2.6-1T"},
	{ID: "opencode", EnvVar: "OPENCODE_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://opencode.ai/zen/v1", DefaultModel: "deepseek-v4-pro"},
	{ID: "opencode-go", EnvVar: "OPENCODE_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://opencode.ai/zen/go/v1", DefaultModel: "deepseek-v4-pro"},
	{ID: "fireworks", EnvVar: "FIREWORKS_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://api.fireworks.ai/inference", DefaultModel: "accounts/fireworks/models/glm-5p2"},
	{ID: "together", EnvVar: "TOGETHER_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://api.together.ai/v1", DefaultModel: "deepseek-ai/DeepSeek-V4-Pro"},
	{ID: "baseten", EnvVar: "BASETEN_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://inference.baseten.co/v1", DefaultModel: "deepseek-ai/DeepSeek-V4-Pro"},
	{ID: "kimi-coding", EnvVar: "KIMI_API_KEY", Dialect: DialectAnthropicMessages, BaseURL: "https://api.kimi.com/coding", DefaultModel: "kimi-for-coding"},
	{ID: "minimax", EnvVar: "MINIMAX_API_KEY", Dialect: DialectAnthropicMessages, BaseURL: "https://api.minimax.io/anthropic", DefaultModel: "MiniMax-M3"},
	{ID: "minimax-cn", EnvVar: "MINIMAX_CN_API_KEY", Dialect: DialectAnthropicMessages, BaseURL: "https://api.minimaxi.com/anthropic", DefaultModel: "MiniMax-M3"},
	{ID: "qwen-token-plan", EnvVar: "QWEN_TOKEN_PLAN_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1", DefaultModel: "qwen3.7-max"},
	{ID: "qwen-token-plan-individual", EnvVar: "QWEN_TOKEN_PLAN_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1", DefaultModel: "qwen3.7-max"},
	{ID: "qwen-token-plan-cn", EnvVar: "QWEN_TOKEN_PLAN_CN_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", DefaultModel: "qwen3.7-max"},
	{ID: "xiaomi", EnvVar: "XIAOMI_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://api.xiaomimimo.com/v1", DefaultModel: "mimo-v2.5-pro"},
	{ID: "xiaomi-token-plan-cn", EnvVar: "XIAOMI_TOKEN_PLAN_CN_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", DefaultModel: "mimo-v2.5-pro"},
	{ID: "xiaomi-token-plan-ams", EnvVar: "XIAOMI_TOKEN_PLAN_AMS_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://token-plan-ams.xiaomimimo.com/v1", DefaultModel: "mimo-v2.5-pro"},
	{ID: "xiaomi-token-plan-sgp", EnvVar: "XIAOMI_TOKEN_PLAN_SGP_API_KEY", Dialect: DialectOpenAICompletions, BaseURL: "https://token-plan-sgp.xiaomimimo.com/v1", DefaultModel: "mimo-v2.5-pro"},
	{ID: "azure-openai-responses", EnvVar: "AZURE_OPENAI_API_KEY", Dialect: DialectOpenAIResponses, BaseURL: "", DefaultModel: "gpt-5.2"},
}

// Deps carries the seams Build and Wire resolve credentials and build
// drivers with: the environment reader, the auth store, and the HTTP
// client. Nil fields are treated as absent sources.
type Deps struct {
	// Env reads environment variables, for example os.Getenv. The
	// credential resolution prefers it over the store.
	Env func(string) string

	// Store is the auth store holding api_key credentials. It is the
	// fallback credential source when the environment is unset.
	Store *authstore.Store

	// HTTP is the client drivers send requests with. Nil makes each
	// driver build its default client.
	HTTP *http.Client
}

// Lookup returns the spec for the provider id.
func Lookup(id string) (Spec, bool) {
	for _, s := range All {
		if s.ID == id {
			return s, true
		}
	}
	return Spec{}, false
}

// Build constructs the driver for one provider spec, resolving the
// credential through the auth store with environment precedence. The
// credential resolution is lazy for most specs: the driver resolves it
// per request and fails the turn when neither the environment nor the
// store carries a key. The cloudflare and azure specs resolve their
// mandatory configuration at build time and return an error when the
// required environment is unset.
func Build(id string, deps Deps) (providers.Driver, error) {
	spec, ok := Lookup(id)
	if !ok {
		return nil, fmt.Errorf("manifest: unknown provider %q", id)
	}
	switch id {
	case "cloudflare-ai-gateway":
		return buildCloudflareAIGateway(spec, deps)
	case "cloudflare-workers-ai":
		return buildCloudflareWorkersAI(spec, deps)
	case "azure-openai-responses":
		return buildAzure(spec, deps)
	}
	switch spec.Dialect {
	case DialectAnthropicMessages:
		return buildAnthropic(spec, deps), nil
	case DialectGemini:
		return buildGemini(spec, deps), nil
	case DialectOpenAIResponses:
		return buildResponses(spec, deps), nil
	case DialectOpenAICompletions:
		return buildCompletions(spec, deps), nil
	default:
		return nil, fmt.Errorf("manifest: %s: unsupported dialect %q", id, spec.Dialect)
	}
}

// credential returns the per-request credential resolver of a spec: the
// auth store with environment precedence, exactly authstore.ResolveCredential.
func credential(spec Spec, deps Deps) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		key, ok := authstore.ResolveCredential(spec.ID, spec.EnvVar, deps.Store, deps.Env)
		if !ok {
			return "", fmt.Errorf("no credential for provider %s: set %s or store an api_key in the auth file", spec.ID, spec.EnvVar)
		}
		return key, nil
	}
}

// completionsEndpoint returns the full chat/completions endpoint of a
// spec. pi-ai appends the path through the OpenAI SDK to the provider
// base URL; mistral and fireworks carry the version segment in their
// model catalogues instead (mistral-conversations.js:157, fireworks
// model data), so their endpoints append /v1/chat/completions.
func completionsEndpoint(spec Spec) string {
	if spec.ID == "mistral" || spec.ID == "fireworks" {
		return spec.BaseURL + "/v1/chat/completions"
	}
	return spec.BaseURL + "/chat/completions"
}

func buildCompletions(spec Spec, deps Deps) providers.Driver {
	return providers.NewOpenAICompletions(providers.Config{
		BaseURL:        completionsEndpoint(spec),
		DefaultHeaders: spec.ExtraHeaders,
		Auth:           credential(spec, deps),
		ProviderID:     spec.ID,
		API:            DialectOpenAICompletions,
	}, deps.HTTP)
}

func buildAnthropic(spec Spec, deps Deps) providers.Driver {
	return providers.NewAnthropic(providers.AnthropicConfig{
		BaseURL:    spec.BaseURL + "/v1/messages",
		APIKey:     credential(spec, deps),
		ProviderID: spec.ID,
	}, deps.HTTP)
}

func buildGemini(spec Spec, deps Deps) providers.Driver {
	return gemini.New(gemini.Config{
		APIKey:     credential(spec, deps),
		BaseURL:    spec.BaseURL,
		ProviderID: spec.ID,
		API:        "google-generative-ai",
	}, deps.HTTP)
}

func buildResponses(spec Spec, deps Deps) providers.Driver {
	return responses.New(responses.Config{
		BaseURL:        spec.BaseURL + "/responses",
		Auth:           credential(spec, deps),
		ProviderID:     spec.ID,
		API:            DialectOpenAIResponses,
		DefaultHeaders: spec.ExtraHeaders,
	}, deps.HTTP)
}

// buildCloudflareAIGateway constructs the AI Gateway driver: the account
// and gateway ids materialize the base URL placeholders, and the API key
// forms the cf-aig-authorization header, so all three are required at
// build time. The key also flows through the auth closure, mirroring
// pi's inline-BYOK mode where the request carries both the Cloudflare
// token and an upstream Authorization header.
func buildCloudflareAIGateway(spec Spec, deps Deps) (providers.Driver, error) {
	accountID, ok := requireEnv("CLOUDFLARE_ACCOUNT_ID", deps)
	if !ok {
		return nil, errors.New("manifest: cloudflare-ai-gateway: set CLOUDFLARE_ACCOUNT_ID")
	}
	gatewayID, ok := requireEnv("CLOUDFLARE_GATEWAY_ID", deps)
	if !ok {
		return nil, errors.New("manifest: cloudflare-ai-gateway: set CLOUDFLARE_GATEWAY_ID")
	}
	key, ok := authstore.ResolveCredential(spec.ID, spec.EnvVar, deps.Store, deps.Env)
	if !ok {
		return nil, errors.New("manifest: cloudflare-ai-gateway: set CLOUDFLARE_API_KEY or store an api_key in the auth file")
	}
	base := strings.NewReplacer(
		"{CLOUDFLARE_ACCOUNT_ID}", accountID,
		"{CLOUDFLARE_GATEWAY_ID}", gatewayID,
	).Replace(spec.BaseURL)
	return providers.NewOpenAICompletions(providers.Config{
		BaseURL: base + "/chat/completions",
		DefaultHeaders: map[string]string{
			"cf-aig-authorization": "Bearer " + key,
		},
		Auth:       func(context.Context) (string, error) { return key, nil },
		ProviderID: spec.ID,
		API:        DialectOpenAICompletions,
	}, deps.HTTP), nil
}

// buildCloudflareWorkersAI constructs the Workers AI driver: the account
// id materializes the base URL placeholder at build time, and the API
// key resolves lazily as the standard Bearer token, matching pi's
// workers-ai auth.
func buildCloudflareWorkersAI(spec Spec, deps Deps) (providers.Driver, error) {
	accountID, ok := requireEnv("CLOUDFLARE_ACCOUNT_ID", deps)
	if !ok {
		return nil, errors.New("manifest: cloudflare-workers-ai: set CLOUDFLARE_ACCOUNT_ID")
	}
	base := strings.ReplaceAll(spec.BaseURL, "{CLOUDFLARE_ACCOUNT_ID}", accountID)
	return providers.NewOpenAICompletions(providers.Config{
		BaseURL:    base + "/chat/completions",
		Auth:       credential(spec, deps),
		ProviderID: spec.ID,
		API:        DialectOpenAICompletions,
	}, deps.HTTP), nil
}

// buildAzure constructs the Azure OpenAI Responses driver: the resource
// endpoint comes from AZURE_OPENAI_BASE_URL or the resource name, the
// api-version from AZURE_OPENAI_API_VERSION (default "v1"), and the
// deployment name follows pi's rule (AZURE_OPENAI_DEPLOYMENT_NAME_MAP
// entry for the model, else the model id) evaluated for the spec's
// default model.
func buildAzure(spec Spec, deps Deps) (providers.Driver, error) {
	baseURL := envOr(deps.Env, "AZURE_OPENAI_BASE_URL", "")
	if baseURL == "" {
		resource := envOr(deps.Env, "AZURE_OPENAI_RESOURCE_NAME", "")
		if resource == "" {
			return nil, errors.New("manifest: azure-openai-responses: set AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME")
		}
		baseURL = "https://" + resource + ".openai.azure.com"
	}
	apiVersion := envOr(deps.Env, "AZURE_OPENAI_API_VERSION", "v1")
	return responses.New(responses.Config{
		BaseURL:    baseURL,
		Auth:       credential(spec, deps),
		ProviderID: spec.ID,
		API:        DialectOpenAIResponses,
		Azure:      true,
		Deployment: azureDeployment(spec, deps),
		APIVersion: apiVersion,
	}, deps.HTTP), nil
}

// azureDeployment resolves the deployment name of the spec's default
// model from the AZURE_OPENAI_DEPLOYMENT_NAME_MAP "model=deployment"
// comma-separated map, falling back to the model id itself.
func azureDeployment(spec Spec, deps Deps) string {
	for model, deployment := range parseDeploymentMap(envOr(deps.Env, "AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "")) {
		if model == spec.DefaultModel && deployment != "" {
			return deployment
		}
	}
	return spec.DefaultModel
}

// parseDeploymentMap parses the "model=deployment,model2=deployment2"
// shape of AZURE_OPENAI_DEPLOYMENT_NAME_MAP, mirroring pi-ai's parser.
func parseDeploymentMap(value string) map[string]string {
	m := make(map[string]string)
	for _, entry := range strings.Split(value, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			m[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return m
}

// requireEnv returns the value of the named environment variable and
// whether it is set and non-empty.
func requireEnv(name string, deps Deps) (string, bool) {
	if deps.Env == nil {
		return "", false
	}
	v := deps.Env(name)
	return v, v != ""
}

// envOr returns the named environment variable, or fallback when it is
// unset, empty, or the env reader is nil.
func envOr(env func(string) string, name, fallback string) string {
	if env == nil {
		return fallback
	}
	if v := env(name); v != "" {
		return v
	}
	return fallback
}
