package tools

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/chess"
	"github.com/Tencent/WeKnora/internal/types"
)

// validateFENArg chuẩn hóa và kiểm tra tham số FEN dùng chung cho các tool cờ
// (analyze/best_move/explain_move). Trả về FEN đã trim; nếu không hợp lệ trả kèm
// một *types.ToolResult lỗi sẵn sàng để tool trả về (giá trị thứ hai != nil).
func validateFENArg(fen string) (string, *types.ToolResult) {
	fen = strings.TrimSpace(fen)
	if fen == "" {
		return "", &types.ToolResult{Success: false, Error: "Thiếu tham số fen"}
	}
	if err := chess.ValidateFEN(fen); err != nil {
		return "", &types.ToolResult{Success: false, Error: fmt.Sprintf("FEN không hợp lệ: %v", err)}
	}
	return fen, nil
}

// friendlyEngineError dịch lỗi từ engine sang thông báo thân thiện (tiếng Việt) cho
// người học/HLV, thay vì lỗi kỹ thuật thô. Dùng chung cho các tool gọi engine để
// khi engine "câm" (sidecar chết/timeout) người dùng vẫn nhận lời nhắn rõ ràng.
func friendlyEngineError(err error) string {
	switch {
	case errors.Is(err, chess.ErrAnalysisTimeout):
		return "Engine cờ phân tích quá lâu và đã hết thời gian chờ. Hãy thử lại với độ sâu nhỏ hơn hoặc thử lại sau."
	case errors.Is(err, chess.ErrEngineUnavailable):
		return "Engine cờ tạm thời không phản hồi (dịch vụ phân tích có thể đang khởi động lại). Vui lòng thử lại sau ít phút."
	case errors.Is(err, chess.ErrInvalidFEN):
		return "Thế cờ (FEN) không hợp lệ — vui lòng kiểm tra lại chuỗi FEN."
	default:
		return fmt.Sprintf("Engine gặp sự cố khi phân tích: %v", err)
	}
}

// chess_common.go gom các tiện ích dùng chung cho nhóm tool cờ vua.

// fenSide trả về "w" hoặc "b" — bên đang đi, đọc từ trường thứ hai của FEN.
// Ủy quyền cho chess.SideToMove (internal/chess/fen.go) để không giữ bản triển
// khai trùng lặp thứ ba (bản thứ nhất: chess.sideToMove; bản thứ hai từng nằm
// ở đây).
func fenSide(fen string) string {
	return chess.SideToMove(fen)
}

// chessBoardData chuyển một chess.Analysis thành map dữ liệu cho frontend
// (display_type "chess_board") để hiển thị bàn cờ tương tác kèm đánh giá.
func chessBoardData(a *chess.Analysis, caption string) map[string]interface{} {
	d := map[string]interface{}{
		"display_type": "chess_board",
		"fen":          a.FEN,
		"side_to_move": a.SideToMove,
		"depth":        a.Depth,
	}
	if a.BestMove != "" {
		d["best_move"] = a.BestMove
	}
	if a.BestMoveSAN != "" {
		d["best_move_san"] = a.BestMoveSAN
	}
	if a.IsMate {
		d["is_mate"] = true
		d["mate_in"] = a.MateIn
	} else {
		d["eval_cp"] = a.EvalCP
	}
	if caption != "" {
		d["caption"] = caption
	}
	return d
}

// formatEvalWhite định dạng đánh giá theo góc nhìn quân Trắng cho phần văn bản
// gửi LLM (ví dụ "+0.45", "-1.20", "Chiếu hết sau 3 nước cho Trắng").
func formatEvalWhite(a *chess.Analysis) string {
	if a.IsMate {
		n := a.MateIn
		// MateIn dương: bên đang đi chiếu hết. Quy về bên Trắng/Đen.
		whiteMating := (n >= 0) == (a.SideToMove != "b")
		moves := n
		if moves < 0 {
			moves = -moves
		}
		side := "Trắng"
		if !whiteMating {
			side = "Đen"
		}
		return fmt.Sprintf("Chiếu hết sau %d nước cho %s", moves, side)
	}
	white := a.WhiteCentipawns()
	return fmt.Sprintf("%+.2f", float64(white)/100)
}

// describeEval mô tả ngắn gọn ai đang ưu thế, phục vụ giải thích cho người học.
func describeEval(a *chess.Analysis) string {
	if a.IsMate {
		return formatEvalWhite(a)
	}
	white := a.WhiteCentipawns()
	abs := white
	if abs < 0 {
		abs = -abs
	}
	var who, level string
	switch {
	case abs < 30:
		return "Thế cờ cân bằng"
	case white > 0:
		who = "Trắng"
	default:
		who = "Đen"
	}
	switch {
	case abs < 80:
		level = "nhỉnh hơn đôi chút"
	case abs < 180:
		level = "ưu thế rõ"
	case abs < 400:
		level = "ưu thế lớn"
	default:
		level = "thắng thế áp đảo"
	}
	return fmt.Sprintf("%s %s (%s)", who, level, formatEvalWhite(a))
}
