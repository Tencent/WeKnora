package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 这个文件真的跑导入：用 SQLite + 真实 repository 走 executeFAQImport，
// 覆盖"导出 → 编辑 → 重新导入"的两条路径（append 到第二个 KB、replace 回同一个
// KB）。chunks.seq_id 是全局唯一索引，gorm 软删的行仍然占着索引值，所以把导出
// payload 里的 seq_id 原样插回去会直接撞唯一约束。

const (
	faqRoundTripTenantID = uint64(7)
	faqRoundTripKBAID    = "kb-faq-roundtrip-a"
	faqRoundTripKBBID    = "kb-faq-roundtrip-b"
)

// ---------------------------------------------------------------------------
// 测试替身
// ---------------------------------------------------------------------------

type faqRoundTripKBService struct {
	interfaces.KnowledgeBaseService
	kbs map[string]*types.KnowledgeBase
}

func (s *faqRoundTripKBService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if kb, ok := s.kbs[id]; ok && kb != nil {
		return kb, nil
	}
	return nil, repository.ErrKnowledgeBaseNotFound
}

// faqRoundTripTagService 只发一个合成标签，导入路径不需要标签落库。
type faqRoundTripTagService struct {
	interfaces.KnowledgeTagService
}

func (faqRoundTripTagService) FindOrCreateTagByName(_ context.Context, kbID, name string) (*types.KnowledgeTag, error) {
	return &types.KnowledgeTag{ID: "tag-untagged", KnowledgeBaseID: kbID, Name: name}, nil
}

type faqRoundTripEmbedder struct{}

func (faqRoundTripEmbedder) Embed(context.Context, string) ([]float32, error) { return nil, nil }
func (faqRoundTripEmbedder) BatchEmbed(context.Context, []string) ([][]float32, error) {
	return nil, nil
}
func (faqRoundTripEmbedder) GetModelName() string { return "roundtrip-embedder" }
func (faqRoundTripEmbedder) GetDimensions() int   { return 8 }
func (faqRoundTripEmbedder) GetModelID() string   { return "roundtrip-embedder" }
func (faqRoundTripEmbedder) BatchEmbedWithPool(
	_ context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	return make([][]float32, len(texts)), nil
}

type faqRoundTripModelService struct {
	interfaces.ModelService
}

func (faqRoundTripModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return faqRoundTripEmbedder{}, nil
}

// faqRoundTripChunkService 把写入落到真实 repository，唯一索引才真的生效。
type faqRoundTripChunkService struct {
	interfaces.ChunkService
	repo interfaces.ChunkRepository
}

func (s faqRoundTripChunkService) CreateChunks(ctx context.Context, chunks []*types.Chunk) error {
	return s.repo.CreateChunks(ctx, chunks)
}

// faqRoundTripEngine 是不落任何索引的空引擎。
type faqRoundTripEngine struct {
	interfaces.RetrieveEngineService
}

