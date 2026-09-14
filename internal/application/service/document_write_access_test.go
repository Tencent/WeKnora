package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type documentKBLookup struct {
	interfaces.KnowledgeBaseService
	values map[string]*types.KnowledgeBase
	err    error
}

func (r *documentKBLookup) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.values[id] == nil {
		return nil, repository.ErrKnowledgeBaseNotFound
	}
	return r.values[id], nil
}

type documentKBRepo struct {
	interfaces.KnowledgeBaseRepository
	lookup *documentKBLookup
}

func (r documentKBRepo) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	return r.lookup.GetKnowledgeBaseByID(ctx, id)
}

type documentKnowledgeSpy struct {
	interfaces.KnowledgeRepository
	writes int
}

func (r *documentKnowledgeSpy) UpdateKnowledge(ctx context.Context, k *types.Knowledge) error {
	r.writes++
	return r.KnowledgeRepository.UpdateKnowledge(ctx, k)
}

func (r *documentKnowledgeSpy) SetKnowledgeTags(ctx context.Context, id string, tags []string) error {
	r.writes++
	return r.KnowledgeRepository.SetKnowledgeTags(ctx, id, tags)
}

func (r *documentKnowledgeSpy) DeleteKnowledgeList(ctx context.Context, tenant uint64, ids []string) error {
	r.writes++
	return r.KnowledgeRepository.DeleteKnowledgeList(ctx, tenant, ids)
}

type documentChunkSpy struct {
	interfaces.ChunkRepository
	writes   int
	imageErr error
}

func (r *documentChunkSpy) CreateChunks(ctx context.Context, chunks []*types.Chunk) error {
	r.writes++
	return r.ChunkRepository.CreateChunks(ctx, chunks)
}

func (r *documentChunkSpy) UpdateChunks(ctx context.Context, chunks []*types.Chunk) error {
	r.writes++
	return r.ChunkRepository.UpdateChunks(ctx, chunks)
}

func (r *documentChunkSpy) UpdateChunk(ctx context.Context, chunk *types.Chunk) error {
	r.writes++
	return r.ChunkRepository.UpdateChunk(ctx, chunk)
}

func (r *documentChunkSpy) DeleteChunks(ctx context.Context, tenant uint64, ids []string) error {
	r.writes++
	return r.ChunkRepository.DeleteChunks(ctx, tenant, ids)
}

func (r *documentChunkSpy) DeleteChunk(ctx context.Context, tenant uint64, id string) error {
	r.writes++
	return r.ChunkRepository.DeleteChunk(ctx, tenant, id)
}

func (r *documentChunkSpy) DeleteByKnowledgeList(ctx context.Context, tenant uint64, ids []string) error {
	r.writes++
	return r.ChunkRepository.DeleteByKnowledgeList(ctx, tenant, ids)
}

func (r *documentChunkSpy) DeleteChunksByKnowledgeID(ctx context.Context, tenant uint64, id string) error {
	r.writes++
	return r.ChunkRepository.DeleteChunksByKnowledgeID(ctx, tenant, id)
}

func (r *documentChunkSpy) SaveChunkRevision(
	ctx context.Context,
	chunk *types.Chunk,
	rev *types.ChunkRevision,
	expected int,
) error {
	r.writes++
	return r.ChunkRepository.SaveChunkRevision(ctx, chunk, rev, expected)
}

func (r *documentChunkSpy) ListImageInfoByKnowledgeIDs(
	ctx context.Context,
	tenant uint64,
	ids []string,
) ([]interfaces.ChunkImageInfo, error) {
	if r.imageErr != nil {
		return nil, r.imageErr
	}
	return r.ChunkRepository.ListImageInfoByKnowledgeIDs(ctx, tenant, ids)
}

type documentGraphSpy struct {
	interfaces.RetrieveGraphRepository
	err   error
	calls int
}

func (r *documentGraphSpy) DelGraph(context.Context, []types.NameSpace) error {
	r.calls++
	return r.err
}

type documentTenantSpy struct {
	interfaces.TenantRepository
	adjustments []int64
}

func (r *documentTenantSpy) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

func (r *documentTenantSpy) AdjustStorageUsed(_ context.Context, _ uint64, delta int64) error {
	r.adjustments = append(r.adjustments, delta)
	return nil
}

