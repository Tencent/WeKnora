package repository_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const folderConcurrencyKnowledgeDDL = `
CREATE TABLE knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'document',
    title VARCHAR(512) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    source VARCHAR(2048) NOT NULL DEFAULT '',
    channel VARCHAR(50) NOT NULL DEFAULT 'web',
    parse_status VARCHAR(32) NOT NULL DEFAULT 'pending',
    pending_subtasks_count INTEGER NOT NULL DEFAULT 0,
    summary_status VARCHAR(32) NOT NULL DEFAULT 'none',
    enable_status VARCHAR(32) NOT NULL DEFAULT 'disabled',
    embedding_model_id VARCHAR(64) NOT NULL DEFAULT '',
    file_name VARCHAR(1024) NOT NULL DEFAULT '',
    file_type VARCHAR(32) NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL DEFAULT 0,
    file_hash VARCHAR(64) NOT NULL DEFAULT '',
    file_path VARCHAR(1024) NOT NULL DEFAULT '',
    storage_size INTEGER NOT NULL DEFAULT 0,
    metadata TEXT DEFAULT '{}',
    custom_metadata TEXT NOT NULL DEFAULT '{}',
    last_faq_import_result TEXT DEFAULT '{}',
    created_at DATETIME,
    updated_at DATETIME,
    processed_at DATETIME,
    error_message TEXT NOT NULL DEFAULT '',
    folder_id VARCHAR(36) NOT NULL DEFAULT '',
    deleted_at DATETIME
);`

func setupFolderConcurrencyDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf(
		"file:%s?_busy_timeout=5000&_journal_mode=WAL",
		filepath.Join(t.TempDir(), "folders.db"),
	)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(10)

	require.NoError(t, db.Exec(`
		CREATE TABLE knowledge_bases (
			id VARCHAR(36) PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(32) NOT NULL DEFAULT 'document',
			deleted_at DATETIME
		);
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE document_folders (
			id VARCHAR(36) PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id VARCHAR(36) NOT NULL,
			parent_id VARCHAR(36) NOT NULL DEFAULT '',
			name VARCHAR(255) NOT NULL,
			path VARCHAR(1024) NOT NULL DEFAULT '',
			depth INTEGER NOT NULL,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		);
		CREATE UNIQUE INDEX uq_document_folders_sibling
			ON document_folders (knowledge_base_id, parent_id, name)
			WHERE deleted_at IS NULL;
	`).Error)
	require.NoError(t, db.Exec(folderConcurrencyKnowledgeDDL).Error)
	require.NoError(t, db.AutoMigrate(&types.TaskPendingOp{}, &types.DataSource{}))
	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id, name, type) VALUES (?, ?, ?, ?)",
		"kb-1", 1, "KB", types.KnowledgeBaseTypeDocument,
	).Error)
	return db
}

func seedFolder(t *testing.T, repo interface {
	CreateFolder(context.Context, *types.DocumentFolder) error
}, id, parentID, name, path string, depth int) {
	t.Helper()
	now := time.Now()
	require.NoError(t, repo.CreateFolder(context.Background(), &types.DocumentFolder{
		ID:              id,
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParentID:        parentID,
		Name:            name,
		Path:            path,
		Depth:           depth,
		CreatedAt:       now,
		UpdatedAt:       now,
	}))
}

func runConcurrently(first, second func() error) (error, error) {
	start := make(chan struct{})
	results := make(chan struct {
		index int
		err   error
	}, 2)
	run := func(index int, fn func() error) {
		<-start
		results <- struct {
			index int
			err   error
		}{index: index, err: fn()}
	}
	go run(0, first)
	go run(1, second)
	close(start)

	var out [2]error
	for range 2 {
		result := <-results
		out[result.index] = result.err
	}
	return out[0], out[1]
}

type noopFolderIndexUpdater struct {
	interfaces.KnowledgeService
}

func (*noopFolderIndexUpdater) UpdateKnowledgeFolderIndex(
	context.Context,
	string,
	[]string,
	string,
) error {
	return nil
}

