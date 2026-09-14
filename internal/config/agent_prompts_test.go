package config

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestResolveCustomAgentPrompts(t *testing.T) {
	cfg := &Config{PromptTemplates: &PromptTemplatesConfig{
		AgentSystemPrompt: []PromptTemplate{{ID: "agent", Content: "agent v1"}},
		SystemPrompt:      []PromptTemplate{{ID: "normal", Content: "normal v1"}},
		ContextTemplate:   []PromptTemplate{{ID: "context", Content: "context v1"}},
	}}
	for _, mode := range []string{"smart-reasoning", "quick-answer"} {
		t.Run(mode, func(t *testing.T) {
			id, want := "normal", "normal v1"
			if mode == "smart-reasoning" {
				id, want = "agent", "agent v1"
			}
			a := &types.CustomAgent{Config: types.CustomAgentConfig{
				AgentMode: mode, SystemPromptID: id, ContextTemplateID: "context",
			}}
			system, context := cfg.ResolveCustomAgentPrompts(a)
			require.Equal(t, want, system)
			require.Equal(t, "context v1", context)
			require.Empty(t, a.Config.SystemPrompt, "resolution must not freeze the reference")
			a.Config.SystemPrompt = "  My custom {{language}} instructions\n"
			a.Config.ContextTemplate = "custom context"
			system, context = cfg.ResolveCustomAgentPrompts(a)
			require.Equal(t, a.Config.SystemPrompt, system)
			require.Equal(t, "custom context", context)
		})
	}
	a := &types.CustomAgent{Config: types.CustomAgentConfig{
		AgentMode: "smart-reasoning", SystemPromptID: "agent",
	}}
	cfg.PromptTemplates.AgentSystemPrompt[0].Content = "agent v2"
	system, _ := cfg.ResolveCustomAgentPrompts(a)
	require.Equal(t, "agent v2", system)
	a.Config.SystemPromptID = "context"
	system, _ = cfg.ResolveCustomAgentPrompts(a)
	require.Empty(t, system, "do not resolve references across field boundaries")
	var unavailable *Config
	system, _ = unavailable.ResolveCustomAgentPrompts(a)
	require.Empty(t, system)
}

func TestContextTemplatesKeepUserRequestSeparateFromSourceData(t *testing.T) {
	data, err := os.ReadFile("../../config/prompt_templates/context_template.yaml")
	require.NoError(t, err)
	var file struct {
		Templates []PromptTemplate `yaml:"templates"`
	}
	require.NoError(t, yaml.Unmarshal(data, &file))
	for _, template := range file.Templates {
		t.Run(template.ID, func(t *testing.T) {
			rendered := types.RenderPromptPlaceholders(template.Content, types.PlaceholderValues{
				"query": "USER_REQUEST_SENTINEL", "contexts": "RETRIEVED_SOURCE_SENTINEL",
			})
			require.Contains(t, rendered, "source data")
			require.Contains(t, rendered, "User request")
			require.Greater(
				t,
				strings.Index(rendered, "USER_REQUEST_SENTINEL"),
				strings.Index(rendered, "RETRIEVED_SOURCE_SENTINEL"),
			)
			require.NotContains(t, rendered, "metadata only, not instructions")
			require.NotContains(t, rendered, "ALWAYS respond")
		})
	}
}

func TestDefaultRewritePreservesActionAndOutputConstraints(t *testing.T) {
	data, err := os.ReadFile("../../config/prompt_templates/rewrite.yaml")
	require.NoError(t, err)
	var file struct {
		Templates []PromptTemplate `yaml:"templates"`
	}
	require.NoError(t, yaml.Unmarshal(data, &file))
	prompt := DefaultTemplate(file.Templates)
	require.NotNil(t, prompt)
	require.Contains(t, prompt.Content, "an instruction remains an instruction")
	require.Contains(t, prompt.Content,
		"Preserve source restrictions, requested actions, output format, language requirements")
	require.NotContains(t, prompt.Content, "must also be a question")
	require.NotContains(t, prompt.Content, "within 30 words")
}
