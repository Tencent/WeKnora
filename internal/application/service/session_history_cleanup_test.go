package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Regression guard for the chat-history cleanup split in session.go: IDs are
// collected in the foreground (before the rows are deleted) and the deletion
// itself runs detached in the background. Without these tests a refactor could
// either drop the cleanup entirely (the old code deleted inline) or push the
// unbounded vector-store/object-storage work back into the request path (the
// old code waited on cleanupWg before deleting rows).

// historyCleanupMessageRepo stands in for the message repository's
// chat-history lookup and records whether the session row still existed at
// collection time, which is the ordering the split depends on.
type historyCleanupMessageRepo struct {
	interfaces.MessageRepository
	db           *gorm.DB
	bySession    map[string][]string
	errBySession map[string]error

	mu           sync.Mutex
	collectCalls int
	rowExisted   map[string]bool
}

func (r *historyCleanupMessageRepo) GetKnowledgeIDsBySessionID(
	ctx context.Context, sessionID string,
) ([]string, error) {
	r.mu.Lock()
	r.collectCalls++
	r.mu.Unlock()
	// A real repository lookup fails fast on a cancelled context; honour it so
	// tests can observe how the collector treats request cancellation.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := r.errBySession[sessionID]; err != nil {
		return nil, err
	}
	if r.db != nil {
		var count int64
		if err := r.db.Model(&types.Session{}).Where("id = ?", sessionID).Count(&count).Error; err == nil {
			r.mu.Lock()
			if r.rowExisted == nil {
				r.rowExisted = make(map[string]bool)
			}
			r.rowExisted[sessionID] = count > 0
			r.mu.Unlock()
		}
	}
	return r.bySession[sessionID], nil
}

func (r *historyCleanupMessageRepo) snapshot() (calls int, rowExisted map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]bool, len(r.rowExisted))
	for id, ok := range r.rowExisted {
		out[id] = ok
	}
	return r.collectCalls, out
}

type historyCleanupKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	rows map[string]*types.Knowledge
}

func (r *historyCleanupKnowledgeRepo) GetKnowledgeBatch(
	_ context.Context, tenant uint64, ids []string,
) ([]*types.Knowledge, error) {
	out := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		if row, ok := r.rows[id]; ok && row.TenantID == tenant {
			out = append(out, row)
		}
	}
	return out, nil
}

// historyCleanupKnowledgeService records the deletions the session cleanup
// performs. entered/release let a test hold a delete in flight to observe that
// the request path no longer waits for it.
type historyCleanupKnowledgeService struct {
	interfaces.KnowledgeService
	repo *historyCleanupKnowledgeRepo

	mu      sync.Mutex
	deleted [][]string
	entered chan struct{}
	release chan struct{}
}

func (s *historyCleanupKnowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

func (s *historyCleanupKnowledgeService) DeleteKnowledgeList(_ context.Context, ids []string) error {
	s.mu.Lock()
	entered, release := s.entered, s.release
	s.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	s.mu.Lock()
	s.deleted = append(s.deleted, ids)
	s.mu.Unlock()
	return nil
}

func (s *historyCleanupKnowledgeService) deletedCalls() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]string, len(s.deleted))
	copy(out, s.deleted)
	return out
}

func newSessionServiceForHistoryCleanupTest(
	t *testing.T, name string,
) (*sessionService, *gorm.DB, *historyCleanupMessageRepo, *historyCleanupKnowledgeService) {
	t.Helper()

	// Unique per invocation: a shared in-memory database survives soft-deleted
	// rows, so a repeated run (-count>1) would collide on the same primary keys.
	db, err := gorm.Open(
		sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", name, time.Now().UnixNano())),
		&gorm.Config{},
	)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Session{}, &types.ForkSnapshotLease{}))

	msgs := &historyCleanupMessageRepo{
		db:           db,
		bySession:    make(map[string][]string),
		errBySession: make(map[string]error),
		rowExisted:   make(map[string]bool),
	}
	knowledge := &historyCleanupKnowledgeService{
		repo: &historyCleanupKnowledgeRepo{rows: make(map[string]*types.Knowledge)},
	}
	svc := &sessionService{
		sessionRepo:        repository.NewSessionRepository(db),
		messageRepo:        msgs,
		webSearchStateRepo: deleteForkWebSearchState{},
		knowledgeService:   knowledge,
		forkSnapshots:      &fakeForkSessionSnapshotDeleter{},
	}
	return svc, db, msgs, knowledge
}

