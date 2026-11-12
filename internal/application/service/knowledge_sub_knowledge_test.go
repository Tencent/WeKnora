package service

import (
	"context"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSubKnowledgeManualCreateAndUpdateUseNativeMapping(t *testing.T) {
	db, e := gorm.Open(
		sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared&_foreign_keys=on"),
		&gorm.Config{},
	)
	require.NoError(t, e)
	sqlDB, e := db.DB()
	require.NoError(t, e)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeTag{}, &types.KnowledgeTagRelation{}))
	ddl, e := os.ReadFile("../../../migrations/sqlite/000053_knowledge_sub_knowledge.up.sql")
	require.NoError(t, e)
	require.NoError(t, db.Exec(string(ddl)).Error)
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 7}
	kb.StorageProviderConfig = &types.StorageProviderConfig{Provider: "local"}
	svc := &knowledgeService{
		repo:      repository.NewKnowledgeRepository(db),
		kbService: &writeKBLookup{kb: kb},
		tagRepo:   repository.NewKnowledgeTagRepository(db),
	}
	ctx := types.WithExecutionTenant(context.Background(), 7)
	for _, id := range []string{"tag-1", "tag-2"} {
		require.NoError(t, db.Create(&types.KnowledgeTag{ID: id, TenantID: 7, KnowledgeBaseID: "kb", Name: id}).Error)
	}
	p := &types.ManualKnowledgePayload{
		Title:   "主帖",
		Content: "正文\n\n![图](https://img.example/a.png)",
		Status:  "draft",
		SubKnowledge: &types.SubKnowledgeInput{
			SourceNamespace: "taptap",
			RootID:          "P100",
			NodeType:        "root",
			SourceID:        "P100",
		},
	}
	p.TagIDs = []string{"tag-1"}
	first, e := svc.CreateKnowledgeFromManual(ctx, "kb", p, "")
	require.NoError(t, e)
	require.NotEmpty(t, first.ID)
	require.NotNil(t, first.SubKnowledge)
	second, e := svc.CreateKnowledgeFromManual(ctx, "kb", p, "")
	require.NoError(t, e)
	require.Equal(t, first.ID, second.ID)
	require.Len(t, first.Tags, 1)
	require.Equal(t, first.Tags, second.Tags, "a retry returns current persisted tags")
	p.TagIDs = []string{"tag-2"}
	third, e := svc.CreateKnowledgeFromManual(ctx, "kb", p, "")
	require.NoError(t, e)
	require.Equal(t, first.ID, third.ID)
	require.Equal(t, first.Tags, third.Tags, "non-fingerprint options do not mutate an existing origin on retry")
	p.Content = "changed"
	_, e = svc.CreateKnowledgeFromManual(ctx, "kb", p, "")
	require.IsType(t, &werrors.AppError{}, e)
	require.Equal(t, 409, e.(*werrors.AppError).HTTPCode)
	// The existing manual update path must retain identity even when callers
	// omit sub_knowledge, and atomically refresh the ingestion fingerprint.
	require.NoError(t, first.SetManualMetadata(types.NewManualKnowledgeMetadata("新正文", "draft", 2)))
	require.NoError(t, svc.persistManualSubKnowledge(ctx, first, &types.ManualKnowledgePayload{}, "draft"))
	r := svc.repo.(interfaces.SubKnowledgeRepository)
	f, e := r.GetSubKnowledgeMapping(ctx, 7, first.ID)
	require.NoError(t, e)
	require.Equal(t, "P100", f.RootID)
	require.Equal(t, f.ContentHash(first.Title, "新正文", "draft"), f.SourceHash)
	changed := f.SubKnowledgeInput
	changed.RootID = "P200"
	changed.SourceID = "P200"
	e = svc.persistManualSubKnowledge(ctx, first, &types.ManualKnowledgePayload{SubKnowledge: &changed}, "draft")
	require.Error(t, e)
	var count int64
	require.NoError(t, db.Model(&types.Knowledge{}).Count(&count).Error)
	require.EqualValues(t, 1, count)

	// Existing manual callers do not need relation metadata.
	plain := &types.ManualKnowledgePayload{Title: "plain", Content: "plain text", Status: "draft"}
	ordinary, e := svc.CreateKnowledgeFromManual(ctx, "kb", plain, "")
	require.NoError(t, e)
	require.Nil(t, ordinary.SubKnowledge)
	require.NoError(t, svc.persistManualSubKnowledge(ctx, ordinary, plain, "draft"))
	_, e = r.GetSubKnowledgeMapping(ctx, 7, ordinary.ID)
	require.ErrorIs(t, e, gorm.ErrRecordNotFound)

	// The authorization result is immutable input, not a new KB read from the
	// seed after a concurrent move. Never follow the seed into another KB.
	require.NoError(
		t,
		db.Model(&types.Knowledge{}).
			Where("id = ?", first.ID).
			Updates(map[string]any{"knowledge_base_id": "other", "parse_status": "completed"}).
			Error,
	)
	_, e = svc.GetSubKnowledge(ctx, first.ID, "kb", "", 10)
	require.IsType(t, &werrors.AppError{}, e)
	require.Equal(t, 404, e.(*werrors.AppError).HTTPCode)
}
