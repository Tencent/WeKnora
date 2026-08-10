package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

var chessLookupPositionTool = BaseTool{
	name: ToolChessLookupPosition,
	description: `Tra một thế cờ MẪU từ Ngân hàng thế cờ để dạy/minh họa (KHÁC
chess_generate_puzzle — puzzle có lời giải để LUYỆN; position là thế cờ THAM
CHIẾU để DẠY/trích dẫn, có thể không có quân Vua nếu là thế cờ giản lược dạy
trẻ mới học).

## Khi nào dùng
- Người dùng muốn minh họa một khái niệm (tàn cuộc, tabiya khai cuộc, mô-típ
  chiến thuật, cấu trúc tốt) bằng một thế cờ mẫu có sẵn trong ngân hàng.
- Có thể lọc theo phân loại (category) và/hoặc cấp độ (level, 6 bậc Tốt→Vua).

## Đầu vào
- category: phân loại (tùy chọn): "tabiya" | "endgame" | "structure" | "motif" | "model" | "basic".
- level: cấp độ (tùy chọn): "tot" | "ma" | "tuong" | "xe" | "hau" | "vua".
- keyword: từ khóa tự do (tùy chọn), khớp tiêu đề/thẻ.

Ưu tiên lấy từ ngân hàng thế cờ của tenant (DB), lọc nới dần nếu không khớp
đủ điều kiện; nếu kho trống thì dùng bộ thế cờ mẫu nhúng sẵn. Trả về một bàn
cờ tương tác kèm chú giải (nếu có). KHÔNG gọi engine phân tích thế cờ này —
thế cờ mẫu có thể không có quân Vua nên phân tích luật cờ sẽ vô nghĩa/lỗi.`,
	schema: utils.GenerateSchema[ChessLookupPositionInput](),
}

// ChessLookupPositionInput là tham số cho tool chess_lookup_position.
type ChessLookupPositionInput struct {
	Category string `json:"category,omitempty" jsonschema:"Phân loại: tabiya | endgame | structure | motif | model | basic (tùy chọn)"`
	Level    string `json:"level,omitempty" jsonschema:"Cấp độ: tot | ma | tuong | xe | hau | vua (tùy chọn)"`
	Keyword  string `json:"keyword,omitempty" jsonschema:"Từ khóa tự do, khớp tiêu đề/thẻ (tùy chọn)"`
}

// PositionSource là nguồn thế cờ từ Ngân hàng thế cờ (DB). Interface tối giản để
// tool không phụ thuộc trực tiếp tầng service; ChessLibraryService (đã có
// ListPositions) thỏa mãn interface này mà không cần khai báo tường minh.
type PositionSource interface {
	ListPositions(ctx context.Context, tenantID uint64, f types.ChessPositionFilter) ([]*types.ChessPosition, error)
}

// ChessLookupPositionTool tra thế cờ mẫu từ Ngân hàng thế cờ (DB), fallback bộ nhúng.
type ChessLookupPositionTool struct {
	BaseTool
	source PositionSource
}

// NewChessLookupPositionTool tạo tool chess_lookup_position. source có thể nil
// (vd khi library chưa cấu hình) → tool dùng bộ thế cờ nhúng sẵn.
func NewChessLookupPositionTool(source PositionSource) *ChessLookupPositionTool {
	return &ChessLookupPositionTool{BaseTool: chessLookupPositionTool, source: source}
}

// chessSamplePosition là một thế cờ mẫu nhúng sẵn dùng làm FALLBACK khi ngân
// hàng thế cờ trống/không khớp. FEN được kiểm tra hợp lệ bằng test.
type chessSamplePosition struct {
	fen        string
	title      string
	category   string
	level      string
	annotation string
}

// embeddedPositions — bộ thế cờ mẫu tối giản: vài tàn cuộc lý thuyết kinh điển
// + một thế cờ giản lược KHÔNG có quân Vua (minh họa ranh giới "cho phép thiếu
// Vua" ngay trong bộ mẫu, không chỉ trong test).
var embeddedPositions = []chessSamplePosition{
	{
		fen:        "8/8/8/4k3/8/4K3/4P3/8 w - - 0 1",
		title:      "Vua+Tốt đấu Vua — thế đối diện (đối ngẫu)",
		category:   "endgame",
		level:      "xe",
		annotation: "Trắng đi. Minh họa quy tắc \"đối diện\" (opposition): Vua Trắng cần giành quyền đối diện để hộ tống Tốt phong Hậu.",
	},
	{
		fen:        "8/8/8/8/8/2k5/8/R2K4 w - - 0 1",
		title:      "Vua+Xe đấu Vua — thế thắng cơ bản",
		category:   "endgame",
		level:      "hau",
		annotation: "Trắng đi. Kỹ thuật ép Vua đối phương ra biên bàn cờ bằng Xe, không để lọt \"nước chiếu hụt\" (stalemate).",
	},
	{
		fen:        "rnbqkb1r/pp1p1ppp/5n2/2p1p3/4P3/5N2/PPPP1PPP/RNBQKB1R w KQkq - 2 4",
		title:      "Tabiya Ván Ý (Italian Game) sau 3 nước mở đầu",
		category:   "tabiya",
		level:      "tuong",
		annotation: "Thế cờ mẫu sau 1.e4 e5 2.Nf3 Nc6 3.Bc4 — nền tảng để so sánh các biến của khai cuộc Ý.",
	},
	{
		fen:        "8/8/8/3p4/4P3/8/8/8 w - - 0 1",
		title:      "Cách Tốt ăn quân (dạy trẻ mới học)",
		category:   "basic",
		level:      "tot",
		annotation: "Thế cờ giản lược KHÔNG có Vua — chỉ minh họa cách Tốt Trắng (e4) ăn chéo Tốt Đen (d5). Dùng cho học viên vừa học luật đi quân.",
	},
}

