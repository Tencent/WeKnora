package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoTagConfigNormalize(t *testing.T) {
	config := &AutoTagConfig{Enabled: true}
	config.Normalize()
	assert.Equal(t, DefaultAutoTagMaxTags, config.MaxTags)

	config.MaxTags = 99
	config.Normalize()
	assert.Equal(t, MaximumAutoTagMaxTags, config.MaxTags)
}

// Configs stored before skip_if_tagged existed decode to nil and must adopt
// the protective default rather than silently appending to tagged documents.
func TestAutoTagConfigSkipIfTaggedDefaultsToTrue(t *testing.T) {
	var legacy AutoTagConfig
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"max_tags":3}`), &legacy))
	assert.True(t, legacy.ShouldSkipIfTagged())

	legacy.Normalize()
	require.NotNil(t, legacy.SkipIfTagged)
	assert.True(t, *legacy.SkipIfTagged)

	var nilConfig *AutoTagConfig
	assert.True(t, nilConfig.ShouldSkipIfTagged())
}

func TestAutoTagConfigSkipIfTaggedHonoursExplicitFalse(t *testing.T) {
	var config AutoTagConfig
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"skip_if_tagged":false}`), &config))
	config.Normalize()
	assert.False(t, config.ShouldSkipIfTagged())
}

func TestEnsureDefaultsClearsAutoTagConfigForNonDocumentKnowledgeBases(t *testing.T) {
	for _, kbType := range []string{KnowledgeBaseTypeFAQ, KnowledgeBaseTypeWiki} {
		kb := &KnowledgeBase{
			Type:          kbType,
			AutoTagConfig: &AutoTagConfig{Enabled: true},
		}
		kb.EnsureDefaults()
		assert.Nil(t, kb.AutoTagConfig, "auto tags must only apply to document KBs (%s)", kbType)
	}
}

func TestEnsureDefaultsNormalizesAutoTagConfigForDocumentKnowledgeBases(t *testing.T) {
	kb := &KnowledgeBase{
		Type:          KnowledgeBaseTypeDocument,
		AutoTagConfig: &AutoTagConfig{Enabled: true},
	}
	kb.EnsureDefaults()
	assert.Equal(t, 3, kb.AutoTagConfig.MaxTags)
}
