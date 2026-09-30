package langdetect

import (
	"testing"

	"golang.org/x/text/unicode/norm"
)

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
		{"short mixed", "ok thanks bạn", ""},
		{"mixed first turn with Vietnamese cues", "How do I reset my password? Mình quên mật khẩu rồi", "Vietnamese"},
		{"French", "Je voudrais acheter un nouveau produit", ""},
		{"Portuguese", "Gostaria de comprar um novo produto", ""},
		{"Romanian", "Vreau să cumpăr ceva nou", ""},
		{"Croatian", "Đakovo je lijep grad", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Detect(tt.text); got != tt.want {
				t.Fatalf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectShortVietnameseFirstTurn(t *testing.T) {
	for _, tt := range []struct {
		name, text string
	}{
		{"price", "Giá bao nhiêu vậy?"},
		{"stock", "Shop ơi còn hàng không?"},
		{"thanks", "Cảm ơn bạn nhiều nha"},
		{"password", "Em muốn đổi mật khẩu thì làm sao ạ"},
		{"upload timeout", "Mình bị lỗi timeout khi gọi endpoint upload file, check giúp mình với"},
	} {
		for _, form := range []struct {
			name, text string
		}{{"NFC", norm.NFC.String(tt.text)}, {"NFD", norm.NFD.String(tt.text)}} {
			t.Run(tt.name+"/"+form.name, func(t *testing.T) {
				if got := Detect(form.text); got != "Vietnamese" {
					t.Fatalf("Detect() = %q, want Vietnamese", got)
				}
			})
		}
	}
}
