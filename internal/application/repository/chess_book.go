package repository

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// chess_book.go bổ sung thao tác lưu trữ cho "Thư viện sách cờ vua" (kệ/sách/
// chương/ảnh/phiên bản) vào CÙNG struct chessLibraryRepository (khai báo ở
// chess_library.go) — cùng pattern "ngăn" đã dùng cho position: không phải
// repository riêng, để container.go không cần Provide mới.

// ---- Kệ ----

func (r *chessLibraryRepository) shelfQuery(ctx context.Context, tenantID uint64, f types.ChessShelfFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if f.Kind != "" {
		q = q.Where("kind = ?", f.Kind)
	}
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("slug ILIKE ? OR title ILIKE ?", kw, kw)
	}
	return q
}

func (r *chessLibraryRepository) ListShelves(ctx context.Context, tenantID uint64, f types.ChessShelfFilter) ([]*types.ChessShelf, error) {
	var shelves []*types.ChessShelf
	err := r.shelfQuery(ctx, tenantID, f).Order("sort_order ASC, created_at DESC").Find(&shelves).Error
	return shelves, err
}

func (r *chessLibraryRepository) GetShelf(ctx context.Context, tenantID uint64, id string) (*types.ChessShelf, error) {
	var s types.ChessShelf
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *chessLibraryRepository) GetShelfBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessShelf, error) {
	var s types.ChessShelf
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND slug = ?", tenantID, slug).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// ShelfSlugs trả toàn bộ slug kệ "sống" của tenant — pool ứng viên fuzzy-resolve.
func (r *chessLibraryRepository) ShelfSlugs(ctx context.Context, tenantID uint64) ([]string, error) {
	var slugs []string
	err := r.db.WithContext(ctx).Model(&types.ChessShelf{}).
		Where("tenant_id = ? AND slug <> ''", tenantID).
		Pluck("slug", &slugs).Error
	return slugs, err
}

func (r *chessLibraryRepository) ShelfSlugExists(ctx context.Context, tenantID uint64, slug string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.ChessShelf{}).
		Where("tenant_id = ? AND slug = ?", tenantID, slug).Limit(1).Count(&count).Error
	return count > 0, err
}

func (r *chessLibraryRepository) CreateShelf(ctx context.Context, shelf *types.ChessShelf) error {
	return r.db.WithContext(ctx).Create(shelf).Error
}

// UpdateShelf cố ý KHÔNG đụng cột slug (như UpdateGame/UpdatePosition) — đổi
// slug đi qua UpdateShelfSlug để luôn kèm ghi alias ở tầng service.
func (r *chessLibraryRepository) UpdateShelf(ctx context.Context, shelf *types.ChessShelf) error {
	return r.db.WithContext(ctx).
		Model(&types.ChessShelf{}).
		Where("tenant_id = ? AND id = ?", shelf.TenantID, shelf.ID).
		Updates(map[string]interface{}{
			"title": shelf.Title, "kind": shelf.Kind, "description": shelf.Description,
			"cover_url": shelf.CoverURL, "sort_order": shelf.SortOrder,
		}).Error
}

func (r *chessLibraryRepository) UpdateShelfSlug(ctx context.Context, tenantID uint64, id, slug string) error {
	return r.db.WithContext(ctx).
		Model(&types.ChessShelf{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Update("slug", slug).Error
}

func (r *chessLibraryRepository) DeleteShelf(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.ChessShelf{}).Error
}

func (r *chessLibraryRepository) CountBooksOnShelf(ctx context.Context, tenantID uint64, shelfID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.ChessShelfBook{}).
		Where("tenant_id = ? AND shelf_id = ?", tenantID, shelfID).Count(&count).Error
	return count, err
}

// ---- Nối kệ↔sách (nhiều-nhiều) ----

