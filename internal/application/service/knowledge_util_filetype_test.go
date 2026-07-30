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
