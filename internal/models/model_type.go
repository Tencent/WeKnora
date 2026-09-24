package models

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

var frontendTypes = map[string]types.ModelType{
	"chat":      types.ModelTypeKnowledgeQA,
	"embedding": types.ModelTypeEmbedding,
	"rerank":    types.ModelTypeRerank,
	"vlm":       types.ModelTypeVLLM,
	"vllm":      types.ModelTypeVLLM,
	"asr":       types.ModelTypeASR,
}

// ParseModelType accepts both frontend ("chat") and backend ("KnowledgeQA")
// spellings.
func ParseModelType(s string) (types.ModelType, bool) {
	if t, ok := frontendTypes[strings.ToLower(strings.TrimSpace(s))]; ok {
		return t, true
	}
	switch types.ModelType(s) {
	case types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeRerank,
		types.ModelTypeVLLM, types.ModelTypeASR:
		return types.ModelType(s), true
	}
	return "", false
}
