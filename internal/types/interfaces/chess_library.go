package interfaces

import (
	"context"
	"io"

	"github.com/Tencent/WeKnora/internal/types"
)

// ChessLibraryService định nghĩa nghiệp vụ kho ván đấu & ngân hàng bài tập cờ vua.
type ChessLibraryService interface {
	// ---- Ván đấu ----
	ListGames(ctx context.Context, tenantID uint64, f types.ChessGameFilter) ([]*types.ChessGame, error)
	GetGame(ctx context.Context, tenantID uint64, id string) (*types.ChessGame, error)
	// GetGameBySlug giải mã wikilink [[game/<slug>]] về ván cờ.
	GetGameBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessGame, error)
	// GetGameBacklinks liệt kê trang wiki/bài giảng trỏ tới ván cờ này.
	GetGameBacklinks(ctx context.Context, tenantID uint64, slug string) ([]types.ChessBacklink, error)
	CreateGame(ctx context.Context, game *types.ChessGame) (*types.ChessGame, error)
	UpdateGame(ctx context.Context, game *types.ChessGame) (*types.ChessGame, error)
	// RenameGameSlug đổi slug ván sang newSlug (chuẩn hóa + đảm bảo duy nhất) và ghi
	// alias slug-cũ→mới để wikilink cũ vẫn sống.
	RenameGameSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessGame, error)
	DeleteGame(ctx context.Context, tenantID uint64, id string) error
	// ImportGames tách một PGN nhiều ván và tạo nhiều ChessGame; trả số ván đã thêm.
	ImportGames(ctx context.Context, tenantID uint64, pgn string) (int, error)

	// ---- Bài tập ----
	ListPuzzles(ctx context.Context, tenantID uint64, f types.ChessPuzzleFilter) ([]*types.ChessPuzzle, error)
	GetPuzzle(ctx context.Context, tenantID uint64, id string) (*types.ChessPuzzle, error)
	// GetPuzzleBySlug giải mã wikilink [[puzzle/<slug>]] về thế cờ/bài tập.
	GetPuzzleBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessPuzzle, error)
	// GetPuzzleBacklinks liệt kê trang wiki/bài giảng trỏ tới thế cờ này.
	GetPuzzleBacklinks(ctx context.Context, tenantID uint64, slug string) ([]types.ChessBacklink, error)
	CreatePuzzle(ctx context.Context, puzzle *types.ChessPuzzle) (*types.ChessPuzzle, error)
	UpdatePuzzle(ctx context.Context, puzzle *types.ChessPuzzle) (*types.ChessPuzzle, error)
	// RenamePuzzleSlug đổi slug bài tập sang newSlug + ghi alias slug-cũ→mới.
	RenamePuzzleSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessPuzzle, error)
	DeletePuzzle(ctx context.Context, tenantID uint64, id string) error
	RandomPuzzle(ctx context.Context, tenantID uint64, f types.ChessPuzzleFilter) (*types.ChessPuzzle, error)

	// ---- Thế cờ (Ngân hàng thế cờ) ----
	ListPositions(ctx context.Context, tenantID uint64, f types.ChessPositionFilter) ([]*types.ChessPosition, error)
	GetPosition(ctx context.Context, tenantID uint64, id string) (*types.ChessPosition, error)
	// GetPositionBySlug giải mã wikilink [[position/<slug>]] về thế cờ.
	GetPositionBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessPosition, error)
	// GetPositionBacklinks liệt kê trang wiki/bài giảng/thế cờ khác trỏ tới thế cờ này.
	GetPositionBacklinks(ctx context.Context, tenantID uint64, slug string) ([]types.ChessBacklink, error)
	CreatePosition(ctx context.Context, position *types.ChessPosition) (*types.ChessPosition, error)
	UpdatePosition(ctx context.Context, position *types.ChessPosition) (*types.ChessPosition, error)
	// RenamePositionSlug đổi slug thế cờ sang newSlug + ghi alias slug-cũ→mới.
	RenamePositionSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessPosition, error)
	DeletePosition(ctx context.Context, tenantID uint64, id string) error
	// ListPositionsByGame liệt kê các thế cờ đã trích từ MỘT ván cụ thể.
	ListPositionsByGame(ctx context.Context, tenantID uint64, gameID string) ([]*types.ChessPosition, error)

	// ---- Thư viện sách: Kệ ----
	ListShelves(ctx context.Context, tenantID uint64, f types.ChessShelfFilter) ([]*types.ChessShelf, error)
	GetShelf(ctx context.Context, tenantID uint64, id string) (*types.ChessShelf, error)
	// GetShelfBySlug giải mã slug kệ (kệ KHÔNG phải đích wikilink, chỉ điều hướng UI).
	GetShelfBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessShelf, error)
	CreateShelf(ctx context.Context, shelf *types.ChessShelf) (*types.ChessShelf, error)
	UpdateShelf(ctx context.Context, shelf *types.ChessShelf) (*types.ChessShelf, error)
	// RenameShelfSlug đổi slug kệ sang newSlug + ghi alias slug-cũ→mới.
	RenameShelfSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessShelf, error)
	DeleteShelf(ctx context.Context, tenantID uint64, id string) error
	// SetShelfBooks GHI ĐÈ toàn bộ danh sách sách trên một kệ theo đúng thứ tự
	// truyền vào (xóa-rồi-chèn-lại trong transaction, mẫu ReplaceForLesson).
	SetShelfBooks(ctx context.Context, tenantID uint64, shelfID string, bookIDs []string) error
	// ListShelvesOfBook liệt kê mọi kệ đang chứa một cuốn sách (hiển thị ở form sửa sách).
	ListShelvesOfBook(ctx context.Context, tenantID uint64, bookID string) ([]*types.ChessShelf, error)

	// ---- Thư viện sách: Sách ----
	ListBooks(ctx context.Context, tenantID uint64, f types.ChessBookFilter) ([]*types.ChessBook, error)
	GetBook(ctx context.Context, tenantID uint64, id string) (*types.ChessBook, error)
	// GetBookBySlug giải mã wikilink [[book/<slug>]] về sách.
	GetBookBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessBook, error)
	// GetBookBacklinks liệt kê trang wiki/bài giảng/chương khác trỏ tới sách này.
	GetBookBacklinks(ctx context.Context, tenantID uint64, slug string) ([]types.ChessBacklink, error)
	CreateBook(ctx context.Context, book *types.ChessBook) (*types.ChessBook, error)
	UpdateBook(ctx context.Context, book *types.ChessBook) (*types.ChessBook, error)
	// RenameBookSlug đổi slug sách sang newSlug + ghi alias slug-cũ→mới.
	RenameBookSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessBook, error)
	// DeleteBook xóa sách VÀ cascade: chương (kèm ref 2 chiều + gỡ index từng
	// chương), pivot kệ, lịch sử phiên bản, ảnh (cả bản ghi lẫn file vật lý).
	DeleteBook(ctx context.Context, tenantID uint64, id string) error

	// ---- Thư viện sách: Chương ----
	ListChapters(ctx context.Context, tenantID uint64, bookID string) ([]*types.ChessBookChapter, error)
	// SearchChapters tìm chương theo từ khóa (slug/title/CONTENT — khác
	// SearchLessons hiện KHÔNG tìm trong content) toàn tenant — autocomplete wikilink.
	SearchChapters(ctx context.Context, tenantID uint64, keyword string, limit int) ([]*types.ChessBookChapter, error)
	GetChapter(ctx context.Context, tenantID uint64, id string) (*types.ChessBookChapter, error)
	// GetChapterBySlug giải mã wikilink [[chapter/<slug>]] về chương.
	GetChapterBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessBookChapter, error)
	// GetChapterBacklinks liệt kê trang wiki/bài giảng/chương khác trỏ tới chương này.
	GetChapterBacklinks(ctx context.Context, tenantID uint64, slug string) ([]types.ChessBacklink, error)
	CreateChapter(ctx context.Context, chapter *types.ChessBookChapter) (*types.ChessBookChapter, error)
	// UpdateChapter cập nhật chương; nếu Title/Content thực sự đổi thì lưu bản
	// TRƯỚC khi ghi đè vào lịch sử phiên bản (summary là ghi chú thay đổi, tùy chọn).
	UpdateChapter(ctx context.Context, chapter *types.ChessBookChapter, summary string) (*types.ChessBookChapter, error)
	// RenameChapterSlug đổi slug chương sang newSlug + ghi alias slug-cũ→mới.
	RenameChapterSlug(ctx context.Context, tenantID uint64, id, newSlug string) (*types.ChessBookChapter, error)
	DeleteChapter(ctx context.Context, tenantID uint64, id string) error
	// ReorderChapters ghi lại sort_order theo đúng thứ tự chapterIDs truyền vào.
	ReorderChapters(ctx context.Context, tenantID uint64, bookID string, chapterIDs []string) error

	// ---- Thư viện sách: Lịch sử phiên bản chương ----
	ListChapterRevisions(ctx context.Context, tenantID uint64, chapterID string) ([]*types.ChessChapterRevision, error)
	GetChapterRevision(ctx context.Context, tenantID uint64, revisionID string) (*types.ChessChapterRevision, error)
	// RestoreChapterRevision ghi nội dung một bản cũ trở lại làm bản hiện tại
	// (bản thân thao tác khôi phục CŨNG tạo một bản phiên bản mới trước đó).
	RestoreChapterRevision(ctx context.Context, tenantID uint64, chapterID, revisionID string) (*types.ChessBookChapter, error)

	// ---- Thư viện sách: Ảnh chèn trong chương ----
	// UploadBookImage lưu file qua FileService rồi ghi bản ghi ChessBookImage.
	UploadBookImage(ctx context.Context, tenantID uint64, bookID, fileName, mime string, data []byte) (*types.ChessBookImage, error)
	// GetBookImage trả metadata + luồng đọc nội dung ảnh (caller PHẢI Close()).
	GetBookImage(ctx context.Context, tenantID uint64, imageID string) (*types.ChessBookImage, io.ReadCloser, error)

	// ---- Export / Import ----
	// ExportGamesPGN xuất các ván (theo filter) thành một PGN nhiều ván.
	ExportGamesPGN(ctx context.Context, tenantID uint64, f types.ChessGameFilter) (string, error)
	// ExportPuzzles xuất các bài tập (theo filter) để sao lưu/chia sẻ.
	ExportPuzzles(ctx context.Context, tenantID uint64, f types.ChessPuzzleFilter) ([]types.ChessPuzzleBundle, error)
	// ImportPuzzles nhập danh sách bài tập (luôn tạo mới); trả số bài đã thêm.
	ImportPuzzles(ctx context.Context, tenantID uint64, items []types.ChessPuzzleBundle) (int, error)
	// ExportPositions xuất các thế cờ (theo filter) để sao lưu/chia sẻ.
	ExportPositions(ctx context.Context, tenantID uint64, f types.ChessPositionFilter) ([]types.ChessPositionBundle, error)
	// ImportPositions nhập danh sách thế cờ (luôn tạo mới); trả số thế cờ đã thêm.
	ImportPositions(ctx context.Context, tenantID uint64, items []types.ChessPositionBundle) (int, error)
	// ExportBooks xuất các sách (kèm chương, theo filter) để sao lưu/chia sẻ.
	ExportBooks(ctx context.Context, tenantID uint64, f types.ChessBookFilter) ([]types.ChessBookBundle, error)
	// ImportBooks nhập danh sách sách (kèm chương), luôn tạo mới; trả số sách đã thêm.
	ImportBooks(ctx context.Context, tenantID uint64, items []types.ChessBookBundle) (int, error)

	// ReindexAll đẩy lại toàn bộ ván + bài tập của tenant vào KB tri thức cờ (chỉ
	// tác dụng khi CHESS_KB_INDEX bật). FAIL-LOUD nếu KB cờ chưa có embedding model.
	// Trả báo cáo trung thực (tổng / đã enqueue / lỗi) — "enqueued" ≠ "đã embed".
	ReindexAll(ctx context.Context, tenantID uint64) (*types.ChessReindexResult, error)
	// IndexStatus báo cáo trạng thái KB "Tri thức cờ vua" để chẩn đoán RAG cờ
	// (KB tồn tại?, có embedding model?, bao nhiêu doc completed/pending/failed).
	IndexStatus(ctx context.Context) (*types.ChessIndexStatus, error)
}

