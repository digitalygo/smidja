# Providers manifest

This document freezes the API-key provider catalogue of the smidja harness, block 6 of Fase 2. The catalogue lives in `internal/providers/manifest`: one `Spec` per provider in `All`, a `Build` constructor that turns a spec into a driver, and a `Wire` helper that registers every constructible spec on a `providers.Registry`.

The list mirrors the API-key rows of the pi providers table (`docs/providers.md` in the pi package, `env-api-keys.js` for the environment mapping), minus the excluded and OAuth-only providers. Base URLs are copied from the pi-ai provider sources; the notes column cites the exact source line of each.

## Scope

The manifest covers 32 API-key providers. Excluded from the pi table:

- `amazon-bedrock`, `github-copilot`, and `radius`, which need dedicated auth flows and wire protocols.
- `google-vertex` (Application Default Credentials), `huggingface`, `moonshotai`, `moonshotai-cn`, and `openrouter-images`, which the Fase 2 list defers.
- The OAuth-only variants `openrouter-oauth`, `anthropic-oauth`, `codex`, `xai-subscription`, and `kimi-coding-oauth`, which block 7 (OAuth flows) wires separately. The API-key variants of the same providers stay in this manifest.

## Frozen provider list

| providerID | env var | wire protocol / driver | base URL | notes |
|---|---|---|---|---|
| anthropic | `ANTHROPIC_API_KEY` | anthropic-messages / Anthropic | https://api.anthropic.com | anthropic.js:43; API-key auth, OAuth variant separate; default `claude-sonnet-4-6` |
| openai | `OPENAI_API_KEY` | openai-responses / Responses | https://api.openai.com/v1 | openai.js:9; endpoint derives `/responses` (openai-responses.js:187); default `gpt-5.2` |
| deepseek | `DEEPSEEK_API_KEY` | openai-completions / OpenAICompletions | https://api.deepseek.com | deepseek.js:9; default `deepseek-v4-pro` |
| nvidia | `NVIDIA_API_KEY` | openai-completions / OpenAICompletions | https://integrate.api.nvidia.com/v1 | nvidia.js:9; default `meta/llama-3.3-70b-instruct` |
| google | `GEMINI_API_KEY` | gemini / Gemini | https://generativelanguage.googleapis.com/v1beta | google.js:9; manifest dialect `gemini` maps to the google-generative-ai protocol (google-generative-ai.js:262); default `gemini-2.5-pro` |
| mistral | `MISTRAL_API_KEY` | openai-completions / OpenAICompletions | https://api.mistral.ai | mistral.js:9; endpoint appends `/v1/chat/completions` (mistral-conversations.js:157); default `codestral-latest` |
| groq | `GROQ_API_KEY` | openai-completions / OpenAICompletions | https://api.groq.com/openai/v1 | groq.js:9; default `llama-3.3-70b-versatile` |
| cerebras | `CEREBRAS_API_KEY` | openai-completions / OpenAICompletions | https://api.cerebras.ai/v1 | cerebras.js:9; default `gpt-oss-120b` |
| cloudflare-ai-gateway | `CLOUDFLARE_API_KEY` | openai-completions / OpenAICompletions | https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat | cloudflare-ai-gateway.json model data; account and gateway ids materialize the placeholders (cloudflare-auth.js:2-3, cloudflare-stream.js); auth header `cf-aig-authorization` (cloudflare-auth.js:59-62); `/openai` and `/anthropic` routes serve the other model families; default `workers-ai/@cf/moonshotai/kimi-k2.6` |
| cloudflare-workers-ai | `CLOUDFLARE_API_KEY` | openai-completions / OpenAICompletions | https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1 | cloudflare-workers-ai.json model data; account id materializes the placeholder; default `@cf/moonshotai/kimi-k2.6` |
| xai | `XAI_API_KEY` | openai-completions / OpenAICompletions | https://api.x.ai/v1 | xai.js:11; pi also exposes the responses dialect, completions is frozen; default `grok-4.6` |
| openrouter | `OPENROUTER_API_KEY` | openai-completions / OpenAICompletions | https://openrouter.ai/api/v1 | openrouter.js:10; default `anthropic/claude-sonnet-4.5` |
| vercel-ai-gateway | `AI_GATEWAY_API_KEY` | anthropic-messages / Anthropic | https://ai-gateway.vercel.sh | vercel-ai-gateway.js:9; default `alibaba/qwen3-max` |
| zai | `ZAI_API_KEY` | openai-completions / OpenAICompletions | https://api.z.ai/api/coding/paas/v4 | zai.js:9; default `glm-5.2` |
| zai-coding-cn | `ZAI_CODING_CN_API_KEY` | openai-completions / OpenAICompletions | https://open.bigmodel.cn/api/coding/paas/v4 | zai-coding-cn.js:9; default `glm-5.2` |
| ant-ling | `ANT_LING_API_KEY` | openai-completions / OpenAICompletions | https://api.ant-ling.com/v1 | ant-ling.js:9; default `Ling-2.6-1T` |
| opencode | `OPENCODE_API_KEY` | openai-completions / OpenAICompletions | https://opencode.ai/zen/v1 | opencode.js has no baseUrl, the model data carries it (opencode.json); default `deepseek-v4-pro` |
| opencode-go | `OPENCODE_API_KEY` | openai-completions / OpenAICompletions | https://opencode.ai/zen/go/v1 | opencode-go.json model data; shares `OPENCODE_API_KEY` with opencode; default `deepseek-v4-pro` |
| fireworks | `FIREWORKS_API_KEY` | openai-completions / OpenAICompletions | https://api.fireworks.ai/inference | fireworks.js:10; endpoint appends `/v1/chat/completions` per model data (fireworks.json); default `accounts/fireworks/models/glm-5p2` |
| together | `TOGETHER_API_KEY` | openai-completions / OpenAICompletions | https://api.together.ai/v1 | together.js:9; default `deepseek-ai/DeepSeek-V4-Pro` |
| baseten | `BASETEN_API_KEY` | openai-completions / OpenAICompletions | https://inference.baseten.co/v1 | baseten.js:9; default `deepseek-ai/DeepSeek-V4-Pro` |
| kimi-coding | `KIMI_API_KEY` | anthropic-messages / Anthropic | https://api.kimi.com/coding | kimi-coding.js:10; OAuth variant separate; default `kimi-for-coding` |
| minimax | `MINIMAX_API_KEY` | anthropic-messages / Anthropic | https://api.minimax.io/anthropic | minimax.js:9; default `MiniMax-M3` |
| minimax-cn | `MINIMAX_CN_API_KEY` | anthropic-messages / Anthropic | https://api.minimaxi.com/anthropic | minimax-cn.js:9; default `MiniMax-M3` |
| qwen-token-plan | `QWEN_TOKEN_PLAN_API_KEY` | openai-completions / OpenAICompletions | https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1 | qwen-token-plan.js:9; default `qwen3.7-max` |
| qwen-token-plan-individual | `QWEN_TOKEN_PLAN_API_KEY` | openai-completions / OpenAICompletions | https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1 | qwen-token-plan-individual.js:9; shares `QWEN_TOKEN_PLAN_API_KEY` with qwen-token-plan; default `qwen3.7-max` |
| qwen-token-plan-cn | `QWEN_TOKEN_PLAN_CN_API_KEY` | openai-completions / OpenAICompletions | https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1 | qwen-token-plan-cn.js:9; default `qwen3.7-max` |
| xiaomi | `XIAOMI_API_KEY` | openai-completions / OpenAICompletions | https://api.xiaomimimo.com/v1 | xiaomi.js:9; default `mimo-v2.5-pro` |
| xiaomi-token-plan-cn | `XIAOMI_TOKEN_PLAN_CN_API_KEY` | openai-completions / OpenAICompletions | https://token-plan-cn.xiaomimimo.com/v1 | xiaomi-token-plan-cn.js:9; default `mimo-v2.5-pro` |
| xiaomi-token-plan-ams | `XIAOMI_TOKEN_PLAN_AMS_API_KEY` | openai-completions / OpenAICompletions | https://token-plan-ams.xiaomimimo.com/v1 | xiaomi-token-plan-ams.js:9; default `mimo-v2.5-pro` |
| xiaomi-token-plan-sgp | `XIAOMI_TOKEN_PLAN_SGP_API_KEY` | openai-completions / OpenAICompletions | https://token-plan-sgp.xiaomimimo.com/v1 | xiaomi-token-plan-sgp.js:9; default `mimo-v2.5-pro` |
| azure-openai-responses | `AZURE_OPENAI_API_KEY` | openai-responses / Responses | env-driven | azure-openai-responses.js:161-174; no static base URL in pi, the endpoint comes from `AZURE_OPENAI_BASE_URL` or `AZURE_OPENAI_RESOURCE_NAME`; deployment and api-version from env, see below; default `gpt-5.2` |