func (faqRoundTripEngine) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (faqRoundTripEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (faqRoundTripEngine) BatchIndex(
	context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType,
) error {
	return nil
}

func (faqRoundTripEngine) EstimateStorageSize(
	context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType,
) int64 {
	return 0
}

func (faqRoundTripEngine) DeleteByChunkIDList(context.Context, []string, int, string) error {
	return nil
}

type faqRoundTripRegistry struct {
	interfaces.RetrieveEngineRegistry
}

func (faqRoundTripRegistry) GetRetrieveEngineService(
	types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return faqRoundTripEngine{}, nil
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type faqRoundTripHarness struct {
	svc       *knowledgeService
	chunkRepo interfaces.ChunkRepository
	ctx       context.Context
	kbs       map[string]*types.KnowledgeBase
}

func newFAQRoundTripHarness(t *testing.T) *faqRoundTripHarness {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "faq-roundtrip.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.Chunk{}, &types.KnowledgeTag{}))

	tenant := &types.Tenant{
		ID: faqRoundTripTenantID,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
			{RetrieverEngineType: types.PostgresRetrieverEngineType, RetrieverType: types.VectorRetrieverType},
		}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, faqRoundTripTenantID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	kbs := map[string]*types.KnowledgeBase{
		faqRoundTripKBAID: {
			ID: faqRoundTripKBAID, TenantID: faqRoundTripTenantID, Name: "FAQ A", Type: types.KnowledgeBaseTypeFAQ,
		},
		faqRoundTripKBBID: {
			ID: faqRoundTripKBBID, TenantID: faqRoundTripTenantID, Name: "FAQ B", Type: types.KnowledgeBaseTypeFAQ,
		},
	}

	chunkRepo := repository.NewChunkRepository(db)
	svc := &knowledgeService{
		repo:           repository.NewKnowledgeRepository(db),
		kbService:      &faqRoundTripKBService{kbs: kbs},
		chunkService:   faqRoundTripChunkService{repo: chunkRepo},
		chunkRepo:      chunkRepo,
		tagRepo:        repository.NewKnowledgeTagRepository(db),
		tagService:     faqRoundTripTagService{},
		modelService:   faqRoundTripModelService{},
		retrieveEngine: faqRoundTripRegistry{},
	}

	return &faqRoundTripHarness{svc: svc, chunkRepo: chunkRepo, ctx: ctx, kbs: kbs}
}

// ctxForKB 授予目标 KB 的 task write（WithKBTaskWrite 每次只发一个 KB 的授权）。
func (h *faqRoundTripHarness) ctxForKB(t *testing.T, kbID string) context.Context {
	t.Helper()
	ctx, err := access.WithKBTaskWrite(h.ctx, h.kbs[kbID], faqRoundTripTenantID)
	require.NoError(t, err)
	return ctx
}

// seedKB 写入带已知 seq_id 的 FAQ 条目，返回每个条目的 seq_id。
func (h *faqRoundTripHarness) seedKB(t *testing.T, kbID string, seqIDs []int64) {
	t.Helper()
	ctx := h.ctxForKB(t, kbID)

	knowledge := &types.Knowledge{
		ID:              "faq-knowledge-" + kbID,
		TenantID:        faqRoundTripTenantID,
		KnowledgeBaseID: kbID,
		Type:            types.KnowledgeTypeFAQ,
	}
	require.NoError(t, h.svc.repo.CreateKnowledge(ctx, knowledge))

	chunks := make([]*types.Chunk, 0, len(seqIDs))
	for i, seqID := range seqIDs {
		chunk := &types.Chunk{
			ID:              fmt.Sprintf("%s-chunk-%d", kbID, i),
			TenantID:        faqRoundTripTenantID,
			KnowledgeID:     knowledge.ID,
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
	require.NoError(t, h.chunkRepo.CreateChunks(ctx, chunks))
}

// exportEntries 走真实导出路径拿条目，并断言导出带的是真实 seq_id。
func (h *faqRoundTripHarness) exportEntries(t *testing.T, kbID string) []types.FAQEntryPayload {
	t.Helper()
	raw, err := h.svc.ExportFAQEntriesJSON(h.ctxForKB(t, kbID), kbID)
	require.NoError(t, err)

	var exported []types.FAQExportEntry
	require.NoError(t, json.Unmarshal(raw, &exported))
	require.NotEmpty(t, exported)
	for _, entry := range exported {
		require.NotZero(t, entry.ID, "导出的 id 恒为 0：查询投影没有选中 seq_id")
	}

	payload, err := json.Marshal(exported)
	require.NoError(t, err)
	var entries []types.FAQEntryPayload
	require.NoError(t, json.Unmarshal(payload, &entries))
	return entries
}

// importEntries 真的执行一次导入。
func (h *faqRoundTripHarness) importEntries(
	t *testing.T, kbID, taskID, mode string, entries []types.FAQEntryPayload,
) (*types.FAQImportProgress, error) {
	t.Helper()
	progress := &types.FAQImportProgress{
		TaskID: taskID,
		KBID:   kbID,
		Total:  len(entries),
	}
	err := h.svc.executeFAQImport(
		h.ctxForKB(t, kbID),
		taskID,
		kbID,
		&types.FAQBatchUpsertPayload{
			Entries:     entries,
			Mode:        mode,
			KnowledgeID: "faq-knowledge-" + kbID,
			TaskID:      taskID,
		},
		faqRoundTripTenantID,
		0,
		progress,
	)
	return progress, err
}

// ---------------------------------------------------------------------------
// 测试
// ---------------------------------------------------------------------------

func TestFAQExportImportRoundTripReassignsTakenSeqIDs(t *testing.T) {
	seeded := []int64{1001, 1002}

	t.Run("append into a second knowledge base", func(t *testing.T) {
		h := newFAQRoundTripHarness(t)
		h.seedKB(t, faqRoundTripKBAID, seeded)
		entries := h.exportEntries(t, faqRoundTripKBAID)

		progress, err := h.importEntries(t, faqRoundTripKBBID, "task-append", types.FAQBatchModeAppend, entries)
		require.NoError(t, err, "append 到第二个 KB 不该撞 chunks.seq_id 唯一索引")
		require.Equal(t, len(seeded), remappedSeqIDCount(t, progress),
			"原 KB 的行还占着 seq_id，导入应给这些条目重新分配 id 并在进度里计数")

		// 原 KB 的条目必须原样保留
		h.assertKBEntries(t, faqRoundTripKBAID, seeded)

		// 目标 KB 的条目一条都不能少，id 必须换掉且全局唯一
		got := h.assertKBEntries(t, faqRoundTripKBBID, nil)
		for _, seqID := range seeded {
			require.NotContains(t, got, seqID, "目标 KB 不该复用原 KB 仍占用的 seq_id %d", seqID)
		}
		require.ElementsMatch(t, []string{"问题-1001", "问题-1002"}, valuesOf(got))
	})

	t.Run("replace back into the same knowledge base after an edit", func(t *testing.T) {
		h := newFAQRoundTripHarness(t)
		h.seedKB(t, faqRoundTripKBAID, seeded)
		entries := h.exportEntries(t, faqRoundTripKBAID)

		// 导出 → 编辑：改答案，id 不变。replace 会软删旧行再按原 id 重新插入。
		for i := range entries {
			entries[i].Answers = append(entries[i].Answers, "编辑后的答案")
		}

		progress, err := h.importEntries(t, faqRoundTripKBAID, "task-replace", types.FAQBatchModeReplace, entries)
		require.NoError(t, err, "replace 回同一个 KB 不该撞软删行占着的 chunks.seq_id")
		require.Equal(t, len(seeded), remappedSeqIDCount(t, progress),
			"旧行软删后仍占 seq_id，导入应给这些条目重新分配 id 并在进度里计数")

		got := h.assertKBEntries(t, faqRoundTripKBAID, nil)
		require.ElementsMatch(t, []string{"问题-1001", "问题-1002"}, valuesOf(got))
		for _, seqID := range seeded {
			require.NotContains(t, got, seqID, "软删行仍占着 seq_id %d，不该复用", seqID)
		}
	})
}

// assertKBEntries 校验该 KB 下未删除的 FAQ 条目，返回 seq_id -> 标准问。
// expectedSeqIDs 非空时要求完全一致。
func (h *faqRoundTripHarness) assertKBEntries(t *testing.T, kbID string, expectedSeqIDs []int64) map[int64]string {
	t.Helper()
	ctx := h.ctxForKB(t, kbID)
	knowledges, err := h.svc.repo.ListKnowledgeByKnowledgeBaseID(ctx, faqRoundTripTenantID, kbID)
	require.NoError(t, err)
	var knowledge *types.Knowledge
	for _, candidate := range knowledges {
		if candidate.Type == types.KnowledgeTypeFAQ {
			knowledge = candidate
			break
		}
	}
	require.NotNil(t, knowledge, "%s 应该有 FAQ knowledge", kbID)

	all, err := h.chunkRepo.ListAllChunksByKnowledgeID(ctx, faqRoundTripTenantID, knowledge.ID)
	require.NoError(t, err)

	got := make(map[int64]string, len(all))
	for _, chunk := range all {
		if chunk.ChunkType != types.ChunkTypeFAQ {
			continue
		}
		require.NotContains(t, got, chunk.SeqID, "seq_id %d 在 %s 里重复", chunk.SeqID, kbID)
		meta, err := chunk.FAQMetadata()
		require.NoError(t, err)
		require.NotNil(t, meta)
		got[chunk.SeqID] = meta.StandardQuestion
	}

	if expectedSeqIDs != nil {
		require.Len(t, got, len(expectedSeqIDs))
		for _, seqID := range expectedSeqIDs {
			require.Contains(t, got, seqID)
		}
	}
	return got
}

func valuesOf(m map[int64]string) []string {
	values := make([]string, 0, len(m))
	for _, v := range m {
		values = append(values, v)
	}
	return values
}

// remappedSeqIDCount 从进度的 JSON 形态里读重分配计数。进度本身就是以 JSON 存
// Redis 并返回给前端的，这里是调用方真正看到的契约；用 JSON 而不是 Go 字段，也让
// 反向验证（回退源码保留测试）仍然能编译、真的跑到行为断言上。
func remappedSeqIDCount(t *testing.T, progress *types.FAQImportProgress) int {
	t.Helper()
	raw, err := json.Marshal(progress)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	count, ok := decoded["seq_id_remapped_count"]
	require.True(t, ok, "导入进度应带 seq_id_remapped_count 字段，告诉调用方 id 变了")
	value, ok := count.(float64)
	require.True(t, ok, "seq_id_remapped_count 应为数字")
	return int(value)
}