type documentFileSpy struct {
	interfaces.FileService
	deleted      []string
	beforeDelete func()
}

func (r *documentFileSpy) DeleteFile(_ context.Context, path string) error {
	if r.beforeDelete != nil {
		r.beforeDelete()
	}
	r.deleted = append(r.deleted, path)
	return nil
}

type documentWriteFixture struct {
	svc       *knowledgeService
	chunks    *chunkService
	repo      *documentKnowledgeSpy
	chunkRepo *documentChunkSpy
	kbs       *documentKBLookup
	graph     *documentGraphSpy
	files     *documentFileSpy
	tenants   *documentTenantSpy
	db        *gorm.DB
	ctx       context.Context
}

func newDocumentWriteFixture(t *testing.T) *documentWriteFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "document.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(
		t,
		db.AutoMigrate(
			&types.Knowledge{},
			&types.Chunk{},
			&types.KnowledgeTag{},
			&types.KnowledgeTagRelation{},
			&types.ChunkRevision{},
		),
	)
	repo := &documentKnowledgeSpy{KnowledgeRepository: repository.NewKnowledgeRepository(db)}
	chunkRepo := &documentChunkSpy{ChunkRepository: repository.NewChunkRepository(db)}
	kbs := &documentKBLookup{values: map[string]*types.KnowledgeBase{
		"kb": {ID: "kb", TenantID: 7}, "other": {ID: "other", TenantID: 7},
	}}
	for _, row := range []*types.Knowledge{
		{
			ID:              "doc",
			TenantID:        7,
			KnowledgeBaseID: "kb",
			Title:           "original",
			Type:            types.KnowledgeTypeManual,
			ParseStatus:     types.ManualKnowledgeStatusDraft,
			StorageSize:     5,
		},

		{
			ID:              "other-doc",
			TenantID:        7,
			KnowledgeBaseID: "other",
			Title:           "other",
			ParseStatus:     types.ParseStatusCompleted,
		},
	} {
		require.NoError(t, db.Create(row).Error)
	}
	for _, row := range []*types.Chunk{
		{
			ID:              "chunk",
			TenantID:        7,
			KnowledgeID:     "doc",
			KnowledgeBaseID: "kb",
			ChunkType:       types.ChunkTypeText,
			Content:         "original",
			IndexStatus:     "ready",
		},

		{
			ID:              "other-chunk",
			TenantID:        7,
			KnowledgeID:     "other-doc",
			KnowledgeBaseID: "other",
			ChunkType:       types.ChunkTypeText,
		},
	} {
		require.NoError(t, db.Create(row).Error)
	}
	require.NoError(t, db.Create(&types.KnowledgeTag{ID: "tag", TenantID: 7, KnowledgeBaseID: "kb", Name: "tag"}).Error)
	graph, files, tenants := &documentGraphSpy{}, &documentFileSpy{}, &documentTenantSpy{}
	chunks := &chunkService{chunkRepository: chunkRepo, knowledgeRepo: repo, kbRepository: documentKBRepo{lookup: kbs}}
	svc := &knowledgeService{
		repo:         repo,
		chunkRepo:    chunkRepo,
		chunkService: chunks,
		kbService:    kbs,
		graphEngine:  graph,
		fileSvc:      files,
		tenantRepo:   tenants,
		tagRepo:      repository.NewKnowledgeTagRepository(db),
	}
	ctx := types.WithCaller(
		context.Background(),
		types.Caller{TenantID: 1, UserID: "editor", Role: types.TenantRoleAdmin},
	)
	grant := &access.KBAccess{
		KnowledgeBase:     kbs.values["kb"],
		Caller:            types.CallerFromContext(ctx),
		EffectiveTenantID: 7,
		Permission:        types.OrgRoleEditor,
	}
	ctx = grant.Context(ctx)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 7})
	return &documentWriteFixture{
		svc:       svc,
		chunks:    chunks,
		repo:      repo,
		chunkRepo: chunkRepo,
		kbs:       kbs,
		graph:     graph,
		files:     files,
		tenants:   tenants,
		db:        db,
		ctx:       ctx,
	}
}

