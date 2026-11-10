package milvus

import (
	"testing"

	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/stretchr/testify/require"
)

func TestDetectAnalyzerName(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "Chinese",
			text: "Transformer中多头注意力的三种使用方式及解码器自注意力掩码原因",
			want: milvusAnalyzerChinese,
		},
		{name: "English", text: "How does multi-head attention work", want: milvusAnalyzerEnglish},
		{name: "ChineseWithTechnicalTerm", text: "AI 注意力", want: milvusAnalyzerChinese},
		{name: "JapaneseKanjiWithKana", text: "東京都の設定", want: milvusAnalyzerDefault},
		{name: "KoreanHangul", text: "서울 설정", want: milvusAnalyzerDefault},
		{name: "Symbols", text: "12345 --", want: milvusAnalyzerDefault},
		{name: "Empty", text: "", want: milvusAnalyzerDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, detectAnalyzerName(tt.text))
		})
	}
}

func TestMultiAnalyzerParams(t *testing.T) {
	params := multiAnalyzerParams()

	require.Equal(t, fieldLanguage, params["by_field"])
	require.Equal(t, map[string]string{
		"type": milvusAnalyzerEnglish,
	}, params["analyzers"].(map[string]any)[milvusAnalyzerEnglish])
	require.Equal(t, map[string]string{
		"type": milvusAnalyzerChinese,
	}, params["analyzers"].(map[string]any)[milvusAnalyzerChinese])
	require.Equal(t, map[string]string{
		"tokenizer": "icu",
	}, params["analyzers"].(map[string]any)[milvusAnalyzerDefault])
}

func TestNormalizeAnalyzerName(t *testing.T) {
	require.Equal(t, milvusAnalyzerEnglish, normalizeAnalyzerName(" EN "))
	require.Equal(t, milvusAnalyzerChinese, normalizeAnalyzerName("zh"))
	require.Equal(t, milvusAnalyzerDefault, normalizeAnalyzerName("unknown"))
}

func TestAnalyzerNameForUpsert(t *testing.T) {
	require.Equal(t, milvusAnalyzerDefault, analyzerNameForUpsert(nil))
	require.Equal(t, milvusAnalyzerChinese, analyzerNameForUpsert(&MilvusVectorEmbedding{
		Content: "AI 注意力",
	}))
	require.Equal(t, milvusAnalyzerEnglish, analyzerNameForUpsert(&MilvusVectorEmbedding{
		Content:  "AI 注意力",
		Language: "en",
	}))
	require.Equal(t, milvusAnalyzerEnglish, analyzerNameForUpsert(&MilvusVectorEmbedding{
		Content:  "hello",
		Language: "  ",
	}))
}

func TestAnalyzerModeFromSchema(t *testing.T) {
	legacySchema := entity.NewSchema().WithField(
		entity.NewField().WithName(fieldContent).WithDataType(entity.FieldTypeVarChar),
	)
	require.Equal(t, collectionAnalyzerLegacy, analyzerModeFromSchema(legacySchema))

	multilingualSchema := entity.NewSchema().WithField(
		entity.NewField().WithName(fieldContent).
			WithDataType(entity.FieldTypeVarChar).
			WithMultiAnalyzerParams(multiAnalyzerParams()),
	).WithField(
		entity.NewField().WithName(fieldLanguage).WithDataType(entity.FieldTypeVarChar),
	)
	require.Equal(t, collectionAnalyzerMulti, analyzerModeFromSchema(multilingualSchema))
}
