package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAutoTagConfigNormalize(t *testing.T) {
	config := &AutoTagConfig{Enabled: true}
	config.Normalize()
	assert.Equal(t, 3, config.MaxTags)

	config.MaxTags = 99
	config.Normalize()
	assert.Equal(t, 10, config.MaxTags)
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
