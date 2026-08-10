package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// wikiChessRefRepository lưu bảng liên kết wiki -> đối tượng cờ trên GORM.
type wikiChessRefRepository struct {
	db *gorm.DB
}

// NewWikiChessRefRepository tạo repository liên kết wiki -> cờ.
func NewWikiChessRefRepository(db *gorm.DB) interfaces.WikiChessRefRepository {
	return &wikiChessRefRepository{db: db}
}

// ReplaceForPage xóa toàn bộ tham chiếu cũ của trang rồi chèn lại danh sách mới
// trong một transaction (đồng bộ khi tạo/cập nhật trang wiki).
func (r *wikiChessRefRepository) ReplaceForPage(
	ctx context.Context, tenantID uint64, kbID, pageSlug string, refs []types.WikiChessRef,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("source_type = ? AND kb_id = ? AND page_slug = ?",
			types.ChessRefSourceWiki, kbID, pageSlug).
			Delete(&types.WikiChessRef{}).Error; err != nil {
			return err
		}
		if len(refs) == 0 {
			return nil
		}
		for i := range refs {
			refs[i].SourceType = types.ChessRefSourceWiki
		}
		return tx.Create(&refs).Error
	})
}

// ReplaceForLesson đồng bộ tham chiếu cờ TỪ nội dung một bài giảng (nguồn lesson;
// kb_id rỗng, page_slug = slug bài giảng).
func (r *wikiChessRefRepository) ReplaceForLesson(
	ctx context.Context, tenantID uint64, lessonSlug string, refs []types.WikiChessRef,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("source_type = ? AND tenant_id = ? AND page_slug = ?",
			types.ChessRefSourceLesson, tenantID, lessonSlug).
			Delete(&types.WikiChessRef{}).Error; err != nil {
			return err
		}
		if len(refs) == 0 {
			return nil
		}
		for i := range refs {
			refs[i].SourceType = types.ChessRefSourceLesson
			refs[i].KBID = ""
		}
		return tx.Create(&refs).Error
	})
}

// ReplaceForPosition đồng bộ tham chiếu cờ TỪ chú giải một thế cờ (nguồn
// position; kb_id rỗng, page_slug = slug thế cờ). Xóa theo tenant_id +
// page_slug — CÙNG mẫu ReplaceForLesson (không phải ReplaceForPage): thế cờ
// thuộc tenant, không thuộc KB.
func (r *wikiChessRefRepository) ReplaceForPosition(
	ctx context.Context, tenantID uint64, positionSlug string, refs []types.WikiChessRef,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("source_type = ? AND tenant_id = ? AND page_slug = ?",
			types.ChessRefSourcePosition, tenantID, positionSlug).
			Delete(&types.WikiChessRef{}).Error; err != nil {
			return err
		}
		if len(refs) == 0 {
			return nil
		}
		for i := range refs {
			refs[i].SourceType = types.ChessRefSourcePosition
			refs[i].KBID = ""
		}
		return tx.Create(&refs).Error
	})
}

// ReplaceForChapter đồng bộ tham chiếu cờ TỪ nội dung một chương sách (nguồn
// chapter; kb_id rỗng, page_slug = slug chương). Cùng mẫu ReplaceForLesson
// (không phải ReplaceForPage): chương thuộc tenant, không thuộc KB.
func (r *wikiChessRefRepository) ReplaceForChapter(
	ctx context.Context, tenantID uint64, chapterSlug string, refs []types.WikiChessRef,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("source_type = ? AND tenant_id = ? AND page_slug = ?",
			types.ChessRefSourceChapter, tenantID, chapterSlug).
			Delete(&types.WikiChessRef{}).Error; err != nil {
			return err
		}
		if len(refs) == 0 {
			return nil
		}
		for i := range refs {
			refs[i].SourceType = types.ChessRefSourceChapter
			refs[i].KBID = ""
		}
		return tx.Create(&refs).Error
	})
}

func (r *wikiChessRefRepository) DeleteForPage(ctx context.Context, kbID, pageSlug string) error {
	return r.db.WithContext(ctx).
		Where("source_type = ? AND kb_id = ? AND page_slug = ?", types.ChessRefSourceWiki, kbID, pageSlug).
		Delete(&types.WikiChessRef{}).Error
}

// DeleteForLesson xóa mọi tham chiếu cờ TỪ một bài giảng (khi xóa bài giảng).
func (r *wikiChessRefRepository) DeleteForLesson(ctx context.Context, tenantID uint64, lessonSlug string) error {
	return r.db.WithContext(ctx).
		Where("source_type = ? AND tenant_id = ? AND page_slug = ?", types.ChessRefSourceLesson, tenantID, lessonSlug).
		Delete(&types.WikiChessRef{}).Error
}

