package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/stretchr/testify/require"
	sqlitedriver "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	sqlite_vec.Auto()
	code := m.Run()
	sqlite_vec.Cancel()
	os.Exit(code)
}

func newSQLiteRetrieverTestRepository(t *testing.T) *sqliteRepository {
	t.Helper()

	db, err := gorm.Open(
		sqlitedriver.Open(filepath.Join(t.TempDir(), "retriever.db")),
		&gorm.Config{},
	)
	require.NoError(t, err)

	repository := NewSQLiteRetrieveEngineRepository(db)
	repo, ok := repository.(*sqliteRepository)
	require.True(t, ok)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	return repo
}

func insertSQLiteVectorTestRow(
	t *testing.T,
	repo *sqliteRepository,
	chunkID string,
	knowledgeBaseID string,
	knowledgeID string,
	tagID string,
	enabled bool,
	embedding []float32,
) {
	t.Helper()

	dimension := len(embedding)
	repo.ensureVecTable(dimension)
	require.True(t, repo.vecTables[dimension])

	row := &sqliteEmbedding{
		SourceID:        chunkID,
		SourceType:      int(types.ChunkSourceType),
		ChunkID:         chunkID,
		KnowledgeID:     knowledgeID,
		KnowledgeBaseID: knowledgeBaseID,
		TagID:           tagID,
		Content:         chunkID,
		Dimension:       dimension,
		IsEnabled:       &enabled,
	}
	require.NoError(t, repo.db.Create(row).Error)

	blob, err := sqlite_vec.SerializeFloat32(embedding)
	require.NoError(t, err)
	insertSQL := fmt.Sprintf(
		"INSERT INTO %s(rowid, embedding) VALUES (?, ?)",
		vecTableName(dimension),
	)
	require.NoError(t, repo.db.Exec(insertSQL, row.ID, blob).Error)
}

