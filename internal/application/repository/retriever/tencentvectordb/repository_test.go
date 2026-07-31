package tencentvectordb

import (
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/tencent/vectordatabase-sdk-go/tcvdbtext/encoder"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
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

func TestTencentVectorDBDefaultsToReplicaNumberOne(t *testing.T) {
	repo := NewTencentVectorDBRetrieveEngineRepository(nil, "", nil).(*repository)

	assert.Equal(t, 1, repo.replicasNum)
}

func TestTencentVectorDBUsesEnvReplicaNumber(t *testing.T) {
	t.Setenv(envTencentVectorDBReplicaNum, "0")
	repo := NewTencentVectorDBRetrieveEngineRepository(nil, "", nil).(*repository)

	assert.Equal(t, 0, repo.replicasNum)
}

func TestTencentVectorDBUsesPositiveEnvReplicaNumber(t *testing.T) {
	t.Setenv(envTencentVectorDBReplicaNum, "3")
	repo := NewTencentVectorDBRetrieveEngineRepository(nil, "", nil).(*repository)

	assert.Equal(t, 3, repo.replicasNum)
}

func TestTencentVectorDBUsesConfiguredReplicaNumber(t *testing.T) {
	t.Setenv(envTencentVectorDBReplicaNum, "0")
	repo := NewTencentVectorDBRetrieveEngineRepository(nil, "", &types.IndexConfig{
		ReplicaNumber: 2,
	}).(*repository)

	assert.Equal(t, 2, repo.replicasNum)
}

func TestCollectionNameUsesDimensionSuffixByDefault(t *testing.T) {
	repo := NewTencentVectorDBRetrieveEngineRepository(nil, "", nil).(*repository)

	assert.Equal(t, "weknora_embeddings_1024", repo.collectionName(1024))
	assert.True(t, repo.matchesCollection("weknora_embeddings_1024"))
	assert.False(t, repo.matchesCollection("weknora_embeddings"))
}

func TestCollectionNameRespectsExplicitCollectionName(t *testing.T) {
	repo := NewTencentVectorDBRetrieveEngineRepository(nil, "", &types.IndexConfig{
		CollectionName: "custom_collection",
	}).(*repository)

	assert.Equal(t, "custom_collection", repo.collectionName(1024))
	assert.True(t, repo.matchesCollection("custom_collection"))
	assert.False(t, repo.matchesCollection("custom_collection_1024"))
}

func TestCollectionAlreadyExistsErrorDetection(t *testing.T) {
	assert.True(t, isCollectionAlreadyExistsErr(errors.New("code: 15202, collection already exists")))
	assert.True(t, isCollectionAlreadyExistsErr(errors.New("Collection Already Exist")))
	assert.False(t, isCollectionAlreadyExistsErr(errors.New("permission denied")))
	assert.False(t, isCollectionAlreadyExistsErr(nil))
}

func TestCollectionCreateRaceRequiresFolderIndexVerification(t *testing.T) {
	createErr := errors.New("code: 15202, collection already exists")
	verifyCalls := 0

	err := verifyCollectionCreateResult("weknora_embeddings_1024", createErr, func() error {
		verifyCalls++
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, verifyCalls)
}

func TestCollectionCreateSuccessRequiresReadyFolderIndex(t *testing.T) {
	verifyCalls := 0

	err := verifyCollectionCreateResult("weknora_embeddings_1024", nil, func() error {
		verifyCalls++
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, verifyCalls)
}

func TestCollectionCreateFailureDoesNotVerifySchema(t *testing.T) {
	verifyCalls := 0

	err := verifyCollectionCreateResult(
		"weknora_embeddings_1024",
		errors.New("permission denied"),
		func() error {
			verifyCalls++
			return nil
		},
	)

	assert.ErrorContains(t, err, "create collection weknora_embeddings_1024")
	assert.Zero(t, verifyCalls)
}

func TestHasFilterIndex(t *testing.T) {
	indexes := tcvectordb.Indexes{
		FilterIndex: []tcvectordb.FilterIndex{
			{FieldName: fieldKnowledgeBaseID},
			{FieldName: fieldFolderID},
		},
	}

	assert.True(t, hasFilterIndex(indexes, fieldFolderID))
	assert.False(t, hasFilterIndex(indexes, "missing"))
	assert.False(t, hasFilterIndex(tcvectordb.Indexes{}, fieldFolderID))
}

func TestValidateFolderIndex(t *testing.T) {
	tests := []struct {
		name    string
		index   *tcvectordb.FilterIndex
		wantErr string
	}{
		{
			name: "valid",
			index: &tcvectordb.FilterIndex{
				FieldName: fieldFolderID,
				FieldType: tcvectordb.String,
				IndexType: tcvectordb.FILTER,
			},
		},
		{
			name:    "missing",
			wantErr: "missing required folder_id filter index",
		},
		{
			name: "wrong field type",
			index: &tcvectordb.FilterIndex{
				FieldName: fieldFolderID,
				FieldType: tcvectordb.Uint64,
				IndexType: tcvectordb.FILTER,
			},
			wantErr: "incompatible folder_id field type",
		},
		{
			name: "wrong index type",
			index: &tcvectordb.FilterIndex{
				FieldName: fieldFolderID,
				FieldType: tcvectordb.String,
				IndexType: tcvectordb.PRIMARY,
			},
			wantErr: "incompatible folder_id index type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFolderIndex("weknora_embeddings_1024", tt.index)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestValidateFolderIndexStatus(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		requireReady bool
		wantReady    bool
		wantErr      string
	}{
		{name: "ready", status: "ready", wantReady: true},
		{name: "ready ignores case", status: " READY ", requireReady: true, wantReady: true},
		{name: "building allows ordinary operations", status: "building"},
		{name: "training allows ordinary operations", status: "training"},
		{
			name:         "building blocks folder retrieval",
			status:       "building",
			requireReady: true,
			wantErr:      "retry after index construction completes",
		},
		{
			name:         "training blocks folder retrieval",
			status:       "training",
			requireReady: true,
			wantErr:      "retry after index construction completes",
		},
		{name: "failed", status: "failed", wantErr: "folder index build failed"},
		{name: "empty", wantErr: "empty folder index status"},
		{name: "unknown", status: "paused", wantErr: "unknown folder index status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ready, err := validateFolderIndexStatus(
				"weknora_embeddings_1024",
				tcvectordb.IndexStatus{Status: tt.status},
				tt.requireReady,
			)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantReady, ready)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
			assert.False(t, ready)
		})
	}
}

func TestCollectionPreparedSeparatesSchemaPresenceFromFolderIndexReadiness(t *testing.T) {
	repo := &repository{}
	collectionName := "weknora_embeddings_1024"

	repo.markCollectionPrepared(collectionName, false)
	assert.True(t, repo.collectionPrepared(collectionName, false))
	assert.False(t, repo.collectionPrepared(collectionName, true))

	repo.markCollectionPrepared(collectionName, true)
	assert.True(t, repo.collectionPrepared(collectionName, false))
	assert.True(t, repo.collectionPrepared(collectionName, true))
}

func TestIndexAlreadyExistsErrorDetection(t *testing.T) {
	assert.True(t, isIndexAlreadyExistsErr(errors.New("index already exists")))
	assert.True(t, isIndexAlreadyExistsErr(errors.New("Index Already Exist")))
	assert.False(t, isIndexAlreadyExistsErr(errors.New("permission denied")))
	assert.False(t, isIndexAlreadyExistsErr(nil))
}

func TestRemapCopiedEmbeddingsMovesCopiedRowsToRoot(t *testing.T) {
	docs := []tcvectordb.Document{{
		Id: "source-id",
		Fields: map[string]tcvectordb.Field{
			fieldSourceID:        {Val: "source-chunk"},
			fieldChunkID:         {Val: "source-chunk"},
			fieldKnowledgeID:     {Val: "source-knowledge"},
			fieldKnowledgeBaseID: {Val: "source-kb"},
			fieldFolderID:        {Val: "source-folder"},
		},
	}}

	copied := remapCopiedEmbeddings(
		docs,
		map[string]string{"source-knowledge": "target-knowledge"},
		map[string]string{"source-chunk": "target-chunk"},
		"target-kb",
	)

	if assert.Len(t, copied, 1) {
		assert.Equal(t, "", copied[0].FolderID)
		assert.Equal(t, "target-chunk", copied[0].ChunkID)
		assert.Equal(t, "target-knowledge", copied[0].KnowledgeID)
		assert.Equal(t, "target-kb", copied[0].KnowledgeBaseID)
	}
}
