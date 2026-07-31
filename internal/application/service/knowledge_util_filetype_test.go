package service

import "testing"

func TestIsValidFileTypeHTML(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{name: "html", filename: "index.html", want: true},
		{name: "uppercase html", filename: "INDEX.HTML", want: true},
		{name: "htm", filename: "legacy.htm", want: true},
		{name: "unsupported", filename: "payload.exe", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidFileType(tt.filename); got != tt.want {
				t.Fatalf("isValidFileType(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestIsAllowedFileURLExtension(t *testing.T) {
	tests := []struct {
		name string
		ext  string
		want bool
	}{
		{name: "xlsx", ext: "xlsx", want: true},
		{name: "xls", ext: "xls", want: true},
		{name: "csv", ext: "csv", want: true},
		{name: "dot prefix", ext: ".xlsx", want: true},
		{name: "uppercase", ext: "XLSX", want: true},
		{name: "pdf", ext: "pdf", want: true},
		{name: "unsupported", ext: "exe", want: false},
		{name: "empty", ext: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAllowedFileURLExtension(tt.ext); got != tt.want {
				t.Fatalf("isAllowedFileURLExtension(%q) = %v, want %v", tt.ext, got, tt.want)
			}
		})
	}
}
