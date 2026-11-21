package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type historyClearTenantService struct {
	interfaces.TenantService
	mu          sync.Mutex
	tenant      *types.Tenant
	writes      int
	failWriteAt int
}

func (s *historyClearTenantService) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyOfTenant := *s.tenant
	if s.tenant.ChatHistoryConfig != nil {
		cfg := *s.tenant.ChatHistoryConfig
		copyOfTenant.ChatHistoryConfig = &cfg
	}
	return &copyOfTenant, nil
}

func (s *historyClearTenantService) SetChatHistoryConfig(
	ctx context.Context, tenantID uint64, config *types.ChatHistoryConfig,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.writes++
	if s.failWriteAt == s.writes {
		return errors.New("configuration write failed")
	}
	if tenantID != s.tenant.ID {
		return errors.New("configuration update must be a tenant-scoped partial write")
	}
	cfg := *config
	s.tenant.ChatHistoryConfig = &cfg
	return nil
}

type historyClearKBService struct {
	interfaces.KnowledgeBaseService
	kb      *types.KnowledgeBase
	deleted []string
	created []*types.KnowledgeBase
}

func (s *historyClearKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	copyOfKB := *s.kb
	return &copyOfKB, nil
}

func (s *historyClearKBService) DeleteKnowledgeBase(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *historyClearKBService) CreateKnowledgeBase(
	ctx context.Context, kb *types.KnowledgeBase,
) (*types.KnowledgeBase, error) {
	kb.ID = "new-history"
	kb.TenantID = types.MustTenantIDFromContext(ctx)
	s.created = append(s.created, kb)
	s.kb = kb
	return kb, nil
}

type historyClearKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	mu       sync.Mutex
	rows     map[string]*types.Knowledge
	countErr error
}

func (r *historyClearKnowledgeRepo) CountAllKnowledgeByKnowledgeBaseID(
	ctx context.Context, tenantID uint64, kbID string,
) (int64, error) {
	rows, err := r.ListKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		return 0, err
	}
	return int64(len(rows)), r.countErr
}

func (r *historyClearKnowledgeRepo) ListKnowledgeByKnowledgeBaseID(
	_ context.Context, tenantID uint64, kbID string,
) ([]*types.Knowledge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []*types.Knowledge
	for _, row := range r.rows {
		if row.TenantID == tenantID && row.KnowledgeBaseID == kbID {
			copyOfRow := *row
			rows = append(rows, &copyOfRow)
		}
	}
	return rows, nil
}

func (r *historyClearKnowledgeRepo) GetKnowledgeBatch(
	_ context.Context, tenantID uint64, ids []string,
) ([]*types.Knowledge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []*types.Knowledge
	for _, id := range ids {
		if row := r.rows[id]; row != nil && row.TenantID == tenantID {
			copyOfRow := *row
			rows = append(rows, &copyOfRow)
		}
	}
	return rows, nil
}

type historyClearKnowledgeService struct {
	interfaces.KnowledgeService
	repo        *historyClearKnowledgeRepo
	batches     []int
	failBatchAt int
	createGate  func()
	createdKB   string
	skipDelete  bool
}

