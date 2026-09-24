package langdetect

import "testing"

func TestDetect(t *testing.T) {
	for _, tt := range []struct {
		name, text, want string
	}{
		{"Vietnamese", "Tôi muốn tìm thông tin về đơn hàng của mình. " +
			"Bạn có thể giúp tôi kiểm tra trạng thái giao hàng không?", "Vietnamese"},
		{"English", "Please help me find information about my order and check the delivery status.", "English"},
		{"Chinese", "我想查询我的订单信息，请帮我确认目前的配送状态。", "Mandarin"},
		{"short", "ok", ""},
		{"link", "https://example.com/a/long/path/with/english/words", ""},
		{"non linguistic", "12345 😀 ?", ""},
		{"mixed Vietnamese and English", "This is a mixed Vietnamese English sentence " +
			"nhưng tôi cần giúp đỡ về đơn hàng của mình.", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Detect(tt.text); got != tt.want {
				t.Fatalf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}
