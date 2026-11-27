package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// jsonCastPostgresDB opens a throwaway schema on the test database so these
// tests run against the column types AutoMigrate actually creates. That is the
// point of them: `chunks.metadata` and `wiki_pages.category_path` are `json`
// columns because their models say gorm:"type:json", and the jsonb functions
// have no `json` overload.
func jsonCastPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN to run the JSON column cast tests")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	schema := "json_cast_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		require.NoError(t, adminSQL.Close())
	})
	separator := " "
	if strings.Contains(dsn, "://") {
		separator = "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
	}
	db, err := gorm.Open(postgres.Open(dsn+separator+"search_path="+schema), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Chunk{}, &types.WikiPage{}, &types.WikiFolder{}))
	return db
}

func postgresFAQChunk(t *testing.T, db *gorm.DB, id, metadata string) *types.Chunk {
	t.Helper()
	chunk := &types.Chunk{
		ID:              id,
		TenantID:        7,
		KnowledgeBaseID: "kb",
		KnowledgeID:     "doc",
		ChunkType:       types.ChunkTypeFAQ,
		Status:          int(types.ChunkStatusIndexed),
		Metadata:        types.JSON([]byte(metadata)),
	}
	require.NoError(t, db.Create(chunk).Error)
	return chunk
}

// The duplicate check runs on every FAQ create and update. On PostgreSQL the
// metadata column is `json`, so `metadata->'similar_questions'` yields json and
// COALESCE refuses to mix it with the jsonb fallback:
//
//	ERROR: COALESCE could not convert type jsonb to json
//
// which surfaced as "failed to check FAQ question duplicate" and made every
// FAQ write fail on a postgres deployment.
func TestFindFAQChunkWithDuplicateQuestionPostgres(t *testing.T) {
	db := jsonCastPostgresDB(t)
	postgresFAQChunk(t, db, "faq-1", `{
		"standard_question": "标准问",
		"similar_questions": ["相似问A", "相似问B"]
	}`)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	found, err := repo.FindFAQChunkWithDuplicateQuestion(ctx, 7, "kb", "", []string{"相似问A"})
	require.NoError(t, err)
	require.NotNil(t, found, "a similar question must match the stored entry")
	require.Equal(t, "faq-1", found.ID)

	found, err = repo.FindFAQChunkWithDuplicateQuestion(ctx, 7, "kb", "", []string{"标准问"})
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, "faq-1", found.ID)

	found, err = repo.FindFAQChunkWithDuplicateQuestion(ctx, 7, "kb", "", []string{"没出现过的问题"})
	require.NoError(t, err)
	require.Nil(t, found)
}

// The same cast is needed by the generated-question sampler: an entry with no
// `generated_questions` key has to fall back to the jsonb empty array.
func TestListRecentDocumentChunksWithQuestionsPostgres(t *testing.T) {
	db := jsonCastPostgresDB(t)
	withQuestions := &types.Chunk{
		ID:              "doc-with",
		TenantID:        7,
		KnowledgeBaseID: "kb",
		KnowledgeID:     "doc",
		ChunkType:       types.ChunkTypeText,
		Status:          int(types.ChunkStatusIndexed),
		Metadata:        types.JSON([]byte(`{"generated_questions": ["问题A"]}`)),
	}
	withoutQuestions := &types.Chunk{
		ID:              "doc-without",
		TenantID:        7,
		KnowledgeBaseID: "kb",
		KnowledgeID:     "doc",
		ChunkType:       types.ChunkTypeText,
		Status:          int(types.ChunkStatusIndexed),
		Metadata:        types.JSON([]byte(`{"other": "value"}`)),
	}
	require.NoError(t, db.Create(withQuestions).Error)
	require.NoError(t, db.Create(withoutQuestions).Error)

	chunks, err := NewChunkRepository(db).ListRecentDocumentChunksWithQuestions(
		context.Background(), 7, []string{"kb"}, nil, 10)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, "doc-with", chunks[0].ID)
}

// The wiki index and the wiki_path ordering both rank pages by whether they sit
// in a folder, via jsonb_array_length(category_path). category_path is a `json`
// column too, so the ordering failed before the query could return a row:
//
//	ERROR: function jsonb_array_length(json) does not exist
func TestListByTypeLightPostgres(t *testing.T) {
	db := jsonCastPostgresDB(t)
	require.NoError(t, db.Create(&types.WikiPage{
		ID:              "p1",
		TenantID:        7,
		KnowledgeBaseID: "kb",
		PageType:        "entity",
		Slug:            "entity/p1",
		Title:           "P1",
		Status:          types.WikiPageStatusPublished,
	}).Error)
	require.NoError(t, db.Create(&types.WikiPage{
		ID:              "p2",
		TenantID:        7,
		KnowledgeBaseID: "kb",
		PageType:        "entity",
		Slug:            "entity/p2",
		Title:           "P2",
		Status:          types.WikiPageStatusPublished,
		CategoryPath:    types.StringArray{"AI"},
	}).Error)

	entries, total, err := NewWikiPageRepository(db).ListByTypeLight(
		context.Background(), "kb", "entity", 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, entries, 2)
}
