package types

import "time"

// Trạng thái xuất bản NỘI BỘ của sách (cột chess_books.status).
const (
	// ChessBookStatusDraft là bản thảo — KHÔNG được index vào KB tri thức cờ.
	ChessBookStatusDraft = "draft"
	// ChessBookStatusPublished là đã duyệt — được index (nếu CHESS_KB_INDEX bật).
	ChessBookStatusPublished = "published"
)

// chess_book.go định nghĩa thực thể "Thư viện sách cờ vua" — thực thể thứ 6
// (bên cạnh game/puzzle/lesson/course/position): Kệ (ChessShelf) → Sách
// (ChessBook) → Chương (ChessBookChapter). Đây là hệ SOẠN THẢO nội bộ (không
// phải kho ebook PDF/EPUB để đọc) — nội dung chương là markdown có thể chứa
// FEN/PGN/wikilink, cùng khuôn ChessLesson nhưng có thêm lịch sử phiên bản.
//
// Kệ↔sách là NHIỀU-NHIỀU (một cuốn sách nằm được nhiều kệ: chuyên đề, giai
// đoạn, bộ sách, cấp độ — mẫu bookshelves_books của BookStack). Chương là
// danh sách 1 CẤP; trường Part chỉ là nhãn chuỗi để gộp hiển thị theo "Phần",
// không phải bảng riêng.

// ChessShelf là một kệ sách — tập hợp sách theo chuyên đề/chủ đề/giai đoạn/bộ
// sách/cấp độ. KHÔNG phải đích wikilink (chỉ điều hướng UI).
type ChessShelf struct {
	// ID là định danh duy nhất (UUID).
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`
	// TenantID là tenant sở hữu.
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	// Slug là định danh thân thiện (duy nhất theo tenant); dùng cho URL/API,
	// KHÔNG phải đích wikilink.
	Slug string `json:"slug" gorm:"column:slug;type:varchar(255)"`
	// Title là tên kệ (vd "Tàn cuộc", "Bộ STEP Trainer").
	Title string `json:"title" gorm:"type:varchar(255)"`
	// Kind là loại kệ: "theme" (chuyên đề) | "phase" (giai đoạn ván cờ) |
	// "series" (bộ sách) | "level" (cấp độ) | "" (không phân loại).
	Kind string `json:"kind" gorm:"type:varchar(16)"`
	// Description là mô tả kệ (markdown ngắn).
	Description string `json:"description" gorm:"type:text"`
	// CoverURL là ảnh bìa kệ (tùy chọn).
	CoverURL string `json:"cover_url" gorm:"column:cover_url;type:varchar(512)"`
	// SortOrder là thứ tự hiển thị.
	SortOrder int `json:"sort_order" gorm:"default:0"`
	// CreatedAt / UpdatedAt là thời gian tạo/cập nhật.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// BookCount là số sách trên kệ (tính toán, không lưu).
	BookCount int64 `json:"book_count" gorm:"-"`
}

// TableName ánh xạ tới bảng chess_shelves.
func (ChessShelf) TableName() string { return "chess_shelves" }

// ChessBook là một cuốn sách cờ vua (biên soạn trong app — markdown + FEN +
// ván cờ + ảnh, KHÔNG phải file PDF/EPUB để đọc). Chứa nhiều ChessBookChapter.
type ChessBook struct {
	// ID là định danh duy nhất (UUID).
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`
	// TenantID là tenant sở hữu.
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	// Slug là định danh thân thiện (duy nhất theo tenant) làm đích wikilink
	// [[book/<slug>]]. Sinh ở tầng service khi tạo; ổn định sau đó.
	Slug string `json:"slug" gorm:"column:slug;type:varchar(255)"`
	// Title là tên sách.
	Title string `json:"title" gorm:"type:varchar(255)"`
	// Subtitle là phụ đề (tùy chọn).
	Subtitle string `json:"subtitle" gorm:"type:varchar(255)"`
	// Author / Translator / Publisher / Year / ISBN / Language là metadata thư
	// mục (mọi trường tùy chọn).
	Author     string `json:"author" gorm:"type:varchar(255)"`
	Translator string `json:"translator" gorm:"type:varchar(255)"`
	Publisher  string `json:"publisher" gorm:"type:varchar(255)"`
	Year       string `json:"year" gorm:"type:varchar(8)"`
	ISBN       string `json:"isbn" gorm:"column:isbn;type:varchar(32)"`
	Language   string `json:"language" gorm:"type:varchar(16)"`
	// Level là cấp độ trong lộ trình 6 cấp Dương Sinh: "tot"|"ma"|"tuong"|"xe"|
	// "hau"|"vua" (rỗng = mọi cấp).
	Level string `json:"level" gorm:"type:varchar(16);index"`
	// Phase là giai đoạn ván cờ sách tập trung: "opening"|"middlegame"|
	// "endgame"|"tactics"|"rules"|"general".
	Phase string `json:"phase" gorm:"type:varchar(16);index"`
	// ECO là mã khai cuộc (nếu sách chuyên khai cuộc).
	ECO string `json:"eco" gorm:"type:varchar(8)"`
	// Status là trạng thái xuất bản NỘI BỘ: "draft" (bản thảo, KHÔNG index RAG)
	// | "published" (đã duyệt, index vào KB tri thức cờ nếu CHESS_KB_INDEX bật).
	Status string `json:"status" gorm:"type:varchar(16);default:draft;index"`
	// Description là mô tả/giới thiệu sách (markdown ngắn — KHÔNG phát wikilink,
	// nhất quán với ChessCourse.Description).
	Description string `json:"description" gorm:"type:text"`
	// CoverURL là ảnh bìa sách (tùy chọn).
	CoverURL string `json:"cover_url" gorm:"column:cover_url;type:varchar(512)"`
	// Tags là CSV nhẹ (khớp mẫu Tags của ChessPosition).
	Tags string `json:"tags" gorm:"type:varchar(255)"`
	// SortOrder là thứ tự hiển thị mặc định (trong danh sách không lọc theo kệ).
	SortOrder int `json:"sort_order" gorm:"default:0"`
	// CreatedAt / UpdatedAt là thời gian tạo/cập nhật.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// ChapterCount là số chương (tính toán, không lưu).
	ChapterCount int64 `json:"chapter_count" gorm:"-"`
}

