package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const kbCountOwnerTenant uint64 = 31

type kbCountListRepository struct {
	interfaces.KnowledgeBaseRepository
	t  *testing.T
	kb *types.KnowledgeBase
}

func (r *kbCountListRepository) ListKnowledgeBasesByTenantID(
	_ context.Context, tenantID uint64,
) ([]*types.KnowledgeBase, error) {
	r.t.Helper()
	require.Equal(r.t, kbCountOwnerTenant, tenantID)
	return []*types.KnowledgeBase{r.kb}, nil
}

// Exercise the real repository count queries against synthetic SQLite rows.
// Only the KB listing is stubbed; no external services or customer data are used.
func newKBCountFixture(
	t *testing.T, kbType string, statuses []string, chunkCount int,
) (*knowledgeBaseService, *types.KnowledgeBase) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	// Minimal schemas keep this count test independent of unrelated migrations.
	require.NoError(t, db.Exec(`CREATE TABLE knowledges (
		id TEXT PRIMARY KEY, tenant_id INTEGER, knowledge_base_id TEXT,
		parse_status TEXT, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE chunks (
		id TEXT PRIMARY KEY, tenant_id INTEGER, knowledge_base_id TEXT,
		chunk_type TEXT, is_enabled BOOLEAN, deleted_at DATETIME)`).Error)
	kb := &types.KnowledgeBase{ID: "count-fixture-kb", TenantID: kbCountOwnerTenant, Type: kbType}
	for i, status := range statuses {
		require.NoError(t, db.Exec(`INSERT INTO knowledges
			(id, tenant_id, knowledge_base_id, parse_status) VALUES (?, ?, ?, ?)`,
			fmt.Sprintf("document-%d", i), kb.TenantID, kb.ID, status).Error)
	}
	chunkTypes := []string{types.ChunkTypeText, types.ChunkTypeParentText, types.ChunkTypeImageOCR}
	for i := range chunkCount {
		chunkType := chunkTypes[i%len(chunkTypes)]
		if kbType == types.KnowledgeBaseTypeFAQ {
			chunkType = types.ChunkTypeFAQ
		}
		// The existing counter includes disabled and non-leaf/non-text rows.
		require.NoError(t, db.Exec(`INSERT INTO chunks
			(id, tenant_id, knowledge_base_id, chunk_type, is_enabled) VALUES (?, ?, ?, ?, ?)`,
			fmt.Sprintf("chunk-%d", i), kb.TenantID, kb.ID, chunkType, i != 1).Error)
	}
	// Same KB in another tenant, another KB in the owner tenant, and deleted
	// rows must not leak into any of the three exposed count paths.
	for _, row := range []struct {
		id      string
		tenant  uint64
		kbID    string
		deleted any
	}{
		{"other-tenant", 99, kb.ID, nil},
		{"other-kb", kb.TenantID, "different-kb", nil},
		{"soft-deleted", kb.TenantID, kb.ID, "2000-01-01 00:00:00"},
	} {
		require.NoError(t, db.Exec(`INSERT INTO knowledges
			(id, tenant_id, knowledge_base_id, parse_status, deleted_at) VALUES (?, ?, ?, ?, ?)`,
			row.id, row.tenant, row.kbID, types.ParseStatusFinalizing, row.deleted).Error)
		require.NoError(t, db.Exec(`INSERT INTO chunks
			(id, tenant_id, knowledge_base_id, chunk_type, is_enabled, deleted_at) VALUES (?, ?, ?, ?, ?, ?)`,
			row.id, row.tenant, row.kbID, types.ChunkTypeText, true, row.deleted).Error)
	}
	return &knowledgeBaseService{
		repo:      &kbCountListRepository{t: t, kb: kb},
		kgRepo:    repository.NewKnowledgeRepository(db),
		chunkRepo: repository.NewChunkRepository(db),
	}, kb
}

func runKBCountEntry(t *testing.T, entry string, svc *knowledgeBaseService, kb *types.KnowledgeBase) {
	t.Helper()
	// Explicit-tenant and single-KB enrichment must use the owner tenant,
	// even when the caller's execution tenant is different.
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(99))
	switch entry {
	case "list":
		ctx = context.WithValue(ctx, types.TenantIDContextKey, kbCountOwnerTenant)
		got, err := svc.ListKnowledgeBases(ctx)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Same(t, kb, got[0])
	case "list-by-tenant":
		got, err := svc.ListKnowledgeBasesByTenantID(ctx, kbCountOwnerTenant)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Same(t, kb, got[0])
	case "fill":
		require.NoError(t, svc.FillKnowledgeBaseCounts(ctx, kb))
	default:
		t.Fatalf("unknown test entry %q", entry)
	}
}

