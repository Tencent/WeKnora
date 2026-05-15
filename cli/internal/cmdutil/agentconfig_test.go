package cmdutil

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdk "github.com/Tencent/WeKnora/client"
)

func TestLoadConfigFile_YAML(t *testing.T) {
	yaml := strings.NewReader(`
agent_mode: smart-reasoning
model_id: gpt-4
temperature: 0.7
knowledge_bases:
  - kb_abc
  - kb_def
`)
	cfg, err := LoadAgentConfig(yaml, "yaml")
	require.NoError(t, err)
	assert.Equal(t, "smart-reasoning", cfg.AgentMode)
	assert.Equal(t, "gpt-4", cfg.ModelID)
	assert.InDelta(t, 0.7, cfg.Temperature, 0.001)
	assert.Equal(t, []string{"kb_abc", "kb_def"}, cfg.KnowledgeBases)
}

func TestLoadConfigFile_JSON(t *testing.T) {
	js := strings.NewReader(`{"agent_mode":"quick-answer","model_id":"gpt-3.5"}`)
	cfg, err := LoadAgentConfig(js, "json")
	require.NoError(t, err)
	assert.Equal(t, "quick-answer", cfg.AgentMode)
	assert.Equal(t, "gpt-3.5", cfg.ModelID)
}

func TestLoadConfigFile_UnknownKind(t *testing.T) {
	_, err := LoadAgentConfig(strings.NewReader(""), "xml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown config format")
}

func TestLoadConfigFile_BadYAML(t *testing.T) {
	_, err := LoadAgentConfig(strings.NewReader("not: : yaml"), "yaml")
	require.Error(t, err)
}

func TestLoadConfigFile_BadJSON(t *testing.T) {
	_, err := LoadAgentConfig(strings.NewReader("{not json"), "json")
	require.Error(t, err)
}

func TestMergeAgentConfig_FlagsOverrideFile(t *testing.T) {
	base := &sdk.AgentConfig{ModelID: "gpt-3.5"}
	overrides := AgentConfigFlags{ModelIDSet: true, ModelID: "gpt-4"}
	merged := MergeAgentConfig(base, overrides)
	assert.Equal(t, "gpt-4", merged.ModelID)
}

func TestMergeAgentConfig_UnsetFlagsPreserveBase(t *testing.T) {
	base := &sdk.AgentConfig{ModelID: "gpt-3.5", Temperature: 0.5}
	overrides := AgentConfigFlags{} // nothing set
	merged := MergeAgentConfig(base, overrides)
	assert.Equal(t, "gpt-3.5", merged.ModelID)
	assert.InDelta(t, 0.5, merged.Temperature, 0.001)
}

func TestMergeAgentConfig_EveryFieldOverlay(t *testing.T) {
	base := &sdk.AgentConfig{
		AgentMode:       "quick-answer",
		SystemPrompt:    "old",
		ModelID:         "gpt-3.5",
		RerankModelID:   "rerank-old",
		Temperature:     0.1,
		KBSelectionMode: "all",
		KnowledgeBases:  []string{"kb_old"},
	}
	overrides := AgentConfigFlags{
		AgentMode: "smart-reasoning", AgentModeSet: true,
		SystemPrompt: "new", SystemPromptSet: true,
		ModelID: "gpt-4", ModelIDSet: true,
		RerankModelID: "rerank-new", RerankModelIDSet: true,
		Temperature: 0.9, TemperatureSet: true,
		KBSelectionMode: "selected", KBSelectionModeSet: true,
		KnowledgeBases: []string{"kb_new"}, KnowledgeBasesSet: true,
	}
	merged := MergeAgentConfig(base, overrides)
	assert.Equal(t, "smart-reasoning", merged.AgentMode)
	assert.Equal(t, "new", merged.SystemPrompt)
	assert.Equal(t, "gpt-4", merged.ModelID)
	assert.Equal(t, "rerank-new", merged.RerankModelID)
	assert.InDelta(t, 0.9, merged.Temperature, 0.001)
	assert.Equal(t, "selected", merged.KBSelectionMode)
	assert.Equal(t, []string{"kb_new"}, merged.KnowledgeBases)
}

func TestGenerateSkeleton_EmitsAllFields(t *testing.T) {
	var out strings.Builder
	require.NoError(t, GenerateAgentSkeleton(&out))
	body := out.String()
	for _, field := range []string{
		"agent_mode:",
		"model_id:",
		"system_prompt:",
		"knowledge_bases:",
		"fallback_strategy:",
		"context_template:",
		"max_completion_tokens:",
		"embedding_top_k:",
	} {
		assert.Contains(t, body, field, "skeleton missing field %s", field)
	}
}
