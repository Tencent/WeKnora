package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestWikiSuggestionFromPageLocales(t *testing.T) {
	for _, tt := range []struct {
		locale, concept, about string
	}{
		{"ru-RU", "Что такое RAG?", "Расскажи о RAG"},
		{"zh-TW", "什麼是RAG？", "介紹一下RAG"},
		{"ZH-HK", "什麼是RAG？", "介紹一下RAG"},
		{"en", "What is RAG?", "Tell me about RAG"},
		{"fr-FR", "RAG", "RAG"},
	} {
		t.Run(tt.locale, func(t *testing.T) {
			concept := &types.WikiPage{Title: "RAG", PageType: types.WikiPageTypeConcept}
			entity := &types.WikiPage{Title: "RAG", PageType: types.WikiPageTypeEntity}
			assert.Equal(t, tt.concept, wikiSuggestionFromPage(concept, tt.locale))
			assert.Equal(t, tt.about, wikiSuggestionFromPage(entity, tt.locale))
		})
	}
}
