package chess

import "testing"

func TestNormalizeFEN(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{
			name: "FEN cụt chỉ có bố cục quân",
			in:   "8/8/8/3p4/4P3/8/8/8",
			want: "8/8/8/3p4/4P3/8/8/8 w - - 0 1",
		},
		{
			name: "FEN cụt kèm lượt đi",
			in:   "8/8/8/3p4/4P3/8/8/8 w",
			want: "8/8/8/3p4/4P3/8/8/8 w - - 0 1",
		},
		{
			name: "FEN không có Vua vẫn hợp lệ (dạy trẻ: chỉ vài quân Tốt)",
			in:   "8/8/8/3p4/4P3/8/8/8 w - - 0 1",
			want: "8/8/8/3p4/4P3/8/8/8 w - - 0 1",
		},
		{
			name: "FEN chuẩn ván khởi đầu giữ nguyên",
			in:   "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
			want: "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		},
		{
			name:    "chuỗi rỗng lỗi",
			in:      "",
			wantErr: true,
		},
		{
			name:    "bố cục quân sai (thiếu hàng) lỗi",
			in:      "8/8/8/8 w",
			wantErr: true,
		},
		{
			name:    "ký tự rác lỗi",
			in:      "xxxxxxxx/8/8/8/8/8/8/8 w",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeFEN(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("muốn lỗi nhưng không có, got=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("không muốn lỗi nhưng got err=%v", err)
			}
			if got != tc.want {
				t.Fatalf("got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestFENKey(t *testing.T) {
	a := FENKey("8/8/8/3p4/4P3/8/8/8 w - - 0 1")
	b := FENKey("8/8/8/3p4/4P3/8/8/8 w - - 12 34")
	if a != b {
		t.Fatalf("FENKey phải bỏ qua 2 bộ đếm nước: a=%q b=%q", a, b)
	}
	c := FENKey("8/8/8/3p4/4P3/8/8/8 b - - 0 1")
	if a == c {
		t.Fatalf("FENKey phải phân biệt lượt đi khác nhau: a=%q c=%q", a, c)
	}
	if FENKey("") != "" {
		t.Fatalf("FENKey chuỗi rỗng phải trả rỗng")
	}
}

func TestHasBothKings(t *testing.T) {
	cases := []struct {
		name string
		fen  string
		want bool
	}{
		{"ván khởi đầu có đủ 2 Vua", "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", true},
		{"thế cờ dạy trẻ không có Vua nào", "8/8/8/3p4/4P3/8/8/8 w - - 0 1", false},
		{"chỉ có Vua Trắng", "8/8/8/8/8/8/8/4K3 w - - 0 1", false},
		{"chuỗi rỗng", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasBothKings(tc.fen); got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestSideToMoveExported(t *testing.T) {
	if SideToMove("8/8/8/8/8/8/8/8 b - - 0 1") != "b" {
		t.Fatalf("SideToMove phải đọc đúng trường lượt đi")
	}
	if SideToMove("8/8/8/8/8/8/8/8") != "w" {
		t.Fatalf("SideToMove phải mặc định 'w' khi thiếu trường")
	}
}
