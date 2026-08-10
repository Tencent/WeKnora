package service

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// chessKBName là tên KB chuyên dụng chứa tri thức cờ (tự tạo mỗi tenant).
const chessKBName = "Tri thức cờ vua"

// ChessKnowledgeIndexer đồng bộ đối tượng cờ (ván/thế cờ/bài giảng) thành bản ghi
// Knowledge trong "KB tri thức cờ vua" để trợ lý/HLV truy hồi qua RAG.
//
// AN TOÀN:
//   - Gate sau env CHESS_KB_INDEX (mặc định TẮT). Tắt → mọi hàm là no-op.
//   - Mọi thao tác BEST-EFFORT: lỗi chỉ log, KHÔNG bao giờ chặn CRUD đối tượng cờ.
//   - KB cờ được tạo bằng cách SAO CHÉP cấu hình embedding của một KB sẵn có của
//     tenant (cần ≥1 KB đã cấu hình model). Chưa có → bỏ qua index (log).
//
// LƯU Ý: tính năng này cần verify trên full stack (model embedding + vector store
// + worker async) trước khi bật thật.
type ChessKnowledgeIndexer struct {
	kbService        interfaces.KnowledgeBaseService
	knowledgeService interfaces.KnowledgeService
	idxRepo          interfaces.ChessKBIndexRepository
}

// NewChessKnowledgeIndexer tạo indexer tri thức cờ.
func NewChessKnowledgeIndexer(
	kbService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	idxRepo interfaces.ChessKBIndexRepository,
) *ChessKnowledgeIndexer {
	return &ChessKnowledgeIndexer{kbService: kbService, knowledgeService: knowledgeService, idxRepo: idxRepo}
}

