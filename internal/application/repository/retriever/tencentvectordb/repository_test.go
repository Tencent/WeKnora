package tencentvectordb

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/tencent/vectordatabase-sdk-go/tcvdbtext/encoder"
)

func TestSupportIncludesKeywordAndVectorRetrieval(t *testing.T) {
	repo := NewTencentVectorDBRetrieveEngineRepository(nil, "", nil)

	supports := repo.Support()

	assert.Contains(t, supports, types.KeywordsRetrieverType)
	assert.Contains(t, supports, types.VectorRetrieverType)
}

func TestToDocumentIncludesSparseVector(t *testing.T) {
	embedding := &vectorEmbedding{
		ID:              "chunk-1",
		Content:         "腾讯云向量数据库支持关键词检索",
		SourceID:        "source-1",
		SourceType:      int(types.ChunkSourceType),
		ChunkID:         "chunk-1",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		TagID:           "tag-1",
		Embedding:       []float32{0.1, 0.2},
		SparseVector: []encoder.SparseVecItem{
			{TermId: 10, Score: 0.3},
			{TermId: 20, Score: 0.7},
		},
		IsEnabled: true,
	}

	doc := toDocument(embedding)

	assert.Equal(t, embedding.ID, doc.Id)
	assert.Equal(t, embedding.Embedding, doc.Vector)
	assert.Equal(t, embedding.SparseVector, doc.SparseVector)
	assert.Equal(t, "腾讯云向量数据库支持关键词检索", doc.Fields[fieldContent].String())
	assert.Equal(t, uint64(1), doc.Fields[fieldIsEnabled].Uint64())
}

func TestBaseFilterBuildsTencentVectorDBCondition(t *testing.T) {
	repo := &repository{}

	filter := repo.baseFilter(types.RetrieveParams{
		KnowledgeBaseIDs:    []string{"kb-1"},
		KnowledgeIDs:        []string{"knowledge-1", "knowledge-2"},
		TagIDs:              []string{"tag-1"},
		ExcludeKnowledgeIDs: []string{"knowledge-9"},
		ExcludeChunkIDs:     []string{"chunk-9"},
	})
	cond := filter.Cond()

	for _, want := range []string{
		"is_enabled=1",
		"knowledge_base_id in (\"kb-1\")",
		"knowledge_id in (\"knowledge-1\",\"knowledge-2\")",
		"tag_id in (\"tag-1\")",
		"knowledge_id not in (\"knowledge-9\")",
		"chunk_id not in (\"chunk-9\")",
	} {
		assert.True(t, strings.Contains(cond, want), "condition %q should contain %q", cond, want)
	}
}