func TestKnowledgeBaseChunkCounts(t *testing.T) {
	for _, entry := range []string{"list", "list-by-tenant", "fill"} {
		for _, kbType := range []string{types.KnowledgeBaseTypeDocument, "", types.KnowledgeBaseTypeFAQ} {
			for _, count := range []int{0, 3} {
				t.Run(fmt.Sprintf("%s/type=%s/chunks=%d", entry, kbType, count), func(t *testing.T) {
					svc, kb := newKBCountFixture(t, kbType, []string{types.ParseStatusCompleted}, count)
					if count == 0 {
						kb.ChunkCount = 99 // Successful zero counts must replace an old value.
					}
					runKBCountEntry(t, entry, svc, kb)
					assert.Equal(t, int64(count), kb.ChunkCount)
					wantKnowledge := int64(1)
					if kbType == types.KnowledgeBaseTypeFAQ {
						wantKnowledge = 0
					}
					assert.Equal(t, wantKnowledge, kb.KnowledgeCount)
					assert.Zero(t, kb.ProcessingCount)
					assert.False(t, kb.IsProcessing)
				})
			}
		}
	}
}

func TestKnowledgeBaseProcessingCounts(t *testing.T) {
	cases := []struct {
		name       string
		statuses   []string
		processing int64
		knowledge  int64
	}{
		{"pending", []string{types.ParseStatusPending}, 1, 1},
		{"processing", []string{types.ParseStatusProcessing}, 1, 1},
		{"finalizing", []string{types.ParseStatusFinalizing}, 1, 1},
		{"completed", []string{types.ParseStatusCompleted}, 0, 1},
		{"failed", []string{types.ParseStatusFailed}, 0, 1},
		{"cancelled", []string{types.ParseStatusCancelled}, 0, 1},
		{"deleting", []string{types.ParseStatusDeleting}, 0, 0},
		{"mixed", []string{
			types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing,
			types.ParseStatusCompleted, types.ParseStatusFailed, types.ParseStatusCancelled, types.ParseStatusDeleting,
		}, 3, 6},
	}
	for _, entry := range []string{"list", "list-by-tenant", "fill"} {
		for _, kbType := range []string{types.KnowledgeBaseTypeDocument, types.KnowledgeBaseTypeFAQ} {
			for _, tc := range cases {
				t.Run(entry+"/"+kbType+"/"+tc.name, func(t *testing.T) {
					svc, kb := newKBCountFixture(t, kbType, tc.statuses, 0)
					kb.ProcessingCount, kb.IsProcessing = 99, true
					runKBCountEntry(t, entry, svc, kb)
					assert.Equal(t, tc.processing, kb.ProcessingCount)
					assert.Equal(t, tc.processing > 0, kb.IsProcessing)
					if kbType == types.KnowledgeBaseTypeDocument {
						assert.Equal(t, tc.knowledge, kb.KnowledgeCount)
					}
				})
			}
		}
	}
}

type failingKBChunkCounter struct {
	interfaces.ChunkRepository
	calls int
}

func (r *failingKBChunkCounter) CountChunksByKnowledgeBaseID(context.Context, uint64, string) (int64, error) {
	r.calls++
	return 0, errors.New("synthetic count failure")
}

func TestKnowledgeBaseCountsContinueAfterChunkCountFailure(t *testing.T) {
	for _, entry := range []string{"list", "list-by-tenant", "fill"} {
		t.Run(entry, func(t *testing.T) {
			svc, kb := newKBCountFixture(t, types.KnowledgeBaseTypeDocument, []string{types.ParseStatusFinalizing}, 0)
			counter := &failingKBChunkCounter{}
			svc.chunkRepo = counter
			kb.ChunkCount = 41
			runKBCountEntry(t, entry, svc, kb)
			assert.Equal(t, 1, counter.calls)
			assert.Equal(t, int64(41), kb.ChunkCount)
			assert.Equal(t, int64(1), kb.KnowledgeCount)
			assert.Equal(t, int64(1), kb.ProcessingCount)
			assert.True(t, kb.IsProcessing)
		})
	}
}

func TestFillKnowledgeBaseCountsNil(t *testing.T) {
	require.NoError(t, (&knowledgeBaseService{}).FillKnowledgeBaseCounts(context.Background(), nil))
}
