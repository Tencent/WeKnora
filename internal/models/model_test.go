package models

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestParseModelType(t *testing.T) {
	cases := []struct {
		input string
		want  types.ModelType
		ok    bool
	}{
		// Frontend spellings, case-insensitive.
		{input: "chat", want: types.ModelTypeKnowledgeQA, ok: true},
		{input: "CHAT", want: types.ModelTypeKnowledgeQA, ok: true},
		{input: " chat ", want: types.ModelTypeKnowledgeQA, ok: true},
		{input: "embedding", want: types.ModelTypeEmbedding, ok: true},
		{input: "rerank", want: types.ModelTypeRerank, ok: true},
		{input: "vlm", want: types.ModelTypeVLLM, ok: true},
		{input: "vllm", want: types.ModelTypeVLLM, ok: true},
		{input: "asr", want: types.ModelTypeASR, ok: true},
		// Backend spellings, exact case.
		{input: "KnowledgeQA", want: types.ModelTypeKnowledgeQA, ok: true},
		{input: "Embedding", want: types.ModelTypeEmbedding, ok: true},
		{input: "Rerank", want: types.ModelTypeRerank, ok: true},
		{input: "VLLM", want: types.ModelTypeVLLM, ok: true},
		{input: "ASR", want: types.ModelTypeASR, ok: true},
		// Backend spellings, other cases: the value comes from a raw query
		// parameter, so these must behave like the frontend spellings.
		{input: "knowledgeqa", want: types.ModelTypeKnowledgeQA, ok: true},
		{input: "KNOWLEDGEQA", want: types.ModelTypeKnowledgeQA, ok: true},
		{input: "KnowledgeQa", want: types.ModelTypeKnowledgeQA, ok: true},
		{input: "EMBEDDING", want: types.ModelTypeEmbedding, ok: true},
		{input: "RERANK", want: types.ModelTypeRerank, ok: true},
		{input: "Asr", want: types.ModelTypeASR, ok: true},
		{input: " knowledgeqa ", want: types.ModelTypeKnowledgeQA, ok: true},
		// Rejected.
		{input: "", want: "", ok: false},
		{input: "   ", want: "", ok: false},
		{input: "unknown", want: "", ok: false},
		{input: "Knowledge QA", want: "", ok: false},
		{input: "chatbot", want: "", ok: false},
	}
	for _, tc := range cases {
		got, ok := ParseModelType(tc.input)
		assert.Equal(t, tc.ok, ok, "input %q", tc.input)
		assert.Equal(t, tc.want, got, "input %q", tc.input)
	}
}

func TestModelSpecDisplayName(t *testing.T) {
	assert.Equal(t, "DeepSeek R1", ModelSpec{ID: "deepseek-r1", Name: "DeepSeek R1"}.DisplayName())
	assert.Equal(t, "deepseek-r1", ModelSpec{ID: "deepseek-r1"}.DisplayName())
	assert.Equal(t, "", ModelSpec{}.DisplayName())
}

func TestModelSpecAcceptsImages(t *testing.T) {
	assert.True(t, ModelSpec{Input: []string{"text", "image"}}.AcceptsImages())
	assert.False(t, ModelSpec{Input: []string{"text"}}.AcceptsImages())
	assert.False(t, ModelSpec{}.AcceptsImages())
	// The match is exact: "Image" is not an accepted modality spelling.
	assert.False(t, ModelSpec{Input: []string{"Image"}}.AcceptsImages())
}
