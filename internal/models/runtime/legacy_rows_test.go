package runtime_test

// Backward-compatibility guard for model rows written before the vendor
// catalog existed (internal/models/provider). Every shape in this file is one
// that a production `models` table can already contain, so a failure here
// means an existing deployment breaks on upgrade without a migration.

import (
	"bytes"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
)

// legacyProviderIDs is the complete set of parameters.provider values the
// pre-catalog code could write: provider.AllProviders() plus weknoracloud,
// as of internal/models/provider/provider.go before the refactor.
var legacyProviderIDs = []string{
	"generic", "weknoracloud", "aliyun", "zhipu", "volcengine", "hunyuan",
	"siliconflow", "deepseek", "minimax", "moonshot", "modelscope", "qianfan",
	"qiniu", "openai", "anthropic", "gemini", "openrouter", "litellm",
	"requesty", "jina", "mimo", "longcat", "lkeap", "gpustack", "nvidia",
	"novita", "azure_openai",
}

// TestLegacyProviderIDsStillRegistered asserts that no provider id an old row
// may carry has disappeared from the catalog. A missing id would silently
// degrade that row to the generic OpenAI baseline (wrong auth, wrong URL,
// wrong thinking encoding).
func TestLegacyProviderIDsStillRegistered(t *testing.T) {
	for _, id := range legacyProviderIDs {
		if _, ok := modelruntime.Get(id); !ok {
			t.Errorf("provider id %q was writable by the old code but has no vendor now", id)
		}
	}
}

// legacyRow is one realistic pre-refactor `models` row.
type legacyRow struct {
	name   string
	model  string
	typ    types.ModelType
	params types.ModelParameters
}

// legacyRows covers the shapes the old write paths produced: the model editor
// (provider + base_url + api_key, no extra_config at all), the initialization
// wizard (thinking_control / api_version / remote_model_name), YAML builtin
// models, and rows hand-written against an older release.
func legacyRows() []legacyRow {
	rows := []legacyRow{
		{
			name:  "editor row, catalogued model, no extra_config",
			model: "deepseek-chat", typ: types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{Provider: "deepseek", BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-x"},
		},
		{
			name:  "editor row, model the vendor has since retired",
			model: "gpt-3.5-turbo-0301", typ: types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "sk-x"},
		},
		{
			name:  "self-hosted fine-tune behind a generic endpoint",
			model: "acme-corp/llama-3.1-70b-finetune-v7", typ: types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{Provider: "generic", BaseURL: "http://vllm.internal:8000/v1"},
		},
		{
			name:  "row written before parameters.provider existed",
			model: "qwen-plus", typ: types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		},
		{
			name:  "row with neither provider nor base_url",
			model: "some-model", typ: types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{},
		},
		{
			name:  "provider id a hand-edited row invented",
			model: "grok-4", typ: types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{Provider: "xai", BaseURL: "https://api.x.ai/v1"},
		},
		{
			name: "VLM row", model: "qwen-vl-max", typ: types.ModelTypeVLLM,
			params: types.ModelParameters{
				Provider: "aliyun", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
				InterfaceType: "openai", SupportsVision: true,
			},
		},
		{
			name: "local Ollama VLM row (interface_type ollama)", model: "llava:13b", typ: types.ModelTypeVLLM,
			params: types.ModelParameters{
				InterfaceType: "ollama", ParameterSize: "13B", BaseURL: "http://localhost:11434",
			},
		},
		{
			name: "WeKnoraCloud row with app credentials", model: "weknora-chat", typ: types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{
				Provider: "weknoracloud", BaseURL: "https://weknora.weixin.qq.com",
				AppID: "app", AppSecret: "secret",
				ExtraConfig: map[string]string{"remote_model_name": "hunyuan-turbos"},
			},
		},
		{
			name: "Azure row from the initialization wizard", model: "gpt-4o-deployment",
			typ: types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{
				Provider: "azure_openai", BaseURL: "https://my-resource.openai.azure.com",
				ExtraConfig: map[string]string{"api_version": "2024-10-21"},
			},
		},
		{
			name: "Azure row created from the model editor (no api_version)", model: "gpt-4o-deployment",
			typ: types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{
				Provider: "azure_openai", BaseURL: "https://my-resource.openai.azure.com",
			},
		},
		{
			name:  "every legacy extra_config key at once, plus an unknown one",
			model: "custom-model", typ: types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{
				Provider: "generic", BaseURL: "http://vllm.internal:8000/v1",
				ExtraConfig: map[string]string{
					"thinking_control":            "chat_template_kwargs",
					"remote_model_name":           "Qwen/Qwen3-32B",
					"api_version":                 "2024-10-21",
					"secret_key":                  "sk",
					"region":                      "ap-guangzhou",
					"instruction":                 "rank these",
					"truncate_prompt_tokens":      "4096",
					"deployment":                  "prod",
					"freshness":                   "week",
					"a_key_no_release_ever_wrote": "1",
				},
			},
		},
		{
			name: "embedding row with dimension override", model: "text-embedding-v4", typ: types.ModelTypeEmbedding,
			params: types.ModelParameters{
				Provider: "aliyun", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
				EmbeddingParameters: types.EmbeddingParameters{
					Dimension: 1024, TruncatePromptTokens: 2048, SupportsDimensionOverride: true,
				},
			},
		},
		{
			name: "LKEAP signed rerank row", model: "lkeap-rerank", typ: types.ModelTypeRerank,
			params: types.ModelParameters{
				Provider: "lkeap", BaseURL: "https://lkeap.tencentcloudapi.com",
				ExtraConfig: map[string]string{"secret_key": "sk", "region": "ap-guangzhou"},
			},
		},
		{
			name: "custom headers row", model: "gateway-model", typ: types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{
				Provider: "generic", BaseURL: "https://gateway.corp.example.com/v1",
				CustomHeaders: map[string]string{"X-Corp-Route": "llm"},
				ContextWindow: 128000, MaxOutputTokens: 8192, MaxConcurrency: 4,
			},
		},
	}
	// Every legacy provider id, carrying a model name no models.json knows.
	// This is the "custom fine-tune / retired model / self-hosted name" case
	// for each vendor at once.
	for _, id := range legacyProviderIDs {
		rows = append(rows, legacyRow{
			name: "uncatalogued model on provider " + id, model: "a-model-no-catalog-knows",
			typ:    types.ModelTypeKnowledgeQA,
			params: types.ModelParameters{Provider: id},
		})
	}
	return rows
}

