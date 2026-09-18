package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type tagTestIndex struct {
	interfaces.RetrieveEngineRepository
	queries            []types.RetrieveParams
	saved              []*types.IndexInfo
	updates            map[string][]string
	prepareCalls       int
	queryErr, writeErr error
}

func (r *tagTestIndex) Retrieve(_ context.Context, p types.RetrieveParams) ([]*types.RetrieveResult, error) {
	r.queries = append(r.queries, p)
	if len(p.TagIDs) > 0 {
		return nil, r.queryErr
	}
	return nil, nil
}
func (r *tagTestIndex) BatchSave(_ context.Context, infos []*types.IndexInfo, _ map[string]any) error {
	r.saved = infos
	return r.writeErr
}
func (r *tagTestIndex) PrepareDocumentTagIndex(context.Context) error { r.prepareCalls++; return nil }
func (r *tagTestIndex) UpdateDocumentTags(_ context.Context, _ string, tags map[string][]string) error {
	r.updates = tags
	return r.writeErr
}
func (r *tagTestIndex) CopyIndices(context.Context, string, map[string]string, map[string]string, string, int, string) error {
	return r.writeErr
}

type tagTaskRecorder struct {
	payloads []types.DocumentTagSyncPayload
}

func (q *tagTaskRecorder) Enqueue(t *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	var p types.DocumentTagSyncPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return nil, err
	}
	q.payloads = append(q.payloads, p)
	return &asynq.TaskInfo{}, nil
}

func tagIndexFixture(t *testing.T) (*gorm.DB, string, string, string, string) {
	t.Helper()
	db := setupKnowledgeTagTestDB(t)
	require.NoError(t, db.Exec(knowledgeBasesTestDDL).Error)
	kb, doc, a, b := seedKnowledgeTagFixture(t, db)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases(id,tenant_id,name,embedding_model_id,summary_model_id) VALUES (?,1,'kb','embedding','summary')", kb).Error)
	require.NoError(t, NewKnowledgeRepository(db).SetKnowledgeTags(context.Background(), doc, []string{a, b}))
	return db, kb, doc, a, b
}

func TestDocumentTagQueriesUsePersistedReadyWithoutMigration(t *testing.T) {
	db, kb, doc, a, _ := tagIndexFixture(t)
	backend, queue := &tagTestIndex{}, &tagTaskRecorder{}
	index := NewDocumentTagIndexRepository(backend, db, queue)
	ctx := context.Background()
	p := types.RetrieveParams{KnowledgeBaseIDs: []string{kb}, TagIDs: []string{a}}
	_, err := index.Retrieve(ctx, p)
	require.NoError(t, err)
	assert.Empty(t, backend.queries[0].TagIDs)
	assert.Equal(t, []string{doc}, backend.queries[0].KnowledgeIDs)
	assert.Zero(t, backend.prepareCalls)
	assert.Empty(t, queue.payloads)
	require.NoError(t, db.Model(&types.KnowledgeBase{}).Where("id = ?", kb).UpdateColumn("document_tag_ready", true).Error)
	_, err = index.Retrieve(ctx, p)
	require.NoError(t, err)
	assert.Equal(t, p, backend.queries[1])
	// A fresh repository instance must use the durable marker without backfill.
	_, err = NewDocumentTagIndexRepository(backend, db, queue).Retrieve(ctx, p)
	require.NoError(t, err)
	assert.Equal(t, p, backend.queries[2])
	backend.queryErr = errors.New("filter index unavailable")
	_, err = index.Retrieve(ctx, p)
	require.NoError(t, err)
	assert.Empty(t, backend.queries[len(backend.queries)-1].TagIDs)
	assert.True(t, reloadKB(t, db, kb).DocumentTagReady)
	assert.Zero(t, backend.prepareCalls)
	assert.Empty(t, queue.payloads)
}