// Enabled cho biết có bật index tri thức cờ không (env CHESS_KB_INDEX truthy).
func (ix *ChessKnowledgeIndexer) Enabled() bool {
	if ix == nil || ix.kbService == nil || ix.knowledgeService == nil || ix.idxRepo == nil {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CHESS_KB_INDEX")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// IndexGame / IndexPuzzle / IndexLesson đồng bộ một đối tượng cờ (tạo/cập nhật).
// Trả error để caller ĐẾM ĐƯỢC kết quả (vd ReindexAll); caller CRUD vẫn best-effort
// (bỏ qua lỗi để không chặn thao tác). No-op khi gate tắt → trả nil.
func (ix *ChessKnowledgeIndexer) IndexGame(ctx context.Context, g *types.ChessGame) error {
	if !ix.Enabled() || g == nil || g.Slug == "" {
		return nil
	}
	title, content := buildGameKnowledgeText(g)
	return ix.upsert(ctx, g.TenantID, types.ChessRefTypeGame, g.Slug, title, content)
}

func (ix *ChessKnowledgeIndexer) IndexPuzzle(ctx context.Context, p *types.ChessPuzzle) error {
	if !ix.Enabled() || p == nil || p.Slug == "" {
		return nil
	}
	title, content := buildPuzzleKnowledgeText(p)
	return ix.upsert(ctx, p.TenantID, types.ChessRefTypePuzzle, p.Slug, title, content)
}

func (ix *ChessKnowledgeIndexer) IndexLesson(ctx context.Context, l *types.ChessLesson) error {
	if !ix.Enabled() || l == nil || l.Slug == "" {
		return nil
	}
	title, content := buildLessonKnowledgeText(l)
	return ix.upsert(ctx, l.TenantID, types.ChessRefTypeLesson, l.Slug, title, content)
}

func (ix *ChessKnowledgeIndexer) IndexPosition(ctx context.Context, p *types.ChessPosition) error {
	if !ix.Enabled() || p == nil || p.Slug == "" {
		return nil
	}
	title, content := buildPositionKnowledgeText(p)
	return ix.upsert(ctx, p.TenantID, types.ChessRefTypePosition, p.Slug, title, content)
}

// IndexBook chỉ index sách ở trạng thái "published" — bản thảo (draft) KHÔNG
// được đưa vào KB tri thức cờ để agent không trích dẫn nội dung chưa duyệt.
// chapters (tùy chọn) dựng mục lục trong nội dung index; truyền nil nếu caller
// không có sẵn (indexer không tự truy vấn DB chương).
func (ix *ChessKnowledgeIndexer) IndexBook(ctx context.Context, b *types.ChessBook, chapters []*types.ChessBookChapter) error {
	if !ix.Enabled() || b == nil || b.Slug == "" || b.Status != types.ChessBookStatusPublished {
		return nil
	}
	title, content := buildBookKnowledgeText(b, chapters)
	return ix.upsert(ctx, b.TenantID, types.ChessRefTypeBook, b.Slug, title, content)
}

// IndexChapter index một chương — CHỈ khi sách chứa nó đã "published" (cùng
// điều kiện IndexBook). Nhận cả book để lấy tiêu đề sách + kiểm tra Status mà
// không phải truy vấn lại DB trong indexer.
func (ix *ChessKnowledgeIndexer) IndexChapter(ctx context.Context, book *types.ChessBook, ch *types.ChessBookChapter) error {
	if !ix.Enabled() || book == nil || ch == nil || ch.Slug == "" || book.Status != types.ChessBookStatusPublished {
		return nil
	}
	title, content := buildChapterKnowledgeText(book, ch)
	return ix.upsert(ctx, ch.TenantID, types.ChessRefTypeChapter, ch.Slug, title, content)
}

// Remove xóa bản ghi Knowledge tương ứng (khi đối tượng cờ bị xóa).
func (ix *ChessKnowledgeIndexer) Remove(ctx context.Context, tenantID uint64, chessType, slug string) {
	if !ix.Enabled() || slug == "" {
		return
	}
	existing, err := ix.idxRepo.Get(ctx, tenantID, chessType, slug)
	if err != nil || existing == nil {
		return
	}
	if existing.KnowledgeID != "" {
		if err := ix.knowledgeService.DeleteKnowledge(ctx, existing.KnowledgeID); err != nil {
			logger.Warnf(ctx, "chess index: xóa knowledge %s thất bại: %v", existing.KnowledgeID, err)
		}
	}
	if err := ix.idxRepo.Delete(ctx, tenantID, chessType, slug); err != nil {
		logger.Warnf(ctx, "chess index: xóa mapping %s/%s thất bại: %v", chessType, slug, err)
	}
}

// upsert tạo mới hoặc cập nhật bản ghi Knowledge cho một đối tượng cờ. Vẫn log lỗi
// (best-effort cho caller CRUD) NHƯNG trả error để caller cần đếm (ReindexAll) biết.
func (ix *ChessKnowledgeIndexer) upsert(ctx context.Context, tenantID uint64, chessType, slug, title, content string) error {
	payload := &types.ManualKnowledgePayload{
		Title:   title,
		Content: content,
		Status:  types.ManualKnowledgeStatusPublish,
		Channel: "chess",
	}
	existing, _ := ix.idxRepo.Get(ctx, tenantID, chessType, slug)
	if existing != nil && existing.KnowledgeID != "" {
		// Mapping cũ chỉ dùng được khi knowledge còn SỐNG. Nếu knowledge đã bị xóa
		// (vd KB cờ bị xóa để tạo lại theo runbook) → mapping MỒ CÔI: UpdateManualKnowledge
		// sẽ báo "knowledge not found". Khi đó gỡ mapping mồ côi rồi rơi xuống nhánh TẠO MỚI.
		if _, gerr := ix.knowledgeService.GetKnowledgeByID(ctx, existing.KnowledgeID); gerr == nil {
			if _, err := ix.knowledgeService.UpdateManualKnowledge(ctx, existing.KnowledgeID, payload); err != nil {
				logger.Warnf(ctx, "chess index: cập nhật knowledge cho %s/%s thất bại: %v", chessType, slug, err)
				return err
			}
			return nil
		}
		if err := ix.idxRepo.Delete(ctx, tenantID, chessType, slug); err != nil {
			logger.Warnf(ctx, "chess index: gỡ mapping mồ côi %s/%s thất bại: %v", chessType, slug, err)
		}
	}
	kb, err := ix.ensureChessKB(ctx)
	if err != nil || kb == nil {
		if err == nil {
			err = fmt.Errorf("KB cờ không khả dụng")
		}
		logger.Warnf(ctx, "chess index: không có KB cờ để index %s/%s: %v", chessType, slug, err)
		return err
	}
	k, err := ix.knowledgeService.CreateKnowledgeFromManual(ctx, kb.ID, payload, "chess")
	if err != nil || k == nil {
		if err == nil {
			err = fmt.Errorf("tạo knowledge thất bại")
		}
		logger.Warnf(ctx, "chess index: tạo knowledge cho %s/%s thất bại: %v", chessType, slug, err)
		return err
	}
	if err := ix.idxRepo.Upsert(ctx, &types.ChessKBIndex{
		TenantID: tenantID, ChessType: chessType, ChessSlug: slug, KnowledgeID: k.ID, KBID: kb.ID,
	}); err != nil {
		logger.Warnf(ctx, "chess index: lưu mapping %s/%s thất bại: %v", chessType, slug, err)
		return err
	}
	return nil
}

// IndexStatus đọc trạng thái KB "Tri thức cờ vua" để chẩn đoán RAG (KHÔNG tạo KB).
// Tenant lấy từ ctx (như các lời gọi kbService/knowledgeService khác).
func (ix *ChessKnowledgeIndexer) IndexStatus(ctx context.Context) (*types.ChessIndexStatus, error) {
	st := &types.ChessIndexStatus{}
	if ix == nil || ix.kbService == nil || ix.knowledgeService == nil {
		return st, nil
	}
	st.Enabled = ix.Enabled()
	kbs, err := ix.kbService.ListKnowledgeBases(ctx)
	if err != nil {
		return st, err
	}
	var chessKB *types.KnowledgeBase
	for _, kb := range kbs {
		if kb.Name == chessKBName {
			chessKB = kb
			break
		}
	}
	if chessKB == nil {
		return st, nil // KB cờ chưa được tạo (chưa index lần nào / chưa có KB embedding mẫu)
	}
	st.KBExists = true
	st.KBID = chessKB.ID
	st.EmbeddingModelID = chessKB.EmbeddingModelID
	st.EmbeddingConfigured = strings.TrimSpace(chessKB.EmbeddingModelID) != ""
	st.VectorEnabled = chessKB.IsVectorEnabled()
	st.KeywordEnabled = chessKB.IsKeywordEnabled()
	st.Searchable = st.VectorEnabled || st.KeywordEnabled

	ks, err := ix.knowledgeService.ListKnowledgeByKnowledgeBaseID(ctx, chessKB.ID)
	if err != nil {
		return st, err
	}
	st.Total = len(ks)
	for _, k := range ks {
		switch k.ParseStatus {
		case types.ParseStatusCompleted:
			st.Completed++
		case types.ParseStatusFailed:
			st.Failed++
			if st.SampleError == "" && strings.TrimSpace(k.ErrorMessage) != "" {
				st.SampleError = k.ErrorMessage
			}
		default: // pending / processing / finalizing / rỗng
			st.Pending++
		}
		if k.EnableStatus == "enabled" {
			st.EnabledDocs++
		} else {
			st.DisabledDocs++
		}
	}
	return st, nil
}

// ensureChessKB tìm KB cờ của tenant; chưa có thì tạo bằng cách SAO CHÉP cấu hình
// embedding/vector store của một KB sẵn có (cần ≥1 KB đã cấu hình model).
func (ix *ChessKnowledgeIndexer) ensureChessKB(ctx context.Context) (*types.KnowledgeBase, error) {
	kbs, err := ix.kbService.ListKnowledgeBases(ctx)
	if err != nil {
		return nil, err
	}
	var tpl *types.KnowledgeBase
	for _, kb := range kbs {
		if kb.Name == chessKBName {
			// KB cờ đã tồn tại. Nếu vô tình TẮT cả vector lẫn keyword thì
			// knowledge_search KHÔNG "nhìn thấy" KB (capability filter) và embedding
			// bị skip lúc index → RAG rỗng. Cảnh báo rõ để vận hành xử lý (xóa KB cờ
			// rồi reindex để tạo lại đúng cấu hình).
			if !kb.IsVectorEnabled() && !kb.IsKeywordEnabled() {
				logger.Warnf(ctx, "chess index: KB %q ĐANG TẮT cả vector lẫn keyword → "+
					"RAG không truy hồi được; XÓA KB này rồi gọi reindex để tạo lại", chessKBName)
			}
			return kb, nil
		}
		if tpl == nil && kb.EmbeddingModelID != "" {
			tpl = kb
		}
	}
	if tpl == nil {
		return nil, fmt.Errorf("chưa có KB cấu hình embedding để sao chép")
	}
	// Chỉ sao chép các trường cần cho embedding/lưu trữ; bỏ wiki/faq/extract để KB
	// cờ là KB tài liệu thuần. Set TƯỜNG MINH IndexingStrategy (vector+keyword bật)
	// — KHÔNG phụ thuộc EnsureDefaults — để KB cờ LUÔN truy hồi được qua knowledge_search.
	nk := &types.KnowledgeBase{
		Name:                  chessKBName,
		Type:                  "document",
		Description:           "Kho tri thức tự động từ thư viện cờ vua (ván/thế cờ/bài giảng) để trợ lý truy hồi.",
		ChunkingConfig:        tpl.ChunkingConfig,
		ImageProcessingConfig: tpl.ImageProcessingConfig,
		EmbeddingModelID:      tpl.EmbeddingModelID,
		SummaryModelID:        tpl.SummaryModelID,
		VLMConfig:             tpl.VLMConfig,
		ASRConfig:             tpl.ASRConfig,
		StorageProviderConfig: tpl.StorageProviderConfig,
		VectorStoreID:         tpl.VectorStoreID,
		IndexingStrategy:      types.DefaultIndexingStrategy(),
	}
	return ix.kbService.CreateKnowledgeBase(ctx, nk)
}