// TestLegacyRowsValidateAndResolve is the core guarantee: opening an existing
// model in the UI and saving it (PUT /models/{id} runs modelruntime.ValidateRow)
// must not 400, and the chat path must still resolve the row.
func TestLegacyRowsValidateAndResolve(t *testing.T) {
	for _, row := range legacyRows() {
		t.Run(row.name, func(t *testing.T) {
			params := row.params
			if err := modelruntime.ValidateRow(row.model, row.typ, &params); err != nil {
				t.Fatalf("ValidateRow rejected a row the old code accepted: %v", err)
			}
			resolved, err := modelruntime.Resolve(modelruntime.Ref{
				Provider: params.Provider, Model: row.model, BaseURL: params.BaseURL,
				ModelType: row.typ, Extra: params.ExtraConfig, Override: params.Spec,
			})
			if err != nil {
				t.Fatalf("Resolve failed: %v", err)
			}
			if resolved.Vendor == nil {
				t.Fatal("resolved with a nil vendor")
			}
			// Each model type resolves to its own protocol vocabulary. They
			// are separate Go types, so a row can never land on the wrong one.
			switch row.typ {
			case types.ModelTypeRerank:
				if !resolved.RerankAPI.Known() {
					t.Fatalf("resolved to an unknown rerank protocol %q", resolved.RerankAPI)
				}
			case types.ModelTypeEmbedding:
				if !resolved.EmbeddingAPI.Known() {
					t.Fatalf("resolved to an unknown embedding protocol %q", resolved.EmbeddingAPI)
				}
			default:
				if !resolved.API.Known() {
					t.Fatalf("resolved to an unknown protocol %q", resolved.API)
				}
			}
			if resolved.RemoteModel == "" && row.model != "" {
				t.Fatal("resolved to an empty remote model id")
			}
		})
	}
}

