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

const (
	DialectOpenAICompletions = "openai-completions"
	DialectAnthropicMessages = "anthropic-messages"
	DialectGemini            = "gemini"
	DialectOpenAIResponses   = "openai-responses"
)

type Spec struct {
	ID string

	EnvVar string

	Dialect string

	BaseURL string

	ExtraHeaders map[string]string

	DefaultModel string
}

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

type Deps struct {
	Env func(string) string

	Store *authstore.Store

	HTTP *http.Client
}

func Lookup(id string) (Spec, bool) {
	for _, s := range All {
		if s.ID == id {
			return s, true
		}
	}
	return Spec{}, false
}

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

func credential(spec Spec, deps Deps) func(context.Context) (string, error) {
	return func(context.Context) (string, error) {
		key, ok := authstore.ResolveCredential(spec.ID, spec.EnvVar, deps.Store, deps.Env)
		if !ok {
			return "", fmt.Errorf("no credential for provider %s: set %s or store an api_key in the auth file", spec.ID, spec.EnvVar)
		}
		return key, nil
	}
}

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

func azureDeployment(spec Spec, deps Deps) string {
	for model, deployment := range parseDeploymentMap(envOr(deps.Env, "AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "")) {
		if model == spec.DefaultModel && deployment != "" {
			return deployment
		}
	}
	return spec.DefaultModel
}

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

func requireEnv(name string, deps Deps) (string, bool) {
	if deps.Env == nil {
		return "", false
	}
	v := deps.Env(name)
	return v, v != ""
}

func envOr(env func(string) string, name, fallback string) string {
	if env == nil {
		return fallback
	}
	if v := env(name); v != "" {
		return v
	}
	return fallback
}
