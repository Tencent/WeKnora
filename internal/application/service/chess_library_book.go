package service

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/types"
)

// chess_library_book.go bổ sung nghiệp vụ "Thư viện sách cờ vua" (kệ/sách/
// chương/ảnh/phiên bản) vào CÙNG struct chessLibraryService (khai báo ở
// chess_library.go) — cùng pattern "ngăn" đã dùng cho position: không phải
// service riêng, để container.go/router.go chỉ mở rộng chỗ có sẵn.
//
// Ranh giới quan trọng: sách ở đây là hệ SOẠN THẢO nội bộ (markdown + FEN +
// wikilink + ảnh), KHÔNG phải kho ebook PDF/EPUB để đọc. Chỉ sách
// status="published" mới được index vào KB tri thức cờ (draft không rò vào
// câu trả lời của agent) — xem ChessKnowledgeIndexer.IndexBook/IndexChapter.

// maxBookImageSize giới hạn ảnh chèn trong chương — khớp giới hạn ảnh chat
// (internal/handler/session/image_upload.go: maxImageSize).
const maxBookImageSize = 10 << 20 // 10MB

// bookImageExtByMime ánh xạ MIME → phần mở rộng lưu trữ (chỉ nhận ảnh raster
// phổ biến — khớp SUPPORTED_TYPES phía trình soạn thảo).
var bookImageExtByMime = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// userIDOrEmpty đọc user id từ context (rỗng nếu không có — vd tác vụ nền/import).
func userIDOrEmpty(ctx context.Context) string {
	if id, ok := types.UserIDFromContext(ctx); ok {
		return id
	}
	return ""
}

// ---- Kệ ----

func (s *chessLibraryService) ListShelves(ctx context.Context, tenantID uint64, f types.ChessShelfFilter) ([]*types.ChessShelf, error) {
	shelves, err := s.repo.ListShelves(ctx, tenantID, f)
	if err != nil {
		return nil, err
	}
	for _, sh := range shelves {
		if n, err := s.repo.CountBooksOnShelf(ctx, tenantID, sh.ID); err == nil {
			sh.BookCount = n
		}
	}
	return shelves, nil
}

func (s *chessLibraryService) GetShelf(ctx context.Context, tenantID uint64, id string) (*types.ChessShelf, error) {
	sh, err := s.repo.GetShelf(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if n, err := s.repo.CountBooksOnShelf(ctx, tenantID, id); err == nil {
		sh.BookCount = n
	}
	return sh, nil
}

// GetShelfBySlug giải mã slug kệ. Kệ KHÔNG phải đích wikilink (chỉ điều hướng
// UI nội bộ) nên KHÔNG cần alias/fuzzy resolve như book/chapter.
func (s *chessLibraryService) GetShelfBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessShelf, error) {
	return s.repo.GetShelfBySlug(ctx, tenantID, slug)
}

func (s *chessLibraryService) CreateShelf(ctx context.Context, shelf *types.ChessShelf) (*types.ChessShelf, error) {
	if strings.TrimSpace(shelf.Title) == "" {
		return nil, fmt.Errorf("tên kệ không được để trống")
	}
	shelf.ID = uuid.New().String()
	slug, err := ensureUniqueChessSlug(ctx, shelf.TenantID, shelfSlugBase(shelf), shelf.ID, s.repo.ShelfSlugExists)
	if err != nil {
		return nil, err
	}
	shelf.Slug = slug
	if err := s.repo.CreateShelf(ctx, shelf); err != nil {
		return nil, err
	}
	return shelf, nil
}

func (s *chessLibraryService) UpdateShelf(ctx context.Context, shelf *types.ChessShelf) (*types.ChessShelf, error) {
	if _, err := s.repo.GetShelf(ctx, shelf.TenantID, shelf.ID); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateShelf(ctx, shelf); err != nil {
		return nil, err
	}
	return s.GetShelf(ctx, shelf.TenantID, shelf.ID)
}