// ChessLibraryRepository định nghĩa thao tác lưu trữ kho ván & bài tập.
type ChessLibraryRepository interface {
	// ---- Ván đấu ----
	ListGames(ctx context.Context, tenantID uint64, f types.ChessGameFilter) ([]*types.ChessGame, error)
	GetGame(ctx context.Context, tenantID uint64, id string) (*types.ChessGame, error)
	GetGameBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessGame, error)
	// GameSlugs trả mọi slug ván sống của tenant (pool fuzzy-resolve).
	GameSlugs(ctx context.Context, tenantID uint64) ([]string, error)
	GameSlugExists(ctx context.Context, tenantID uint64, slug string) (bool, error)
	CreateGame(ctx context.Context, game *types.ChessGame) error
	CreateGames(ctx context.Context, games []*types.ChessGame) error
	UpdateGame(ctx context.Context, game *types.ChessGame) error
	// UpdateGameSlug chỉ đổi cột slug (tách riêng vì UpdateGame cố tình không đụng slug).
	UpdateGameSlug(ctx context.Context, tenantID uint64, id, slug string) error
	DeleteGame(ctx context.Context, tenantID uint64, id string) error

	// ---- Bài tập ----
	ListPuzzles(ctx context.Context, tenantID uint64, f types.ChessPuzzleFilter) ([]*types.ChessPuzzle, error)
	GetPuzzle(ctx context.Context, tenantID uint64, id string) (*types.ChessPuzzle, error)
	GetPuzzleBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessPuzzle, error)
	// PuzzleSlugs trả mọi slug bài tập sống của tenant (pool fuzzy-resolve).
	PuzzleSlugs(ctx context.Context, tenantID uint64) ([]string, error)
	PuzzleSlugExists(ctx context.Context, tenantID uint64, slug string) (bool, error)
	CreatePuzzle(ctx context.Context, puzzle *types.ChessPuzzle) error
	UpdatePuzzle(ctx context.Context, puzzle *types.ChessPuzzle) error
	// UpdatePuzzleSlug chỉ đổi cột slug (tách riêng như UpdateGameSlug).
	UpdatePuzzleSlug(ctx context.Context, tenantID uint64, id, slug string) error
	DeletePuzzle(ctx context.Context, tenantID uint64, id string) error
	RandomPuzzle(ctx context.Context, tenantID uint64, f types.ChessPuzzleFilter) (*types.ChessPuzzle, error)

	// ---- Thế cờ (Ngân hàng thế cờ) ----
	ListPositions(ctx context.Context, tenantID uint64, f types.ChessPositionFilter) ([]*types.ChessPosition, error)
	GetPosition(ctx context.Context, tenantID uint64, id string) (*types.ChessPosition, error)
	GetPositionBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessPosition, error)
	// PositionSlugs trả mọi slug thế cờ sống của tenant (pool fuzzy-resolve).
	PositionSlugs(ctx context.Context, tenantID uint64) ([]string, error)
	PositionSlugExists(ctx context.Context, tenantID uint64, slug string) (bool, error)
	CreatePosition(ctx context.Context, position *types.ChessPosition) error
	CreatePositions(ctx context.Context, positions []*types.ChessPosition) error
	UpdatePosition(ctx context.Context, position *types.ChessPosition) error
	// UpdatePositionSlug chỉ đổi cột slug (tách riêng như UpdateGameSlug).
	UpdatePositionSlug(ctx context.Context, tenantID uint64, id, slug string) error
	DeletePosition(ctx context.Context, tenantID uint64, id string) error
	ListPositionsByGame(ctx context.Context, tenantID uint64, gameID string) ([]*types.ChessPosition, error)
	// FindByFENKey tìm thế cờ trùng theo fen_key (phục vụ cảnh báo trùng khi tạo mới).
	FindByFENKey(ctx context.Context, tenantID uint64, fenKey string) ([]*types.ChessPosition, error)

	// ---- Thư viện sách: Kệ ----
	ListShelves(ctx context.Context, tenantID uint64, f types.ChessShelfFilter) ([]*types.ChessShelf, error)
	GetShelf(ctx context.Context, tenantID uint64, id string) (*types.ChessShelf, error)
	GetShelfBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessShelf, error)
	// ShelfSlugs trả mọi slug kệ sống của tenant (pool fuzzy-resolve).
	ShelfSlugs(ctx context.Context, tenantID uint64) ([]string, error)
	ShelfSlugExists(ctx context.Context, tenantID uint64, slug string) (bool, error)
	CreateShelf(ctx context.Context, shelf *types.ChessShelf) error
	UpdateShelf(ctx context.Context, shelf *types.ChessShelf) error
	// UpdateShelfSlug chỉ đổi cột slug (tách riêng như UpdateGameSlug).
	UpdateShelfSlug(ctx context.Context, tenantID uint64, id, slug string) error
	DeleteShelf(ctx context.Context, tenantID uint64, id string) error
	CountBooksOnShelf(ctx context.Context, tenantID uint64, shelfID string) (int64, error)

	// ---- Thư viện sách: nối kệ↔sách (nhiều-nhiều) ----
	// SetShelfBooks GHI ĐÈ toàn bộ danh sách sách trên một kệ (xóa-rồi-chèn-lại
	// trong transaction, giữ nguyên thứ tự bookIDs qua cột sort_order).
	SetShelfBooks(ctx context.Context, tenantID uint64, shelfID string, bookIDs []string) error
	// ListShelvesOfBook liệt kê mọi kệ đang chứa một cuốn sách.
	ListShelvesOfBook(ctx context.Context, tenantID uint64, bookID string) ([]*types.ChessShelf, error)
	// RemoveBookFromAllShelves gỡ một cuốn sách khỏi mọi kệ (khi xóa sách).
	RemoveBookFromAllShelves(ctx context.Context, tenantID uint64, bookID string) error
	// RemoveShelfBooks xóa toàn bộ liên kết của một kệ (khi xóa kệ) — KHÔNG đụng sách.
	RemoveShelfBooks(ctx context.Context, tenantID uint64, shelfID string) error

	// ---- Thư viện sách: Sách ----
	ListBooks(ctx context.Context, tenantID uint64, f types.ChessBookFilter) ([]*types.ChessBook, error)
	GetBook(ctx context.Context, tenantID uint64, id string) (*types.ChessBook, error)
	GetBookBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessBook, error)
	// BookSlugs trả mọi slug sách sống của tenant (pool fuzzy-resolve).
	BookSlugs(ctx context.Context, tenantID uint64) ([]string, error)
	BookSlugExists(ctx context.Context, tenantID uint64, slug string) (bool, error)
	CreateBook(ctx context.Context, book *types.ChessBook) error
	UpdateBook(ctx context.Context, book *types.ChessBook) error
	// UpdateBookSlug chỉ đổi cột slug (tách riêng như UpdateGameSlug).
	UpdateBookSlug(ctx context.Context, tenantID uint64, id, slug string) error
	DeleteBook(ctx context.Context, tenantID uint64, id string) error
	CountChapters(ctx context.Context, tenantID uint64, bookID string) (int64, error)

	// ---- Thư viện sách: Chương ----
	ListChapters(ctx context.Context, tenantID uint64, bookID string) ([]*types.ChessBookChapter, error)
	// SearchChapters tìm chương theo từ khóa (slug/title/content) toàn tenant.
	SearchChapters(ctx context.Context, tenantID uint64, keyword string, limit int) ([]*types.ChessBookChapter, error)
	GetChapter(ctx context.Context, tenantID uint64, id string) (*types.ChessBookChapter, error)
	GetChapterBySlug(ctx context.Context, tenantID uint64, slug string) (*types.ChessBookChapter, error)
	// ChapterSlugs trả mọi slug chương sống của tenant (pool fuzzy-resolve).
	ChapterSlugs(ctx context.Context, tenantID uint64) ([]string, error)
	ChapterSlugExists(ctx context.Context, tenantID uint64, slug string) (bool, error)
	CreateChapter(ctx context.Context, chapter *types.ChessBookChapter) error
	UpdateChapter(ctx context.Context, chapter *types.ChessBookChapter) error
	// UpdateChapterSlug chỉ đổi cột slug (tách riêng như UpdateGameSlug).
	UpdateChapterSlug(ctx context.Context, tenantID uint64, id, slug string) error
	DeleteChapter(ctx context.Context, tenantID uint64, id string) error
	DeleteChaptersByBook(ctx context.Context, tenantID uint64, bookID string) error
	// ReorderChapters ghi lại sort_order theo đúng thứ tự chapterIDs (transaction).
	ReorderChapters(ctx context.Context, tenantID uint64, bookID string, chapterIDs []string) error

	// ---- Thư viện sách: Lịch sử phiên bản chương ----
	CreateChapterRevision(ctx context.Context, rev *types.ChessChapterRevision) error
	ListChapterRevisions(ctx context.Context, tenantID uint64, chapterID string) ([]*types.ChessChapterRevision, error)
	GetChapterRevision(ctx context.Context, tenantID uint64, revisionID string) (*types.ChessChapterRevision, error)
	// CountChapterRevisions phục vụ tính revision_number tiếp theo.
	CountChapterRevisions(ctx context.Context, tenantID uint64, chapterID string) (int64, error)
	// DeleteChapterRevisionsByChapter xóa toàn bộ lịch sử của một chương (khi xóa chương).
	DeleteChapterRevisionsByChapter(ctx context.Context, tenantID uint64, chapterID string) error

	// ---- Thư viện sách: Ảnh chèn trong chương ----
	CreateBookImage(ctx context.Context, img *types.ChessBookImage) error
	GetBookImage(ctx context.Context, tenantID uint64, id string) (*types.ChessBookImage, error)
	// ListBookImagesByBook phục vụ xóa file vật lý khi cascade delete sách.
	ListBookImagesByBook(ctx context.Context, tenantID uint64, bookID string) ([]*types.ChessBookImage, error)
	DeleteBookImage(ctx context.Context, tenantID uint64, id string) error
}