## Build wiring

`Build(id, deps)` looks the spec up in `All` and instantiates the driver of its dialect:

| manifest dialect | driver constructor | endpoint derivation |
|---|---|---|
| openai-completions | `providers.NewOpenAICompletions` | `BaseURL` plus `/chat/completions` |
| anthropic-messages | `providers.NewAnthropic` | `BaseURL` plus `/v1/messages` |
| gemini | `gemini.New` | `BaseURL` as the API root, the driver appends the model method |
| openai-responses | `responses.New` | `BaseURL` plus `/responses` |

Two openai-completions providers carry the version segment in their model catalogues instead of the provider base URL, so their endpoints append `/v1/chat/completions`: mistral (mistral-conversations.js:157) and fireworks (fireworks.json). The gemini dialect maps to the google-generative-ai wire protocol, the `API` field the driver emits.

### Credential resolution

The credential resolves through `authstore.ResolveCredential` with environment precedence: the spec's env var wins when set, otherwise the auth store entry under the provider id, matching pi's resolution order. The resolution is lazy for most specs: the driver resolves the key per request and fails the turn with a clear error naming the env var when neither source carries one. `Wire` therefore registers every constructible spec regardless of which keys are configured, and the picker surfaces the auth error at turn time.

### Cloudflare

Both cloudflare specs read `CLOUDFLARE_ACCOUNT_ID` at build time and error when it is unset, because the account id is part of the endpoint. `cloudflare-ai-gateway` additionally requires `CLOUDFLARE_GATEWAY_ID` and resolves `CLOUDFLARE_API_KEY` at build time: the key forms the `cf-aig-authorization` header, the auth the Cloudflare gateway documents, and also flows through the driver auth closure, mirroring pi's inline-BYOK mode where the request carries both the Cloudflare token and an upstream `Authorization` header. `cloudflare-workers-ai` keeps the key lazy and sends it as the standard Bearer token, exactly like pi.