func (s *historyClearKnowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

func (s *historyClearKnowledgeService) DeleteKnowledgeList(ctx context.Context, ids []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.batches = append(s.batches, len(ids))
	if s.failBatchAt == len(s.batches) {
		s.repo.mu.Lock()
		for _, id := range ids {
			s.repo.rows[id].ParseStatus = types.ParseStatusDeleting
		}
		s.repo.mu.Unlock()
		return errors.New("vector store cleanup failed")
	}
	scope, ok := ctx.Value(knowledgeCleanupKey{}).(knowledgeCleanupScope)
	if !ok || scope.tenant != types.MustTenantIDFromContext(ctx) {
		return errors.New("cleanup must use persisted tenant-scoped bindings")
	}
	if s.skipDelete {
		return nil
	}
	s.repo.mu.Lock()
	defer s.repo.mu.Unlock()
	for _, id := range ids {
		row := s.repo.rows[id]
		if row == nil || scope.bindings[id] != row.KnowledgeBaseID || row.TenantID != scope.tenant {
			return errors.New("wrong cleanup binding")
		}
		delete(s.repo.rows, id)
	}
	return nil
}

func (s *historyClearKnowledgeService) CreateKnowledgeFromPassage(
	ctx context.Context, kbID string, _ []string, _ string,
) (*types.Knowledge, error) {
	if s.createGate != nil {
		s.createGate()
	}
	s.repo.mu.Lock()
	defer s.repo.mu.Unlock()
	s.createdKB = kbID
	row := &types.Knowledge{ID: "new-index", TenantID: types.MustTenantIDFromContext(ctx), KnowledgeBaseID: kbID}
	s.repo.rows[row.ID] = row
	return row, nil
}

type historyClearMessageRepo struct {
	interfaces.MessageRepository
	linked []string
}

func (r *historyClearMessageRepo) UpdateMessageKnowledgeID(_ context.Context, _, id string) error {
	r.linked = append(r.linked, id)
	return nil
}

func historyClearFixture(t *testing.T, count int) (
	context.Context, *messageService, *historyClearTenantService, *historyClearKnowledgeService,
) {
	t.Helper()
	tenant := &types.Tenant{ID: 1, Name: "must not be overwritten", ChatHistoryConfig: &types.ChatHistoryConfig{
		Enabled: true, EmbeddingModelID: "embedding", KnowledgeBaseID: "history",
	}}
	tenants := &historyClearTenantService{tenant: tenant}
	repo := &historyClearKnowledgeRepo{rows: map[string]*types.Knowledge{
		"other-kb":     {ID: "other-kb", TenantID: 1, KnowledgeBaseID: "normal-kb"},
		"other-tenant": {ID: "other-tenant", TenantID: 2, KnowledgeBaseID: "history"},
	}}
	for i := range count {
		id := fmt.Sprintf("index-%d", i)
		repo.rows[id] = &types.Knowledge{ID: id, TenantID: 1, KnowledgeBaseID: "history"}
	}
	knowledge := &historyClearKnowledgeService{repo: repo}
	s := &messageService{
		tenantService: tenants,
		kbService: &historyClearKBService{
			kb: &types.KnowledgeBase{ID: "history", TenantID: 1, IsTemporary: true, Name: "__chat_history__"},
		},
		knowService: knowledge,
		messageRepo: &historyClearMessageRepo{},
	}
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	return ctx, s, tenants, knowledge
}

func TestChatHistoryStatsKeepIndexedCountWhenDisabled(t *testing.T) {
	ctx, s, tenants, _ := historyClearFixture(t, 3)
	tenants.tenant.ChatHistoryConfig.Enabled = false
	stats, err := s.GetChatHistoryKBStats(ctx)
	require.NoError(t, err)
	require.False(t, stats.Enabled)
	require.EqualValues(t, 3, stats.IndexedMessageCount)
	require.True(t, stats.HasIndexedMessages, "disabling indexing must not hide retained indexes or unlock the model")
}

func TestChatHistoryClearOnlyDeletesCurrentWorkspaceIndex(t *testing.T) {
	ctx, s, tenants, knowledge := historyClearFixture(t, 245)
	staleTenant := &types.Tenant{ID: 2, ChatHistoryConfig: &types.ChatHistoryConfig{KnowledgeBaseID: "victim"}}
	stats, err := s.ClearChatHistoryIndex(context.WithValue(ctx, types.TenantInfoContextKey, staleTenant))
	require.NoError(t, err)
	require.False(t, stats.Enabled)
	require.False(t, stats.HasIndexedMessages)
	require.Empty(t, stats.KnowledgeBaseID, "re-enable must use a fresh index generation")
	require.Zero(t, stats.IndexedMessageCount)
	require.Equal(t, []int{200, 45}, knowledge.batches)
	require.Len(t, knowledge.repo.rows, 2)
	require.Contains(t, knowledge.repo.rows, "other-kb")
	require.Contains(t, knowledge.repo.rows, "other-tenant")
	require.Empty(t, s.messageRepo.(*historyClearMessageRepo).linked, "original messages must not be rewritten")
	require.Equal(t, "must not be overwritten", tenants.tenant.Name)
	require.Equal(t, []string{"history"}, s.kbService.(*historyClearKBService).deleted)

	_, err = s.ClearChatHistoryIndex(ctx)
	require.NoError(t, err, "a repeated clear is idempotent")
	require.Equal(t, []int{200, 45}, knowledge.batches)
}

func TestChatHistoryClearDoesNotDeleteIfPausingFails(t *testing.T) {
	ctx, s, tenants, knowledge := historyClearFixture(t, 2)
	tenants.failWriteAt = 1
	stats, err := s.ClearChatHistoryIndex(ctx)
	require.ErrorContains(t, err, "configuration write failed")
	require.Nil(t, stats)
	require.True(t, tenants.tenant.ChatHistoryConfig.Enabled)
	require.Len(t, knowledge.repo.rows, 4)
	require.Empty(t, knowledge.batches)
}

func TestChatHistoryClearFailureKeepsPauseAndSupportsRetry(t *testing.T) {
	ctx, s, tenants, knowledge := historyClearFixture(t, 245)
	knowledge.failBatchAt = 2
	stats, err := s.ClearChatHistoryIndex(ctx)
	require.ErrorContains(t, err, "vector store cleanup failed")
	require.Nil(t, stats)
	require.False(t, tenants.tenant.ChatHistoryConfig.Enabled)
	require.Equal(t, "history", tenants.tenant.ChatHistoryConfig.KnowledgeBaseID)
	require.Len(t, knowledge.repo.rows, 47)
	_, err = s.UpdateChatHistoryConfig(ctx, &types.ChatHistoryConfig{Enabled: true, EmbeddingModelID: "different"})
	require.ErrorContains(t, err, "Clear indexed messages", "a partial failure must not unlock a model change")

	stats, err = s.ClearChatHistoryIndex(ctx)
	require.NoError(t, err)
	require.False(t, stats.HasIndexedMessages)
	require.Len(t, knowledge.repo.rows, 2)
	require.Equal(t, []int{200, 45, 45}, knowledge.batches)
}

func TestChatHistoryClearCanRetryFinalConfigurationFailure(t *testing.T) {
	ctx, s, tenants, knowledge := historyClearFixture(t, 2)
	tenants.failWriteAt = 2
	_, err := s.ClearChatHistoryIndex(ctx)
	require.ErrorContains(t, err, "configuration write failed")
	require.False(t, tenants.tenant.ChatHistoryConfig.Enabled)
	require.Equal(t, "history", tenants.tenant.ChatHistoryConfig.KnowledgeBaseID)
	require.Empty(t, s.kbService.(*historyClearKBService).deleted)
	require.Len(t, knowledge.repo.rows, 2)
	stats, err := s.ClearChatHistoryIndex(ctx)
	require.NoError(t, err)
	require.Empty(t, stats.KnowledgeBaseID)
}

func TestChatHistoryClearRejectsNonHistoryAndForeignKnowledgeBases(t *testing.T) {
	for _, change := range []func(*types.KnowledgeBase){
		func(kb *types.KnowledgeBase) { kb.TenantID = 2 },
		func(kb *types.KnowledgeBase) { kb.IsTemporary = false },
		func(kb *types.KnowledgeBase) { kb.Name = "ordinary documents" },
	} {
		ctx, s, tenants, knowledge := historyClearFixture(t, 2)
		change(s.kbService.(*historyClearKBService).kb)
		_, err := s.ClearChatHistoryIndex(ctx)
		require.ErrorContains(t, err, "Not a workspace chat history")
		require.Zero(t, tenants.writes)
		require.Empty(t, knowledge.batches)
	}
}

func TestChatHistoryStatisticsFailClosed(t *testing.T) {
	ctx, s, _, knowledge := historyClearFixture(t, 2)
	knowledge.repo.countErr = errors.New("count unavailable")
	stats, err := s.GetChatHistoryKBStats(ctx)
	require.ErrorContains(t, err, "count unavailable")
	require.Nil(t, stats)
}

func TestChatHistoryClearVerifiesEmptyBeforeDetachingTheIndex(t *testing.T) {
	ctx, s, tenants, knowledge := historyClearFixture(t, 2)
	knowledge.skipDelete = true
	stats, err := s.ClearChatHistoryIndex(ctx)
	require.ErrorContains(t, err, "indexes remain")
	require.Nil(t, stats)
	require.False(t, tenants.tenant.ChatHistoryConfig.Enabled)
	require.Equal(t, "history", tenants.tenant.ChatHistoryConfig.KnowledgeBaseID)
	require.Empty(t, s.kbService.(*historyClearKBService).deleted)
}

func TestChatHistoryClearCountFailureKeepsBindingForRetry(t *testing.T) {
	ctx, s, tenants, knowledge := historyClearFixture(t, 2)
	knowledge.repo.countErr = errors.New("verification unavailable")
	stats, err := s.ClearChatHistoryIndex(ctx)
	require.ErrorContains(t, err, "verification unavailable")
	require.Nil(t, stats)
	require.False(t, tenants.tenant.ChatHistoryConfig.Enabled)
	require.Equal(t, "history", tenants.tenant.ChatHistoryConfig.KnowledgeBaseID)
	require.Empty(t, s.kbService.(*historyClearKBService).deleted)
	knowledge.repo.countErr = nil
	stats, err = s.ClearChatHistoryIndex(ctx)
	require.NoError(t, err)
	require.Empty(t, stats.KnowledgeBaseID)
	require.False(t, stats.HasIndexedMessages)
}

func TestChatHistoryClearCancelledWhileWaitingHasNoSideEffects(t *testing.T) {
	ctx, s, tenants, knowledge := historyClearFixture(t, 2)
	release, err := chatHistoryLocalLocks.lock(ctx, "weknora-chat-history-lock:1")
	require.NoError(t, err)
	defer release()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	stats, err := s.ClearChatHistoryIndex(cancelled)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, stats)
	require.Zero(t, tenants.writes)
	require.Empty(t, knowledge.batches)
}

