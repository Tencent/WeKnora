package utils

import (
	"mime/multipart"
	"net/textproto"
	"testing"
)

func TestRestoreMultipartFilename(t *testing.T) {
	gbkName := string([]byte{0xc7, 0xb0, 0x81, 0x5c, 0xba, 0xf3, '.', 'p', 'd', 'f'})
	tests := []struct {
		name, raw, parsed, want string
	}{
		{"GBK trail byte", gbkName, string([]byte{0xba, 0xf3, '.', 'p', 'd', 'f'}), "前乗后.pdf"},
		{"UTF-8 path", `folder\report.pdf`, "report.pdf", "report.pdf"},
		{"GBK path", gbkName + `/../secret.pdf`, "secret.pdf", "secret.pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := &multipart.FileHeader{Filename: tt.parsed, Header: textproto.MIMEHeader{
				"Content-Disposition": {`form-data; name="file"; filename="` + tt.raw + `"`},
			}}
			RestoreMultipartFilename(file)
			if file.Filename != tt.want {
				t.Fatalf("filename = %q, want %q", file.Filename, tt.want)
			}
		})
	}
}