// TableName ánh xạ tới bảng chess_books.
func (ChessBook) TableName() string { return "chess_books" }

// ChessShelfBook là bản ghi nối kệ↔sách (nhiều-nhiều, mẫu bookshelves_books
// của BookStack — một cuốn sách nằm được nhiều kệ).
type ChessShelfBook struct {
	TenantID  uint64    `json:"tenant_id" gorm:"index"`
	ShelfID   string    `json:"shelf_id" gorm:"column:shelf_id;type:varchar(36);primaryKey"`
	BookID    string    `json:"book_id" gorm:"column:book_id;type:varchar(36);primaryKey"`
	SortOrder int       `json:"sort_order" gorm:"default:0"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName ánh xạ tới bảng chess_shelf_books.
func (ChessShelfBook) TableName() string { return "chess_shelf_books" }

// ChessBookChapter là một chương trong sách — danh sách 1 CẤP (Part chỉ là
// nhãn chuỗi để gộp hiển thị theo "Phần", không phải quan hệ cha-con thật).
// Nội dung là markdown — có thể nhúng khối ```chess (FEN/PGN) và wikilink
// [[...]]/![[...]], cùng khuôn ChessLesson.
type ChessBookChapter struct {
	// ID là định danh duy nhất (UUID).
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`
	// TenantID là tenant sở hữu.
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	// Slug là định danh thân thiện (duy nhất theo TOÀN TENANT, không scope theo
	// sách — bắt buộc vì [[chapter/<slug>]] resolve toàn tenant) làm đích
	// wikilink [[chapter/<slug>]].
	Slug string `json:"slug" gorm:"column:slug;type:varchar(255)"`
	// BookID là sách chứa chương này.
	BookID string `json:"book_id" gorm:"column:book_id;type:varchar(36);index"`
	// Part là nhãn "Phần" để gộp hiển thị (vd "Phần I — Tàn cuộc Tốt"); rỗng =
	// chương không thuộc phần nào, hiển thị trực tiếp trong danh sách sách.
	Part string `json:"part" gorm:"type:varchar(255)"`
	// Title là tên chương.
	Title string `json:"title" gorm:"type:varchar(255);not null"`
	// Content là nội dung markdown (có thể chứa khối ```chess + wikilink).
	Content string `json:"content" gorm:"type:text"`
	// FEN là thế cờ minh họa chính của chương (tùy chọn).
	FEN string `json:"fen" gorm:"type:varchar(128)"`
	// Level là cấp độ 6 bậc Dương Sinh (tùy chọn, có thể khác cấp của sách).
	Level string `json:"level" gorm:"type:varchar(16)"`
	// SortOrder là thứ tự chương trong sách.
	SortOrder int `json:"sort_order" gorm:"default:0"`
	// CreatedAt / UpdatedAt là thời gian tạo/cập nhật.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName ánh xạ tới bảng chess_book_chapters.
func (ChessBookChapter) TableName() string { return "chess_book_chapters" }

// ChessBookImage là một ảnh đã upload để chèn vào nội dung chương (markdown
// tham chiếu qua URL ổn định GET /chess/books/images/:id — KHÔNG dùng
// presigned URL có hạn dùng vì sẽ chết trong nội dung lưu lâu dài).
type ChessBookImage struct {
	ID        string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64    `json:"tenant_id" gorm:"index"`
	BookID    string    `json:"book_id" gorm:"column:book_id;type:varchar(36);index"`
	Path      string    `json:"-" gorm:"type:varchar(512)"` // provider:// path (FileService) — không lộ ra API
	FileName  string    `json:"file_name" gorm:"column:file_name;type:varchar(255)"`
	Mime      string    `json:"mime" gorm:"type:varchar(64)"`
	Size      int64     `json:"size" gorm:"default:0"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName ánh xạ tới bảng chess_book_images.
func (ChessBookImage) TableName() string { return "chess_book_images" }

// ChessChapterRevision là một bản PRE-IMAGE của chương (lưu TRƯỚC khi ghi đè),
// chỉ tạo khi Title/Content thực sự đổi — tránh rác lịch sử khi chỉ sửa
// sort_order/part. Cho phép xem lại & khôi phục (mẫu page_revisions BookStack,
// rút gọn: không phân biệt version/update_draft vì sách ở đây không có draft
// theo trang, chỉ có Status của cả cuốn).
type ChessChapterRevision struct {
	ID             string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID       uint64    `json:"tenant_id" gorm:"index"`
	ChapterID      string    `json:"chapter_id" gorm:"column:chapter_id;type:varchar(36);index"`
	RevisionNumber int       `json:"revision_number" gorm:"column:revision_number;default:0"`
	Title          string    `json:"title" gorm:"type:varchar(255)"`
	Content        string    `json:"content" gorm:"type:text"`
	Summary        string    `json:"summary" gorm:"type:varchar(255)"`
	CreatedBy      string    `json:"created_by" gorm:"column:created_by;type:varchar(64)"`
	CreatedAt      time.Time `json:"created_at"`
}

// TableName ánh xạ tới bảng chess_chapter_revisions.
func (ChessChapterRevision) TableName() string { return "chess_chapter_revisions" }

// ---- Filter ----

// ChessShelfFilter là bộ lọc khi liệt kê kệ.
type ChessShelfFilter struct {
	Kind    string
	Keyword string
}

// ChessBookFilter là bộ lọc khi liệt kê sách.
type ChessBookFilter struct {
	// ShelfID lọc sách thuộc MỘT kệ cụ thể (qua bảng nối chess_shelf_books).
	// Rỗng = không lọc theo kệ.
	ShelfID string
	Level   string
	Phase   string
	Status  string
	// Keyword là tìm kiếm tự do (ILIKE trên slug/title/author/tags).
	Keyword string
}

// ---- Bundle export/import (không kèm ID/slug/tenant để import luôn tạo mới) ----

// ChessChapterBundle là gói export/import 1 chương.
type ChessChapterBundle struct {
	Part      string `json:"part"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	FEN       string `json:"fen"`
	Level     string `json:"level"`
	SortOrder int    `json:"sort_order"`
}

// ChessBookBundle là gói export/import 1 sách kèm chương.
type ChessBookBundle struct {
	Title       string               `json:"title"`
	Subtitle    string               `json:"subtitle"`
	Author      string               `json:"author"`
	Translator  string               `json:"translator"`
	Publisher   string               `json:"publisher"`
	Year        string               `json:"year"`
	ISBN        string               `json:"isbn"`
	Language    string               `json:"language"`
	Level       string               `json:"level"`
	Phase       string               `json:"phase"`
	ECO         string               `json:"eco"`
	Status      string               `json:"status"`
	Description string               `json:"description"`
	CoverURL    string               `json:"cover_url"`
	Tags        string               `json:"tags"`
	SortOrder   int                  `json:"sort_order"`
	Chapters    []ChessChapterBundle `json:"chapters"`
}