func requireForbiddenWrite(t *testing.T, err error) {
	t.Helper()
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, 403, appErr.HTTPCode)
}

func (f *documentWriteFixture) requireNoWrites(t *testing.T) {
	t.Helper()
	require.Zero(t, f.repo.writes)
	require.Zero(t, f.chunkRepo.writes)
	require.Zero(t, f.graph.calls)
	require.Empty(t, f.files.deleted)
}

func TestDocumentAndChunkWritesRequireOperationGrant(t *testing.T) {
	f := newDocumentWriteFixture(t)
	ctx := types.WithExecutionTenant(
		types.WithCaller(context.Background(), types.Caller{TenantID: 7, Role: types.TenantRoleAdmin}),
		7,
	)
	// Even an owner read resolution projects Admin without authorizing writes.
	grant, err := access.ResolveKB(
		ctx,
		access.KBRequest{Caller: types.CallerFromContext(ctx)},
		f.kbs.values["kb"],
		types.OrgRoleViewer,
		nil,
		nil,
	)
	require.NoError(t, err)
	ctx = grant.Context(ctx)
	row, _ := f.chunkRepo.GetChunkByID(f.ctx, 7, "chunk")
	ops := map[string]func() error{
		"set tags": func() error { return f.svc.SetKnowledgeTags(ctx, "doc", nil) },
		"move folder": func() error {
			_,
				err := f.svc.MoveKnowledgeToFolder(ctx,
				"kb",
				[]string{"doc"},
				"folder")
			return err
		},

		"rename folder": func() error {
			_,
				err := f.svc.RenameKnowledgeFolder(ctx,
				"kb",
				"folder",
				"next")
			return err
		},

		"metadata": func() error { return f.svc.UpdateKnowledge(ctx, &types.Knowledge{ID: "doc", Title: "new"}) },
		"manual": func() error {
			_, err := f.svc.UpdateManualKnowledge(
				ctx,
				"doc",
				&types.ManualKnowledgePayload{Content: "body", Status: "draft"},
			)
			return err
		},
		"tag": func() error { return f.svc.UpdateKnowledgeTag(ctx, "doc", []string{"tag"}) },
		"batch tags": func() error {
			return f.svc.UpdateKnowledgeTagBatch(ctx,
				"",
				map[string][]string{"doc": {"tag"}})
		},

		"delete document":  func() error { return f.svc.DeleteKnowledge(ctx, "doc") },
		"delete documents": func() error { return f.svc.DeleteKnowledgeList(ctx, []string{"doc"}) },
		"create chunks":    func() error { return f.chunks.CreateChunks(ctx, []*types.Chunk{row}) },
		"update chunk":     func() error { return f.chunks.UpdateChunk(ctx, row) },
		"update chunks":    func() error { return f.chunks.UpdateChunks(ctx, []*types.Chunk{row}) },
		"edit chunk": func() error {
			body := "new"
			_, err := f.chunks.UpdateDocumentChunk(ctx, "chunk", &body, nil, nil)
			return err
		},
		"upsert question": func() error {
			_,
				err := f.chunks.UpsertGeneratedQuestion(ctx,
				"chunk",
				"",
				"new question")
			return err
		},

		"delete question": func() error { return f.chunks.DeleteGeneratedQuestion(ctx, "chunk", "q") },
		"regenerate questions": func() error {
			_,
				err := f.svc.RegenerateChunkQuestions(ctx,
				"chunk")
			return err
		},

		"update image":                 func() error { return f.svc.UpdateImageInfo(ctx, "doc", "chunk", "[]") },
		"delete chunk":                 func() error { return f.chunks.DeleteChunk(ctx, "chunk") },
		"delete chunks":                func() error { return f.chunks.DeleteChunks(ctx, []string{"chunk"}) },
		"delete document chunks":       func() error { return f.chunks.DeleteChunksByKnowledgeID(ctx, "doc") },
		"delete batch document chunks": func() error { return f.chunks.DeleteByKnowledgeList(ctx, []string{"doc"}) },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) { requireForbiddenWrite(t, op()); f.requireNoWrites(t) })
	}
	require.NoError(t, f.svc.UpdateKnowledge(f.ctx, &types.Knowledge{ID: "doc", Title: "shared edit"}))
	stored, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.Equal(t, "shared edit", stored.Title)
}