// SetShelfBooks xóa toàn bộ liên kết cũ của kệ rồi chèn lại theo đúng thứ tự
// bookIDs (transaction) — cùng khuôn ReplaceForLesson (xóa-rồi-chèn-lại).
func (r *chessLibraryRepository) SetShelfBooks(ctx context.Context, tenantID uint64, shelfID string, bookIDs []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND shelf_id = ?", tenantID, shelfID).
			Delete(&types.ChessShelfBook{}).Error; err != nil {
			return err
		}
		rows := make([]types.ChessShelfBook, 0, len(bookIDs))
		for i, bid := range bookIDs {
			if bid == "" {
				continue
			}
			rows = append(rows, types.ChessShelfBook{TenantID: tenantID, ShelfID: shelfID, BookID: bid, SortOrder: i})
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

// ListShelvesOfBook liệt kê mọi kệ đang chứa một cuốn sách. Select tường minh
// "s.*" vì JOIN với chess_shelf_books (cũng có cột tenant_id/created_at) sẽ
// SELECT * mơ hồ nếu không giới hạn.
func (r *chessLibraryRepository) ListShelvesOfBook(ctx context.Context, tenantID uint64, bookID string) ([]*types.ChessShelf, error) {
	var shelves []*types.ChessShelf
	err := r.db.WithContext(ctx).
		Table("chess_shelves AS s").
		Select("s.*").
		Joins("JOIN chess_shelf_books sb ON sb.shelf_id = s.id AND sb.tenant_id = ?", tenantID).
		Where("sb.book_id = ? AND s.tenant_id = ?", bookID, tenantID).
		Order("s.sort_order ASC, s.created_at DESC").
		Find(&shelves).Error
	return shelves, err
}

// RemoveBookFromAllShelves gỡ một cuốn sách khỏi mọi kệ (khi xóa sách).
func (r *chessLibraryRepository) RemoveBookFromAllShelves(ctx context.Context, tenantID uint64, bookID string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND book_id = ?", tenantID, bookID).
		Delete(&types.ChessShelfBook{}).Error
}

// RemoveShelfBooks xóa toàn bộ liên kết của một kệ (khi xóa kệ) — KHÔNG đụng sách.
func (r *chessLibraryRepository) RemoveShelfBooks(ctx context.Context, tenantID uint64, shelfID string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND shelf_id = ?", tenantID, shelfID).
		Delete(&types.ChessShelfBook{}).Error
}

// ---- Sách ----

func (r *chessLibraryRepository) bookQuery(ctx context.Context, tenantID uint64, f types.ChessBookFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&types.ChessBook{}).Where("chess_books.tenant_id = ?", tenantID)
	if f.Level != "" {
		q = q.Where("chess_books.level = ?", f.Level)
	}
	if f.Phase != "" {
		q = q.Where("chess_books.phase = ?", f.Phase)
	}
	if f.Status != "" {
		q = q.Where("chess_books.status = ?", f.Status)
	}
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		q = q.Where("chess_books.slug ILIKE ? OR chess_books.title ILIKE ? OR chess_books.author ILIKE ? OR chess_books.tags ILIKE ?",
			kw, kw, kw, kw)
	}
	if f.ShelfID != "" {
		q = q.Joins("JOIN chess_shelf_books sb ON sb.book_id = chess_books.id AND sb.tenant_id = ? AND sb.shelf_id = ?",
			tenantID, f.ShelfID)
	}
	return q
}

func (r *chessLibraryRepository) ListBooks(ctx context.Context, tenantID uint64, f types.ChessBookFilter) ([]*types.ChessBook, error) {
	var books []*types.ChessBook
	q := r.bookQuery(ctx, tenantID, f)
	if f.ShelfID != "" {
		// Trong kệ: giữ đúng thứ tự người dùng sắp qua SetShelfBooks.
		q = q.Order("sb.sort_order ASC, chess_books.created_at DESC")
	} else {
		q = q.Order("chess_books.sort_order ASC, chess_books.created_at DESC")
	}
	err := q.Limit(500).Find(&books).Error
	return books, err
}

func (r *chessLibraryRepository) GetBook(ctx context.Context, tenantID uint64, id string) (*types.ChessBook, error) {
	var b types.ChessBook
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&b).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *chessLibraryRepository) GetBookBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessBook, error) {
	var b types.ChessBook
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND slug = ?", tenantID, slug).First(&b).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

// BookSlugs trả toàn bộ slug sách "sống" của tenant — pool ứng viên fuzzy-resolve.
func (r *chessLibraryRepository) BookSlugs(ctx context.Context, tenantID uint64) ([]string, error) {
	var slugs []string
	err := r.db.WithContext(ctx).Model(&types.ChessBook{}).
		Where("tenant_id = ? AND slug <> ''", tenantID).
		Pluck("slug", &slugs).Error
	return slugs, err
}

func (r *chessLibraryRepository) BookSlugExists(ctx context.Context, tenantID uint64, slug string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.ChessBook{}).
		Where("tenant_id = ? AND slug = ?", tenantID, slug).Limit(1).Count(&count).Error
	return count > 0, err
}

func (r *chessLibraryRepository) CreateBook(ctx context.Context, book *types.ChessBook) error {
	return r.db.WithContext(ctx).Create(book).Error
}