func TestFolderDeleteAndChildCreateRemainConsistent(t *testing.T) {
	db := setupFolderConcurrencyDB(t)
	folderRepo := repository.NewDocumentFolderRepository(db)
	folderService := service.NewDocumentFolderService(folderRepo, nil, nil, nil)
	seedFolder(t, folderRepo, "parent", "", "Parent", "Parent", 1)

	deleteErr, createErr := runConcurrently(
		func() error {
			return folderService.DeleteFolder(context.Background(), "kb-1", "parent")
		},
		func() error {
			_, err := folderService.CreateFolder(context.Background(), "kb-1", 1, "parent", "Child")
			return err
		},
	)

	require.True(t,
		(deleteErr == nil && errors.Is(createErr, repository.ErrDocumentFolderNotFound)) ||
			(createErr == nil && errors.Is(deleteErr, service.ErrFolderNotEmpty)),
		"unexpected outcomes: delete=%v create=%v", deleteErr, createErr,
	)
	var orphans int64
	require.NoError(t, db.Raw(`
		SELECT COUNT(*)
		FROM document_folders child
		LEFT JOIN document_folders parent
			ON parent.id = child.parent_id AND parent.deleted_at IS NULL
		WHERE child.deleted_at IS NULL
		  AND child.parent_id <> ''
		  AND parent.id IS NULL
	`).Scan(&orphans).Error)
	require.Zero(t, orphans)
}

func TestFolderRenameAndChildCreateUseCurrentParentPath(t *testing.T) {
	db := setupFolderConcurrencyDB(t)
	folderRepo := repository.NewDocumentFolderRepository(db)
	folderService := service.NewDocumentFolderService(folderRepo, nil, nil, nil)
	seedFolder(t, folderRepo, "parent", "", "Parent", "Parent", 1)

	renameErr, createErr := runConcurrently(
		func() error {
			_, err := folderService.RenameFolder(context.Background(), "kb-1", "parent", "Renamed")
			return err
		},
		func() error {
			_, err := folderService.CreateFolder(context.Background(), "kb-1", 1, "parent", "Child")
			return err
		},
	)

	require.NoError(t, renameErr)
	require.NoError(t, createErr)
	child, err := folderRepo.GetChildFolderByName(context.Background(), "kb-1", "parent", "Child")
	require.NoError(t, err)
	require.Equal(t, "Renamed/Child", child.Path)
}

