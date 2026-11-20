package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestRecoverWindowsMultipartFilename(t *testing.T) {
	const gbkName = "\xc7\xb0\x81\x5c\xba\xf3.pdf" // 前乗后.pdf
	const truncated = "\xba\xf3.pdf"               // 后.pdf
	tests := []struct {
		name        string
		rawName     string
		windowsName string
		want        string
	}{
		{"GBK trail byte", gbkName, truncated, "前乗后.pdf"},
		{"GBK trail byte with directory", "uploads" + string(filepath.Separator) + gbkName, truncated, "前乗后.pdf"},
		{"GBK trail byte without extension", "\x81\x5c", "\x81", "乗"},
		{"UTF-8 Windows path", `uploads\前乗后.pdf`, "前乗后.pdf", "前乗后.pdf"},
		{"GBK without a trail backslash", truncated, truncated, truncated},
		{"real Windows separator with GBK name", "uploads\\\xba\xf3.pdf", truncated, truncated},
		{"invalid GBK", "\xff\\" + truncated, truncated, truncated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			if _, err := writer.CreateFormFile("file", tt.rawName); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/upload", &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			file, header, err := req.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()

			if filepath.Separator == '\\' {
				if header.Filename != tt.windowsName {
					t.Fatalf("FormFile filename = %q, want %q", header.Filename, tt.windowsName)
				}
			} else {
				// Emulate the Windows basename while keeping the header parsed
				// from the same multipart request.
				header.Filename = tt.windowsName
			}
			if got := recoverWindowsMultipartFilename(header); got != tt.want {
				t.Errorf("recovered filename = %q, want %q", got, tt.want)
			}
		})
	}
}