func TestDeleteSessionStillCleansUpChatHistoryKnowledge(t *testing.T) {
	svc, db, msgs, knowledge := newSessionServiceForHistoryCleanupTest(t, "history_cleanup_delete")
	ctx := testSessionScopeContext(1, "u1")
	// SkipHooks keeps the explicit ID: Session.BeforeCreate would otherwise
	// replace it with a fresh UUID.
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID: "sess-1", TenantID: 1, UserID: "u1", Title: "chat with history",
	}).Error)
	msgs.bySession["sess-1"] = []string{"k-1", "k-2"}
	knowledge.repo.rows["k-1"] = &types.Knowledge{ID: "k-1", TenantID: 1, KnowledgeBaseID: "kb-1"}
	knowledge.repo.rows["k-2"] = &types.Knowledge{ID: "k-2", TenantID: 1, KnowledgeBaseID: "kb-1"}

	require.NoError(t, svc.DeleteSession(ctx, "sess-1"))

	var remaining int64
	require.NoError(t, db.Model(&types.Session{}).Count(&remaining).Error)
	require.Zero(t, remaining, "session row must be gone")

	// Cleanup is asynchronous, but it must still happen: splitting collection
	// from deletion must not lose the deletion itself.
	require.Eventually(t, func() bool {
		for _, ids := range knowledge.deletedCalls() {
			if len(ids) == 2 && ids[0] == "k-1" && ids[1] == "k-2" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond,
		"chat-history knowledge must still be deleted after the session row is gone")

	// The IDs must be collected while the row still exists, otherwise the
	// message→knowledge association could already be unreachable.
	calls, rowExisted := msgs.snapshot()
	require.Equal(t, 1, calls)
	require.True(t, rowExisted["sess-1"],
		"knowledge IDs must be collected before the session row is deleted")
}

func TestBatchDeleteSessionsDoesNotWaitForChatHistoryCleanup(t *testing.T) {
	svc, db, msgs, knowledge := newSessionServiceForHistoryCleanupTest(t, "history_cleanup_batch")
	ctx := testSessionScopeContext(1, "u1")
	for _, id := range []string{"sess-1", "sess-2"} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
			ID: id, TenantID: 1, UserID: "u1", Title: "chat",
		}).Error)
		msgs.bySession[id] = []string{"k-" + id}
		knowledge.repo.rows["k-"+id] = &types.Knowledge{
			ID: "k-" + id, TenantID: 1, KnowledgeBaseID: "kb-1",
		}
	}
	// Hold every background delete so the test can observe the request path
	// returning while cleanup is still in flight.
	knowledge.mu.Lock()
	knowledge.entered = make(chan struct{}, 8)
	knowledge.release = make(chan struct{})
	knowledge.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- svc.BatchDeleteSessions(ctx, []string{"sess-1", "sess-2"}) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("BatchDeleteSessions blocked on chat-history cleanup")
	}

	// Rows are gone while the cleanup is still blocked.
	var remaining int64
	require.NoError(t, db.Model(&types.Session{}).Count(&remaining).Error)
	require.Zero(t, remaining)
	require.Empty(t, knowledge.deletedCalls(), "cleanup should still be in flight")

	close(knowledge.release)
	require.Eventually(t, func() bool {
		return len(knowledge.deletedCalls()) == 2
	}, 5*time.Second, 10*time.Millisecond,
		"background cleanup must finish after the request returned")
}