func TestDocumentBatchPreflightDoesNotPartiallyMutate(t *testing.T) {
	for _, invalid := range []string{"other-doc", "missing", ""} {
		t.Run(invalid, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			require.Error(t, f.svc.DeleteKnowledgeList(f.ctx, []string{"doc", invalid}))
			f.requireNoWrites(t)
			require.Error(
				t,
				f.svc.UpdateKnowledgeTagBatch(f.ctx, "", map[string][]string{"doc": {"tag"}, invalid: nil}),
			)
			f.requireNoWrites(t)
			_, err := f.svc.MoveKnowledgeToFolder(f.ctx, "kb", []string{"doc", invalid}, "next")
			require.Error(t, err)
			f.requireNoWrites(t)
			require.Error(t, f.chunks.DeleteByKnowledgeList(f.ctx, []string{"doc", invalid}))
			f.requireNoWrites(t)
		})
	}
	for _, invalid := range []string{"other-chunk", "missing", ""} {
		t.Run("chunks/"+invalid, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			require.Error(t, f.chunks.DeleteChunks(f.ctx, []string{"chunk", invalid}))
			f.requireNoWrites(t)
			good, err := f.chunkRepo.GetChunkByID(f.ctx, 7, "chunk")
			require.NoError(t, err)
			forged := *good
			forged.ID = invalid
			require.Error(t, f.chunks.UpdateChunks(f.ctx, []*types.Chunk{good, &forged}))
			f.requireNoWrites(t)
		})
	}
}

func TestDeletePreflightResolvesKBAndImagesBeforeChangingStatus(t *testing.T) {
	for _, stage := range []string{"KB lookup", "KB owner", "image lookup"} {
		t.Run(stage, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			switch stage {
			case "KB lookup":
				f.kbs.err = errors.New("database unavailable")
			case "KB owner":
				f.kbs.values["kb"].TenantID = 9
			case "image lookup":
				f.chunkRepo.imageErr = errors.New("database unavailable")
			}
			require.Error(t, f.svc.DeleteKnowledge(f.ctx, "doc"))
			f.requireNoWrites(t)
			row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			require.Equal(t, types.ManualKnowledgeStatusDraft, row.ParseStatus)
		})
	}
}

func deleteDocumentTask(t *testing.T, payload types.KnowledgeListDeletePayload) *asynq.Task {
	t.Helper()
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return asynq.NewTask(types.TypeKnowledgeListDelete, data)
}

func TestDeleteTaskScopeRejectsMovedDocumentsBeforeAnyMutation(t *testing.T) {
	f := newDocumentWriteFixture(t)
	task := deleteDocumentTask(
		t,
		types.KnowledgeListDeletePayload{
			TenantID:        7,
			KnowledgeBaseID: "kb",
			KnowledgeIDs:    []string{"doc", "other-doc"},
		},
	)
	require.ErrorIs(t, f.svc.ProcessKnowledgeListDelete(context.Background(), task), asynq.SkipRetry)
	f.requireNoWrites(t)
	task = deleteDocumentTask(
		t,
		types.KnowledgeListDeletePayload{TenantID: 7, KnowledgeIDs: []string{"doc", "other-doc"}},
	)
	require.ErrorIs(t, f.svc.ProcessKnowledgeListDelete(context.Background(), task), asynq.SkipRetry)
	f.requireNoWrites(t)
}