// RenameShelfSlug đổi slug kệ. Không ghi alias (kệ không phải đích wikilink).
func (s *chessLibraryService) RenameShelfSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessShelf, error) {
	sh, err := s.repo.GetShelf(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	base := slugifyChess(newSlug)
	if base == "" {
		return nil, fmt.Errorf("slug mới không hợp lệ (cần chứa chữ/số)")
	}
	if base == sh.Slug {
		return sh, nil
	}
	unique, err := ensureUniqueChessSlug(ctx, tenantID, base, sh.ID, s.repo.ShelfSlugExists)
	if err != nil {
		return nil, err
	}
	if err := s.repo.UpdateShelfSlug(ctx, tenantID, id, unique); err != nil {
		return nil, err
	}
	return s.GetShelf(ctx, tenantID, id)
}

func (s *chessLibraryService) DeleteShelf(ctx context.Context, tenantID uint64, id string) error {
	if err := s.repo.RemoveShelfBooks(ctx, tenantID, id); err != nil {
		return err
	}
	return s.repo.DeleteShelf(ctx, tenantID, id)
}

// SetShelfBooks GHI ĐÈ toàn bộ danh sách sách trên một kệ theo đúng thứ tự
// bookIDs truyền vào.
func (s *chessLibraryService) SetShelfBooks(ctx context.Context, tenantID uint64, shelfID string, bookIDs []string) error {
	if _, err := s.repo.GetShelf(ctx, tenantID, shelfID); err != nil {
		return fmt.Errorf("kệ không tồn tại")
	}
	return s.repo.SetShelfBooks(ctx, tenantID, shelfID, bookIDs)
}

func (s *chessLibraryService) ListShelvesOfBook(ctx context.Context, tenantID uint64, bookID string) ([]*types.ChessShelf, error) {
	return s.repo.ListShelvesOfBook(ctx, tenantID, bookID)
}

// ---- Sách ----

func (s *chessLibraryService) ListBooks(ctx context.Context, tenantID uint64, f types.ChessBookFilter) ([]*types.ChessBook, error) {
	books, err := s.repo.ListBooks(ctx, tenantID, f)
	if err != nil {
		return nil, err
	}
	for _, b := range books {
		if n, err := s.repo.CountChapters(ctx, tenantID, b.ID); err == nil {
			b.ChapterCount = n
		}
	}
	return books, nil
}

func (s *chessLibraryService) GetBook(ctx context.Context, tenantID uint64, id string) (*types.ChessBook, error) {
	b, err := s.repo.GetBook(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if n, err := s.repo.CountChapters(ctx, tenantID, id); err == nil {
		b.ChapterCount = n
	}
	return b, nil
}

func (s *chessLibraryService) GetBookBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessBook, error) {
	b, err := s.repo.GetBookBySlug(ctx, tenantID, slug)
	if err != nil {
		resolved, ok := s.resolveAliasOrFuzzy(ctx, tenantID, types.ChessRefTypeBook, slug, s.repo.BookSlugs)
		if !ok {
			return nil, err
		}
		b, err = s.repo.GetBookBySlug(ctx, tenantID, resolved)
		if err != nil {
			return nil, err
		}
	}
	if n, err := s.repo.CountChapters(ctx, tenantID, b.ID); err == nil {
		b.ChapterCount = n
	}
	return b, nil
}

func (s *chessLibraryService) GetBookBacklinks(ctx context.Context, tenantID uint64, slug string) ([]types.ChessBacklink, error) {
	if s.chessRefRepo == nil {
		return nil, nil
	}
	return s.chessRefRepo.ListBacklinks(ctx, tenantID, types.ChessRefTypeBook, slug)
}

func (s *chessLibraryService) CreateBook(ctx context.Context, book *types.ChessBook) (*types.ChessBook, error) {
	if strings.TrimSpace(book.Title) == "" {
		return nil, fmt.Errorf("tên sách không được để trống")
	}
	if book.Status == "" {
		book.Status = types.ChessBookStatusDraft
	}
	book.ID = uuid.New().String()
	slug, err := ensureUniqueChessSlug(ctx, book.TenantID, bookSlugBase(book), book.ID, s.repo.BookSlugExists)
	if err != nil {
		return nil, err
	}
	book.Slug = slug
	if err := s.repo.CreateBook(ctx, book); err != nil {
		return nil, err
	}
	if s.indexer != nil {
		_ = s.indexer.IndexBook(ctx, book, nil) // no-op nếu draft hoặc CHESS_KB_INDEX tắt
	}
	return book, nil
}

func (s *chessLibraryService) UpdateBook(ctx context.Context, book *types.ChessBook) (*types.ChessBook, error) {
	if _, err := s.repo.GetBook(ctx, book.TenantID, book.ID); err != nil {
		return nil, err
	}
	if book.Status == "" {
		book.Status = types.ChessBookStatusDraft
	}
	if err := s.repo.UpdateBook(ctx, book); err != nil {
		return nil, err
	}
	updated, err := s.repo.GetBook(ctx, book.TenantID, book.ID)
	if err != nil {
		return nil, err
	}
	if n, err := s.repo.CountChapters(ctx, book.TenantID, book.ID); err == nil {
		updated.ChapterCount = n
	}
	s.reindexBookAndChapters(ctx, updated)
	return updated, nil
}

// reindexBookAndChapters đồng bộ lại KB tri thức cờ sau khi Status của sách có
// thể đã đổi: published → index sách + mọi chương; draft (kể cả vừa chuyển từ
// published) → gỡ sách + mọi chương khỏi KB. Best-effort, không chặn CRUD.
func (s *chessLibraryService) reindexBookAndChapters(ctx context.Context, book *types.ChessBook) {
	if s.indexer == nil || book == nil {
		return
	}
	chapters, err := s.repo.ListChapters(ctx, book.TenantID, book.ID)
	if err != nil {
		return
	}
	if book.Status == types.ChessBookStatusPublished {
		_ = s.indexer.IndexBook(ctx, book, chapters)
		for _, ch := range chapters {
			_ = s.indexer.IndexChapter(ctx, book, ch)
		}
		return
	}
	s.indexer.Remove(ctx, book.TenantID, types.ChessRefTypeBook, book.Slug)
	for _, ch := range chapters {
		s.indexer.Remove(ctx, book.TenantID, types.ChessRefTypeChapter, ch.Slug)
	}
}

// RenameBookSlug đổi slug sách sang newSlug + ghi alias slug-cũ→mới.
func (s *chessLibraryService) RenameBookSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessBook, error) {
	b, err := s.repo.GetBook(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	base := slugifyChess(newSlug)
	if base == "" {
		return nil, fmt.Errorf("slug mới không hợp lệ (cần chứa chữ/số)")
	}
	if base == b.Slug {
		return b, nil
	}
	unique, err := ensureUniqueChessSlug(ctx, tenantID, base, b.ID, s.repo.BookSlugExists)
	if err != nil {
		return nil, err
	}
	oldSlug := b.Slug
	if err := s.repo.UpdateBookSlug(ctx, tenantID, id, unique); err != nil {
		return nil, err
	}
	if s.aliasRepo != nil && oldSlug != "" {
		_ = s.aliasRepo.AddAlias(ctx, tenantID, types.ChessRefTypeBook, oldSlug, unique)
	}
	updated, err := s.repo.GetBook(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	s.reindexBookAndChapters(ctx, updated)
	return updated, nil
}

// DeleteBook xóa sách VÀ cascade: chương (kèm ref 2 chiều + gỡ index từng
// chương + lịch sử phiên bản), pivot kệ, ảnh (cả bản ghi lẫn file vật lý).
func (s *chessLibraryService) DeleteBook(ctx context.Context, tenantID uint64, id string) error {
	book, _ := s.repo.GetBook(ctx, tenantID, id)
	chapters, _ := s.repo.ListChapters(ctx, tenantID, id)
	images, _ := s.repo.ListBookImagesByBook(ctx, tenantID, id)

	if err := s.repo.DeleteChaptersByBook(ctx, tenantID, id); err != nil {
		return err
	}
	if err := s.repo.RemoveBookFromAllShelves(ctx, tenantID, id); err != nil {
		return err
	}
	if err := s.repo.DeleteBook(ctx, tenantID, id); err != nil {
		return err
	}

	for _, ch := range chapters {
		_ = s.repo.DeleteChapterRevisionsByChapter(ctx, tenantID, ch.ID)
		if ch.Slug == "" {
			continue
		}
		s.pruneChessRefs(ctx, tenantID, types.ChessRefTypeChapter, ch.Slug) // ref TRỎ TỚI chương
		if s.chessRefRepo != nil {
			_ = s.chessRefRepo.DeleteForChapter(ctx, tenantID, ch.Slug) // ref TỪ nội dung chương
		}
		if s.indexer != nil {
			s.indexer.Remove(ctx, tenantID, types.ChessRefTypeChapter, ch.Slug)
		}
	}
	// Sách KHÔNG phát wikilink (Description không parse ref) → chỉ dọn ref TRỎ TỚI nó.
	if book != nil && book.Slug != "" {
		s.pruneChessRefs(ctx, tenantID, types.ChessRefTypeBook, book.Slug)
		if s.indexer != nil {
			s.indexer.Remove(ctx, tenantID, types.ChessRefTypeBook, book.Slug)
		}
	}
	for _, img := range images {
		if s.fileService != nil && img.Path != "" {
			_ = s.fileService.DeleteFile(ctx, img.Path)
		}
		_ = s.repo.DeleteBookImage(ctx, tenantID, img.ID)
	}
	return nil
}

// ---- Chương ----

func (s *chessLibraryService) ListChapters(ctx context.Context, tenantID uint64, bookID string) ([]*types.ChessBookChapter, error) {
	return s.repo.ListChapters(ctx, tenantID, bookID)
}

func (s *chessLibraryService) SearchChapters(ctx context.Context, tenantID uint64, keyword string, limit int) ([]*types.ChessBookChapter, error) {
	return s.repo.SearchChapters(ctx, tenantID, keyword, limit)
}

func (s *chessLibraryService) GetChapter(ctx context.Context, tenantID uint64, id string) (*types.ChessBookChapter, error) {
	return s.repo.GetChapter(ctx, tenantID, id)
}

func (s *chessLibraryService) GetChapterBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessBookChapter, error) {
	ch, err := s.repo.GetChapterBySlug(ctx, tenantID, slug)
	if err == nil {
		return ch, nil
	}
	if resolved, ok := s.resolveAliasOrFuzzy(ctx, tenantID, types.ChessRefTypeChapter, slug, s.repo.ChapterSlugs); ok {
		return s.repo.GetChapterBySlug(ctx, tenantID, resolved)
	}
	return nil, err
}

func (s *chessLibraryService) GetChapterBacklinks(ctx context.Context, tenantID uint64, slug string) ([]types.ChessBacklink, error) {
	if s.chessRefRepo == nil {
		return nil, nil
	}
	return s.chessRefRepo.ListBacklinks(ctx, tenantID, types.ChessRefTypeChapter, slug)
}

func (s *chessLibraryService) CreateChapter(ctx context.Context, chapter *types.ChessBookChapter) (*types.ChessBookChapter, error) {
	if strings.TrimSpace(chapter.Title) == "" {
		return nil, fmt.Errorf("tên chương không được để trống")
	}
	if strings.TrimSpace(chapter.BookID) == "" {
		return nil, fmt.Errorf("thiếu book_id")
	}
	book, err := s.repo.GetBook(ctx, chapter.TenantID, chapter.BookID)
	if err != nil {
		return nil, fmt.Errorf("sách không tồn tại")
	}
	chapter.ID = uuid.New().String()
	slug, err := ensureUniqueChessSlug(ctx, chapter.TenantID, chapterSlugBase(book.Slug, chapter), chapter.ID, s.repo.ChapterSlugExists)
	if err != nil {
		return nil, err
	}
	chapter.Slug = slug
	if err := s.repo.CreateChapter(ctx, chapter); err != nil {
		return nil, err
	}
	s.syncChapterChessRefs(ctx, chapter)
	if s.indexer != nil {
		_ = s.indexer.IndexChapter(ctx, book, chapter) // best-effort: không chặn CRUD
	}
	return chapter, nil
}

// UpdateChapter cập nhật chương; nếu Title/Content thực sự đổi thì lưu bản
// TRƯỚC khi ghi đè vào lịch sử phiên bản (tránh rác khi chỉ sửa sort_order/part).
func (s *chessLibraryService) UpdateChapter(ctx context.Context, chapter *types.ChessBookChapter, summary string) (*types.ChessBookChapter, error) {
	old, err := s.repo.GetChapter(ctx, chapter.TenantID, chapter.ID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(chapter.Title) == "" {
		return nil, fmt.Errorf("tên chương không được để trống")
	}
	if old.Title != chapter.Title || old.Content != chapter.Content {
		next, _ := s.repo.CountChapterRevisions(ctx, old.TenantID, old.ID)
		_ = s.repo.CreateChapterRevision(ctx, &types.ChessChapterRevision{
			ID: uuid.New().String(), TenantID: old.TenantID, ChapterID: old.ID,
			RevisionNumber: int(next) + 1, Title: old.Title, Content: old.Content,
			Summary: summary, CreatedBy: userIDOrEmpty(ctx),
		})
	}
	if err := s.repo.UpdateChapter(ctx, chapter); err != nil {
		return nil, err
	}
	updated, err := s.repo.GetChapter(ctx, chapter.TenantID, chapter.ID)
	if err != nil {
		return nil, err
	}
	s.syncChapterChessRefs(ctx, updated)
	s.reindexChapter(ctx, updated)
	return updated, nil
}

// reindexChapter nạp lại sách chứa chương (để biết Status + tiêu đề) rồi index.
func (s *chessLibraryService) reindexChapter(ctx context.Context, ch *types.ChessBookChapter) {
	if s.indexer == nil || ch == nil {
		return
	}
	book, err := s.repo.GetBook(ctx, ch.TenantID, ch.BookID)
	if err != nil || book == nil {
		return
	}
	_ = s.indexer.IndexChapter(ctx, book, ch)
}

// RenameChapterSlug đổi slug chương sang newSlug + ghi alias slug-cũ→mới.
func (s *chessLibraryService) RenameChapterSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessBookChapter, error) {
	ch, err := s.repo.GetChapter(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	base := slugifyChess(newSlug)
	if base == "" {
		return nil, fmt.Errorf("slug mới không hợp lệ (cần chứa chữ/số)")
	}
	if base == ch.Slug {
		return ch, nil
	}
	unique, err := ensureUniqueChessSlug(ctx, tenantID, base, ch.ID, s.repo.ChapterSlugExists)
	if err != nil {
		return nil, err
	}
	oldSlug := ch.Slug
	if err := s.repo.UpdateChapterSlug(ctx, tenantID, id, unique); err != nil {
		return nil, err
	}
	if s.aliasRepo != nil && oldSlug != "" {
		_ = s.aliasRepo.AddAlias(ctx, tenantID, types.ChessRefTypeChapter, oldSlug, unique)
	}
	updated, err := s.repo.GetChapter(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	s.syncChapterChessRefs(ctx, updated)
	s.reindexChapter(ctx, updated)
	return updated, nil
}

func (s *chessLibraryService) DeleteChapter(ctx context.Context, tenantID uint64, id string) error {
	ch, _ := s.repo.GetChapter(ctx, tenantID, id)
	if err := s.repo.DeleteChapter(ctx, tenantID, id); err != nil {
		return err
	}
	if ch == nil {
		return nil
	}
	_ = s.repo.DeleteChapterRevisionsByChapter(ctx, tenantID, ch.ID)
	if ch.Slug != "" {
		s.pruneChessRefs(ctx, tenantID, types.ChessRefTypeChapter, ch.Slug)
		if s.chessRefRepo != nil {
			_ = s.chessRefRepo.DeleteForChapter(ctx, tenantID, ch.Slug)
		}
		if s.indexer != nil {
			s.indexer.Remove(ctx, tenantID, types.ChessRefTypeChapter, ch.Slug)
		}
	}
	return nil
}

// ReorderChapters ghi lại sort_order theo đúng thứ tự chapterIDs.
func (s *chessLibraryService) ReorderChapters(ctx context.Context, tenantID uint64, bookID string, chapterIDs []string) error {
	if _, err := s.repo.GetBook(ctx, tenantID, bookID); err != nil {
		return fmt.Errorf("sách không tồn tại")
	}
	return s.repo.ReorderChapters(ctx, tenantID, bookID, chapterIDs)
}

// syncChapterChessRefs đồng bộ wiki_chess_refs theo nội dung chương (nguồn
// chapter) — TÁI DÙNG parseChessRefSlugs (chess_course.go, cùng package).
func (s *chessLibraryService) syncChapterChessRefs(ctx context.Context, ch *types.ChessBookChapter) {
	if s.chessRefRepo == nil || ch == nil || ch.Slug == "" {
		return
	}
	refs := parseChessRefSlugs(ch.Content)
	for i := range refs {
		refs[i].ID = uuid.New().String()
		refs[i].TenantID = ch.TenantID
		refs[i].SourceType = types.ChessRefSourceChapter
		refs[i].PageSlug = ch.Slug
	}
	_ = s.chessRefRepo.ReplaceForChapter(ctx, ch.TenantID, ch.Slug, refs)
}

// ---- Lịch sử phiên bản chương ----

func (s *chessLibraryService) ListChapterRevisions(ctx context.Context, tenantID uint64, chapterID string) ([]*types.ChessChapterRevision, error) {
	return s.repo.ListChapterRevisions(ctx, tenantID, chapterID)
}

func (s *chessLibraryService) GetChapterRevision(ctx context.Context, tenantID uint64, revisionID string) (*types.ChessChapterRevision, error) {
	return s.repo.GetChapterRevision(ctx, tenantID, revisionID)
}

// RestoreChapterRevision ghi nội dung một bản cũ trở lại làm bản hiện tại.
// Tái dùng UpdateChapter để đi đúng luồng: bản ĐANG CÓ (trước khi khôi phục)
// cũng được lưu thành một phiên bản mới, đồng bộ ref + reindex như sửa thường.
func (s *chessLibraryService) RestoreChapterRevision(ctx context.Context, tenantID uint64, chapterID, revisionID string) (*types.ChessBookChapter, error) {
	rev, err := s.repo.GetChapterRevision(ctx, tenantID, revisionID)
	if err != nil {
		return nil, err
	}
	if rev.ChapterID != chapterID {
		return nil, fmt.Errorf("bản phiên bản không thuộc chương này")
	}
	ch, err := s.repo.GetChapter(ctx, tenantID, chapterID)
	if err != nil {
		return nil, err
	}
	ch.Title = rev.Title
	ch.Content = rev.Content
	return s.UpdateChapter(ctx, ch, fmt.Sprintf("Khôi phục từ bản #%d", rev.RevisionNumber))
}

// ---- Ảnh chèn trong chương ----

// UploadBookImage lưu file qua FileService rồi ghi bản ghi ChessBookImage.
func (s *chessLibraryService) UploadBookImage(ctx context.Context, tenantID uint64, bookID, fileName, mime string, data []byte) (*types.ChessBookImage, error) {
	if s.fileService == nil {
		return nil, fmt.Errorf("dịch vụ lưu file chưa sẵn sàng")
	}
	if _, err := s.repo.GetBook(ctx, tenantID, bookID); err != nil {
		return nil, fmt.Errorf("sách không tồn tại")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("file ảnh rỗng")
	}
	if len(data) > maxBookImageSize {
		return nil, fmt.Errorf("ảnh vượt quá %dMB", maxBookImageSize>>20)
	}
	ext, ok := bookImageExtByMime[strings.ToLower(mime)]
	if !ok {
		return nil, fmt.Errorf("định dạng ảnh không hỗ trợ (chỉ png/jpeg/gif/webp)")
	}
	id := uuid.New().String()
	storedName := fmt.Sprintf("book-images/%s%s", id, ext)
	path, err := s.fileService.SaveBytes(ctx, data, tenantID, storedName, false)
	if err != nil {
		return nil, fmt.Errorf("lưu ảnh thất bại: %w", err)
	}
	img := &types.ChessBookImage{
		ID: id, TenantID: tenantID, BookID: bookID, Path: path,
		FileName: fileName, Mime: mime, Size: int64(len(data)),
	}
	if err := s.repo.CreateBookImage(ctx, img); err != nil {
		return nil, err
	}
	return img, nil
}

// GetBookImage trả metadata + luồng đọc nội dung ảnh (caller PHẢI Close()).
func (s *chessLibraryService) GetBookImage(ctx context.Context, tenantID uint64, imageID string) (*types.ChessBookImage, io.ReadCloser, error) {
	img, err := s.repo.GetBookImage(ctx, tenantID, imageID)
	if err != nil {
		return nil, nil, err
	}
	if s.fileService == nil {
		return nil, nil, fmt.Errorf("dịch vụ lưu file chưa sẵn sàng")
	}
	rc, err := s.fileService.GetFile(ctx, img.Path)
	if err != nil {
		return nil, nil, err
	}
	return img, rc, nil
}

// ---- Export / Import ----

// ExportBooks xuất các sách (kèm chương, theo filter) để sao lưu/chia sẻ.
func (s *chessLibraryService) ExportBooks(ctx context.Context, tenantID uint64, f types.ChessBookFilter) ([]types.ChessBookBundle, error) {
	books, err := s.repo.ListBooks(ctx, tenantID, f)
	if err != nil {
		return nil, err
	}
	out := make([]types.ChessBookBundle, 0, len(books))
	for _, b := range books {
		chapters, err := s.repo.ListChapters(ctx, tenantID, b.ID)
		if err != nil {
			return nil, err
		}
		bundle := types.ChessBookBundle{
			Title: b.Title, Subtitle: b.Subtitle, Author: b.Author, Translator: b.Translator,
			Publisher: b.Publisher, Year: b.Year, ISBN: b.ISBN, Language: b.Language,
			Level: b.Level, Phase: b.Phase, ECO: b.ECO, Status: b.Status,
			Description: b.Description, CoverURL: b.CoverURL, Tags: b.Tags, SortOrder: b.SortOrder,
		}
		for _, ch := range chapters {
			bundle.Chapters = append(bundle.Chapters, types.ChessChapterBundle{
				Part: ch.Part, Title: ch.Title, Content: ch.Content, FEN: ch.FEN,
				Level: ch.Level, SortOrder: ch.SortOrder,
			})
		}
		out = append(out, bundle)
	}
	return out, nil
}

// ImportBooks nhập danh sách sách (kèm chương), LUÔN tạo mới trong tenant hiện
// tại (tái dùng CreateBook/CreateChapter để sinh ID/slug/đồng bộ ref/index).
// Trả số sách đã thêm; bỏ qua bản lỗi để 1 bản xấu không chặn cả lô.
func (s *chessLibraryService) ImportBooks(ctx context.Context, tenantID uint64, items []types.ChessBookBundle) (int, error) {
	created := 0
	for i := range items {
		it := items[i]
		if strings.TrimSpace(it.Title) == "" {
			continue
		}
		book, err := s.CreateBook(ctx, &types.ChessBook{
			TenantID: tenantID, Title: it.Title, Subtitle: it.Subtitle, Author: it.Author,
			Translator: it.Translator, Publisher: it.Publisher, Year: it.Year, ISBN: it.ISBN,
			Language: it.Language, Level: it.Level, Phase: it.Phase, ECO: it.ECO, Status: it.Status,
			Description: it.Description, CoverURL: it.CoverURL, Tags: it.Tags, SortOrder: it.SortOrder,
		})
		if err != nil {
			continue
		}
		for j := range it.Chapters {
			c := it.Chapters[j]
			if strings.TrimSpace(c.Title) == "" {
				continue
			}
			_, _ = s.CreateChapter(ctx, &types.ChessBookChapter{
				TenantID: tenantID, BookID: book.ID, Part: c.Part, Title: c.Title,
				Content: c.Content, FEN: c.FEN, Level: c.Level, SortOrder: c.SortOrder,
			})
		}
		created++
	}
	if created == 0 && len(items) > 0 {
		return 0, fmt.Errorf("không nhập được sách nào (kiểm tra định dạng JSON)")
	}
	return created, nil
}