func TestChatHistoryClearWithoutConfigurationAndTenant(t *testing.T) {
	ctx, s, tenants, knowledge := historyClearFixture(t, 0)
	tenants.tenant.ChatHistoryConfig = nil
	stats, err := s.ClearChatHistoryIndex(ctx)
	require.NoError(t, err)
	require.Equal(t, &types.ChatHistoryKBStats{}, stats)
	require.Zero(t, tenants.writes)
	require.Empty(t, knowledge.batches)
	_, err = s.ClearChatHistoryIndex(context.Background())
	require.ErrorContains(t, err, "Workspace is empty")
}

func TestChatHistoryModelChangeRequiresClearEvenWhenDisabled(t *testing.T) {
	ctx, s, tenants, _ := historyClearFixture(t, 2)
	tenants.tenant.ChatHistoryConfig.Enabled = false
	_, err := s.UpdateChatHistoryConfig(ctx, &types.ChatHistoryConfig{
		Enabled: true, EmbeddingModelID: "different", KnowledgeBaseID: "injected",
	})
	require.ErrorContains(t, err, "Clear indexed messages")
	require.Zero(t, tenants.writes)
	_, err = s.ClearChatHistoryIndex(ctx)
	require.NoError(t, err)
	cfg, err := s.UpdateChatHistoryConfig(ctx, &types.ChatHistoryConfig{
		Enabled: true, EmbeddingModelID: "different", KnowledgeBaseID: "injected",
	})
	require.NoError(t, err)
	require.Equal(t, "new-history", cfg.KnowledgeBaseID)
	require.Equal(t, "different", s.kbService.(*historyClearKBService).created[0].EmbeddingModelID)
}

