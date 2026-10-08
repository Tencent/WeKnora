// Package milvus implements the Milvus retrieve engine, including multilingual BM25.
package milvus

import (
	"strings"
	"unicode"

	"github.com/milvus-io/milvus/client/v2/entity"
)

const (
	fieldContent          = "content"
	fieldLanguage         = "language"
	milvusAnalyzerEnglish = "english"
	milvusAnalyzerChinese = "chinese"
	milvusAnalyzerDefault = "default"
)

type collectionAnalyzerMode uint8

const (
	collectionAnalyzerLegacy collectionAnalyzerMode = iota
	collectionAnalyzerMulti
)

func analyzerModeFromSchema(schema *entity.Schema) collectionAnalyzerMode {
	if schema == nil {
		return collectionAnalyzerLegacy
	}

	hasLanguageField := false
	hasMultiAnalyzer := false
	for _, field := range schema.Fields {
		if field == nil {
			continue
		}
		if field.Name == fieldLanguage {
			hasLanguageField = true
		}
		if field.Name == fieldContent && field.TypeParams != nil &&
			field.TypeParams["multi_analyzer_params"] != "" {
			hasMultiAnalyzer = true
		}
	}

	if hasLanguageField && hasMultiAnalyzer {
		return collectionAnalyzerMulti
	}
	return collectionAnalyzerLegacy
}

// multiAnalyzerParams returns the analyzer configuration used by new Milvus
// collections. The language field on each row selects the analyzer used for
// that row.
func multiAnalyzerParams() map[string]any {
	return map[string]any{
		"analyzers": map[string]any{
			milvusAnalyzerEnglish: map[string]string{
				"type": milvusAnalyzerEnglish,
			},
			milvusAnalyzerChinese: map[string]string{
				"type": milvusAnalyzerChinese,
			},
			milvusAnalyzerDefault: map[string]string{
				"tokenizer": "icu",
			},
		},
		"by_field": fieldLanguage,
		"alias": map[string]string{
			"en": milvusAnalyzerEnglish,
			"zh": milvusAnalyzerChinese,
		},
	}
}

// detectAnalyzerName selects an analyzer for a document or query. Mixed or
// unsupported-language text uses ICU as a safe fallback because Milvus uses
// one analyzer name per document and per query.
func detectAnalyzerName(text string) string {
	var hanCount, latinCount int
	hasJapaneseKana := false
	hasHangul := false
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			hasJapaneseKana = true
		case unicode.Is(unicode.Hangul, r):
			hasHangul = true
		case unicode.Is(unicode.Han, r):
			hanCount++
		case r <= unicode.MaxASCII && ((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')):
			latinCount++
		}
	}

	// Jieba does not preserve Japanese or Korean word boundaries. Route those
	// scripts to ICU instead of treating shared Han characters as Chinese.
	if hasJapaneseKana || hasHangul {
		return milvusAnalyzerDefault
	}
	// Any remaining Han character makes the text Chinese-oriented. This is
	// important for technical queries such as "AI 注意力": choosing English just
	// because ASCII characters are more numerous would drop the Chinese terms.
	if hanCount > 0 {
		return milvusAnalyzerChinese
	}
	if latinCount > 0 {
		return milvusAnalyzerEnglish
	}

	return milvusAnalyzerDefault
}

func analyzerNameForUpsert(embedding *MilvusVectorEmbedding) string {
	if embedding == nil {
		return milvusAnalyzerDefault
	}
	if strings.TrimSpace(embedding.Language) == "" {
		return detectAnalyzerName(embedding.Content)
	}
	return normalizeAnalyzerName(embedding.Language)
}

// normalizeAnalyzerName keeps externally supplied values within the analyzer
// names defined by multiAnalyzerParams.
func normalizeAnalyzerName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case milvusAnalyzerEnglish, "en":
		return milvusAnalyzerEnglish
	case milvusAnalyzerChinese, "zh":
		return milvusAnalyzerChinese
	default:
		return milvusAnalyzerDefault
	}
}
