package utils

import "testing"

func TestIsActiveBrowserContentExt(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		{".svg", true},
		{".SVG", true},
		{".svgz", true},
		{".html", true},
		{".HTM", true},
		{".xhtml", true},
		{".xml", true},
		{".js", true},
		{".mjs", true},
		{".css", true},
		{".png", false},
		{".pdf", false},
		{".json", false},
		{"", false},
		{"svg", false},
	}
	for _, tt := range tests {
		if got := IsActiveBrowserContentExt(tt.ext); got != tt.want {
			t.Errorf("IsActiveBrowserContentExt(%q) = %v, want %v", tt.ext, got, tt.want)
		}
	}
}

func TestGetContentTypeByExt(t *testing.T) {
	tests := []struct {
		ext  string
		want string
	}{
		{".pdf", "application/pdf"},
		{".PDF", "application/pdf"},
		{".csv", "text/csv; charset=utf-8"},
		{".json", "application/json"},
		{".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{".png", "image/png"},
		{".jpg", "image/jpeg"},
		{".jpeg", "image/jpeg"},
		{".txt", "text/plain; charset=utf-8"},
		{".md", "text/markdown; charset=utf-8"},
		{".gz", "application/gzip"},
		{".xyz", "application/octet-stream"},
		{"", "application/octet-stream"},
		// Active browser content is never served with its native type.
		{".svg", "application/octet-stream"},
		{".SVG", "application/octet-stream"},
		{".html", "application/octet-stream"},
		{".js", "application/octet-stream"},
	}
	for _, tt := range tests {
		if got := GetContentTypeByExt(tt.ext); got != tt.want {
			t.Errorf("GetContentTypeByExt(%q) = %q, want %q", tt.ext, got, tt.want)
		}
	}
}

func TestSafeContentTypeByFilename(t *testing.T) {
	tests := []struct {
		filename string
		wantType string
		wantOK   bool
	}{
		{"report.pdf", "application/pdf", true},
		{"photo.PNG", "image/png", true},
		{"archive.tar.gz", "application/gzip", true},
		{"notes.txt", "text/plain; charset=utf-8", true},
		// No extension: unknown type, but safe to display inline.
		{"README", "application/octet-stream", true},
		// Active content is forced to download, never previewed inline.
		{"chart.svg", "application/octet-stream", false},
		{"page.SVG", "application/octet-stream", false},
		{"index.html", "application/octet-stream", false},
		{"app.js", "application/octet-stream", false},
		{"style.css", "application/octet-stream", false},
	}
	for _, tt := range tests {
		gotType, gotOK := SafeContentTypeByFilename(tt.filename)
		if gotType != tt.wantType || gotOK != tt.wantOK {
			t.Errorf("SafeContentTypeByFilename(%q) = (%q, %v), want (%q, %v)",
				tt.filename, gotType, gotOK, tt.wantType, tt.wantOK)
		}
	}
}