func TestDocumentTagFallbackPreservesScopeAndEmptyIntersection(t *testing.T) {
	db, kb, doc, a, _ := tagIndexFixture(t)
	backend := &tagTestIndex{}
	index := NewDocumentTagIndexRepository(backend, db, nil)
	p := types.RetrieveParams{KnowledgeBaseIDs: []string{kb}, TagIDs: []string{a, "other"}, KnowledgeIDs: []string{doc}, ExcludeChunkIDs: []string{"excluded"}}
	_, err := index.Retrieve(context.Background(), p)
	require.NoError(t, err)
	assert.Equal(t, []string{doc}, backend.queries[0].KnowledgeIDs)
	assert.Equal(t, p.ExcludeChunkIDs, backend.queries[0].ExcludeChunkIDs)
	p.KnowledgeIDs = []string{"outside-scope"}
	_, err = index.Retrieve(context.Background(), p)
	require.NoError(t, err)
	assert.Len(t, backend.queries, 1)
	p.KnowledgeBaseIDs = nil
	_, err = index.Retrieve(context.Background(), p)
	require.Error(t, err)
}

func TestDocumentTagIndexWritesHydrateAndScheduleOnlyLegacyKBs(t *testing.T) {
	db, kb, doc, a, b := tagIndexFixture(t)
	backend, queue := &tagTestIndex{}, &tagTaskRecorder{}
	index := NewDocumentTagIndexRepository(backend, db, queue)
	info := &types.IndexInfo{KnowledgeBaseID: kb, KnowledgeID: doc, TagIDs: []string{"stale"}}
	require.NoError(t, index.Save(context.Background(), info, nil))
	assert.ElementsMatch(t, []string{a, b}, backend.saved[0].TagIDs)
	assert.Equal(t, []string{"stale"}, info.TagIDs)
	require.Len(t, queue.payloads, 1)
	assert.Equal(t, kb, queue.payloads[0].KnowledgeBaseID)
	assert.False(t, reloadKB(t, db, kb).DocumentTagReady, "one successful document cannot certify legacy KB")
	require.NoError(t, db.Model(&types.KnowledgeBase{}).Where("id = ?", kb).UpdateColumn("document_tag_ready", true).Error)
	require.NoError(t, index.Save(context.Background(), info, nil))
	assert.Len(t, queue.payloads, 1)
	backend.writeErr = errors.New("offline")
	require.Error(t, index.Save(context.Background(), info, nil))
	assert.True(t, reloadKB(t, db, kb).DocumentTagReady)
}

func TestDocumentTagSyncReloadsCurrentTagsAndClearsDeletedDocuments(t *testing.T) {
	db, kb, doc, _, b := tagIndexFixture(t)
	backend := &tagTestIndex{}
	index := NewDocumentTagIndexRepository(backend, db, nil).(interfaces.DocumentTagProjection)
	require.NoError(t, NewKnowledgeRepository(db).SetKnowledgeTags(context.Background(), doc, []string{b}))
	require.NoError(t, index.SyncDocumentTags(context.Background(), kb, []string{doc, "outside-kb"}))
	assert.Equal(t, map[string][]string{doc: {b}}, backend.updates)
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", doc).UpdateColumn("deleted_at", gorm.Expr("CURRENT_TIMESTAMP")).Error)
	require.NoError(t, index.SyncDocumentTags(context.Background(), kb, []string{doc}))
	assert.Equal(t, map[string][]string{doc: {}}, backend.updates)
}

type tagSyncFunc func(context.Context, []string) error

func (f tagSyncFunc) Sync(ctx context.Context, ids []string) error { return f(ctx, ids) }

func TestDocumentTagMutationCommitsBeforeSyncAndRetriesUnchangedValue(t *testing.T) {
	db, kb, doc, a, _ := tagIndexFixture(t)
	require.NoError(t, db.Model(&types.KnowledgeBase{}).Where("id = ?", kb).UpdateColumn("document_tag_ready", true).Error)
	calls := 0
	syncer := tagSyncFunc(func(ctx context.Context, ids []string) error {
		calls++
		assert.Equal(t, []string{doc}, ids)
		var relations []types.KnowledgeTagRelation
		require.NoError(t, db.Where("knowledge_id = ?", doc).Find(&relations).Error)
		require.Len(t, relations, 1)
		assert.Equal(t, a, relations[0].TagID)
		if calls == 1 {
			return errors.New("projection unavailable")
		}
		return nil
	})
	repo := NewKnowledgeRepositoryWithTagSync(db, syncer)
	require.Error(t, repo.SetKnowledgeTags(context.Background(), doc, []string{a}))
	require.NoError(t, repo.SetKnowledgeTags(context.Background(), doc, []string{a}))
	assert.Equal(t, 2, calls)
	assert.True(t, reloadKB(t, db, kb).DocumentTagReady)
}