// levelToDB/categoryToDB không cần chuẩn hóa phức tạp như difficulty của puzzle
// — người gọi (LLM) đã được mô tả rõ tập giá trị hợp lệ trong schema; chỉ cần
// strings.ToLower + TrimSpace để khoan dung sai khác hoa/thường.
func normLower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Execute tra một thế cờ mẫu: ưu tiên ngân hàng thế cờ thật, fallback bộ nhúng.
func (t *ChessLookupPositionTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var input ChessLookupPositionInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Không đọc được tham số: %v", err)}, err
	}

	if p := t.pickFromBank(ctx, normLower(input.Category), normLower(input.Level), strings.TrimSpace(input.Keyword)); p != nil {
		title := p.Title
		if strings.TrimSpace(title) == "" {
			title = p.Slug
		}
		return positionResult(p.FEN, title, p.Category, p.Level, p.Annotation), nil
	}

	return t.pickEmbedded(normLower(input.Category), normLower(input.Level), normLower(input.Keyword)), nil
}

// pickFromBank thử lấy một thế cờ từ kho thật theo bộ lọc nới dần: (category +
// level + keyword) → (category + level) → (level) → (category) → (bất kỳ).
// Trả nil nếu không có nguồn/không có tenant/kho trống.
func (t *ChessLookupPositionTool) pickFromBank(ctx context.Context, category, level, keyword string) *types.ChessPosition {
	if t.source == nil {
		return nil
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil
	}
	filters := []types.ChessPositionFilter{
		{Category: category, Level: level, Keyword: keyword},
		{Category: category, Level: level},
		{Level: level},
		{Category: category},
		{},
	}
	tried := map[string]bool{}
	for _, f := range filters {
		key := f.Category + "\x00" + f.Level + "\x00" + f.Keyword
		if tried[key] {
			continue
		}
		tried[key] = true
		list, err := t.source.ListPositions(ctx, tenantID, f)
		if err != nil || len(list) == 0 {
			continue
		}
		return list[rand.Intn(len(list))]
	}
	return nil
}

// pickEmbedded chọn một thế cờ mẫu từ bộ nhúng sẵn (khớp lỏng category/level/keyword).
func (t *ChessLookupPositionTool) pickEmbedded(category, level, keyword string) *types.ToolResult {
	var candidates []chessSamplePosition
	for _, p := range embeddedPositions {
		if category != "" && p.category != category {
			continue
		}
		if level != "" && p.level != level {
			continue
		}
		if keyword != "" && !strings.Contains(normLower(p.title), keyword) {
			continue
		}
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		candidates = embeddedPositions // không khớp → trả bất kỳ (tool không bao giờ "câm")
	}
	p := candidates[rand.Intn(len(candidates))]
	return positionResult(p.fen, p.title, p.category, p.level, p.annotation)
}

// positionResult dựng ToolResult chuẩn (bàn cờ tương tác + mô tả) cho một thế
// cờ mẫu. CỐ Ý không gán "plies"/"pgn" và không gọi engine — thế cờ có thể
// không có quân Vua (dạy trẻ mới học).
func positionResult(fen, title, category, level, annotation string) *types.ToolResult {
	side := "Trắng"
	if fenSide(fen) == "b" {
		side = "Đen"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Thế cờ mẫu: %s (%s đi)\n", title, side)
	if category != "" {
		fmt.Fprintf(&b, "Phân loại: %s\n", category)
	}
	if level != "" {
		fmt.Fprintf(&b, "Cấp độ: %s\n", level)
	}
	if strings.TrimSpace(annotation) != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(annotation))
	}

	data := map[string]interface{}{
		"display_type": "chess_board",
		"fen":          fen,
		"side_to_move": fenSide(fen),
		"caption":      title,
	}

	return &types.ToolResult{Success: true, Output: b.String(), Data: data}
}
