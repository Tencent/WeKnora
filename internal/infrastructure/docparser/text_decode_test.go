package docparser

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

// TestSimpleFormatReaderDecodesTextEncodings checks that text files saved in
// an encoding other than UTF-8 are read as text. They were passed on as raw
// bytes, so a GBK .txt, a CSV from a Chinese Excel and a UTF-16 file from
// Notepad reached the index as invalid UTF-8.
func TestSimpleFormatReaderDecodesTextEncodings(t *testing.T) {
	gbk := func(s string) []byte {
		b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	utf16 := func(order unicode.Endianness, s string) []byte {
		b, err := unicode.UTF16(order, unicode.UseBOM).NewEncoder().Bytes([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	tests := []struct {
		name     string
		fileType string
		content  []byte
		want     string
	}{
		{"GBK text", "txt", gbk("中文内容，测试"), "中文内容，测试"},
		{"GBK markdown", "md", gbk("# 标题\n\n正文"), "# 标题\n\n正文"},
		{"GBK CSV from Excel", "csv", gbk("姓名,城市\n张三,北京\n"), "| 姓名 | 城市 |\n| --- | --- |\n| 张三 | 北京 |\n"},
		{"UTF-16LE with BOM", "txt", utf16(unicode.LittleEndian, "Hello 世界"), "Hello 世界"},
		{"UTF-16BE with BOM", "txt", utf16(unicode.BigEndian, "Hello 世界"), "Hello 世界"},
		{"Latin-1 fallback", "txt", []byte("caf\xe9 \x81 menu"), "café \u0081 menu"},
		// Notepad's "ANSI" on a Western Windows: GB18030 rejects the bytes
		// around the quotes, and Latin-1 read them as control characters.
		{
			"Windows-1252 punctuation", "txt",
			[]byte("\x93Quoted\x94 text \x96 costs 5 \x80."),
			"\u201cQuoted\u201d text \u2013 costs 5 \u20ac.",
		},
		{"UTF-8 unchanged", "txt", []byte("plain UTF-8 文本"), "plain UTF-8 文本"},
		// Kept byte for byte: the source locators map a verbatim UTF-8 text
		// file onto itself and count past its byte order mark.
		{"UTF-8 BOM kept", "md", []byte("\xef\xbb\xbf# Title\n"), "\xef\xbb\xbf# Title\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := (&SimpleFormatReader{}).Read(context.Background(), &types.ReadRequest{
				FileType:    tt.fileType,
				FileName:    "file." + tt.fileType,
				FileContent: tt.content,
			})
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if res.MarkdownContent != tt.want {
				t.Errorf("MarkdownContent = %q, want %q", res.MarkdownContent, tt.want)
			}
		})
	}
}
