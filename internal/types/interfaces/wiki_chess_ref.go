package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WikiChessRefRepository quản lý bảng liên kết wiki -> đối tượng cờ.
type WikiChessRefRepository interface {
	// ReplaceForPage thay toàn bộ tham chiếu cờ của một trang wiki (xóa cũ + chèn mới).
	ReplaceForPage(ctx context.Context, tenantID uint64, kbID, pageSlug string, refs []types.WikiChessRef) error
	// ReplaceForLesson thay toàn bộ tham chiếu cờ TỪ nội dung một bài giảng.
	ReplaceForLesson(ctx context.Context, tenantID uint64, lessonSlug string, refs []types.WikiChessRef) error
	// ReplaceForPosition thay toàn bộ tham chiếu cờ TỪ chú giải một thế cờ (nguồn
	// position; kb_id rỗng, page_slug = slug thế cờ — cùng mẫu ReplaceForLesson).
	ReplaceForPosition(ctx context.Context, tenantID uint64, positionSlug string, refs []types.WikiChessRef) error
	// ReplaceForChapter thay toàn bộ tham chiếu cờ TỪ nội dung một chương sách
	// (nguồn chapter; kb_id rỗng, page_slug = slug chương — cùng mẫu ReplaceForLesson).
	ReplaceForChapter(ctx context.Context, tenantID uint64, chapterSlug string, refs []types.WikiChessRef) error
	// DeleteForPage xóa mọi tham chiếu cờ của một trang wiki (khi xóa trang).
	DeleteForPage(ctx context.Context, kbID, pageSlug string) error
	// DeleteForLesson xóa mọi tham chiếu cờ TỪ một bài giảng (khi xóa bài giảng).
	DeleteForLesson(ctx context.Context, tenantID uint64, lessonSlug string) error
	// DeleteForPosition xóa mọi tham chiếu cờ TỪ một thế cờ (khi xóa thế cờ).
	DeleteForPosition(ctx context.Context, tenantID uint64, positionSlug string) error
	// DeleteForChapter xóa mọi tham chiếu cờ TỪ một chương sách (khi xóa chương).
	DeleteForChapter(ctx context.Context, tenantID uint64, chapterSlug string) error
	// DeleteForChess xóa mọi tham chiếu trỏ tới một đối tượng cờ (khi xóa đối tượng).
	DeleteForChess(ctx context.Context, tenantID uint64, chessType, chessSlug string) error
	// ListBacklinks liệt kê trang/bài giảng đang trỏ tới một đối tượng cờ.
	ListBacklinks(ctx context.Context, tenantID uint64, chessType, chessSlug string) ([]types.ChessBacklink, error)
	// ListByKB lấy toàn bộ tham chiếu cờ của một KB (dựng đồ thị).
	ListByKB(ctx context.Context, kbID string) ([]types.WikiChessRef, error)
}