func TestDeleteTaskRetriesAfterPartialCleanupAndAlreadyDeletedRows(t *testing.T) {
	f := newDocumentWriteFixture(t)
	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("file_path", "local://doc").Error)
	task := deleteDocumentTask(
		t,
		types.KnowledgeListDeletePayload{
			TenantID:        7,
			KnowledgeBaseID: "kb",
			KnowledgeIDs:    []string{"doc", "doc", "already-gone"},
		},
	)
	f.graph.err = errors.New("graph unavailable")
	err := f.svc.ProcessKnowledgeListDelete(context.Background(), task)
	require.ErrorContains(t, err, "graph unavailable")
	require.NotErrorIs(t, err, asynq.SkipRetry)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusDeleting, row.ParseStatus)
	require.Empty(t, f.files.deleted)
	require.Empty(t, f.tenants.adjustments)
	f.graph.err = nil
	f.files.beforeDelete = func() { _, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc"); require.Error(t, err) }
	require.NoError(t, f.svc.ProcessKnowledgeListDelete(context.Background(), task))
	require.Equal(t, []string{"local://doc"}, f.files.deleted)
	require.Equal(t, []int64{-5}, f.tenants.adjustments)
	// A completed task does not need live KB metadata or current user grants.
	delete(f.kbs.values, "kb")
	writes, calls := f.repo.writes, f.graph.calls
	require.NoError(t, f.svc.ProcessKnowledgeListDelete(context.Background(), task))
	require.Equal(t, writes, f.repo.writes)
	require.Equal(t, calls, f.graph.calls)
}

func TestKnowledgeCleanupScopeCannotAuthorizeGeneralWritesOrExtraIDs(t *testing.T) {
	f := newDocumentWriteFixture(t)
	ctx := withKnowledgeCleanup(context.Background(), 7, map[string]string{"doc": "kb"})
	require.Zero(t, types.CallerFromContext(ctx).TenantID)
	requireForbiddenWrite(t, f.svc.UpdateKnowledge(ctx, &types.Knowledge{ID: "doc", Title: "no"}))
	requireForbiddenWrite(t, f.svc.DeleteKnowledgeList(ctx, []string{"doc", "other-doc"}))
	requireForbiddenWrite(
		t,
		f.svc.DeleteKnowledgeList(types.WithCaller(ctx, types.Caller{TenantID: 7}), []string{"doc"}),
	)
	f.requireNoWrites(t)
	require.NoError(t, f.svc.DeleteKnowledge(ctx, "doc"))
}

func TestReferencedKnowledgeCleanupPinsItsIndependentKB(t *testing.T) {
	f := newDocumentWriteFixture(t)
	requireForbiddenWrite(t, deleteReferencedKnowledge(f.ctx, f.svc, "kb", []string{"doc", "other-doc"}))
	f.requireNoWrites(t)
	// Session cleanup authority comes from exact persisted references and can
	// continue even when the caller has no permission to edit the history KB.
	ctx := types.WithExecutionTenant(types.WithCaller(context.Background(), types.Caller{TenantID: 7}), 7)
	require.NoError(t, deleteReferencedKnowledge(ctx, f.svc, "kb", []string{"doc", "already-gone"}))
}

func TestChunkEditRejectsCrossDocumentParentBeforeSavingRevision(t *testing.T) {
	f := newDocumentWriteFixture(t)
	require.NoError(
		t,
		f.db.Model(&types.Chunk{}).Where("id = ?", "chunk").Update("parent_chunk_id", "other-chunk").Error,
	)
	body := "edit"
	_, err := f.chunks.UpdateDocumentChunk(f.ctx, "chunk", &body, nil, nil)
	requireForbiddenWrite(t, err)
	f.requireNoWrites(t)
}

func TestChunkEditRejectsCrossDocumentChildBeforeSavingRevision(t *testing.T) {
	f := newDocumentWriteFixture(t)
	require.NoError(
		t,
		f.db.Model(&types.Chunk{}).Where("id = ?", "other-chunk").Update("parent_chunk_id", "chunk").Error,
	)
	body := "edit"
	_, err := f.chunks.UpdateDocumentChunk(f.ctx, "chunk", &body, nil, nil)
	requireForbiddenWrite(t, err)
	f.requireNoWrites(t)
}

func TestChunkBatchWriteAllowsOnlyStoredOwnership(t *testing.T) {
	f := newDocumentWriteFixture(t)
	row, err := f.chunkRepo.GetChunkByID(f.ctx, 7, "chunk")
	require.NoError(t, err)
	row.Content = "updated"
	require.NoError(t, f.chunks.UpdateChunks(f.ctx, []*types.Chunk{row}))
	stored, err := f.chunkRepo.GetChunkByID(f.ctx, 7, "chunk")
	require.NoError(t, err)
	require.Equal(t, "updated", stored.Content)
	require.NoError(t, f.chunks.DeleteChunks(f.ctx, []string{"chunk", "chunk"}))
	_, err = f.chunkRepo.GetChunkByID(f.ctx, 7, "chunk")
	require.ErrorIs(t, err, repository.ErrChunkNotFound)
}

