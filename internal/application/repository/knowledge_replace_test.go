package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func replacementFixture(t *testing.T) (*gorm.DB, interfaces.KnowledgeRepository, *types.Knowledge, *types.Knowledge) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "replacement.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeTagRelation{}))
	repo := NewKnowledgeRepository(db)
	before := &types.Knowledge{
		ID: "old", TenantID: 7, KnowledgeBaseID: "kb-1", Type: "file",
		FilePath: "old.md", ParseStatus: types.ParseStatusCompleted, EnableStatus: "enabled",
		Metadata: types.JSON(`{"datasource_id":"ds-1","external_id":"doc-1"}`),
	}
	require.NoError(t, repo.CreateKnowledge(context.Background(), before))
	before, err = repo.GetKnowledgeByID(context.Background(), 7, before.ID)
	require.NoError(t, err)
	after := &types.Knowledge{
		ID: "new", TenantID: 7, KnowledgeBaseID: "kb-1", Type: "file",
		FilePath: "new.md", ParseStatus: types.ParseStatusPending, Metadata: before.Metadata,
	}
	return db, repo, before, after
}

func TestReplaceKnowledgeRollsBackRetirementWhenInsertFails(t *testing.T) {
	db, repo, before, after := replacementFixture(t)
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_replacement BEFORE INSERT ON knowledges
		WHEN NEW.id = 'new' BEGIN SELECT RAISE(FAIL, 'forced insert failure'); END`).Error)

	err := repo.ReplaceKnowledge(context.Background(), before, after, nil)
	require.ErrorContains(t, err, "forced insert failure")
	persisted, err := repo.GetKnowledgeByID(context.Background(), 7, before.ID)
	require.NoError(t, err)
	require.Equal(t, "old.md", persisted.FilePath)
	require.Equal(t, types.ParseStatusCompleted, persisted.ParseStatus)
	require.False(t, persisted.DeletedAt.Valid)
	_, err = repo.GetKnowledgeByID(context.Background(), 7, after.ID)
	require.Error(t, err)
}

func TestReplaceKnowledgeCommitsBothRowsAndRejectsStaleReplacement(t *testing.T) {
	db, repo, before, after := replacementFixture(t)
	require.NoError(t, repo.ReplaceKnowledge(context.Background(), before, after, nil))
	_, err := repo.GetKnowledgeByID(context.Background(), 7, before.ID)
	require.Error(t, err)
	persisted, err := repo.GetKnowledgeByID(context.Background(), 7, after.ID)
	require.NoError(t, err)
	require.Equal(t, after.Metadata, persisted.Metadata)
	var retired types.Knowledge
	require.NoError(t, db.Unscoped().First(&retired, "id = ?", before.ID).Error)
	require.True(t, retired.DeletedAt.Valid, "retain a tombstone until resource cleanup succeeds")

	after.ID = "racing-replacement"
	require.ErrorContains(t, repo.ReplaceKnowledge(context.Background(), before, after, nil), "knowledge changed")
	var count int64
	require.NoError(t, db.Model(&types.Knowledge{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestReplaceKnowledgeRejectsChangedOrForeignSnapshot(t *testing.T) {
	for _, change := range []string{"updated", "tenant", "kb", "same-id", "delete-failure"} {
		t.Run(change, func(t *testing.T) {
			db, repo, before, after := replacementFixture(t)
			switch change {
			case "updated":
				require.NoError(t, repo.UpdateKnowledgeColumn(context.Background(), before.ID,
					"updated_at", before.UpdatedAt.Add(time.Second)))
			case "tenant":
				after.TenantID++
			case "kb":
				after.KnowledgeBaseID = "kb-other"
			case "same-id":
				after.ID = before.ID
			case "delete-failure":
				require.NoError(t, db.Exec(`CREATE TRIGGER fail_retirement BEFORE UPDATE OF deleted_at ON knowledges
					BEGIN SELECT RAISE(FAIL, 'forced delete failure'); END`).Error)
			}
			require.Error(t, repo.ReplaceKnowledge(context.Background(), before, after, nil))
			_, err := repo.GetKnowledgeByID(context.Background(), 7, before.ID)
			require.NoError(t, err)
			var count int64
			require.NoError(t, db.Unscoped().Model(&types.Knowledge{}).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestCheckKnowledgeExistsExcludesOnlyReplacementSource(t *testing.T) {
	for _, kind := range []string{"file", "url"} {
		t.Run(kind, func(t *testing.T) {
			_, repo, before, _ := replacementFixture(t)
			require.NoError(t, repo.UpdateKnowledgeColumns(context.Background(), before.ID,
				map[string]interface{}{
					"type": kind, "file_hash": "same-hash", "file_type": "md", "source": "https://example.com/doc",
				}))
			params := &types.KnowledgeCheckParams{
				Type: kind, FileHash: "same-hash", FileType: "md",
				URL: "https://example.com/doc", ExcludeKnowledgeID: before.ID,
			}
			exists, _, err := repo.CheckKnowledgeExists(context.Background(), 7, "kb-1", params)
			require.NoError(t, err)
			require.False(t, exists)
			other := &types.Knowledge{
				ID: "other", TenantID: 7, KnowledgeBaseID: "kb-1", Type: kind,
				FileHash: "same-hash", FileType: "md", Source: params.URL, ParseStatus: types.ParseStatusCompleted,
			}
			require.NoError(t, repo.CreateKnowledge(context.Background(), other))
			exists, duplicate, err := repo.CheckKnowledgeExists(context.Background(), 7, "kb-1", params)
			require.NoError(t, err)
			require.True(t, exists)
			require.Equal(t, other.ID, duplicate.ID)
		})
	}
}

func TestReplaceKnowledgeTagsCommitOrRollbackWithRows(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "tag failure"}[fail], func(t *testing.T) {
			db, repo, before, after := replacementFixture(t)
			require.NoError(t, repo.SetKnowledgeTags(context.Background(), before.ID, []string{"old-tag"}))
			if fail {
				require.NoError(t, db.Exec(`CREATE TRIGGER fail_new_tags BEFORE INSERT ON knowledge_tag_relations
					WHEN NEW.knowledge_id = 'new' BEGIN SELECT RAISE(FAIL, 'forced tag failure'); END`).Error)
			}
			err := repo.ReplaceKnowledge(context.Background(), before, after, []string{"new-tag"})
			if fail {
				require.ErrorContains(t, err, "forced tag failure")
				_, err = repo.GetKnowledgeByID(context.Background(), 7, before.ID)
				require.NoError(t, err)
				_, err = repo.GetKnowledgeByID(context.Background(), 7, after.ID)
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			var relations []types.KnowledgeTagRelation
			require.NoError(t, db.Where("knowledge_id = ?", after.ID).Find(&relations).Error)
			if fail {
				require.Empty(t, relations)
			} else {
				require.Len(t, relations, 1)
				require.Equal(t, "new-tag", relations[0].TagID)
			}
		})
	}
}