func retrieveSQLiteVectorChunkIDs(
	t *testing.T,
	repo *sqliteRepository,
	params types.RetrieveParams,
) []string {
	t.Helper()

	results, err := repo.vectorRetrieve(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NoError(t, results[0].Error)

	chunkIDs := make([]string, len(results[0].Results))
	for i, result := range results[0].Results {
		chunkIDs[i] = result.ChunkID
	}
	return chunkIDs
}

func TestVectorRetrieveFiltersMetadataBeforeTopK(t *testing.T) {
	repo := newSQLiteRetrieverTestRepository(t)

	insertSQLiteVectorTestRow(
		t, repo, "wrong-kb", "kb-other", "knowledge-scope", "tag-scope", true, []float32{1, 0},
	)
	insertSQLiteVectorTestRow(
		t, repo, "wrong-knowledge", "kb-scope", "knowledge-other", "tag-scope", true, []float32{0.999, 0.001},
	)
	insertSQLiteVectorTestRow(
		t, repo, "disabled", "kb-scope", "knowledge-scope", "tag-scope", false, []float32{0.998, 0.002},
	)
	insertSQLiteVectorTestRow(
		t, repo, "wrong-tag", "kb-scope", "knowledge-scope", "tag-other", true, []float32{0.997, 0.003},
	)
	insertSQLiteVectorTestRow(
		t, repo, "in-scope", "kb-scope", "knowledge-scope", "tag-scope", true, []float32{0.8, 0.6},
	)

	chunkIDs := retrieveSQLiteVectorChunkIDs(t, repo, types.RetrieveParams{
		Embedding:        []float32{1, 0},
		KnowledgeBaseIDs: []string{"kb-scope"},
		KnowledgeIDs:     []string{"knowledge-scope"},
		TagIDs:           []string{"tag-scope"},
		TopK:             1,
	})

	require.Equal(t, []string{"in-scope"}, chunkIDs)
}

func TestVectorRetrieveMetadataFilterCombinations(t *testing.T) {
	repo := newSQLiteRetrieverTestRepository(t)

	insertSQLiteVectorTestRow(
		t, repo, "scope-a", "kb-a", "knowledge-a", "tag-a", true, []float32{1, 0},
	)
	insertSQLiteVectorTestRow(
		t, repo, "scope-b", "kb-a", "knowledge-b", "tag-b", true, []float32{0.9, 0.1},
	)
	insertSQLiteVectorTestRow(
		t, repo, "other-kb", "kb-b", "knowledge-a", "tag-a", true, []float32{0.8, 0.2},
	)
	insertSQLiteVectorTestRow(
		t, repo, "disabled", "kb-a", "knowledge-disabled", "tag-disabled", false, []float32{0.999, 0.001},
	)

	tests := []struct {
		name         string
		knowledgeIDs []string
		tagIDs       []string
		kbIDs        []string
		want         []string
	}{
		{
			name:  "KB only",
			kbIDs: []string{"kb-a"},
			want:  []string{"scope-a", "scope-b"},
		},
		{
			name:         "KB AND KnowledgeIDs",
			kbIDs:        []string{"kb-a"},
			knowledgeIDs: []string{"knowledge-b"},
			want:         []string{"scope-b"},
		},
		{
			name:   "KB AND TagIDs",
			kbIDs:  []string{"kb-a"},
			tagIDs: []string{"tag-b"},
			want:   []string{"scope-b"},
		},
		{
			name:         "KB AND KnowledgeIDs AND TagIDs",
			kbIDs:        []string{"kb-a"},
			knowledgeIDs: []string{"knowledge-b"},
			tagIDs:       []string{"tag-b"},
			want:         []string{"scope-b"},
		},
		{
			name:  "wrong KB",
			kbIDs: []string{"kb-missing"},
		},
		{
			name:         "wrong KnowledgeID",
			kbIDs:        []string{"kb-a"},
			knowledgeIDs: []string{"knowledge-missing"},
		},
		{
			name:   "wrong TagID",
			kbIDs:  []string{"kb-a"},
			tagIDs: []string{"tag-missing"},
		},
		{
			name:         "disabled embedding",
			kbIDs:        []string{"kb-a"},
			knowledgeIDs: []string{"knowledge-disabled"},
			tagIDs:       []string{"tag-disabled"},
		},
		{
			name:         "nil KnowledgeIDs",
			kbIDs:        []string{"kb-a"},
			knowledgeIDs: nil,
			tagIDs:       []string{"tag-a"},
			want:         []string{"scope-a"},
		},
		{
			name:         "empty KnowledgeIDs",
			kbIDs:        []string{"kb-a"},
			knowledgeIDs: []string{},
			tagIDs:       []string{"tag-a"},
			want:         []string{"scope-a"},
		},
		{
			name:         "nil TagIDs",
			kbIDs:        []string{"kb-a"},
			knowledgeIDs: []string{"knowledge-a"},
			tagIDs:       nil,
			want:         []string{"scope-a"},
		},
		{
			name:         "empty TagIDs",
			kbIDs:        []string{"kb-a"},
			knowledgeIDs: []string{"knowledge-a"},
			tagIDs:       []string{},
			want:         []string{"scope-a"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			chunkIDs := retrieveSQLiteVectorChunkIDs(t, repo, types.RetrieveParams{
				Embedding:        []float32{1, 0},
				KnowledgeBaseIDs: test.kbIDs,
				KnowledgeIDs:     test.knowledgeIDs,
				TagIDs:           test.tagIDs,
				TopK:             10,
			})
			require.ElementsMatch(t, test.want, chunkIDs)
		})
	}
}

func TestVectorRetrieveSupports10000KnowledgeIDsInOneVectorQuery(t *testing.T) {
	repo := newSQLiteRetrieverTestRepository(t)
	insertSQLiteVectorTestRow(
		t,
		repo,
		"matching-chunk",
		"kb-scope",
		"knowledge-match",
		"tag-scope",
		true,
		[]float32{1, 0},
	)

	knowledgeIDs := make([]string, 10000)
	for i := range knowledgeIDs {
		knowledgeIDs[i] = fmt.Sprintf("knowledge-%05d", i)
	}
	knowledgeIDs[len(knowledgeIDs)-1] = "knowledge-match"

	chunkIDs := retrieveSQLiteVectorChunkIDs(t, repo, types.RetrieveParams{
		Embedding:        []float32{1, 0},
		KnowledgeBaseIDs: []string{"kb-scope"},
		KnowledgeIDs:     knowledgeIDs,
		TopK:             1,
	})

	require.Equal(t, []string{"matching-chunk"}, chunkIDs)
}

func TestRetrieveReturnsKeywordHelperError(t *testing.T) {
	repo := newSQLiteRetrieverTestRepository(t)
	sqlDB, err := repo.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	results, err := repo.Retrieve(context.Background(), types.RetrieveParams{
		Query:         "keyword",
		RetrieverType: types.KeywordsRetrieverType,
		TopK:          1,
	})

	require.Error(t, err)
	require.Nil(t, results)
	require.Contains(t, err.Error(), "sqlite keyword retrieve failed")
}

func TestRetrieveReturnsVectorHelperError(t *testing.T) {
	repo := newSQLiteRetrieverTestRepository(t)
	repo.vecTables[3] = true

	results, err := repo.Retrieve(context.Background(), types.RetrieveParams{
		Embedding:     []float32{1, 0, 0},
		RetrieverType: types.VectorRetrieverType,
		TopK:          1,
	})

	require.Error(t, err)
	require.Nil(t, results)
	require.Contains(t, err.Error(), "sqlite vector retrieve failed")
}

func TestRetrieveHybridFailsClosedWhenEitherBranchFails(t *testing.T) {
	t.Run("keyword branch", func(t *testing.T) {
		repo := newSQLiteRetrieverTestRepository(t)
		sqlDB, err := repo.db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())

		results, err := repo.Retrieve(context.Background(), types.RetrieveParams{
			Query:     "hybrid",
			Embedding: []float32{1, 0},
			TopK:      1,
		})

		require.Error(t, err)
		require.Nil(t, results)
	})

	t.Run("vector branch after keyword success", func(t *testing.T) {
		repo := newSQLiteRetrieverTestRepository(t)
		require.NoError(t, repo.Save(
			context.Background(),
			&types.IndexInfo{
				Content:         "hybrid",
				SourceID:        "hybrid-source",
				SourceType:      types.ChunkSourceType,
				ChunkID:         "hybrid-chunk",
				KnowledgeID:     "hybrid-knowledge",
				KnowledgeBaseID: "hybrid-kb",
				IsEnabled:       true,
			},
			nil,
		))

		keywordsResults, err := repo.keywordsRetrieve(context.Background(), types.RetrieveParams{
			Query: "hybrid",
			TopK:  1,
		})
		require.NoError(t, err)
		require.Len(t, keywordsResults, 1)
		require.Len(t, keywordsResults[0].Results, 1)

		repo.vecTables[2] = true
		results, err := repo.Retrieve(context.Background(), types.RetrieveParams{
			Query:     "hybrid",
			Embedding: []float32{1, 0},
			TopK:      1,
		})

		require.Error(t, err)
		require.Nil(t, results)
	})
}

