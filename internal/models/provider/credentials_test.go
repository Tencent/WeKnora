package provider

// Credentials spec tests (design v2 §6.8): provider-level declaration,
// synthesis chain in EffectiveCapabilities, and JSON exposure through the
// capabilities payload the /models/providers DTO serializes verbatim.

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCredentialsFor(t *testing.T) {
	openAIStyle := []CredentialFieldSpec{
		{Key: CredentialKeyAPIKey, Required: true, LabelKey: "model.credentials.apiKey"},
	}
	custom := []CredentialFieldSpec{{Key: CredentialKeyAPIKey}}

	// openai 系与 anthropic：必填 api_key。
	for _, name := range []ProviderName{
		ProviderOpenAI, ProviderAzureOpenAI, ProviderAnthropic, ProviderDeepSeek,
		ProviderAliyun, ProviderMoonshot, ProviderZhipu,
	} {
		assert.Equal(t, openAIStyle, credentialsFor(name), "provider %s", name)
	}
	// lkeap/volcengine：api_key 必填 + app_secret 可选（AK/SK 签名 rerank，
	// rerank/lkeap_reranker.go / rerank/volcengine_reranker.go 调用期校验）。
	signedRerank := append(append([]CredentialFieldSpec{}, openAIStyle...),
		CredentialFieldSpec{Key: CredentialKeyAppSecret, LabelKey: "model.editor.appSecretLabel"})
	assert.Equal(t, signedRerank, credentialsFor(ProviderVolcengine))
	assert.Equal(t, signedRerank, credentialsFor(ProviderLKEAP))
	// 自定义/自部署：api_key 可空。
	assert.Equal(t, custom, credentialsFor(ProviderGeneric))
	// weknoracloud：空数组（空间级凭证 + 租户回退，不做每模型表单）。
	assert.Equal(t, []CredentialFieldSpec{}, credentialsFor(ProviderWeKnoraCloud))
}

func TestEffectiveCapabilitiesFillsCredentials(t *testing.T) {
	// 合成路径。
	got := ProviderInfo{
		Name:       ProviderOpenAI,
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA},
	}.EffectiveCapabilities()
	require.NotNil(t, got.Chat)
	require.Len(t, got.Credentials, 1)
	assert.Equal(t, CredentialKeyAPIKey, got.Credentials[0].Key)
	assert.True(t, got.Credentials[0].Required)

	// weknoracloud 合成路径：空数组（非 nil），DTO 显示 []。
	got = ProviderInfo{
		Name:       ProviderWeKnoraCloud,
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA},
	}.EffectiveCapabilities()
	require.NotNil(t, got.Chat)
	require.NotNil(t, got.Credentials)
	assert.Empty(t, got.Credentials)

	// 显式声明路径：显式 Credentials 获胜。
	explicit := []CredentialFieldSpec{{Key: CredentialKeyAppID, Required: true}}
	got = ProviderInfo{
		Name:         ProviderOpenAI,
		ModelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA},
		Capabilities: Capabilities{Chat: &ChatCaps{}, Credentials: explicit},
	}.EffectiveCapabilities()
	assert.Equal(t, explicit, got.Credentials)

	// 显式声明但未设 Credentials：仍按厂商知识表回填。
	got = ProviderInfo{
		Name:         ProviderWeKnoraCloud,
		ModelTypes:   []types.ModelType{types.ModelTypeKnowledgeQA},
		Capabilities: Capabilities{Chat: &ChatCaps{}},
	}.EffectiveCapabilities()
	require.NotNil(t, got.Credentials)
	assert.Empty(t, got.Credentials)
}

func TestCapabilitiesJSONExposesCredentials(t *testing.T) {
	// /models/providers 的 DTO 将 Capabilities 原样序列化；credentials 字段
	// 必须出现在 JSON 中。
	caps := ProviderInfo{
		Name:       ProviderOpenAI,
		ModelTypes: []types.ModelType{types.ModelTypeKnowledgeQA},
	}.EffectiveCapabilities()
	data, err := json.Marshal(caps)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"common": {"streaming": true, "health_probe": true, "token_counting": false,
			"usage_reporting": "full", "model_listing": {"supported": false}},
		"chat": {"thinking": {"supported": true, "can_disable": true,
			"supported_levels": ["low","medium","high"], "default_level": "medium"},
			"input_modalities": ["text"], "protocol": "openai_chat", "parallel_tool_calls": true},
		"credentials": [{"key": "api_key", "required": true, "label_key": "model.credentials.apiKey"}]
	}`, string(data))
}
