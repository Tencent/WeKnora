package service

import (
	"context"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNativeKnowledgeManualParentOnlyInput(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeTag{}, &types.KnowledgeTagRelation{}))
	require.NoError(t, db.Migrator().DropColumn(&types.Knowledge{}, "ParentKnowledgeID"))
	ddl, err := os.ReadFile("../../../migrations/sqlite/000054_native_knowledge_parent.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(ddl)).Error)
	require.NoError(
		t,
		db.Exec(
			`CREATE TABLE knowledge_bases(id TEXT PRIMARY KEY,tenant_id BIGINT,deleted_at DATETIME);
			 INSERT INTO knowledge_bases VALUES ('kb',7,NULL)`,
		).Error,
	)
	repo := repository.NewKnowledgeRepository(db)
	parent := &types.Knowledge{
		TenantID:        7,
		KnowledgeBaseID: "kb",
		Title:           "root",
		Type:            types.KnowledgeTypeManual,
		ParseStatus:     types.ParseStatusCompleted,
	}
	require.NoError(t, repo.CreateKnowledge(context.Background(), parent))
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 7}
	kb.StorageProviderConfig = &types.StorageProviderConfig{Provider: "local"}
	svc := &knowledgeService{repo: repo, kbService: &writeKBLookup{kb: kb}}
	ctx := types.WithExecutionTenant(context.Background(), 7)
	p := &types.ManualKnowledgePayload{
		Title:             "child",
		Content:           "full child content",
		Status:            "draft",
		ParentKnowledgeID: parent.ID,
	}
	child, err := svc.CreateKnowledgeFromManual(ctx, "kb", p, "")
	require.NoError(t, err)
	require.NotEqual(t, parent.ID, child.ID)
	require.Equal(t, parent.ID, child.ParentKnowledgeID)
	require.Nil(t, child.SubKnowledge, "native parent does not require external-source identity")
	saved, err := repo.GetKnowledgeByID(ctx, 7, child.ID)
	require.NoError(t, err)
	require.Equal(t, parent.ID, saved.ParentKnowledgeID)
	p.ParentKnowledgeID = child.ID
	p.Title = "grandchild"
	grand, err := svc.CreateKnowledgeFromManual(ctx, "kb", p, "")
	require.NoError(t, err)
	require.Equal(t, child.ID, grand.ParentKnowledgeID)
	p.ParentKnowledgeID = "missing"
	_, err = svc.CreateKnowledgeFromManual(ctx, "kb", p, "")
	require.Error(t, err)
	// Invalid file/URL parents are rejected before opening files or fetching URLs.
	_, err = svc.CreateKnowledgeFromFileWithParent(ctx, "kb", nil, nil, nil, "", nil, "", nil, "missing")
	require.Error(t, err)
	_, err = svc.CreateKnowledgeFromURLWithParent(
		ctx,
		"kb",
		"https://example.invalid/a",
		"",
		"",
		nil,
		"",
		nil,
		"",
		nil,
		"missing",
	)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Model(&types.Knowledge{}).Count(&count).Error)
	require.EqualValues(t, 3, count)
}
