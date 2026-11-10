package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type writeKBLookup struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *writeKBLookup) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type writeChunkSpy struct {
	interfaces.ChunkRepository
	writes int
}

func (s *writeChunkSpy) UpdateChunkFieldsByTagID(
	ctx context.Context,
	tenant uint64,
	kb, tag string,
	enabled *bool,
	set, clearFlags types.ChunkFlags,
	next *string,
	exclude []string,
) ([]string, error) {
	s.writes++
	return s.ChunkRepository.UpdateChunkFieldsByTagID(ctx, tenant, kb, tag, enabled, set, clearFlags, next, exclude)
}

func (s *writeChunkSpy) UpdateChunks(ctx context.Context, chunks []*types.Chunk) error {
	s.writes++
	return s.ChunkRepository.UpdateChunks(ctx, chunks)
}

func (s *writeChunkSpy) UpdateChunkFlagsBatch(
	ctx context.Context,
	tenant uint64,
	kb string,
	set, clearFlags map[string]types.ChunkFlags,
) error {
	s.writes++
	return s.ChunkRepository.UpdateChunkFlagsBatch(ctx, tenant, kb, set, clearFlags)
}

type writeChunkDeleter struct {
	interfaces.ChunkService
	deleted []string
}

func (s *writeChunkDeleter) DeleteChunk(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

type writeIndexSpy struct {
	interfaces.RetrieveEngineService
	enabled map[string]bool
	tags    map[string]string
}

func (*writeIndexSpy) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (*writeIndexSpy) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (s *writeIndexSpy) BatchUpdateChunkEnabledStatus(_ context.Context, values map[string]bool) error {
	s.enabled = values
	return nil
}

func (s *writeIndexSpy) BatchUpdateChunkTagID(_ context.Context, values map[string]string) error {
	s.tags = values
	return nil
}

type faqWriteFixture struct {
	svc     *knowledgeService
	tags    *knowledgeTagService
	db      *gorm.DB
	ctx     context.Context
	chunks  *writeChunkSpy
	deletes *writeChunkDeleter
	index   *writeIndexSpy
	kb      *types.KnowledgeBase
}

func newFAQWriteFixture(t *testing.T) *faqWriteFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.Chunk{}, &types.KnowledgeTag{}))
	store := "write-store"
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 7, Type: types.KnowledgeBaseTypeFAQ, VectorStoreID: &store}
	lookup := &writeKBLookup{kb: kb}
	chunks := &writeChunkSpy{ChunkRepository: repository.NewChunkRepository(db)}
	deletes := &writeChunkDeleter{}
	index := &writeIndexSpy{}
	svc := &knowledgeService{
		kbService: lookup, chunkRepo: chunks, chunkService: deletes,
		tagRepo: repository.NewKnowledgeTagRepository(db), repo: repository.NewKnowledgeRepository(db),
		retrieveEngine: &fakeFanoutRegistry{byStore: map[string]interfaces.RetrieveEngineService{store: index}},
		ownership:      &fakeOwnership{owned: map[string]uint64{store: 7}},
	}
	base := types.WithCaller(
		context.Background(),
		types.Caller{TenantID: 1, UserID: "caller", Role: types.TenantRoleAdmin},
	)
	grant := &access.KBAccess{
		KnowledgeBase:     kb,
		Caller:            types.CallerFromContext(base),
		EffectiveTenantID: 7,
		Permission:        types.OrgRoleEditor,
	}
	ctx := context.WithValue(grant.Context(base), types.TenantInfoContextKey, &types.Tenant{ID: 7})
	for _, tag := range []*types.KnowledgeTag{
		{ID: "tag", SeqID: 11, TenantID: 7, KnowledgeBaseID: "kb", Name: "tag"},
		{ID: "next", SeqID: 12, TenantID: 7, KnowledgeBaseID: "kb", Name: "next"},
		{ID: "foreign", SeqID: 22, TenantID: 7, KnowledgeBaseID: "other", Name: "foreign"},
	} {
		require.NoError(t, db.Create(tag).Error)
	}
	for _, chunk := range []*types.Chunk{
		{
			ID:              "one",
			SeqID:           1,
			TenantID:        7,
			KnowledgeBaseID: "kb",
			KnowledgeID:     "faq",
			ChunkType:       types.ChunkTypeFAQ,
			TagID:           "tag",
			IsEnabled:       true,
		},

		{
			ID:              "two",
			SeqID:           2,
			TenantID:        7,
			KnowledgeBaseID: "other",
			KnowledgeID:     "foreign-faq",
			ChunkType:       types.ChunkTypeFAQ,
			TagID:           "foreign",
			IsEnabled:       true,
		},
	} {
		require.NoError(t, db.Create(chunk).Error)
	}
	require.NoError(
		t,
		db.Create(&types.Knowledge{ID: "faq", TenantID: 7, KnowledgeBaseID: "kb", Type: types.KnowledgeTypeFAQ}).Error,
	)
	tags := &knowledgeTagService{kbService: lookup, repo: svc.tagRepo}
	tags.chunkRepo = chunks
	svc.tagService = tags
	return &faqWriteFixture{
		svc:     svc,
		tags:    tags,
		db:      db,
		ctx:     ctx,
		chunks:  chunks,
		deletes: deletes,
		index:   index,
		kb:      kb,
	}
}

