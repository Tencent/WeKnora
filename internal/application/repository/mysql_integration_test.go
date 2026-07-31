package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	appdb "github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
	gomysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mysqlgorm "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func openMySQLRepositoryIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MYSQL_TEST_DSN to run real MySQL repository integration tests")
	}
	cfg, err := gomysql.ParseDSN(dsn)
	require.NoError(t, err)
	if !strings.HasPrefix(strings.ToLower(cfg.DBName), "weknora_mysql_test_") {
		t.Fatalf(
			"refusing MySQL repository integration test for database %q; name must start with weknora_mysql_test_",
			cfg.DBName,
		)
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("DB_DRIVER")), "mysql") {
		mainConfig, configErr := appdb.MySQLMainDatabaseConfigFromEnv()
		require.NoError(t, configErr)
		parsedMain, parseErr := gomysql.ParseDSN(mainConfig.ApplicationDSN)
		require.NoError(t, parseErr)
		if parsedMain.DBName != cfg.DBName || parsedMain.Addr != cfg.Addr || parsedMain.User != cfg.User {
			t.Fatalf(
				"DB_* TLS integration endpoint %s@%s/%s does not match WEKNORA_MYSQL_TEST_DSN %s@%s/%s",
				parsedMain.User, parsedMain.Addr, parsedMain.DBName,
				cfg.User, cfg.Addr, cfg.DBName,
			)
		}
		dsn = mainConfig.ApplicationDSN
	}

	db, err := gorm.Open(mysqlgorm.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func TestMySQLCustomAgentFollowUpModelUsageIntegration(t *testing.T) {
	db := openMySQLRepositoryIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const (
		agentID  = "pr1904-follow-up-model"
		tenantID = uint64(990001)
		modelID  = "model-only-in-follow-ups"
	)
	cleanup := func() {
		require.NoError(
			t,
			db.WithContext(context.Background()).Unscoped().
				Where("id = ? AND tenant_id = ?", agentID, tenantID).
				Delete(&types.CustomAgent{}).Error,
		)
	}
	cleanup()
	t.Cleanup(cleanup)

	agent := &types.CustomAgent{
		ID:       agentID,
		Name:     "follow-up model guard",
		TenantID: tenantID,
		Config: types.CustomAgentConfig{
			QuestionSuggestions: &types.QuestionSuggestionConfig{
				FollowUps: types.FollowUpSuggestionConfig{ModelID: modelID},
			},
		},
	}
	repo := NewCustomAgentRepository(db)
	require.NoError(t, repo.CreateAgent(ctx, agent))

	count, err := repo.CountByModelID(ctx, tenantID, modelID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestMySQLWikiCategoryPathIntegration(t *testing.T) {
	db := openMySQLRepositoryIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const (
		pageID = "pr1904-wiki-category-path"
		kbID   = "pr1904-wiki-category-kb"
	)
	cleanup := func() {
		require.NoError(
			t,
			db.WithContext(context.Background()).Unscoped().
				Where("id = ?", pageID).
				Delete(&types.WikiPage{}).Error,
		)
	}
	cleanup()
	t.Cleanup(cleanup)

	page := &types.WikiPage{
		ID:              pageID,
		TenantID:        990002,
		KnowledgeBaseID: kbID,
		Slug:            "alpha/beta/page",
		Title:           "Nested category page",
		PageType:        types.WikiPageTypeSummary,
		Status:          types.WikiPageStatusPublished,
		CategoryPath:    types.StringArray{"alpha", "beta"},
		Depth:           2,
		WikiPath:        "summary/alpha/beta/page",
	}
	repo := NewWikiPageRepository(db)
	require.NoError(t, repo.Create(ctx, page))

	pages, total, err := repo.List(ctx, &types.WikiPageListRequest{
		KnowledgeBaseID: kbID,
		CategoryPath:    types.StringArray{"alpha", "beta"},
		Page:            1,
		PageSize:        10,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, pages, 1)
	assert.Equal(t, pageID, pages[0].ID)

	_, total, err = repo.List(ctx, &types.WikiPageListRequest{
		KnowledgeBaseID: kbID,
		CategoryPath:    types.StringArray{"beta", "alpha"},
		Page:            1,
		PageSize:        10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

func TestMySQLDeleteFolderIntegration(t *testing.T) {
	db := openMySQLRepositoryIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const kbID = "pr1904-delete-folder-kb"
	cleanup := func() {
		require.NoError(
			t,
			db.WithContext(context.Background()).Unscoped().
				Where("knowledge_base_id = ?", kbID).
				Delete(&types.WikiPage{}).Error,
		)
		require.NoError(
			t,
			db.WithContext(context.Background()).Unscoped().
				Where("knowledge_base_id = ?", kbID).
				Delete(&types.WikiFolder{}).Error,
		)
	}
	cleanup()
	t.Cleanup(cleanup)

	repo := NewWikiPageRepository(db)
	folders := []*types.WikiFolder{
		{
			ID:              "pr1904-empty-folder",
			TenantID:        990003,
			KnowledgeBaseID: kbID,
			Name:            "empty",
			Path:            "empty",
			Depth:           1,
		},
		{
			ID:              "pr1904-parent-folder",
			TenantID:        990003,
			KnowledgeBaseID: kbID,
			Name:            "parent",
			Path:            "parent",
			Depth:           1,
		},
		{
			ID:              "pr1904-child-folder",
			TenantID:        990003,
			KnowledgeBaseID: kbID,
			ParentID:        "pr1904-parent-folder",
			Name:            "child",
			Path:            "parent/child",
			Depth:           2,
		},
		{
			ID:              "pr1904-page-folder",
			TenantID:        990003,
			KnowledgeBaseID: kbID,
			Name:            "with-page",
			Path:            "with-page",
			Depth:           1,
		},
	}
	for _, folder := range folders {
		require.NoError(t, repo.CreateFolder(ctx, folder))
	}
	require.NoError(t, repo.Create(ctx, &types.WikiPage{
		ID:              "pr1904-page-in-folder",
		TenantID:        990003,
		KnowledgeBaseID: kbID,
		Slug:            "page-in-folder",
		Title:           "Page in folder",
		PageType:        types.WikiPageTypeSummary,
		Status:          types.WikiPageStatusPublished,
		FolderID:        "pr1904-page-folder",
	}))

	require.NoError(t, repo.DeleteFolder(ctx, kbID, "pr1904-empty-folder"))
	_, err := repo.GetFolderByID(ctx, kbID, "pr1904-empty-folder")
	assert.ErrorIs(t, err, ErrWikiFolderNotFound)

	err = repo.DeleteFolder(ctx, kbID, "pr1904-parent-folder")
	assert.ErrorIs(t, err, ErrWikiFolderNotEmpty)

	err = repo.DeleteFolder(ctx, kbID, "pr1904-page-folder")
	assert.ErrorIs(t, err, ErrWikiFolderNotEmpty)

	err = repo.DeleteFolder(ctx, kbID, "pr1904-missing-folder")
	assert.True(t, errors.Is(err, ErrWikiFolderNotFound), "error = %v", err)
}

func TestMySQLTaskQueueClaimIntegration(t *testing.T) {
	db := openMySQLRepositoryIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const (
		taskType = "pr1904:mysql:claim"
		scope    = "knowledge_base"
		scopeID  = "pr1904-mysql-claim-kb"
	)
	cleanup := func() {
		require.NoError(
			t,
			db.WithContext(context.Background()).
				Where("task_type = ? AND scope = ? AND scope_id = ?", taskType, scope, scopeID).
				Delete(&types.TaskPendingOp{}).Error,
		)
	}
	cleanup()
	t.Cleanup(cleanup)

	repo := NewTaskPendingOpsRepository(db)
	for _, op := range []*types.TaskPendingOp{
		makePendingOp(taskType, scope, scopeID, "ingest", "k1", nil),
		makePendingOp(taskType, scope, scopeID, "retract", "k1", nil),
		makePendingOp(taskType, scope, scopeID, "ingest", "k2", nil),
		makePendingOp(taskType, scope, scopeID, "ingest", "k3", nil),
	} {
		require.NoError(t, repo.Enqueue(ctx, op))
	}

	staleBefore := time.Now().Add(-time.Hour)
	first, err := repo.ClaimBatch(ctx, taskType, scope, scopeID, 1, staleBefore)
	require.NoError(t, err)
	require.Len(t, first, 2, "one dedup key must claim all of its eligible rows")
	for _, op := range first {
		assert.Equal(t, "k1", op.DedupKey)
		require.NotNil(t, op.ClaimedAt)
		assert.False(t, op.ClaimedAt.IsZero())
	}

	second, err := repo.ClaimBatch(ctx, taskType, scope, scopeID, 2, staleBefore)
	require.NoError(t, err)
	require.Len(t, second, 2)
	assert.Equal(t, []string{"k2", "k3"}, []string{second[0].DedupKey, second[1].DedupKey})

	newCount, err := repo.IncrFailCount(ctx, second[0].ID)
	require.NoError(t, err)
	assert.Equal(t, 1, newCount)
}

func TestMySQLTaskQueueConcurrentClaimsIntegration(t *testing.T) {
	db := openMySQLRepositoryIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const (
		taskType = "pr1904:mysql:concurrent"
		scope    = "knowledge_base"
		scopeID  = "pr1904-mysql-concurrent-kb"
	)
	cleanup := func() {
		require.NoError(
			t,
			db.WithContext(context.Background()).
				Where("task_type = ? AND scope = ? AND scope_id = ?", taskType, scope, scopeID).
				Delete(&types.TaskPendingOp{}).Error,
		)
	}
	cleanup()
	t.Cleanup(cleanup)

	repo := NewTaskPendingOpsRepository(db)
	for _, key := range []string{"k1", "k2", "k3", "k4"} {
		for _, operation := range []string{"ingest", "retract"} {
			require.NoError(t, repo.Enqueue(ctx, makePendingOp(
				taskType, scope, scopeID, operation, key, nil,
			)))
		}
	}

	type claimResult struct {
		rows []*types.TaskPendingOp
		err  error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			<-start
			rows, err := repo.ClaimBatch(
				ctx, taskType, scope, scopeID, 2, time.Now().Add(-time.Hour),
			)
			results <- claimResult{rows: rows, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	keyOwner := make(map[string]int)
	keyCounts := make(map[string]int)
	rowIDs := make(map[int64]struct{})
	owner := 0
	for result := range results {
		require.NoError(t, result.err)
		assert.LessOrEqual(t, len(result.rows), 4)
		assert.Zero(t, len(result.rows)%2, "a dedup key must not be split across claimers")
		for _, row := range result.rows {
			if previousOwner, ok := keyOwner[row.DedupKey]; ok {
				assert.Equal(t, owner, previousOwner, "dedup key was split across claimers")
			} else {
				keyOwner[row.DedupKey] = owner
			}
			keyCounts[row.DedupKey]++
			if _, duplicate := rowIDs[row.ID]; duplicate {
				t.Errorf("row %d was returned to more than one claimer", row.ID)
			}
			rowIDs[row.ID] = struct{}{}
		}
		owner++
	}

	// InnoDB can transiently skip a broader locked range than the two selected
	// anchor rows, so one concurrent caller may receive an empty batch. A later
	// poll must still claim every untouched key without splitting or duplication.
	for {
		rows, err := repo.ClaimBatch(
			ctx, taskType, scope, scopeID, 2, time.Now().Add(-time.Hour),
		)
		require.NoError(t, err)
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if previousOwner, ok := keyOwner[row.DedupKey]; ok {
				assert.Equal(t, owner, previousOwner, "dedup key was split across claimers")
			} else {
				keyOwner[row.DedupKey] = owner
			}
			keyCounts[row.DedupKey]++
			if _, duplicate := rowIDs[row.ID]; duplicate {
				t.Errorf("row %d was returned to more than one claimer", row.ID)
			}
			rowIDs[row.ID] = struct{}{}
		}
		owner++
	}
	require.Len(t, keyOwner, 4)
	require.Len(t, rowIDs, 8)
	for key, count := range keyCounts {
		assert.Equal(t, 2, count, "dedup key %s was not claimed as one complete group", key)
	}
}
