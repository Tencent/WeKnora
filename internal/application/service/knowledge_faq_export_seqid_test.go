package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// faqExportKBLookup serves a single FAQ knowledge base without a real KB store.
type faqExportKBLookup struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (r *faqExportKBLookup) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if r.kb == nil || id != r.kb.ID {
		return nil, repository.ErrKnowledgeBaseNotFound
	}
	return r.kb, nil
}

// TestExportFAQEntriesJSONRoundTripPreservesSeqID 覆盖"导出 → 编辑 → 重新导入"闭环：
// 导出的 id 必须来自 chunk.seq_id，否则恒为 0，而导入端只在 id > 0 时才恢复 seq_id，
// 往返就会给每条 FAQ 换一个新 id。
func TestExportFAQEntriesJSONRoundTripPreservesSeqID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "faq-export.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.Chunk{}, &types.KnowledgeTag{}))

	const (
		tenantID  = uint64(7)
		kbID      = "kb-faq"
		faqKnowID = "faq-knowledge"
	)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	require.NoError(t, db.Create(&types.Knowledge{
		ID:              faqKnowID,
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		Type:            types.KnowledgeTypeFAQ,
	}).Error)

	seqIDs := []int64{1001, 1002}
	chunks := make([]*types.Chunk, 0, len(seqIDs))
	for i, seqID := range seqIDs {
		chunk := &types.Chunk{
			ID:              fmt.Sprintf("faq-chunk-%d", i),
			TenantID:        tenantID,
			KnowledgeID:     faqKnowID,
			KnowledgeBaseID: kbID,
			ChunkType:       types.ChunkTypeFAQ,
			Status:          int(types.ChunkStatusIndexed),
			IsEnabled:       true,
			SeqID:           seqID,
		}
		require.NoError(t, chunk.SetFAQMetadata(&types.FAQChunkMetadata{
			StandardQuestion: fmt.Sprintf("问题-%d", seqID),
			Answers:          []string{fmt.Sprintf("回答-%d", seqID)},
		}))
		chunks = append(chunks, chunk)
	}
	require.NoError(t, repository.NewChunkRepository(db).CreateChunks(ctx, chunks))

	svc := &knowledgeService{
		repo:      repository.NewKnowledgeRepository(db),
		chunkRepo: repository.NewChunkRepository(db),
		tagRepo:   repository.NewKnowledgeTagRepository(db),
		kbService: &faqExportKBLookup{kb: &types.KnowledgeBase{
			ID:       kbID,
			TenantID: tenantID,
			Type:     types.KnowledgeBaseTypeFAQ,
		}},
	}

	raw, err := svc.ExportFAQEntriesJSON(ctx, kbID)
	require.NoError(t, err)

	var entries []types.FAQExportEntry
	require.NoError(t, json.Unmarshal(raw, &entries))
	require.Len(t, entries, len(seqIDs))

	exportedIDs := make([]int64, 0, len(entries))
	for _, entry := range entries {
		// 投影漏掉 seq_id 时这里恒为 0，导入端会当成"未指定 id"而分配新的 seq_id。
		require.NotZero(t, entry.ID, "导出的 id 恒为 0：查询投影没有选中 seq_id")
		require.Equal(t, fmt.Sprintf("问题-%d", entry.ID), entry.StandardQuestion,
			"导出条目的 id 与条目内容不匹配")
		exportedIDs = append(exportedIDs, entry.ID)
	}
	require.ElementsMatch(t, seqIDs, exportedIDs)
}