// TestEveryCataloguedModelResolves walks the whole catalog so a row naming any
// shipped model id — which is what the old model picker wrote — is known to
// resolve.
func TestEveryCataloguedModelResolves(t *testing.T) {
	for _, v := range modelruntime.List() {
		for _, m := range v.Models() {
			if m.ID == "" {
				continue
			}
			// An entry that declares this build cannot serve it must refuse,
			// on save as at construction; vendors_test pins that it does.
			if bytes.Contains(m.Compat, []byte("unsupported_reason")) {
				continue
			}
			modelType := m.Type
			if modelType == "" {
				modelType = types.ModelTypeKnowledgeQA
			}
			if err := modelruntime.ValidateRow(m.ID, modelType, &types.ModelParameters{Provider: v.ID}); err != nil {
				t.Errorf("%s/%s: ValidateRow: %v", v.ID, m.ID, err)
			}
		}
	}
}

// legacyThinkingControl is the old chat.parseThinkingOverride mapping, from
// internal/models/chat/thinking.go before the refactor. The four values are
// the only ones the old frontend wrote (frontend/src/utils/thinkingControl.ts),
// but an unrecognized non-empty value historically fell back to
// chat_template_kwargs and must keep doing so.
var legacyThinkingControl = map[string]api.ThinkingFormat{
	"none":                 api.ThinkingFormatNone,
	"enable_thinking":      api.ThinkingFormatEnableThinking,
	"thinking_type":        api.ThinkingFormatThinkingType,
	"chat_template_kwargs": api.ThinkingFormatChatTemplateKwargs,
	"enabled":              api.ThinkingFormatChatTemplateKwargs,
	"ENABLE_THINKING":      api.ThinkingFormatEnableThinking,
	"  thinking_type  ":    api.ThinkingFormatThinkingType,
}

// TestLegacyThinkingControlMapping pins extra_config.thinking_control to the
// old semantics for every value a stored row can hold, including on a
// catalogued non-reasoning model where the new silencing pass would otherwise
// drop the thinking fields.
func TestLegacyThinkingControlMapping(t *testing.T) {
	for value, want := range legacyThinkingControl {
		t.Run(value, func(t *testing.T) {
			resolved, err := modelruntime.Resolve(modelruntime.Ref{
				Provider: "generic", Model: "Qwen/Qwen3-32B",
				BaseURL: "http://vllm.internal:8000/v1", ModelType: types.ModelTypeKnowledgeQA,
				Extra: map[string]string{"thinking_control": value},
			})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got := resolved.OpenAICompletions.ThinkingFormat; got != want {
				t.Fatalf("thinking_control=%q resolved to %q, old code selected %q", value, got, want)
			}
		})
	}
}

