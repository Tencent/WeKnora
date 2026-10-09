package utils

import (
	"sort"
	"testing"
)

func TestNormalizeImportExtension(t *testing.T) {
	tests := []struct {
		ext  string
		want string
	}{
		{"pdf", "pdf"},
		{".PDF", "pdf"},
		{" .XLSX ", "xlsx"},
		{".", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := NormalizeImportExtension(tt.ext); got != tt.want {
			t.Errorf("NormalizeImportExtension(%q) = %q, want %q", tt.ext, got, tt.want)
		}
	}
}

func TestIsSupportedImportExtension(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		{"pdf", true},
		{" .XLSX ", true},
		{"XMIND", true},
		{"exe", false},
		{"mp4", false},
		{"unknown", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsSupportedImportExtension(tt.ext); got != tt.want {
			t.Errorf("IsSupportedImportExtension(%q) = %v, want %v", tt.ext, got, tt.want)
		}
	}
}

func TestSupportedImportExtensions(t *testing.T) {
	exts := SupportedImportExtensions()
	if len(exts) != len(supportedImportFileExtensions) {
		t.Fatalf("SupportedImportExtensions() has %d entries, want %d", len(exts), len(supportedImportFileExtensions))
	}
	if !sort.StringsAreSorted(exts) {
		t.Errorf("SupportedImportExtensions() is not sorted: %v", exts)
	}
	for _, ext := range exts {
		if !IsSupportedImportExtension(ext) {
			t.Errorf("SupportedImportExtensions() contains unsupported extension %q", ext)
		}
	}
}
