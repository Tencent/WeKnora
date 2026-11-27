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
// spellings. Both spellings match case-insensitively: the value comes from
// a raw query parameter, so "knowledgeqa" must work the same as "chat" and
// "KnowledgeQA" instead of being rejected as unknown.
func ParseModelType(s string) (types.ModelType, bool) {
	normalized := strings.ToLower(strings.TrimSpace(s))
	if t, ok := frontendTypes[normalized]; ok {
		return t, true
	}
	switch normalized {
	case "knowledgeqa":
		return types.ModelTypeKnowledgeQA, true
	case "embedding":
		return types.ModelTypeEmbedding, true
	case "rerank":
		return types.ModelTypeRerank, true
	case "vllm":
		return types.ModelTypeVLLM, true
	case "asr":
		return types.ModelTypeASR, true
	}
	return "", false
}
