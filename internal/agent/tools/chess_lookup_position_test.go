package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/chess"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakePositionSource là PositionSource giả cho test: trả một danh sách cố định
// (hoặc lỗi) và ghi lại bộ lọc của LẦN GỌI ĐẦU để kiểm tra lọc nới dần.
type fakePositionSource struct {
	positions   []*types.ChessPosition
	err         error
	calls       int
	firstFilter types.ChessPositionFilter
}

func (f *fakePositionSource) ListPositions(_ context.Context, _ uint64, flt types.ChessPositionFilter) ([]*types.ChessPosition, error) {
	f.calls++
	if f.calls == 1 {
		f.firstFilter = flt
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.positions, nil
}

func runLookupPosition(t *testing.T, tool *ChessLookupPositionTool, ctx context.Context, in ChessLookupPositionInput) map[string]interface{} {
	t.Helper()
	args, _ := json.Marshal(in)
	res, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute lỗi: %v", err)
	}
	if res == nil || !res.Success {
		t.Fatalf("kỳ vọng Success, nhận: %+v", res)
	}
	if res.Data["display_type"] != "chess_board" {
		t.Fatalf("kỳ vọng display_type chess_board, nhận %v", res.Data["display_type"])
	}
	if _, hasPGN := res.Data["pgn"]; hasPGN {
		t.Fatalf("KHÔNG được gán pgn — thế cờ mẫu có thể không có quân Vua, đi qua chess.js sẽ vỡ")
	}
	if _, hasPlies := res.Data["plies"]; hasPlies {
		t.Fatalf("KHÔNG được gán plies")
	}
	return res.Data
}

func isEmbeddedPositionFEN(fen string) bool {
	for _, p := range embeddedPositions {
		if p.fen == fen {
			return true
		}
	}
	return false
}

// Khi ngân hàng thế cờ có dữ liệu → tool phải dùng thế cờ từ DB (không phải bộ
// nhúng), và truyền đúng category/level/keyword ở lượt lọc đầu tiên.
func TestLookupPosition_UsesBankWhenAvailable(t *testing.T) {
	dbFEN := "8/8/8/8/8/2k5/8/R2K4 w - - 0 1"
	src := &fakePositionSource{positions: []*types.ChessPosition{
		{FEN: dbFEN, Title: "Vua+Xe đấu Vua", Category: "endgame", Level: "hau"},
	}}
	tool := NewChessLookupPositionTool(src)

	data := runLookupPosition(t, tool, ctxWithTenant(1),
		ChessLookupPositionInput{Category: "endgame", Level: "hau"})

	if data["fen"] != dbFEN {
		t.Errorf("kỳ vọng FEN từ kho %q, nhận %q", dbFEN, data["fen"])
	}
	if src.calls == 0 {
		t.Errorf("kỳ vọng có gọi ListPositions")
	}
	if src.firstFilter.Category != "endgame" || src.firstFilter.Level != "hau" {
		t.Errorf("bộ lọc lần gọi đầu sai: %+v", src.firstFilter)
	}
}

// Kho lỗi/trống → fallback bộ thế cờ mẫu nhúng (tool không "câm").
func TestLookupPosition_FallbackWhenBankEmpty(t *testing.T) {
	src := &fakePositionSource{err: errors.New("không có bản ghi")}
	tool := NewChessLookupPositionTool(src)

	data := runLookupPosition(t, tool, ctxWithTenant(1), ChessLookupPositionInput{})
	fen, _ := data["fen"].(string)
	if !isEmbeddedPositionFEN(fen) {
		t.Errorf("kỳ vọng FEN từ bộ nhúng, nhận %q", fen)
	}
}

// Không có nguồn (source nil) → dùng bộ nhúng.
func TestLookupPosition_NilSourceFallback(t *testing.T) {
	tool := NewChessLookupPositionTool(nil)
	data := runLookupPosition(t, tool, context.Background(), ChessLookupPositionInput{})
	fen, _ := data["fen"].(string)
	if !isEmbeddedPositionFEN(fen) {
		t.Errorf("kỳ vọng FEN từ bộ nhúng, nhận %q", fen)
	}
}

// Không có tenant trong ctx → KHÔNG truy vấn DB, fallback bộ nhúng.
func TestLookupPosition_NoTenantSkipsBank(t *testing.T) {
	src := &fakePositionSource{positions: []*types.ChessPosition{{FEN: "8/8/8/8/4k3/8/4K3/8 w - - 0 1"}}}
	tool := NewChessLookupPositionTool(src)

	data := runLookupPosition(t, tool, context.Background(), ChessLookupPositionInput{}) // không tenant
	if src.calls != 0 {
		t.Errorf("không có tenant thì không nên gọi DB; calls=%d", src.calls)
	}
	fen, _ := data["fen"].(string)
	if !isEmbeddedPositionFEN(fen) {
		t.Errorf("kỳ vọng FEN từ bộ nhúng, nhận %q", fen)
	}
}

// Bộ thế cờ nhúng CỐ Ý chứa một thế cờ không có quân Vua (dạy trẻ mới học) —
// khớp lọc category="basic" phải trả đúng thế cờ đó, không bị lọc nhầm.
func TestLookupPosition_EmbeddedBasicPositionHasNoKing(t *testing.T) {
	tool := NewChessLookupPositionTool(nil)
	data := runLookupPosition(t, tool, context.Background(), ChessLookupPositionInput{Category: "basic"})
	fen, _ := data["fen"].(string)
	if fen == "" {
		t.Fatalf("thiếu fen trong kết quả")
	}
	if !isEmbeddedPositionFEN(fen) {
		t.Fatalf("kỳ vọng FEN từ bộ nhúng, nhận %q", fen)
	}
}

// Mọi FEN nhúng sẵn phải hợp lệ CẤU TRÚC theo chess.ValidateFEN (không đòi
// quân Vua) — nếu ai đó gõ sai khi thêm thế cờ mẫu mới, test này bắt ngay.
func TestEmbeddedPositions_AllHaveValidFENStructure(t *testing.T) {
	for _, p := range embeddedPositions {
		if err := chess.ValidateFEN(p.fen); err != nil {
			t.Errorf("thế cờ mẫu %q có FEN không hợp lệ: %v", p.title, err)
		}
	}
}
