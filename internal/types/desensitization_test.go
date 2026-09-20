package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDesensitizationConfigNormalizeDefaults(t *testing.T) {
	cfg := &DesensitizationConfig{Enabled: true}
	cfg.Normalize()
	assert.Equal(t, DesensitizationEngineBuiltin, cfg.Engine)
	assert.Equal(t, DesensitizationMaskReplace, cfg.MaskStyle)
}

func TestDesensitizationConfigNormalizeDropsUnknownTypesAndEmptyRules(t *testing.T) {
	cfg := &DesensitizationConfig{
		Enabled:     true,
		Engine:      DesensitizationEngineBuiltin,
		MaskStyle:   "hash",
		EntityTypes: []string{"cn_id_card", "cn_id_card", "person_name", " cn_mobile "},
		Rules: []DesensitizationRule{
			{Name: "  ", Pattern: ""},
			{Name: "order", Pattern: `ORD-\d+`},
		},
	}
	cfg.Normalize()
	assert.Equal(t, DesensitizationEngineBuiltin, cfg.Engine)
	assert.Equal(t, DesensitizationMaskReplace, cfg.MaskStyle)
	assert.Equal(t, []string{DesensitizationEntityCNIDCard, DesensitizationEntityCNMobile}, cfg.EntityTypes)
	require.Len(t, cfg.Rules, 1)
	assert.Equal(t, "order", cfg.Rules[0].Name)
}

func TestDesensitizationConfigNormalizeKeepsUnknownEngine(t *testing.T) {
	cfg := &DesensitizationConfig{Enabled: true, Engine: "nope"}
	cfg.Normalize()
	assert.Equal(t, "nope", cfg.Engine)
}

func TestDesensitizationConfigNilIsDisabled(t *testing.T) {
	var cfg *DesensitizationConfig
	assert.False(t, cfg.IsEnabled())
}

func TestChunkingConfigHasCloudParserEngine(t *testing.T) {
	cfg := ChunkingConfig{ParserEngineRules: []ParserEngineRule{
		{FileTypes: []string{"pdf"}, Engine: "anydoc"},
	}}
	assert.False(t, cfg.HasCloudParserEngine())

	cfg.ParserEngineRules = append(cfg.ParserEngineRules, ParserEngineRule{
		FileTypes: []string{"docx"}, Engine: "mineru_cloud",
	})
	assert.True(t, cfg.HasCloudParserEngine())
}

func TestEnsureDefaultsClearsDesensitizationForNonDocumentKBs(t *testing.T) {
	kb := &KnowledgeBase{
		Type:                  KnowledgeBaseTypeFAQ,
		DesensitizationConfig: &DesensitizationConfig{Enabled: true},
	}
	kb.EnsureDefaults()
	assert.Nil(t, kb.DesensitizationConfig)
}

func TestDesensitizationConfigJSONRoundTrip(t *testing.T) {
	in := DesensitizationConfig{
		Enabled:     true,
		Engine:      DesensitizationEngineBuiltin,
		MaskStyle:   DesensitizationMaskPartial,
		EntityTypes: []string{DesensitizationEntityCNIDCard},
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var out DesensitizationConfig
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, in, out)
}
