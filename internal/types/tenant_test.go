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

func TestRetrieverEngineMappingIncludesMySQLHybridCapabilities(t *testing.T) {
	mapping := GetRetrieverEngineMapping()

	assert.Contains(t, mapping["mysql"], RetrieverEngineParams{
		RetrieverType:       KeywordsRetrieverType,
		RetrieverEngineType: MySQLRetrieverEngineType,
	})
	assert.Contains(t, mapping["mysql"], RetrieverEngineParams{
		RetrieverType:       VectorRetrieverType,
		RetrieverEngineType: MySQLRetrieverEngineType,
	})
}
