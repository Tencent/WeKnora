package handler

import (
	"io"
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/types"
)

// chess_book.go bổ sung API "Thư viện sách cờ vua" (kệ/sách/chương/ảnh/phiên
// bản) vào CÙNG ChessLibraryHandler (khai báo ở chess_library.go) — cùng
// pattern "ngăn" đã dùng cho position: không phải handler riêng, để router.go
// chỉ cần mở rộng RegisterChessLibraryRoutes đã có.

// ---- Kệ ----

// ListShelves GET /chess/shelves?kind=&q=
func (h *ChessLibraryHandler) ListShelves(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	shelves, err := h.service.ListShelves(ctx, tenantID, types.ChessShelfFilter{
		Kind: c.Query("kind"), Keyword: c.Query("q"),
	})
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, shelves)
}

// GetShelf GET /chess/shelves/:id
func (h *ChessLibraryHandler) GetShelf(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	sh, err := h.service.GetShelf(ctx, tenantID, c.Param("id"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, sh)
}

// GetShelfBySlug GET /chess/shelves/by-slug/:slug
func (h *ChessLibraryHandler) GetShelfBySlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	sh, err := h.service.GetShelfBySlug(ctx, tenantID, c.Param("slug"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, sh)
}

type shelfBody struct {
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	CoverURL    string `json:"cover_url"`
	SortOrder   int    `json:"sort_order"`
}

// CreateShelf POST /chess/shelves
func (h *ChessLibraryHandler) CreateShelf(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b shelfBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	sh, err := h.service.CreateShelf(ctx, &types.ChessShelf{
		TenantID: tenantID, Title: b.Title, Kind: b.Kind,
		Description: b.Description, CoverURL: b.CoverURL, SortOrder: b.SortOrder,
	})
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, sh)
}

// UpdateShelf PUT /chess/shelves/:id
func (h *ChessLibraryHandler) UpdateShelf(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b shelfBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	sh, err := h.service.UpdateShelf(ctx, &types.ChessShelf{
		ID: c.Param("id"), TenantID: tenantID, Title: b.Title, Kind: b.Kind,
		Description: b.Description, CoverURL: b.CoverURL, SortOrder: b.SortOrder,
	})
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, sh)
}

// RenameShelfSlug PUT /chess/shelves/:id/slug {slug}
func (h *ChessLibraryHandler) RenameShelfSlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b slugBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	sh, err := h.service.RenameShelfSlug(ctx, tenantID, c.Param("id"), b.Slug)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, sh)
}

// DeleteShelf DELETE /chess/shelves/:id
func (h *ChessLibraryHandler) DeleteShelf(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	if err := h.service.DeleteShelf(ctx, tenantID, c.Param("id")); err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, gin.H{"deleted": true})
}