// DeleteForPosition xóa mọi tham chiếu cờ TỪ một thế cờ (khi xóa thế cờ).
func (r *wikiChessRefRepository) DeleteForPosition(ctx context.Context, tenantID uint64, positionSlug string) error {
	return r.db.WithContext(ctx).
		Where("source_type = ? AND tenant_id = ? AND page_slug = ?", types.ChessRefSourcePosition, tenantID, positionSlug).
		Delete(&types.WikiChessRef{}).Error
}

// DeleteForChapter xóa mọi tham chiếu cờ TỪ một chương sách (khi xóa chương).
func (r *wikiChessRefRepository) DeleteForChapter(ctx context.Context, tenantID uint64, chapterSlug string) error {
	return r.db.WithContext(ctx).
		Where("source_type = ? AND tenant_id = ? AND page_slug = ?", types.ChessRefSourceChapter, tenantID, chapterSlug).
		Delete(&types.WikiChessRef{}).Error
}

func (r *wikiChessRefRepository) DeleteForChess(ctx context.Context, tenantID uint64, chessType, chessSlug string) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND chess_type = ? AND chess_slug = ?", tenantID, chessType, chessSlug).
		Delete(&types.WikiChessRef{}).Error
}

// ListBacklinks gộp nguồn TRANG WIKI (join wiki_pages, bỏ trang xóa mềm) và
// nguồn BÀI GIẢNG (join chess_lessons) đang trỏ tới đối tượng cờ.
func (r *wikiChessRefRepository) ListBacklinks(
	ctx context.Context, tenantID uint64, chessType, chessSlug string,
) ([]types.ChessBacklink, error) {
	var wiki []types.ChessBacklink
	if err := r.db.WithContext(ctx).
		Table("wiki_chess_refs AS r").
		Select("'wiki' AS source_type, r.kb_id AS kb_id, r.page_slug AS page_slug, COALESCE(p.title, '') AS page_title").
		Joins("LEFT JOIN wiki_pages AS p ON p.knowledge_base_id = r.kb_id AND p.slug = r.page_slug AND p.deleted_at IS NULL").
		Where("r.source_type = ? AND r.tenant_id = ? AND r.chess_type = ? AND r.chess_slug = ?",
			types.ChessRefSourceWiki, tenantID, chessType, chessSlug).
		Order("page_title").
		Scan(&wiki).Error; err != nil {
		return nil, err
	}

	var lesson []types.ChessBacklink
	if err := r.db.WithContext(ctx).
		Table("wiki_chess_refs AS r").
		Select("'lesson' AS source_type, '' AS kb_id, r.page_slug AS page_slug, COALESCE(l.title, '') AS page_title").
		Joins("LEFT JOIN chess_lessons AS l ON l.slug = r.page_slug AND l.tenant_id = r.tenant_id").
		Where("r.source_type = ? AND r.tenant_id = ? AND r.chess_type = ? AND r.chess_slug = ?",
			types.ChessRefSourceLesson, tenantID, chessType, chessSlug).
		Order("page_title").
		Scan(&lesson).Error; err != nil {
		return nil, err
	}

	var position []types.ChessBacklink
	if err := r.db.WithContext(ctx).
		Table("wiki_chess_refs AS r").
		Select("'position' AS source_type, '' AS kb_id, r.page_slug AS page_slug, COALESCE(ps.title, ps.slug, '') AS page_title").
		Joins("LEFT JOIN chess_positions AS ps ON ps.slug = r.page_slug AND ps.tenant_id = r.tenant_id").
		Where("r.source_type = ? AND r.tenant_id = ? AND r.chess_type = ? AND r.chess_slug = ?",
			types.ChessRefSourcePosition, tenantID, chessType, chessSlug).
		Order("page_title").
		Scan(&position).Error; err != nil {
		return nil, err
	}

	// Nguồn CHƯƠNG SÁCH (nội dung chương có thể chứa wikilink).
	var chapter []types.ChessBacklink
	if err := r.db.WithContext(ctx).
		Table("wiki_chess_refs AS r").
		Select("'chapter' AS source_type, '' AS kb_id, r.page_slug AS page_slug, COALESCE(ch.title, ch.slug, '') AS page_title").
		Joins("LEFT JOIN chess_book_chapters AS ch ON ch.slug = r.page_slug AND ch.tenant_id = r.tenant_id").
		Where("r.source_type = ? AND r.tenant_id = ? AND r.chess_type = ? AND r.chess_slug = ?",
			types.ChessRefSourceChapter, tenantID, chessType, chessSlug).
		Order("page_title").
		Scan(&chapter).Error; err != nil {
		return nil, err
	}

	out := append(append(wiki, lesson...), position...)
	return append(out, chapter...), nil
}

func (r *wikiChessRefRepository) ListByKB(ctx context.Context, kbID string) ([]types.WikiChessRef, error) {
	var refs []types.WikiChessRef
	err := r.db.WithContext(ctx).Where("kb_id = ?", kbID).Find(&refs).Error
	return refs, err
}
