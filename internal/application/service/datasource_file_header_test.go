package service

import (
	"bytes"
	"io"
	"mime"
	"testing"
)

func TestBytesToFileHeader(t *testing.T) {
	content := []byte("# Quarterly report\n\n季度报告\n\x00\xff")
	tests := []struct {
		name         string
		filename     string
		wantFilename string
		content      []byte
	}{
		{name: "plain", filename: "report.md", wantFilename: "report.md", content: content},
		{name: "unicode", filename: "季度报告.md", wantFilename: "季度报告.md", content: content},
		{name: "quotes", filename: `Q4 "Final".md`, wantFilename: `Q4 "Final".md`, content: content},
		{name: "semicolon", filename: "Q4; Final.md", wantFilename: "Q4; Final.md", content: content},
		{
			name:         "quoted folder",
			filename:     `Reports "2026"/Q4 "Final".md`,
			wantFilename: `Q4 "Final".md`,
			content:      content,
		},
		{name: "empty content", filename: `Q4 "Empty".md`, wantFilename: `Q4 "Empty".md`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := bytesToFileHeader(tt.content, tt.filename)
			if err != nil {
				t.Fatalf("bytesToFileHeader(%q): %v", tt.filename, err)
			}
			if file.Filename != tt.wantFilename {
				t.Errorf("filename = %q, want %q", file.Filename, tt.wantFilename)
			}
			if file.Size != int64(len(tt.content)) {
				t.Errorf("size = %d, want %d", file.Size, len(tt.content))
			}
			if got := file.Header.Get("Content-Type"); got != "application/octet-stream" {
				t.Errorf("content type = %q, want application/octet-stream", got)
			}
			mediaType, params, err := mime.ParseMediaType(file.Header.Get("Content-Disposition"))
			if err != nil {
				t.Fatalf("parse content disposition: %v", err)
			}
			if mediaType != "form-data" || params["name"] != "file" || params["filename"] != tt.filename {
				t.Errorf("content disposition = %q, want file part for %q",
					file.Header.Get("Content-Disposition"), tt.filename)
			}
			opened, err := file.Open()
			if err != nil {
				t.Fatalf("open file: %v", err)
			}
			t.Cleanup(func() {
				if err := opened.Close(); err != nil {
					t.Errorf("close file: %v", err)
				}
			})
			got, err := io.ReadAll(opened)
			if err != nil {
				t.Fatalf("read file: %v", err)
			}
			if !bytes.Equal(got, tt.content) {
				t.Errorf("content = %q, want %q", got, tt.content)
			}
		})
	}
}
