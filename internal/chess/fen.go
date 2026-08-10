package chess

import "strings"

// fen.go gom các tiện ích FEN dùng chung cho "ngân hàng thế cờ" (chess_positions).
// Khác với board.go (ParsePGN, FENAfterMove...) vốn giả định thế cờ hợp lệ theo
// luật (đủ 2 Vua) để dựng notnil.Game, các hàm ở đây CỐ Ý không đòi hỏi điều đó:
// ngân hàng thế cờ phải lưu được cả thế cờ giản lược dùng dạy trẻ (ví dụ chỉ vài
// quân Tốt, không có Vua). Xem ValidateFEN (board.go) — notnil.FEN/decodeFEN chỉ
// kiểm cấu trúc 6 trường, KHÔNG kiểm tra sự tồn tại của Vua.

// NormalizeFEN chuẩn hóa một chuỗi FEN có thể "cụt" (thiếu 1-5 trường sau cùng,
// kiểu người dùng chỉ dán phần bố cục quân + lượt đi) thành FEN đầy đủ 6 trường
// rồi xác thực bằng ValidateFEN. Trường thiếu được bù giá trị mặc định:
// lượt đi "w", nhập thành "-", bắt tốt qua đường "-", nửa-nước "0", nước "1".
// Trả về FEN đã chuẩn hóa (luôn đủ 6 trường) hoặc lỗi nếu không hợp lệ.
func NormalizeFEN(fen string) (string, error) {
	fields := strings.Fields(fen)
	if len(fields) == 0 {
		return "", ErrInvalidFEN
	}
	defaults := []string{"", "w", "-", "-", "0", "1"}
	for len(fields) < 6 {
		fields = append(fields, defaults[len(fields)])
	}
	// Cắt bớt nếu dư trường (giữ 6 đầu) — ValidateFEN sẽ tự bắt lỗi cấu trúc.
	if len(fields) > 6 {
		fields = fields[:6]
	}
	normalized := strings.Join(fields, " ")
	if err := ValidateFEN(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

// FENKey trả về khóa phát hiện thế cờ trùng lặp: 4 trường đầu của FEN (bố cục
// quân, lượt đi, quyền nhập thành, ô bắt tốt qua đường) — bỏ 2 bộ đếm nước vì
// chúng không đổi bản chất thế cờ. Trả về chuỗi rỗng nếu fen không tách được
// tối thiểu 1 trường.
func FENKey(fen string) string {
	fields := strings.Fields(fen)
	if len(fields) == 0 {
		return ""
	}
	n := 4
	if len(fields) < n {
		n = len(fields)
	}
	return strings.Join(fields[:n], " ")
}

// HasBothKings báo cho biết trường bố cục quân của fen có đủ CẢ hai Vua (đúng
// 1 'K' và 1 'k') hay không. Dùng làm cổng chặn trước khi gọi engine/notnil —
// những đường xử lý đó giả định thế cờ hợp lệ theo luật và sẽ vỡ (hoặc trả kết
// quả vô nghĩa) với thế cờ thiếu Vua vốn được ngân hàng thế cờ cho phép lưu.
func HasBothKings(fen string) bool {
	fields := strings.Fields(fen)
	if len(fields) == 0 {
		return false
	}
	board := fields[0]
	white, black := 0, 0
	for _, r := range board {
		switch r {
		case 'K':
			white++
		case 'k':
			black++
		}
	}
	return white == 1 && black == 1
}

// SideToMove trả về "w" hoặc "b" — bên đang đi, đọc từ trường thứ hai của fen.
// Mặc định "w" nếu thiếu/không hợp lệ. Đây là bản EXPORTED của sideToMove
// (dùng nội bộ package chess) — internal/agent/tools.fenSide gọi lại hàm này
// thay vì giữ một bản triển khai trùng lặp thứ ba.
func SideToMove(fen string) string {
	return sideToMove(fen)
}
