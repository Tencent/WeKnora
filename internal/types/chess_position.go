package types

import "time"

// ChessPosition là MỘT thế cờ trong "Ngân hàng thế cờ" — khác ChessPuzzle (bài
// TẬP có lời giải, để LUYỆN), position là thế cờ THAM CHIẾU để DẠY/trích dẫn/
// phân tích: tàn cuộc lý thuyết, tabiya khai cuộc, mô-típ chiến thuật/cấu trúc
// tốt. CỐ Ý cho phép FEN không có quân Vua (thế cờ giản lược dạy trẻ mới học) —
// validate ở tầng service dùng chess.NormalizeFEN/chess.ValidateFEN (không đòi
// hỏi luật cờ đầy đủ), KHÔNG được đi qua chess.js ở frontend hay
// notnil.NewGame/engine ở backend (những đường đó giả định đủ 2 Vua).
type ChessPosition struct {
	// ID là định danh duy nhất (UUID).
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`
	// TenantID là tenant sở hữu.
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	// Slug là định danh thân thiện (duy nhất theo tenant) làm đích wikilink
	// [[position/<slug>]]. Sinh ở tầng service khi tạo; ổn định sau đó.
	Slug string `json:"slug" gorm:"column:slug;type:varchar(255)"`
	// Title là tiêu đề thế cờ (vd "Vua+Xe đấu Vua — thế thắng cơ bản").
	Title string `json:"title" gorm:"type:varchar(255)"`
	// FEN là thế cờ, đã chuẩn hóa đủ 6 trường qua chess.NormalizeFEN.
	FEN string `json:"fen" gorm:"type:varchar(128);not null"`
	// FENKey là khóa phát hiện trùng lặp (chess.FENKey: 4 trường đầu, bỏ 2 bộ
	// đếm nước) — chỉ để CẢNH BÁO khi tạo mới, không chặn (một thế cờ có thể
	// hợp lý xuất hiện ở nhiều mục đích dạy khác nhau).
	FENKey string `json:"fen_key" gorm:"column:fen_key;type:varchar(96)"`
	// Category là phân loại: "tabiya" | "endgame" | "structure" | "motif" |
	// "model" | "basic".
	Category string `json:"category" gorm:"type:varchar(32);index"`
	// Level là cấp độ trong lộ trình 6 cấp Dương Sinh: "tot"|"ma"|"tuong"|"xe"|
	// "hau"|"vua".
	Level string `json:"level" gorm:"type:varchar(16);index"`
	// ECO là mã khai cuộc (nối thế cờ tabiya với khai cuộc/chess_lookup_opening).
	ECO string `json:"eco" gorm:"type:varchar(8)"`
	// SideToMove là bên đang đi ("w"/"b"), suy ra từ FEN lúc lưu.
	SideToMove string `json:"side_to_move" gorm:"column:side_to_move;type:varchar(1)"`
	// Orientation là hướng bàn cờ mặc định khi hiển thị để dạy ("w"/"b").
	Orientation string `json:"orientation" gorm:"type:varchar(1)"`
	// Assessment là nhãn đánh giá HLV tự đặt (vd "Trắng thắng", "Hòa", "Trắng ưu").
	Assessment string `json:"assessment" gorm:"type:varchar(64)"`
	// Annotation là chú giải markdown — ĐƯỢC chứa wikilink [[...]]/![[...]] để
	// thế cờ trở thành nguồn phát liên kết (xem syncPositionChessRefs).
	Annotation string `json:"annotation" gorm:"type:text"`
	// Tags là CSV nhẹ (khớp mẫu Theme của ChessPuzzle).
	Tags string `json:"tags" gorm:"type:varchar(255)"`
	// Source là nguồn (tùy chọn, vd tên sách/giáo trình).
	Source string `json:"source" gorm:"type:varchar(255)"`
	// SourceGameID là ID ván cờ đã trích thế cờ này ra (rỗng nếu không trích từ ván).
	SourceGameID string `json:"source_game_id" gorm:"column:source_game_id;type:varchar(36);index"`
	// SourcePly là thế cờ này ứng với sau nửa-nước thứ mấy của ván nguồn.
	SourcePly int `json:"source_ply" gorm:"column:source_ply;default:0"`
	// CreatedAt / UpdatedAt là thời gian tạo/cập nhật.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName ánh xạ tới bảng chess_positions.
func (ChessPosition) TableName() string { return "chess_positions" }

// ChessPositionFilter là bộ lọc khi liệt kê thế cờ.
type ChessPositionFilter struct {
	Category string
	Level    string
	ECO      string
	// SourceGameID lọc các thế cờ đã trích từ MỘT ván cụ thể (panel "Thế cờ đã
	// trích" trong Kho ván). Rỗng = không lọc theo ván nguồn.
	SourceGameID string
	// Keyword là tìm kiếm tự do (ILIKE trên slug/title/tags) — dùng cho
	// autocomplete wikilink. Rỗng = không lọc.
	Keyword string
}

// ChessPositionBundle là gói export/import 1 thế cờ (không kèm ID/slug/tenant
// để khi import luôn tạo mới trong tenant đích) — cùng khuôn với ChessPuzzleBundle.
type ChessPositionBundle struct {
	Title       string `json:"title"`
	FEN         string `json:"fen"`
	Category    string `json:"category"`
	Level       string `json:"level"`
	ECO         string `json:"eco"`
	Orientation string `json:"orientation"`
	Assessment  string `json:"assessment"`
	Annotation  string `json:"annotation"`
	Tags        string `json:"tags"`
	Source      string `json:"source"`
}