func TestDocumentWriteTaskAndAPIKeyScopesStayNarrow(t *testing.T) {
	f := newDocumentWriteFixture(t)
	ctx, err := access.WithKBTaskWrite(context.Background(), f.kbs.values["kb"], 7)
	require.NoError(t, err)
	require.NoError(t, f.svc.UpdateKnowledge(ctx, &types.Knowledge{ID: "doc", Title: "worker edit"}))
	requireForbiddenWrite(t, f.svc.UpdateKnowledge(ctx, &types.Knowledge{ID: "other-doc", Title: "no"}))
	scope := types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb"},
		Capabilities:     types.StringArray{string(types.APIKeyCapabilityRetrieve)},
	}
	requireForbiddenWrite(
		t,
		f.svc.UpdateKnowledge(types.WithTenantAPIKeyScope(f.ctx, scope), &types.Knowledge{ID: "doc", Title: "no"}),
	)
	require.Zero(t, types.CallerFromContext(ctx).TenantID)
}

func TestLegacySingleKBDeleteTaskRemainsExecutable(t *testing.T) {
	f := newDocumentWriteFixture(t)
	task := deleteDocumentTask(
		t,
		types.KnowledgeListDeletePayload{TenantID: 7, KnowledgeIDs: []string{"doc", "already-gone"}},
	)
	require.NoError(t, f.svc.ProcessKnowledgeListDelete(context.Background(), task))
	_, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.Error(t, err)
}

func TestMultiKBDeleteRequiresAndAcceptsEveryKBGrant(t *testing.T) {
	f := newDocumentWriteFixture(t)
	grant := &access.KBAccess{
		KnowledgeBase:     f.kbs.values["other"],
		Caller:            types.CallerFromContext(f.ctx),
		EffectiveTenantID: 7,
		Permission:        types.OrgRoleEditor,
	}
	require.NoError(t, f.svc.DeleteKnowledgeList(grant.Context(f.ctx), []string{"doc", "other-doc"}))
	rows, err := f.repo.GetKnowledgeBatch(f.ctx, 7, []string{"doc", "other-doc"})
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestDeleteCheckpointRejectsChangedOrDeletedDocument(t *testing.T) {
	for _, action := range []string{"moved", "deleted"} {
		t.Run(action, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			plan, err := f.svc.planKnowledgeDelete(f.ctx, []string{"doc"})
			require.NoError(t, err)
			if action == "moved" {
				require.NoError(
					t,
					f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("knowledge_base_id", "other").Error,
				)
			} else {
				require.NoError(t, f.db.Where("id = ?", "doc").Delete(&types.Knowledge{}).Error)
			}
			require.Error(t, f.svc.executeKnowledgeDelete(plan, true))
			require.Zero(t, f.graph.calls)
			require.Zero(t, f.chunkRepo.writes)
			require.Empty(t, f.files.deleted)
			row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			if action == "moved" {
				require.NoError(t, err)
				require.Equal(t, "other", row.KnowledgeBaseID)
				require.Equal(t, types.ManualKnowledgeStatusDraft, row.ParseStatus)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestDocumentWritesRejectMoveInProgress(t *testing.T) {
	f := newDocumentWriteFixture(t)
	row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
	require.NoError(t, err)
	require.NoError(t, setTransferState(row, knowledgeTransferState{Operation: access.KBTransferMove, Phase: "moving"}))
	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("metadata", row.Metadata).Error)
	for _, op := range []func() error{
		func() error { return f.svc.DeleteKnowledge(f.ctx, "doc") },
		func() error { return f.svc.UpdateKnowledge(f.ctx, &types.Knowledge{ID: "doc", Title: "changed"}) },
		func() error { return f.chunks.DeleteChunk(f.ctx, "chunk") },
		func() error { return f.svc.DeleteKnowledgeList(f.ctx, []string{"doc"}) },
	} {
		var appErr *apperrors.AppError
		require.ErrorAs(t, op(), &appErr)
		require.Equal(t, 409, appErr.HTTPCode)
	}
	f.requireNoWrites(t)
}