### Azure

The azure spec has no static base URL; `Build` resolves it from `AZURE_OPENAI_BASE_URL`, falling back to `https://<AZURE_OPENAI_RESOURCE_NAME>.openai.azure.com`, and errors when neither is set. `AZURE_OPENAI_API_VERSION` sets the api-version query parameter (default `v1`). The deployment name follows pi's rule, `AZURE_OPENAI_DEPLOYMENT_NAME_MAP` entry for the model else the model id, evaluated for the spec's default model: the responses driver takes a single deployment name at construction, so per-model maps apply to the default model until per-model wiring lands.

## Source map

The base URL of every row comes from the pi-ai dist sources:

- provider base URLs: `pi-ai/dist/providers/anthropic.js:43`, `openai.js:9`, `deepseek.js:9`, `nvidia.js:9`, `google.js:9`, `mistral.js:9`, `groq.js:9`, `cerebras.js:9`, `xai.js:11`, `openrouter.js:10`, `vercel-ai-gateway.js:9`, `zai.js:9`, `zai-coding-cn.js:9`, `ant-ling.js:9`, `fireworks.js:10`, `together.js:9`, `baseten.js:9`, `kimi-coding.js:10`, `minimax.js:9`, `minimax-cn.js:9`, `qwen-token-plan.js:9`, `qwen-token-plan-individual.js:9`, `qwen-token-plan-cn.js:9`, `xiaomi.js:9`, `xiaomi-token-plan-cn.js:9`, `xiaomi-token-plan-ams.js:9`, `xiaomi-token-plan-sgp.js:9`.
- cloudflare endpoints: `pi-ai/dist/providers/data/cloudflare-ai-gateway.json` and `data/cloudflare-workers-ai.json`; placeholder resolution in `cloudflare-stream.js` and `cloudflare-auth.js:2-3`; the `cf-aig-authorization` header in `cloudflare-auth.js:59-62`.
- opencode endpoints: `pi-ai/dist/providers/data/opencode.json` and `data/opencode-go.json`; the provider files carry no base URL.
- wire paths: `pi-ai/dist/api/openai-completions.js` (SDK appends `/chat/completions`), `anthropic-messages.js:670-709` (SDK appends `/v1/messages`), `mistral-conversations.js:157` (`v1/chat/completions`), `openai-responses.js:187` (`/responses`), `google-generative-ai.js:262-264`.
- azure: `pi-ai/dist/api/azure-openai-responses.js:161-174` (base URL, resource name, api-version defaults), `:35` (deployment name map).
- environment variable mapping: `pi-ai/dist/env-api-keys.js` and the pi `docs/providers.md` API-key table.

## OAuth providers

The subscription and OAuth variants of manifest providers are out of scope for this block and wire separately in block 7: `openrouter-oauth`, `anthropic-oauth`, `codex`, `xai-subscription`, and `kimi-coding-oauth`. The smidja drivers already carry the OAuth seams they need: `providers.Anthropic` switches on `OAuth` for subscription auth, and `responses` has the Codex mode.

## Provider count

The manifest freezes 32 providers: 24 openai-completions, 5 anthropic-messages, 1 gemini, and 2 openai-responses (openai and azure-openai-responses).
