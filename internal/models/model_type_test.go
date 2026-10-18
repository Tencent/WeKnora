package models

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The frontend names every type in its own vocabulary; the API accepts both
// spellings. Video is a real type now, so both "video" and "Video" have to
// resolve — otherwise the model editor could list the type but never save it.
func TestParseModelTypeAcceptsVideoInBothSpellings(t *testing.T) {
	for _, input := range []string{"video", "Video", " VIDEO ", "video "} {
		got, ok := ParseModelType(input)
		require.True(t, ok, "ParseModelType(%q)", input)
		assert.Equal(t, types.ModelTypeVideo, got, "ParseModelType(%q)", input)
	}
}

// The other spellings keep working: adding a type must not shadow the existing
// ones.
func TestParseModelTypeKeepsTheExistingTypes(t *testing.T) {
	cases := map[string]types.ModelType{
		"chat":      types.ModelTypeKnowledgeQA,
		"embedding": types.ModelTypeEmbedding,
		"rerank":    types.ModelTypeRerank,
		"vllm":      types.ModelTypeVLLM,
		"vlm":       types.ModelTypeVLLM,
		"asr":       types.ModelTypeASR,
		"ASR":       types.ModelTypeASR,
	}
	for input, want := range cases {
		got, ok := ParseModelType(input)
		require.True(t, ok, "ParseModelType(%q)", input)
		assert.Equal(t, want, got, "ParseModelType(%q)", input)
	}
	if _, ok := ParseModelType("video_understanding"); ok {
		t.Fatal("an unknown spelling must not resolve to a model type")
	}
}