// SetShelfBooks PUT /chess/shelves/:id/books {book_ids:[...]} — ghi đè toàn bộ
// danh sách sách trên kệ theo đúng thứ tự truyền vào.
func (h *ChessLibraryHandler) SetShelfBooks(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b struct {
		BookIDs []string `json:"book_ids"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	if err := h.service.SetShelfBooks(ctx, tenantID, c.Param("id"), b.BookIDs); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, gin.H{"saved": true})
}

// ---- Sách ----

// ListBooks GET /chess/books?shelf_id=&level=&phase=&status=&q=
func (h *ChessLibraryHandler) ListBooks(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	books, err := h.service.ListBooks(ctx, tenantID, types.ChessBookFilter{
		ShelfID: c.Query("shelf_id"), Level: c.Query("level"), Phase: c.Query("phase"),
		Status: c.Query("status"), Keyword: c.Query("q"),
	})
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, books)
}

// GetBook GET /chess/books/:id
func (h *ChessLibraryHandler) GetBook(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	b, err := h.service.GetBook(ctx, tenantID, c.Param("id"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, b)
}

// GetBookBySlug GET /chess/books/by-slug/:slug — giải mã wikilink [[book/<slug>]].
func (h *ChessLibraryHandler) GetBookBySlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	b, err := h.service.GetBookBySlug(ctx, tenantID, c.Param("slug"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, b)
}

// GetBookBacklinks GET /chess/books/by-slug/:slug/backlinks
func (h *ChessLibraryHandler) GetBookBacklinks(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	links, err := h.service.GetBookBacklinks(ctx, tenantID, c.Param("slug"))
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, links)
}

// ListShelvesOfBook GET /chess/books/:id/shelves — kệ đang chứa sách (form sửa sách).
func (h *ChessLibraryHandler) ListShelvesOfBook(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	shelves, err := h.service.ListShelvesOfBook(ctx, tenantID, c.Param("id"))
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, shelves)
}

type bookBody struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Author      string `json:"author"`
	Translator  string `json:"translator"`
	Publisher   string `json:"publisher"`
	Year        string `json:"year"`
	ISBN        string `json:"isbn"`
	Language    string `json:"language"`
	Level       string `json:"level"`
	Phase       string `json:"phase"`
	ECO         string `json:"eco"`
	Status      string `json:"status"`
	Description string `json:"description"`
	CoverURL    string `json:"cover_url"`
	Tags        string `json:"tags"`
	SortOrder   int    `json:"sort_order"`
}

func bookFromBody(id string, tenantID uint64, b bookBody) *types.ChessBook {
	return &types.ChessBook{
		ID: id, TenantID: tenantID, Title: b.Title, Subtitle: b.Subtitle, Author: b.Author,
		Translator: b.Translator, Publisher: b.Publisher, Year: b.Year, ISBN: b.ISBN,
		Language: b.Language, Level: b.Level, Phase: b.Phase, ECO: b.ECO, Status: b.Status,
		Description: b.Description, CoverURL: b.CoverURL, Tags: b.Tags, SortOrder: b.SortOrder,
	}
}

// CreateBook POST /chess/books
func (h *ChessLibraryHandler) CreateBook(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b bookBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	book, err := h.service.CreateBook(ctx, bookFromBody("", tenantID, b))
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, book)
}

// UpdateBook PUT /chess/books/:id
func (h *ChessLibraryHandler) UpdateBook(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b bookBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	book, err := h.service.UpdateBook(ctx, bookFromBody(c.Param("id"), tenantID, b))
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, book)
}

// RenameBookSlug PUT /chess/books/:id/slug {slug} — đổi slug sách, giữ link cũ qua alias.
func (h *ChessLibraryHandler) RenameBookSlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b slugBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	book, err := h.service.RenameBookSlug(ctx, tenantID, c.Param("id"), b.Slug)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, book)
}

// DeleteBook DELETE /chess/books/:id — cascade chương/kệ/ảnh/lịch sử.
func (h *ChessLibraryHandler) DeleteBook(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	if err := h.service.DeleteBook(ctx, tenantID, c.Param("id")); err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, gin.H{"deleted": true})
}

// ExportBooks GET /chess/books/export?shelf_id=&level=&phase=&status= — sách kèm chương (JSON).
func (h *ChessLibraryHandler) ExportBooks(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	items, err := h.service.ExportBooks(ctx, tenantID, types.ChessBookFilter{
		ShelfID: c.Query("shelf_id"), Level: c.Query("level"), Phase: c.Query("phase"), Status: c.Query("status"),
	})
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, items)
}

// ImportBooks POST /chess/books/import {books:[...]} — tạo mới; trả số đã thêm.
func (h *ChessLibraryHandler) ImportBooks(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b struct {
		Books []types.ChessBookBundle `json:"books"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	count, err := h.service.ImportBooks(ctx, tenantID, b.Books)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, gin.H{"imported": count})
}

// ---- Ảnh chèn trong chương ----

// UploadBookImage POST /chess/books/:id/images (multipart form field "file")
// → {id, url}. url là đường dẫn ổn định GET /chess/books/images/:id (KHÔNG
// phải presigned URL có hạn dùng — nội dung chương lưu URL này lâu dài).
func (h *ChessLibraryHandler) UploadBookImage(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	fh, err := c.FormFile("file")
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	f, err := fh.Open()
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	mimeType := fh.Header.Get("Content-Type")
	img, err := h.service.UploadBookImage(ctx, tenantID, c.Param("id"), fh.Filename, mimeType, data)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, gin.H{"id": img.ID, "url": "/api/v1/chess/books/images/" + img.ID})
}

