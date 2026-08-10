package types

import "time"

// ChessKBIndex ánh xạ một đối tượng cờ (game/puzzle/lesson) tới bản ghi Knowledge
// đã được index vào "KB tri thức cờ vua", để trợ lý truy hồi nội dung qua RAG.
type ChessKBIndex struct {
	ID          string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID    uint64    `json:"tenant_id" gorm:"index"`
	ChessType   string    `json:"chess_type" gorm:"type:varchar(16)"`
	ChessSlug   string    `json:"chess_slug" gorm:"type:varchar(255)"`
	KnowledgeID string    `json:"knowledge_id" gorm:"type:varchar(36)"`
	KBID        string    `json:"kb_id" gorm:"column:kb_id;type:varchar(36)"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName ánh xạ tới bảng chess_kb_index.
func (ChessKBIndex) TableName() string { return "chess_kb_index" }

// ChessIndexStatus tóm tắt trạng thái KB "Tri thức cờ vua" để CHẨN ĐOÁN RAG cờ.
// Đọc qua GET /chess/library/index-status. Mục đích: biến phỏng đoán "vì sao RAG
// rỗng" thành dữ liệu — không cần gõ SQL trên production.
type ChessIndexStatus struct {
	Enabled             bool   `json:"enabled"`              // CHESS_KB_INDEX bật?
	KBExists            bool   `json:"kb_exists"`            // KB "Tri thức cờ vua" tồn tại?
	KBID                string `json:"kb_id"`                // ID KB cờ (rỗng nếu chưa có)
	EmbeddingModelID    string `json:"embedding_model_id"`   // model embedding KB cờ đang dùng
	EmbeddingConfigured bool   `json:"embedding_configured"` // KB có embedding model ID?
	// VectorSearchable: KB có bật vector HOẶC keyword index không. ĐÂY là điều kiện
	// để knowledge_search "nhìn thấy" KB (capability filter) VÀ để embedding thực sự
	// chạy lúc index. false = NGUYÊN NHÂN GỐC: KB bị loại khỏi search + chunk không embed.
	VectorEnabled  bool   `json:"vector_enabled"`
	KeywordEnabled bool   `json:"keyword_enabled"`
	Searchable     bool   `json:"searchable"`    // vector || keyword (capability để agent search)
	Total          int    `json:"total"`         // tổng Knowledge trong KB cờ
	Completed      int    `json:"completed"`     // parse_status=completed
	Pending        int    `json:"pending"`       // pending/processing/finalizing
	Failed         int    `json:"failed"`        // parse_status=failed
	EnabledDocs    int    `json:"enabled_docs"`  // enable_status=enabled (truy hồi được)
	DisabledDocs   int    `json:"disabled_docs"` // enable_status≠enabled (KHÔNG truy hồi)
	SampleError    string `json:"sample_error"`  // mẫu error_message của bản ghi failed
}

// ChessReindexResult báo cáo TRUNG THỰC kết quả reindex (POST /chess/library/reindex).
// Khác bản cũ chỉ trả 2 số "đã index": ở đây tách rõ tổng vs số đã enqueue vs lỗi —
// vì embedding chạy NỀN, "enqueued" không đồng nghĩa "đã embed". Kiểm tra hoàn tất
// embedding qua ChessIndexStatus (completed) sau ~1 phút.
type ChessReindexResult struct {
	GamesTotal     int      `json:"games_total"`      // số ván của tenant
	PuzzlesTotal   int      `json:"puzzles_total"`    // số bài tập của tenant
	PositionsTotal int      `json:"positions_total"`  // số thế cờ của tenant
	Enqueued       int      `json:"enqueued"`         // số bản ghi đã đẩy đi index (chờ embed nền)
	Failed         int      `json:"failed"`           // số bản ghi lỗi ngay khi đẩy
	Errors         []string `json:"errors,omitempty"` // mẫu lỗi (tối đa 5)
}