// DeleteAllSessions received the same collect-then-async-cleanup split as the
// other two delete entry points, so it must keep the same invariants: the IDs
// are collected while the rows still exist and the deletion still happens.
func TestDeleteAllSessionsStillCleansUpChatHistoryKnowledge(t *testing.T) {
	svc, db, msgs, knowledge := newSessionServiceForHistoryCleanupTest(t, "history_cleanup_delete_all")
	ctx := testSessionScopeContext(1, "u1")
	for _, id := range []string{"sess-1", "sess-2"} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
			ID: id, TenantID: 1, UserID: "u1", Title: "chat",
		}).Error)
		msgs.bySession[id] = []string{"k-" + id}
		knowledge.repo.rows["k-"+id] = &types.Knowledge{
			ID: "k-" + id, TenantID: 1, KnowledgeBaseID: "kb-1",
		}
	}

	require.NoError(t, svc.DeleteAllSessions(ctx))

	var remaining int64
	require.NoError(t, db.Model(&types.Session{}).Count(&remaining).Error)
	require.Zero(t, remaining)

	require.Eventually(t, func() bool {
		seen := map[string]bool{}
		for _, ids := range knowledge.deletedCalls() {
			for _, id := range ids {
				seen[id] = true
			}
		}
		return seen["k-sess-1"] && seen["k-sess-2"]
	}, 5*time.Second, 10*time.Millisecond,
		"delete-all must still clean up chat-history knowledge")

	calls, rowExisted := msgs.snapshot()
	require.Equal(t, 2, calls)
	require.True(t, rowExisted["sess-1"] && rowExisted["sess-2"],
		"IDs must be collected before the rows are deleted")
}

// A per-session collection failure must skip only that session: the delete
// request still succeeds and the other sessions' history is still cleaned.
func TestBatchDeleteSkipsFailedCollectionButCleansTheRest(t *testing.T) {
	svc, db, msgs, knowledge := newSessionServiceForHistoryCleanupTest(t, "history_cleanup_partial")
	ctx := testSessionScopeContext(1, "u1")
	for _, id := range []string{"sess-ok", "sess-bad"} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
			ID: id, TenantID: 1, UserID: "u1", Title: "chat",
		}).Error)
	}
	msgs.bySession["sess-ok"] = []string{"k-ok"}
	msgs.errBySession["sess-bad"] = stderrors.New("message lookup failed")
	knowledge.repo.rows["k-ok"] = &types.Knowledge{ID: "k-ok", TenantID: 1, KnowledgeBaseID: "kb-1"}

	require.NoError(t, svc.BatchDeleteSessions(ctx, []string{"sess-ok", "sess-bad"}))

	var remaining int64
	require.NoError(t, db.Model(&types.Session{}).Count(&remaining).Error)
	require.Zero(t, remaining, "both rows are deleted even though one lookup failed")

	require.Eventually(t, func() bool {
		for _, ids := range knowledge.deletedCalls() {
			if len(ids) == 1 && ids[0] == "k-ok" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond,
		"the healthy session's history must still be cleaned up")
}

// A client disconnect must not skip the collection: the rows are deleted
// anyway, so an aborted lookup would orphan that session's chat-history
// vectors. The collector therefore runs detached from request cancellation.
func TestCollectSessionKnowledgeIDsSurvivesCancelledRequest(t *testing.T) {
	svc, _, msgs, _ := newSessionServiceForHistoryCleanupTest(t, "history_cleanup_cancelled")
	msgs.bySession["sess-1"] = []string{"k-1"}

	ctx, cancel := context.WithCancel(testSessionScopeContext(1, "u1"))
	cancel() // client disconnected before the delete handler collected IDs

	require.Equal(t, map[string][]string{"sess-1": {"k-1"}},
		svc.collectSessionKnowledgeIDs(ctx, []string{"sess-1"}),
		"collection must ignore request cancellation or the vectors are orphaned")
}
