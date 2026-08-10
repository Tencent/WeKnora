package interfaces

import (
	"context"

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
}