func TestDocumentTagDeletionSyncsAffectedDocumentsAfterCommit(t *testing.T) {
	db, _, doc, a, b := tagIndexFixture(t)
	syncer := tagSyncFunc(func(ctx context.Context, ids []string) error {
		assert.Equal(t, []string{doc}, ids)
		var tags []string
		require.NoError(t, db.Table("knowledge_tag_relations").Where("knowledge_id = ?", doc).Pluck("tag_id", &tags).Error)
		assert.Equal(t, []string{b}, tags)
		return nil
	})
	require.NoError(t, NewKnowledgeTagRepositoryWithTagSync(db, syncer).Delete(context.Background(), 1, a))
}

func TestDocumentTagAutomaticAddAndClearUsePostCommitSync(t *testing.T) {
	db, kb, doc, a, b := tagIndexFixture(t)
	calls := 0
	syncer := tagSyncFunc(func(ctx context.Context, ids []string) error {
		calls++
		var tags []string
		require.NoError(t, db.Table("knowledge_tag_relations").Where("knowledge_id = ?", doc).Pluck("tag_id", &tags).Error)
		if calls == 1 {
			assert.ElementsMatch(t, []string{a, b}, tags)
		} else {
			assert.Empty(t, tags)
		}
		return nil
	})
	repo := NewKnowledgeRepositoryWithTagSync(db, syncer)
	require.NoError(t, repo.AddKnowledgeTagRelations(context.Background(), 1, kb, doc, []string{a, b}))
	require.NoError(t, repo.DeleteKnowledgeTagRelations(context.Background(), doc))
	assert.Equal(t, 2, calls)
}

func TestDocumentTagFAQBypassesProjectionAndLegacyCopySchedulesBootstrap(t *testing.T) {
	db, kb, doc, _, _ := tagIndexFixture(t)
	backend, queue := &tagTestIndex{}, &tagTaskRecorder{}
	index := NewDocumentTagIndexRepository(backend, db, queue)
	faq := &types.IndexInfo{KnowledgeType: types.KnowledgeTypeFAQ, TagID: "faq-tag"}
	require.NoError(t, index.Save(context.Background(), faq, nil))
	_, err := index.Retrieve(context.Background(), types.RetrieveParams{KnowledgeType: types.KnowledgeTypeFAQ, TagIDs: []string{"faq-tag"}})
	require.NoError(t, err)
	assert.Zero(t, backend.prepareCalls)
	assert.Equal(t, "faq-tag", backend.saved[0].TagID)
	require.NoError(t, db.Model(&types.KnowledgeBase{}).Where("id = ?", kb).UpdateColumn("document_tag_ready", true).Error)
	backend.writeErr = errors.New("partially copied")
	require.Error(t, index.CopyIndices(context.Background(), "source", map[string]string{"source-doc": doc}, nil, kb, 3, ""))
	assert.False(t, reloadKB(t, db, kb).DocumentTagReady)
	require.Len(t, queue.payloads, 1)
}

func TestNewKBReadySurvivesUnrelatedStaleSave(t *testing.T) {
	db := setupKBTestDB(t)
	repo := NewKnowledgeBaseRepository(db)
	kb := makeKB(nil)
	require.NoError(t, repo.CreateKnowledgeBase(context.Background(), kb))
	assert.True(t, reloadKB(t, db, kb.ID).DocumentTagReady)
	kb.DocumentTagReady = false
	kb.Name = "renamed"
	require.NoError(t, repo.UpdateKnowledgeBase(context.Background(), kb))
	assert.True(t, reloadKB(t, db, kb.ID).DocumentTagReady)
}
