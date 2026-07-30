package retriever_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This is a cross-backend architecture guard. External stores are not
// required in unit CI, so it verifies that every formally supported adapter
// retains its explicit idempotent-write primitive or stable physical ID.
func TestSupportedVectorStoresDeclareIdempotentBatchSave(t *testing.T) {
	t.Parallel()
	_, here, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Dir(here)

	cases := []struct {
		name   string
		path   string
		marker string
	}{
		{"postgres", "postgres/repository.go", "Transaction(func(tx *gorm.DB) error"},
		{"sqlite", "sqlite/repository.go", "Transaction(func(tx *gorm.DB) error"},
		{"elasticsearch-v7", "elasticsearch/v7/repository.go", "docID := embedding.SourceID"},
		{"elasticsearch-v8", "elasticsearch/v8/repository.go", "IndexOp(types.IndexOperation"},
		{"opensearch", "opensearch/crud.go", `"_id":    info.SourceID`},
		{"qdrant", "qdrant/repository.go", "vectorstoreid.StablePointID"},
		{"milvus", "milvus/repository.go", "vectorstoreid.StablePointID"},
		{"weaviate", "weaviate/repository.go", "vectorstoreid.CompatibleUUIDPointID"},
		{"tencent-vector-db", "tencentvectordb/repository.go", "embedding.ID = indexInfo.SourceID"},
		{"doris", "doris/repository.go", "emb.ID = emb.SourceID"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tc.path)))
			require.NoError(t, err)
			require.True(t, strings.Contains(string(body), tc.marker),
				"%s must retain the BatchSave idempotency mechanism %q", tc.name, tc.marker)
		})
	}
}

func TestQdrantAndMilvusDoNotAllocateRandomIDsInBatchSave(t *testing.T) {
	t.Parallel()
	_, here, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Dir(here)
	for _, path := range []string{"qdrant/repository.go", "milvus/repository.go"} {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		require.NoError(t, err)
		source := string(body)
		batchStart := strings.Index(source, ") BatchSave(")
		require.NotEqual(t, -1, batchStart)
		batchEnd := strings.Index(source[batchStart:], "\nfunc (")
		require.NotEqual(t, -1, batchEnd)
		batchBody := source[batchStart : batchStart+batchEnd]
		require.Contains(t, batchBody, "vectorstoreid.StablePointID")
		require.NotContains(t, batchBody, "uuid.New()")
	}
}