func TestRetrieveRejectsUnknownNonEmptyRetrieverType(t *testing.T) {
	repo := newSQLiteRetrieverTestRepository(t)

	results, err := repo.Retrieve(context.Background(), types.RetrieveParams{
		RetrieverType: types.RetrieverType("unsupported"),
	})

	require.Error(t, err)
	require.Nil(t, results)
	require.Contains(t, err.Error(), "unsupported SQLite retriever type")
}

func TestRetrievePreservesContextErrorChain(t *testing.T) {
	tests := []struct {
		name         string
		buildContext func() (context.Context, context.CancelFunc)
		want         error
	}{
		{
			name: "canceled",
			buildContext: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			want: context.Canceled,
		},
		{
			name: "deadline exceeded",
			buildContext: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 0)
			},
			want: context.DeadlineExceeded,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			repo := newSQLiteRetrieverTestRepository(t)
			ctx, cancel := test.buildContext()
			defer cancel()
			require.True(t, errors.Is(ctx.Err(), test.want))

			results, err := repo.Retrieve(ctx, types.RetrieveParams{
				Query:         "context",
				RetrieverType: types.KeywordsRetrieverType,
				TopK:          1,
			})

			require.Error(t, err)
			require.Nil(t, results)
			require.True(t, errors.Is(err, test.want))
		})
	}
}
