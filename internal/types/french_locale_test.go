package types

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestFrenchBuiltinAgentMetadata(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "builtin_agents.yaml"))
	require.NoError(t, err)
	var file builtinAgentsFile
	require.NoError(t, yaml.Unmarshal(data, &file))
	require.NotEmpty(t, file.BuiltinAgents)
	entries := make(map[string]*BuiltinAgentEntry, len(file.BuiltinAgents))
	for i := range file.BuiltinAgents {
		entry := &file.BuiltinAgents[i]
		require.NotEmpty(t, entry.I18n["fr-FR"].Name, entry.ID)
		require.NotEmpty(t, entry.I18n["fr-FR"].Description, entry.ID)
		entries[entry.ID] = entry
	}
	t.Cleanup(OverrideBuiltinAgentEntriesForTest(entries))
	ctx := context.WithValue(context.Background(), LanguageContextKey, "fr-FR")
	for _, entry := range file.BuiltinAgents {
		agent := GetBuiltinAgentWithContext(ctx, entry.ID, 1)
		require.NotNil(t, agent)
		assert.Equal(t, entry.I18n["fr-FR"].Name, agent.Name)
		assert.Equal(t, entry.I18n["fr-FR"].Description, agent.Description)
	}
	// Changing the UI locale must preserve an existing agent's user settings.
	agent := &CustomAgent{
		ID: BuiltinQuickAnswerID, TenantID: 1, Name: "Quick Answer",
		Config: CustomAgentConfig{SystemPrompt: "Custom instructions", Temperature: 0.42},
	}
	before := agent.Config
	ApplyBuiltinAgentLocalization(ctx, agent)
	assert.Equal(t, "Réponse rapide", agent.Name)
	assert.Equal(t, before, agent.Config)
	assert.Equal(t, "French", LanguageLocaleName("fr-FR"))
}

func TestFrenchAgentTypePresetMetadata(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "agent_type_presets.yaml"))
	require.NoError(t, err)
	var file struct {
		Presets []AgentTypePresetEntry `yaml:"agent_type_presets"`
	}
	require.NoError(t, yaml.Unmarshal(data, &file))
	require.NotEmpty(t, file.Presets)
	for _, preset := range file.Presets {
		localized := resolveAgentTypeI18n(preset.I18n, "fr-FR")
		require.NotEmpty(t, localized["fr-FR"].Label, preset.ID)
		require.NotEmpty(t, localized["fr-FR"].Description, preset.ID)
		assert.Equal(t, preset.I18n["default"], localized["default"])
	}
}
