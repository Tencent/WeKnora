package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRetrieverEngineMappingIncludesTencentVectorDBHybridCapabilities(t *testing.T) {
	mapping := GetRetrieverEngineMapping()

	assert.Contains(t, mapping["tencent_vectordb"], RetrieverEngineParams{
		RetrieverType:       KeywordsRetrieverType,
		RetrieverEngineType: TencentVectorDBRetrieverEngineType,
	})
	assert.Contains(t, mapping["tencent_vectordb"], RetrieverEngineParams{
		RetrieverType:       VectorRetrieverType,
		RetrieverEngineType: TencentVectorDBRetrieverEngineType,
	})
}