func TestConcurrentFolderCreatesCannotExceedLimit(t *testing.T) {
	db := setupFolderConcurrencyDB(t)
	folderRepo := repository.NewDocumentFolderRepository(db)
	folderService := service.NewDocumentFolderService(folderRepo, nil, nil, nil)

	now := time.Now()
	folders := make([]*types.DocumentFolder, 0, types.MaxFoldersPerKB-1)
	for i := 0; i < types.MaxFoldersPerKB-1; i++ {
		name := fmt.Sprintf("folder-%04d", i)
		folders = append(folders, &types.DocumentFolder{
			ID:              fmt.Sprintf("id-%04d", i),
			TenantID:        1,
			KnowledgeBaseID: "kb-1",
			Name:            name,
			Path:            name,
			Depth:           1,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}
	require.NoError(t, db.CreateInBatches(folders, 50).Error)

	firstErr, secondErr := runConcurrently(
		func() error {
			_, err := folderService.CreateFolder(context.Background(), "kb-1", 1, "", "last-a")
			return err
		},
		func() error {
			_, err := folderService.CreateFolder(context.Background(), "kb-1", 1, "", "last-b")
			return err
		},
	)

	require.True(t,
		(firstErr == nil && errors.Is(secondErr, service.ErrFolderLimitExceeded)) ||
			(secondErr == nil && errors.Is(firstErr, service.ErrFolderLimitExceeded)),
		"unexpected outcomes: first=%v second=%v", firstErr, secondErr,
	)
	count, err := folderRepo.CountAllFolders(context.Background(), "kb-1")
	require.NoError(t, err)
	require.Equal(t, int64(types.MaxFoldersPerKB), count)
}

func TestFolderDeleteAndDocumentCreateRemainConsistent(t *testing.T) {
	db := setupFolderConcurrencyDB(t)
	folderRepo := repository.NewDocumentFolderRepository(db)
	folderService := service.NewDocumentFolderService(folderRepo, nil, nil, nil)
	knowledgeRepo := repository.NewKnowledgeRepository(db)
	seedFolder(t, folderRepo, "folder", "", "Folder", "Folder", 1)

	deleteErr, createErr := runConcurrently(
		func() error {
			return folderService.DeleteFolder(context.Background(), "kb-1", "folder")
		},
		func() error {
			return knowledgeRepo.CreateKnowledge(context.Background(), &types.Knowledge{
				ID:              "knowledge-1",
				TenantID:        1,
				KnowledgeBaseID: "kb-1",
				Type:            "document",
				Title:           "Document",
				FolderID:        "folder",
			})
		},
	)

	require.True(t,
		(deleteErr == nil && errors.Is(createErr, repository.ErrDocumentFolderNotFound)) ||
			(createErr == nil && errors.Is(deleteErr, service.ErrFolderNotEmpty)),
		"unexpected outcomes: delete=%v create=%v", deleteErr, createErr,
	)
	var orphans int64
	require.NoError(t, db.Raw(`
		SELECT COUNT(*)
		FROM knowledges knowledge
		LEFT JOIN document_folders folder
			ON folder.id = knowledge.folder_id
		   AND folder.knowledge_base_id = knowledge.knowledge_base_id
		   AND folder.tenant_id = knowledge.tenant_id
		   AND folder.deleted_at IS NULL
		WHERE knowledge.deleted_at IS NULL
		  AND knowledge.folder_id <> ''
		  AND folder.id IS NULL
	`).Scan(&orphans).Error)
	require.Zero(t, orphans)
}

func TestKeepDocumentsDeleteAndProcessingStartRemainConsistent(t *testing.T) {
	db := setupFolderConcurrencyDB(t)
	folderRepo := repository.NewDocumentFolderRepository(db)
	knowledgeRepo := repository.NewKnowledgeRepository(db)
	folderService := service.NewDocumentFolderService(
		folderRepo,
		&noopFolderIndexUpdater{},
		nil,
		nil,
	)
	seedFolder(t, folderRepo, "folder", "", "Folder", "Folder", 1)

	ctx := context.Background()
	require.NoError(t, knowledgeRepo.CreateKnowledge(ctx, &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            "document",
		Title:           "Document",
		ParseStatus:     types.ParseStatusFailed,
		FolderID:        "folder",
	}))
	workerKnowledge, err := knowledgeRepo.GetKnowledgeByID(ctx, 1, "knowledge-1")
	require.NoError(t, err)
	workerKnowledge.ParseStatus = types.ParseStatusProcessing

	deleteErr, processingErr := runConcurrently(
		func() error {
			return folderService.DeleteFolderTree(
				ctx,
				"kb-1",
				1,
				"folder",
				types.DocumentFolderDeleteModeKeepDocuments,
			)
		},
		func() error {
			return knowledgeRepo.StartKnowledgeProcessing(ctx, workerKnowledge)
		},
	)

	require.NoError(t, processingErr)
	reloaded, err := knowledgeRepo.GetKnowledgeByID(ctx, 1, "knowledge-1")
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusProcessing, reloaded.ParseStatus)
	require.Equal(t, reloaded.FolderID, workerKnowledge.FolderID,
		"the worker must use the placement serialized by the shared KB lock")

	if deleteErr == nil {
		require.Equal(t, types.DocumentFolderRootID, reloaded.FolderID)
		_, err = folderRepo.GetFolderByID(ctx, "kb-1", "folder")
		require.ErrorIs(t, err, repository.ErrDocumentFolderNotFound)
		return
	}

	require.ErrorIs(t, deleteErr, service.ErrFolderDocumentsProcessing)
	require.Equal(t, "folder", reloaded.FolderID)
	_, err = folderRepo.GetFolderByID(ctx, "kb-1", "folder")
	require.NoError(t, err)
}