func TestChatHistoryClearPreventsStaleTurnsFromRecreatingIndexes(t *testing.T) {
	ctx, s, _, knowledge := historyClearFixture(t, 2)
	// Snapshot the tenant as an already-running chat request would do.
	before, err := s.tenantService.GetTenantByID(ctx, 1)
	require.NoError(t, err)
	stale := context.WithValue(ctx, types.TenantInfoContextKey, before)
	_, err = s.ClearChatHistoryIndex(ctx)
	require.NoError(t, err)
	s.IndexMessageToKB(stale, "question", "answer", "message", "session")
	require.Empty(t, knowledge.createdKB)

	_, err = s.UpdateChatHistoryConfig(ctx, &types.ChatHistoryConfig{Enabled: true, EmbeddingModelID: "embedding"})
	require.NoError(t, err)
	s.IndexMessageToKB(stale, "question", "answer", "message", "session")
	require.Equal(t, "new-history", knowledge.createdKB, "even the same model must not reuse the cleared generation")
}

func TestChatHistoryClearWaitsForIndexingAcrossReplicas(t *testing.T) {
	ctx, s, tenants, knowledge := historyClearFixture(t, 0)
	server := miniredis.RunT(t)
	first := redis.NewClient(&redis.Options{Addr: server.Addr()})
	second := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	s.historyRedis = first
	replica := *s
	replica.historyRedis = second

	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	knowledge.createGate = func() { close(entered); <-release }
	go func() {
		defer close(done)
		s.IndexMessageToKB(ctx, "question", "answer", "message", "session")
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("index did not acquire its lease")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err := replica.ClearChatHistoryIndex(waitCtx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, tenants.writes, "a second replica must not pause/delete before the in-flight insertion finishes")
	unblock()
	<-done
	_, err = replica.ClearChatHistoryIndex(ctx)
	require.NoError(t, err)
	require.Equal(t, []int{1}, knowledge.batches,
		"the clear must include the index admitted before it acquired the lease")
	require.Len(t, knowledge.repo.rows, 2)
}

func TestChatHistoryLockFailureDoesNotFallBackToProcessLocalLock(t *testing.T) {
	ctx, s, tenants, knowledge := historyClearFixture(t, 2)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	server.Close()
	s.historyRedis = client
	_, err := s.ClearChatHistoryIndex(ctx)
	require.Error(t, err)
	require.Zero(t, tenants.writes)
	require.Empty(t, knowledge.batches)
}