// GetBookImage GET /chess/books/images/:imageId — stream ảnh (inline).
func (h *ChessLibraryHandler) GetBookImage(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	img, rc, err := h.service.GetBookImage(ctx, tenantID, c.Param("imageId"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	defer rc.Close()
	contentType := img.Mime
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": img.FileName}))
	c.Header("Cache-Control", "private, max-age=3600")
	c.Stream(func(w io.Writer) bool {
		_, _ = io.Copy(w, rc)
		return false
	})
}

// ---- Chương ----

// ListChapters GET /chess/books/:id/chapters
func (h *ChessLibraryHandler) ListChapters(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	chapters, err := h.service.ListChapters(ctx, tenantID, c.Param("id"))
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, chapters)
}

// ReorderChapters PUT /chess/books/:id/chapters/reorder {chapter_ids:[...]}
func (h *ChessLibraryHandler) ReorderChapters(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b struct {
		ChapterIDs []string `json:"chapter_ids"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	if err := h.service.ReorderChapters(ctx, tenantID, c.Param("id"), b.ChapterIDs); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, gin.H{"saved": true})
}

type chapterBody struct {
	Part      string `json:"part"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	FEN       string `json:"fen"`
	Level     string `json:"level"`
	SortOrder int    `json:"sort_order"`
	// Summary là ghi chú thay đổi (tùy chọn) — chỉ dùng khi PUT (bỏ qua lúc POST).
	Summary string `json:"summary"`
}

// CreateChapter POST /chess/books/:id/chapters
func (h *ChessLibraryHandler) CreateChapter(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b chapterBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	ch, err := h.service.CreateChapter(ctx, &types.ChessBookChapter{
		TenantID: tenantID, BookID: c.Param("id"), Part: b.Part, Title: b.Title,
		Content: b.Content, FEN: b.FEN, Level: b.Level, SortOrder: b.SortOrder,
	})
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, ch)
}

// GetChapter GET /chess/chapters/:chapter_id
func (h *ChessLibraryHandler) GetChapter(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	ch, err := h.service.GetChapter(ctx, tenantID, c.Param("chapter_id"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, ch)
}

// GetChapterBySlug GET /chess/chapters/by-slug/:slug — giải mã wikilink [[chapter/<slug>]].
func (h *ChessLibraryHandler) GetChapterBySlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	ch, err := h.service.GetChapterBySlug(ctx, tenantID, c.Param("slug"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, ch)
}

// GetChapterBacklinks GET /chess/chapters/by-slug/:slug/backlinks
func (h *ChessLibraryHandler) GetChapterBacklinks(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	links, err := h.service.GetChapterBacklinks(ctx, tenantID, c.Param("slug"))
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, links)
}

// UpdateChapter PUT /chess/chapters/:chapter_id
func (h *ChessLibraryHandler) UpdateChapter(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b chapterBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	ch, err := h.service.UpdateChapter(ctx, &types.ChessBookChapter{
		ID: c.Param("chapter_id"), TenantID: tenantID, Part: b.Part, Title: b.Title,
		Content: b.Content, FEN: b.FEN, Level: b.Level, SortOrder: b.SortOrder,
	}, b.Summary)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, ch)
}

// RenameChapterSlug PUT /chess/chapters/:chapter_id/slug {slug}
func (h *ChessLibraryHandler) RenameChapterSlug(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	var b slugBody
	if err := c.ShouldBindJSON(&b); err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	ch, err := h.service.RenameChapterSlug(ctx, tenantID, c.Param("chapter_id"), b.Slug)
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, ch)
}

// DeleteChapter DELETE /chess/chapters/:chapter_id
func (h *ChessLibraryHandler) DeleteChapter(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	if err := h.service.DeleteChapter(ctx, tenantID, c.Param("chapter_id")); err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, gin.H{"deleted": true})
}

// ---- Lịch sử phiên bản chương ----

// ListChapterRevisions GET /chess/chapters/:chapter_id/revisions
func (h *ChessLibraryHandler) ListChapterRevisions(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	revs, err := h.service.ListChapterRevisions(ctx, tenantID, c.Param("chapter_id"))
	if err != nil {
		chessFail(c, http.StatusInternalServerError, err)
		return
	}
	chessOK(c, revs)
}

// GetChapterRevision GET /chess/chapters/:chapter_id/revisions/:rev_id
func (h *ChessLibraryHandler) GetChapterRevision(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	rev, err := h.service.GetChapterRevision(ctx, tenantID, c.Param("rev_id"))
	if err != nil {
		chessFail(c, http.StatusNotFound, err)
		return
	}
	chessOK(c, rev)
}

// RestoreChapterRevision POST /chess/chapters/:chapter_id/revisions/:rev_id/restore
func (h *ChessLibraryHandler) RestoreChapterRevision(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := types.MustTenantIDFromContext(ctx)
	ch, err := h.service.RestoreChapterRevision(ctx, tenantID, c.Param("chapter_id"), c.Param("rev_id"))
	if err != nil {
		chessFail(c, http.StatusBadRequest, err)
		return
	}
	chessOK(c, ch)
}