// TestLegacyThinkingControlSurvivesCatalogSilencing guards the interaction
// between a stored thinking_control and silenceThinkingForNonReasoningModel:
// an operator who explicitly picked an encoding must keep it even when the
// catalog marks the model non-reasoning.
func TestLegacyThinkingControlSurvivesCatalogSilencing(t *testing.T) {
	resolved, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: "openai", Model: "gpt-4o", ModelType: types.ModelTypeKnowledgeQA,
		Extra: map[string]string{"thinking_control": "chat_template_kwargs"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.OpenAICompletions.ThinkingFormat != api.ThinkingFormatChatTemplateKwargs {
		t.Fatalf("explicit thinking_control was overridden by the catalog: %q",
			resolved.OpenAICompletions.ThinkingFormat)
	}
}

// TestUncataloguedModelKeepsThinkingSwitch documents the deliberate asymmetry
// in silenceThinkingForNonReasoningModel: a model nobody catalogued (the
// self-hosted case) keeps its thinking switch, which is what old rows relied
// on, while a catalogued non-reasoning model loses it.
func TestUncataloguedModelKeepsThinkingSwitch(t *testing.T) {
	unknown, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: "generic", Model: "acme/llama-3.1-70b-finetune",
		BaseURL: "http://vllm.internal:8000/v1", ModelType: types.ModelTypeKnowledgeQA,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if unknown.Cataloged {
		t.Fatal("expected the fine-tune to be uncatalogued")
	}
	if unknown.OpenAICompletions.ThinkingFormat != api.ThinkingFormatChatTemplateKwargs {
		t.Fatalf("uncatalogued model lost its thinking switch: %q",
			unknown.OpenAICompletions.ThinkingFormat)
	}
}

// TestLegacyRemoteModelNameHonoured pins extra_config.remote_model_name, which
// the old chat and embedding paths both read.
func TestLegacyRemoteModelNameHonoured(t *testing.T) {
	resolved, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: "generic", Model: "display-name", BaseURL: "http://vllm.internal:8000/v1",
		ModelType: types.ModelTypeKnowledgeQA,
		Extra:     map[string]string{"remote_model_name": "Qwen/Qwen3-32B"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.RemoteModel != "Qwen/Qwen3-32B" {
		t.Fatalf("remote_model_name ignored: wire model is %q", resolved.RemoteModel)
	}
}

// TestUnknownProviderFallsBackToGeneric checks the reverse direction: a row
// naming a provider this build does not ship degrades to the generic
// OpenAI-compatible baseline instead of failing the write.
func TestUnknownProviderFallsBackToGeneric(t *testing.T) {
	resolved, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: "a-vendor-from-the-future", Model: "m",
		BaseURL: "https://example.com/v1", ModelType: types.ModelTypeKnowledgeQA,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Vendor.ID != providers.GenericID {
		t.Fatalf("unknown provider resolved to %q, want %q", resolved.Vendor.ID, providers.GenericID)
	}
	if resolved.API != api.APIOpenAICompletions {
		t.Fatalf("unknown provider resolved to protocol %q", resolved.API)
	}
}

// TestLegacyStoredBaseURLSelectsSameProtocol pins the protocol an existing
// row resolves to. Old rows always store an explicit base_url (the model
// editor pre-filled the provider default), so the stored URL — not the new
// vendor default — decides the wire protocol on upgrade. The Gemini case and
// the first-party OpenAI move to Responses are covered in
// internal/models/parity; what is asserted here is the relay case, where the
// same provider id must NOT follow OpenAI onto /responses.
func TestLegacyStoredBaseURLSelectsSameProtocol(t *testing.T) {
	cases := []struct {
		provider, baseURL string
		want              api.API
		why               string
	}{
		{
			provider: "openai", baseURL: "https://llm-relay.corp.example.com/v1",
			want: api.APIOpenAICompletions,
			why:  "a relay that only speaks Chat Completions must stay on it",
		},
		{
			provider: "openai", baseURL: "https://litellm.corp.example.com/v1",
			want: api.APIOpenAICompletions,
			why:  "an OpenAI row pointed at a LiteLLM proxy keeps Chat Completions",
		},
		{
			provider: "anthropic", baseURL: "https://api.anthropic.com/v1",
			want: api.APIAnthropicMessages,
			why:  "the old code already dispatched anthropic to the Messages protocol",
		},
		{
			provider: "generic", baseURL: "http://vllm.internal:8000/v1",
			want: api.APIOpenAICompletions, why: "unchanged",
		},
	}
	for _, tc := range cases {
		t.Run(tc.provider+" "+tc.baseURL, func(t *testing.T) {
			resolved, err := modelruntime.Resolve(modelruntime.Ref{
				Provider: tc.provider, Model: "some-model", BaseURL: tc.baseURL,
				ModelType: types.ModelTypeKnowledgeQA,
			})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if resolved.API != tc.want {
				t.Fatalf("protocol %q, want %q (%s)", resolved.API, tc.want, tc.why)
			}
		})
	}
}

// TestLegacyFreeFormAPIExtraKeyIsRejected documents the one stored shape this
// refactor does break, so the trade-off stays visible.
//
// extra_config has always been a free-form map[string]string: the old backend
// read only thinking_control, remote_model_name, api_version, secret_key,
// region, instruction and truncate_prompt_tokens from it and ignored every
// other key. The catalog gave the key "api" a meaning — it forces the wire
// protocol — and ValidateRow now rejects a value that is not one of the five
// protocol names. A row that an operator hand-populated with, say,
// {"api": "v1"} therefore fails PUT /models/{id} with 400 and fails every
// chat call, even though nothing about that row changed.
//
// Nothing in WeKnora ever wrote this key, so only hand-built rows and
// hand-written builtin_models.yaml entries are affected. If that is judged too
// sharp, the fix is to ignore an unparseable extra_config.api in Resolve and
// keep the strict check for Spec.API, which no old row can carry.
func TestLegacyFreeFormAPIExtraKeyIsRejected(t *testing.T) {
	err := modelruntime.ValidateRow("gpt-4o", types.ModelTypeKnowledgeQA, &types.ModelParameters{
		Provider: "openai", BaseURL: "https://api.openai.com/v1",
		ExtraConfig: map[string]string{"api": "v1"},
	})
	if err == nil {
		t.Skip("extra_config.api is now tolerant of legacy free-form values; " +
			"delete this test and the release note that goes with it")
	}
	t.Logf("documented break: %v", err)
}