func TestFAQAndTagWritesRejectUnscopedServiceCalls(t *testing.T) {
	f := newFAQWriteFixture(t)
	ctx := types.WithExecutionTenant(types.WithCaller(context.Background(), types.Caller{TenantID: 1}), 7)
	operations := map[string]func(context.Context) error{
		"create FAQ": func(ctx context.Context) error {
			_, err := f.svc.CreateFAQEntry(ctx, "kb", &types.FAQEntryPayload{})
			return err
		},
		"update FAQ": func(ctx context.Context) error {
			_, err := f.svc.UpdateFAQEntry(ctx, "kb", 1, &types.FAQEntryPayload{})
			return err
		},
		"similar questions": func(ctx context.Context) error {
			_, err := f.svc.AddSimilarQuestions(ctx, "kb", 1, []string{"question"})
			return err
		},
		"status": func(ctx context.Context) error { return f.svc.UpdateFAQEntryStatus(ctx, "kb", "one", false) },
		"fields": func(ctx context.Context) error {
			return f.svc.UpdateFAQEntryFieldsBatch(
				ctx,
				"kb",
				&types.FAQEntryFieldsBatchUpdate{ByID: map[int64]types.FAQEntryFieldsUpdate{1: {}}},
			)
		},
		"single tag": func(ctx context.Context) error { return f.svc.UpdateFAQEntryTag(ctx, "kb", "one", nil) },
		"batch tags": func(ctx context.Context) error {
			return f.svc.UpdateFAQEntryTagBatch(ctx, "kb", map[int64]*int64{1: nil})
		},
		"delete": func(ctx context.Context) error { return f.svc.DeleteFAQEntries(ctx, "kb", []int64{1}) },
		"import": func(ctx context.Context) error {
			_, err := f.svc.UpsertFAQEntries(
				ctx,
				"kb",
				&types.FAQBatchUpsertPayload{Entries: []types.FAQEntryPayload{{}}},
			)
			return err
		},
		"import display": func(ctx context.Context) error {
			return f.svc.UpdateLastFAQImportResultDisplayStatus(ctx, "kb", "close")
		},
		"create tag": func(ctx context.Context) error {
			_,
				err := f.tags.CreateTag(ctx,
				"kb",
				"new",
				"",
				0)
			return err
		},

		"find/create tag": func(ctx context.Context) error {
			_,
				err := f.tags.FindOrCreateTagByName(ctx,
				"kb",
				"tag")
			return err
		},

		"update tag": func(ctx context.Context) error {
			_,
				err := f.tags.UpdateTag(ctx,
				"tag",
				nil,
				nil,
				nil)
			return err
		},

		"delete tag": func(ctx context.Context) error { return f.tags.DeleteTag(ctx, "tag", true, false, nil) },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) { require.Error(t, operation(ctx)) })
	}
	require.Zero(t, f.chunks.writes)
	require.Empty(t, f.deletes.deleted)
	// The same service accepts a correctly scoped shared Editor operation.
	tag, err := f.tags.CreateTag(f.ctx, "kb", "created", "", 0)
	require.NoError(t, err)
	require.Equal(t, uint64(7), tag.TenantID)
}