// UpdateBook cố ý KHÔNG đụng cột slug — đổi slug đi qua UpdateBookSlug để luôn
// kèm ghi alias ở tầng service.
func (r *chessLibraryRepository) UpdateBook(ctx context.Context, book *types.ChessBook) error {
	return r.db.WithContext(ctx).
		Model(&types.ChessBook{}).
		Where("tenant_id = ? AND id = ?", book.TenantID, book.ID).
		Updates(map[string]interface{}{
			"title": book.Title, "subtitle": book.Subtitle, "author": book.Author,
			"translator": book.Translator, "publisher": book.Publisher, "year": book.Year,
			"isbn": book.ISBN, "language": book.Language, "level": book.Level, "phase": book.Phase,
			"eco": book.ECO, "status": book.Status, "description": book.Description,
			"cover_url": book.CoverURL, "tags": book.Tags, "sort_order": book.SortOrder,
		}).Error
}

func (r *chessLibraryRepository) UpdateBookSlug(ctx context.Context, tenantID uint64, id, slug string) error {
	return r.db.WithContext(ctx).
		Model(&types.ChessBook{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Update("slug", slug).Error
}

func (r *chessLibraryRepository) DeleteBook(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.ChessBook{}).Error
}

func (r *chessLibraryRepository) CountChapters(ctx context.Context, tenantID uint64, bookID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.ChessBookChapter{}).
		Where("tenant_id = ? AND book_id = ?", tenantID, bookID).Count(&count).Error
	return count, err
}

// ---- Chương ----

func (r *chessLibraryRepository) ListChapters(ctx context.Context, tenantID uint64, bookID string) ([]*types.ChessBookChapter, error) {
	var chapters []*types.ChessBookChapter
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND book_id = ?", tenantID, bookID).
		Order("sort_order ASC, created_at ASC").
		Find(&chapters).Error
	return chapters, err
}

// SearchChapters tìm chương theo từ khóa (slug/title, VÀ content trên Postgres
// qua full-text — khác SearchLessons hiện chỉ tìm slug/title). Chỉ chọn cột
// nhẹ (không tải Content) để tiết kiệm cho autocomplete.
func (r *chessLibraryRepository) SearchChapters(ctx context.Context, tenantID uint64, keyword string, limit int) ([]*types.ChessBookChapter, error) {
	if limit <= 0 {
		limit = 10
	}
	q := r.db.WithContext(ctx).Model(&types.ChessBookChapter{}).
		Select("id", "tenant_id", "book_id", "slug", "title", "part", "created_at").
		Where("tenant_id = ?", tenantID)
	if keyword != "" {
		kw := "%" + keyword + "%"
		if r.db.Dialector != nil && r.db.Dialector.Name() == "postgres" {
			q = q.Where("slug ILIKE ? OR title ILIKE ? OR "+
				"to_tsvector('simple', coalesce(title,'') || ' ' || coalesce(content,'')) @@ plainto_tsquery('simple', ?)",
				kw, kw, keyword)
		} else {
			// SQLite ("lite"): chưa có tsvector/pg_trgm — fallback ILIKE thường trên content.
			q = q.Where("slug ILIKE ? OR title ILIKE ? OR content ILIKE ?", kw, kw, kw)
		}
	}
	var chapters []*types.ChessBookChapter
	err := q.Order("created_at DESC").Limit(limit).Find(&chapters).Error
	return chapters, err
}

func (r *chessLibraryRepository) GetChapter(ctx context.Context, tenantID uint64, id string) (*types.ChessBookChapter, error) {
	var ch types.ChessBookChapter
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

func (r *chessLibraryRepository) GetChapterBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessBookChapter, error) {
	var ch types.ChessBookChapter
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND slug = ?", tenantID, slug).First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

// ChapterSlugs trả toàn bộ slug chương "sống" của tenant — pool ứng viên fuzzy-resolve.
func (r *chessLibraryRepository) ChapterSlugs(ctx context.Context, tenantID uint64) ([]string, error) {
	var slugs []string
	err := r.db.WithContext(ctx).Model(&types.ChessBookChapter{}).
		Where("tenant_id = ? AND slug <> ''", tenantID).
		Pluck("slug", &slugs).Error
	return slugs, err
}

func (r *chessLibraryRepository) ChapterSlugExists(ctx context.Context, tenantID uint64, slug string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.ChessBookChapter{}).
		Where("tenant_id = ? AND slug = ?", tenantID, slug).Limit(1).Count(&count).Error
	return count > 0, err
}

func (r *chessLibraryRepository) CreateChapter(ctx context.Context, chapter *types.ChessBookChapter) error {
	return r.db.WithContext(ctx).Create(chapter).Error
}

