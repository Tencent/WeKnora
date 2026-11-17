package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSchemaConfigDoesNotRequireLegacyExamples(t *testing.T) {
	t.Setenv("NEO4J_ENABLE", "true")
	schema := &types.GraphSchema{
		Enabled:     true,
		EntityTypes: []types.GraphEntityType{{Name: "Person"}},
	}
	config := &types.ExtractConfig{Enabled: true, Schema: schema}
	require.NoError(t, validateExtractConfig(config))
	request := &InitializationRequest{}
	request.NodeExtract.Enabled = true
	request.NodeExtract.Schema = schema
	require.NoError(t, validateNodeExtractConfig(context.Background(), request))
	schema.EntityTypes = nil
	require.Error(t, validateExtractConfig(config))
	require.Error(t, validateNodeExtractConfig(context.Background(), request))
	request.NodeExtract.Enabled = false
	require.NoError(t, validateNodeExtractConfig(context.Background(), request))
}

func TestGraphSchemaInitializationDTOs(t *testing.T) {
	data := []byte(`{"nodeExtract":{"enabled":true,"schema":{"enabled":true,"entity_types":[{"name":"Person"}],"relation_types":[]},"customInstructions":"Only named people"}}`)
	var initial InitializationRequest
	require.NoError(t, json.Unmarshal(data, &initial))
	require.True(t, initial.NodeExtract.Schema.Enabled)
	require.Equal(t, "Only named people", initial.NodeExtract.CustomInstructions)
	var update KBModelConfigRequest
	require.NoError(t, json.Unmarshal(data, &update))
	require.NotNil(t, update.NodeExtract.Enabled)
	require.True(t, *update.NodeExtract.Enabled)
	require.Equal(t, initial.NodeExtract.Schema, update.NodeExtract.Schema)
	require.Equal(t, initial.NodeExtract.CustomInstructions, *update.NodeExtract.CustomInstructions)
}

func TestGraphSchemaInitializationInstructionLimit(t *testing.T) {
	t.Setenv("NEO4J_ENABLE", "true")
	request := &InitializationRequest{}
	request.NodeExtract.Enabled = true
	request.NodeExtract.Schema = &types.GraphSchema{
		Enabled: true, EntityTypes: []types.GraphEntityType{{Name: "Person"}},
	}
	request.NodeExtract.CustomInstructions = strings.Repeat("人", types.MaxCustomPromptInstructionsLength)
	require.NoError(t, validateNodeExtractConfig(context.Background(), request))
	request.NodeExtract.CustomInstructions += "人"
	require.Error(t, validateNodeExtractConfig(context.Background(), request))
}

func TestGraphSchemaInitializationPreservesTypedExamples(t *testing.T) {
	data := []byte(`{"nodeExtract":{
		"enabled":true,
		"schema":{"enabled":true,"entity_types":[{"name":"Person"},{"name":"Company"}],
			"relation_types":[{"name":"WORKS_AT","source_type":"Person","target_type":"Company"}]},
		"text":"Alex works at Alex.",
		"nodes":[{"name":"Alex","entity_type":"Person","attributes":[]},{"name":"Alex","entity_type":"Company","attributes":[]}],
		"relations":[{"node1":"Alex","node2":"Alex","type":"WORKS_AT","source_type":"Person","target_type":"Company"}]
	}}`)
	var initial InitializationRequest
	require.NoError(t, json.Unmarshal(data, &initial))
	var update KBModelConfigRequest
	require.NoError(t, json.Unmarshal(data, &update))
	require.Equal(t, initial.NodeExtract.Nodes, *update.NodeExtract.Nodes)
	require.Equal(t, initial.NodeExtract.Relations, *update.NodeExtract.Relations)
	require.Equal(t, "Person", initial.NodeExtract.Nodes[0].EntityType)
	require.Equal(t, "Company", initial.NodeExtract.Nodes[1].EntityType)
	require.Equal(t, "Person", initial.NodeExtract.Relations[0].SourceType)
	require.Equal(t, "Company", initial.NodeExtract.Relations[0].TargetType)

	kb := &types.KnowledgeBase{}
	(&InitializationHandler{}).applyKnowledgeBaseInitialization(kb, &initial, nil)
	require.True(t, kb.ExtractConfig.HasSchema())
	require.Len(t, kb.ExtractConfig.Nodes, 2)
	require.Equal(t, initial.NodeExtract.Nodes[0], *kb.ExtractConfig.Nodes[0])
	require.Equal(t, initial.NodeExtract.Nodes[1], *kb.ExtractConfig.Nodes[1])
	require.Equal(t, initial.NodeExtract.Relations[0], *kb.ExtractConfig.Relations[0])
	saved, err := json.Marshal(kb.ExtractConfig)
	require.NoError(t, err)
	var restored types.ExtractConfig
	require.NoError(t, json.Unmarshal(saved, &restored))
	require.Equal(t, kb.ExtractConfig.Schema, restored.Schema)
	require.Equal(t, kb.ExtractConfig.Relations, restored.Relations)
	require.Equal(t, "Person", restored.Nodes[0].EntityType)
	require.Equal(t, "Company", restored.Nodes[1].EntityType)
	restoredJSON, err := json.Marshal(restored)
	require.NoError(t, err)
	require.JSONEq(t, string(saved), string(restoredJSON))
}