func TestFAQBatchPreflightRejectsBeforeAnyWrite(t *testing.T) {
	no := false
	foreign := int64(22)
	for name, req := range map[string]*types.FAQEntryFieldsBatchUpdate{
		"foreign entry after group": {
			ByTag: map[int64]types.FAQEntryFieldsUpdate{11: {IsEnabled: &no}},
			ByID:  map[int64]types.FAQEntryFieldsUpdate{2: {IsEnabled: &no}},
		},

		"missing entry after group": {
			ByTag: map[int64]types.FAQEntryFieldsUpdate{11: {IsEnabled: &no}},
			ByID:  map[int64]types.FAQEntryFieldsUpdate{999: {}},
		},

		"foreign source tag": {ByTag: map[int64]types.FAQEntryFieldsUpdate{
			11: {IsEnabled: &no},
			22: {IsEnabled: &no},
		}},

		"foreign destination tag": {ByID: map[int64]types.FAQEntryFieldsUpdate{1: {TagID: &foreign}}},
		"foreign exclusion": {
			ByTag:      map[int64]types.FAQEntryFieldsUpdate{11: {IsEnabled: &no}},
			ExcludeIDs: []int64{2},
		},

		"missing exclusion": {
			ByTag:      map[int64]types.FAQEntryFieldsUpdate{11: {IsEnabled: &no}},
			ExcludeIDs: []int64{999},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFAQWriteFixture(t)
			require.Error(t, f.svc.UpdateFAQEntryFieldsBatch(f.ctx, "kb", req))
			require.Zero(t, f.chunks.writes)
			chunk, err := f.chunks.GetChunkByID(f.ctx, 7, "one")
			require.NoError(t, err)
			require.True(t, chunk.IsEnabled)
			require.Equal(t, "tag", chunk.TagID)
		})
	}
}

func TestFAQDeleteValidatesEntireSelectionAndParents(t *testing.T) {
	for _, ids := range [][]int64{{1, 2}, {1, 999}, {1, 0}} {
		t.Run(string(mustWriteJSON(t, ids)), func(t *testing.T) {
			f := newFAQWriteFixture(t)
			require.Error(t, f.svc.DeleteFAQEntries(f.ctx, "kb", ids))
			require.Empty(t, f.deletes.deleted)
		})
	}
	t.Run("foreign parent", func(t *testing.T) {
		f := newFAQWriteFixture(t)
		require.NoError(
			t,
			f.db.Model(&types.Knowledge{}).Where("id = ?", "faq").Update("knowledge_base_id", "other").Error,
		)
		require.Error(t, f.svc.DeleteFAQEntries(f.ctx, "kb", []int64{1}))
		require.Empty(t, f.deletes.deleted)
	})
}

func TestFAQFieldGroupsAndExplicitUpdatesPreservePrecedence(t *testing.T) {
	f := newFAQWriteFixture(t)
	yes, no := true, false
	next := int64(12)
	req := &types.FAQEntryFieldsBatchUpdate{
		ByTag: map[int64]types.FAQEntryFieldsUpdate{11: {TagID: &next, IsRecommended: &yes}},
		ByID:  map[int64]types.FAQEntryFieldsUpdate{1: {IsEnabled: &no, IsRecommended: &no}},
	}
	require.NoError(t, f.svc.UpdateFAQEntryFieldsBatch(f.ctx, "kb", req))
	chunk, err := f.chunks.GetChunkByID(f.ctx, 7, "one")
	require.NoError(t, err)
	require.False(t, chunk.IsEnabled)
	require.Equal(t, "next", chunk.TagID, "an explicit enabled patch must preserve the group's tag change")
	require.False(t, chunk.Flags.HasFlag(types.ChunkFlagRecommended))
	require.Equal(t, map[string]string{"one": "next"}, f.index.tags)
	require.Equal(t, map[string]bool{"one": false}, f.index.enabled)
}

func TestFAQTagOnlyBatchUsesValidatedPlanAndSynchronizesIndex(t *testing.T) {
	f := newFAQWriteFixture(t)
	foreign := int64(22)
	require.Error(t, f.svc.UpdateFAQEntryTagBatch(f.ctx, "kb", map[int64]*int64{1: &foreign}))
	require.Zero(t, f.chunks.writes)
	require.NoError(t, f.svc.UpdateFAQEntryTagBatch(f.ctx, "kb", map[int64]*int64{1: nil}))
	require.Equal(t, map[string]string{"one": ""}, f.index.tags)
}