// UpdateChapter cố ý KHÔNG đụng book_id (chương không "di chuyển" sang sách
// khác qua sửa nội dung, giống CourseID của ChessLesson) lẫn slug.
func (r *chessLibraryRepository) UpdateChapter(ctx context.Context, chapter *types.ChessBookChapter) error {
	return r.db.WithContext(ctx).
		Model(&types.ChessBookChapter{}).
		Where("tenant_id = ? AND id = ?", chapter.TenantID, chapter.ID).
		Updates(map[string]interface{}{
			"part": chapter.Part, "title": chapter.Title, "content": chapter.Content,
			"fen": chapter.FEN, "level": chapter.Level, "sort_order": chapter.SortOrder,
		}).Error
}

func (r *chessLibraryRepository) UpdateChapterSlug(ctx context.Context, tenantID uint64, id, slug string) error {
	return r.db.WithContext(ctx).
		Model(&types.ChessBookChapter{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Update("slug", slug).Error
}

func (r *chessLibraryRepository) DeleteChapter(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.ChessBookChapter{}).Error
}

func (r *chessLibraryRepository) DeleteChaptersByBook(ctx context.Context, tenantID uint64, bookID string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND book_id = ?", tenantID, bookID).
		Delete(&types.ChessBookChapter{}).Error
}

// ReorderChapters ghi lại sort_order theo đúng thứ tự chapterIDs (transaction).
func (r *chessLibraryRepository) ReorderChapters(ctx context.Context, tenantID uint64, bookID string, chapterIDs []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, id := range chapterIDs {
			if id == "" {
				continue
			}
			if err := tx.Model(&types.ChessBookChapter{}).
				Where("tenant_id = ? AND book_id = ? AND id = ?", tenantID, bookID, id).
				Update("sort_order", i).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- Lịch sử phiên bản chương ----

func (r *chessLibraryRepository) CreateChapterRevision(ctx context.Context, rev *types.ChessChapterRevision) error {
	return r.db.WithContext(ctx).Create(rev).Error
}

func (r *chessLibraryRepository) ListChapterRevisions(ctx context.Context, tenantID uint64, chapterID string) ([]*types.ChessChapterRevision, error) {
	var revs []*types.ChessChapterRevision
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND chapter_id = ?", tenantID, chapterID).
		Order("revision_number DESC").
		Find(&revs).Error
	return revs, err
}

func (r *chessLibraryRepository) GetChapterRevision(ctx context.Context, tenantID uint64, revisionID string) (*types.ChessChapterRevision, error) {
	var rev types.ChessChapterRevision
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, revisionID).First(&rev).Error; err != nil {
		return nil, err
	}
	return &rev, nil
}

// CountChapterRevisions phục vụ tính revision_number tiếp theo (bắt đầu từ 1).
func (r *chessLibraryRepository) CountChapterRevisions(ctx context.Context, tenantID uint64, chapterID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.ChessChapterRevision{}).
		Where("tenant_id = ? AND chapter_id = ?", tenantID, chapterID).Count(&count).Error
	return count, err
}

// DeleteChapterRevisionsByChapter xóa toàn bộ lịch sử của một chương (khi xóa chương).
func (r *chessLibraryRepository) DeleteChapterRevisionsByChapter(ctx context.Context, tenantID uint64, chapterID string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND chapter_id = ?", tenantID, chapterID).
		Delete(&types.ChessChapterRevision{}).Error
}

// ---- Ảnh chèn trong chương ----

func (r *chessLibraryRepository) CreateBookImage(ctx context.Context, img *types.ChessBookImage) error {
	return r.db.WithContext(ctx).Create(img).Error
}

func (r *chessLibraryRepository) GetBookImage(ctx context.Context, tenantID uint64, id string) (*types.ChessBookImage, error) {
	var img types.ChessBookImage
	if err := r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&img).Error; err != nil {
		return nil, err
	}
	return &img, nil
}

// ListBookImagesByBook phục vụ xóa file vật lý khi cascade delete sách.
func (r *chessLibraryRepository) ListBookImagesByBook(ctx context.Context, tenantID uint64, bookID string) ([]*types.ChessBookImage, error) {
	var imgs []*types.ChessBookImage
	err := r.db.WithContext(ctx).Where("tenant_id = ? AND book_id = ?", tenantID, bookID).Find(&imgs).Error
	return imgs, err
}

func (r *chessLibraryRepository) DeleteBookImage(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&types.ChessBookImage{}).Error
}
