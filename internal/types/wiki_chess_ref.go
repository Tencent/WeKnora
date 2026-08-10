package types

import "time"

// Loại đối tượng cờ có thể là đích của wikilink.
const (
	ChessRefTypeGame     = "game"
	ChessRefTypePuzzle   = "puzzle"
	ChessRefTypeLesson   = "lesson"
	ChessRefTypeCourse   = "course"
	ChessRefTypePosition = "position"
	// ChessRefTypeBook là sách trong Thư viện sách cờ vua — chỉ là ĐÍCH wikilink
	// [[book/<slug>]] (mô tả sách là blurb ngắn, không phát wikilink — nhất
	// quán với ChessRefTypeCourse).
	ChessRefTypeBook = "book"
	// ChessRefTypeChapter là chương trong sách — vừa là ĐÍCH [[chapter/<slug>]]
	// vừa là NGUỒN (nội dung chương có thể chứa wikilink, xem ChessRefSourceChapter).
	ChessRefTypeChapter = "chapter"
)

// Loại NGUỒN tham chiếu (cột source_type trong wiki_chess_refs).
const (
	ChessRefSourceWiki     = "wiki"
	ChessRefSourceLesson   = "lesson"
	ChessRefSourcePosition = "position"
	// ChessRefSourceChapter đánh dấu tham chiếu cờ PHÁT ra từ nội dung một
	// chương sách (page_slug = slug chương, kb_id rỗng — cùng mẫu lesson/position).
	ChessRefSourceChapter = "chapter"
)

// WikiChessRef ghi nhận một tham chiếu từ trang wiki tới một đối tượng cờ
// (ván/thế cờ/bài giảng) qua wikilink [[game/<slug>]]. Bảng join thuộc về phía
// wiki (giống wiki_page_issues), giúp backlink + đồ thị mà không nhồi cột vào
// các bảng chess (vốn lệch phạm vi: chess theo tenant, wiki theo KB).
type WikiChessRef struct {
	ID         string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID   uint64    `json:"tenant_id" gorm:"index"`
	SourceType string    `json:"source_type" gorm:"column:source_type;type:varchar(16);default:wiki"`
	KBID       string    `json:"kb_id" gorm:"column:kb_id;type:varchar(36);index"`
	PageSlug   string    `json:"page_slug" gorm:"type:varchar(255)"`
	ChessType  string    `json:"chess_type" gorm:"type:varchar(16)"`
	ChessSlug  string    `json:"chess_slug" gorm:"type:varchar(255)"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// TableName ánh xạ tới bảng wiki_chess_refs.
func (WikiChessRef) TableName() string { return "wiki_chess_refs" }

// ChessBacklink là một nguồn (trang wiki, bài giảng, HOẶC thế cờ khác) đang trỏ
// tới đối tượng cờ, để hiển thị "Được liên kết bởi". source_type quyết định
// cách điều hướng: 'wiki' → kb_id + page_slug (mở trang wiki); 'lesson' →
// page_slug là slug bài giảng (mở trong Khóa học); 'position' → page_slug là
// slug thế cờ nguồn (mở trong Ngân hàng thế cờ) — chú giải (annotation) của một
// thế cờ có thể chứa wikilink trỏ sang ván/thế cờ khác.
type ChessBacklink struct {
	SourceType string `json:"source_type"`
	KBID       string `json:"kb_id"`
	PageSlug   string `json:"page_slug"`
	PageTitle  string `json:"page_title"`
}