func TestFAQImportRejectsForeignTagsBeforeEnqueue(t *testing.T) {
	f := newFAQWriteFixture(t)
	_, err := f.svc.UpsertFAQEntries(
		f.ctx,
		"kb",
		&types.FAQBatchUpsertPayload{Entries: []types.FAQEntryPayload{{TagID: 22}}},
	)
	require.Error(t, err) // Redis, file storage and the queue are intentionally unwired.
	require.Zero(t, f.chunks.writes)
	_, err = f.svc.resolveTagID(f.ctx, "kb", &types.FAQEntryPayload{TagID: 22})
	require.Error(t, err)
	resolver := f.svc.buildFAQTagResolver(f.ctx, "kb", []types.FAQEntryPayload{{TagID: 22}})
	_, err = resolver(&types.FAQEntryPayload{TagID: 22})
	require.Error(t, err)
}

func TestFAQImportWorkerRejectsMismatchedScopeBeforeSideEffects(t *testing.T) {
	for _, tenant := range []uint64{7, 8} {
		t.Run(string(mustWriteJSON(t, tenant)), func(t *testing.T) {
			f := newFAQWriteFixture(t)
			require.NoError(
				t,
				f.db.Create(
					&types.Knowledge{
						ID:              "foreign-faq",
						TenantID:        7,
						KnowledgeBaseID: "other",
						Type:            types.KnowledgeTypeFAQ,
					},
				).Error,
			)
			payload := types.FAQImportPayload{TenantID: tenant, KBID: "kb", KnowledgeID: "foreign-faq", TaskID: "task"}
			err := f.svc.ProcessFAQImport(
				context.Background(),
				asynq.NewTask(types.TypeFAQImport, mustWriteJSON(t, payload)),
			)
			require.ErrorIs(t, err, asynq.SkipRetry)
			require.Zero(t, f.chunks.writes)
		})
	}
}

func TestTagDeleteRejectsInvalidExclusionsBeforeDeletion(t *testing.T) {
	for _, id := range []string{"two", "missing"} {
		t.Run(id, func(t *testing.T) {
			f := newFAQWriteFixture(t)
			require.Error(t, f.tags.DeleteTag(f.ctx, "tag", true, false, []string{id}))
			tag, err := f.svc.tagRepo.GetByID(f.ctx, 7, "tag")
			require.NoError(t, err)
			require.NotNil(t, tag)
			chunk, err := f.chunks.GetChunkByID(f.ctx, 7, "one")
			require.NoError(t, err)
			require.NotNil(t, chunk)
		})
	}
}

func mustWriteJSON(t *testing.T, value interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return data
}

func TestSharedFAQWriteLoadsOwnerTenantInfoWithoutReplacingCaller(t *testing.T) {
	f := newFAQWriteFixture(t)
	f.svc.tenantRepo = &processSyncTenantRepo{tenant: &types.Tenant{ID: 7}}
	ctx := context.WithValue(f.ctx, types.TenantInfoContextKey, &types.Tenant{ID: 1})
	_, scoped, err := f.svc.writableFAQKnowledgeBase(ctx, "kb")
	require.NoError(t, err)
	owner, _ := types.TenantInfoFromContext(scoped)
	require.Equal(t, uint64(7), owner.ID)
	require.Equal(t, uint64(1), types.CallerFromContext(scoped).TenantID)
	original, _ := types.TenantInfoFromContext(ctx)
	require.Equal(t, uint64(1), original.ID)
}

func TestDataSourceTagCreationReceivesOnlyItsTaskKBGrant(t *testing.T) {
	h := newSyncDeletionHarness(t, false, "ds-tag-scope", "log-tag-scope", nil, nil)
	tags := h.svc.tagService.(*processSyncTagService)
	_, err := h.run(t)
	require.NoError(t, err)
	require.NotNil(t, tags.ctx)
	require.Zero(t, types.CallerFromContext(tags.ctx).TenantID)
	require.NoError(
		t,
		access.RequireKBWrite(tags.ctx, &types.KnowledgeBase{ID: h.ds.KnowledgeBaseID, TenantID: h.ds.TenantID}),
	)
	require.Error(t, access.RequireKBWrite(tags.ctx, &types.KnowledgeBase{ID: "other", TenantID: h.ds.TenantID}))
}
