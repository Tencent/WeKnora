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

func TestIsSupportedImportExtension(t *testing.T) {
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
		{name: "surrounding space", ext: " xlsx ", want: true},
		{name: "pdf", ext: "pdf", want: true},
		{name: "unsupported", ext: "exe", want: false},
		{name: "video", ext: "mp4", want: false},
		{name: "empty", ext: "", want: false},
		{name: "unknown sentinel", ext: unknownFileType, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSupportedImportExtension(tt.ext); got != tt.want {
				t.Fatalf("isSupportedImportExtension(%q) = %v, want %v", tt.ext, got, tt.want)
			}
		})
	}
}

// Direct upload and URL import must agree on the accepted extension set,
// otherwise #2447 (xlsx accepted on upload, rejected on URL import) regresses.
func TestImportExtensionSetIsSharedAcrossPaths(t *testing.T) {
	for ext := range supportedImportFileExtensions {
		if !isValidFileType("file." + ext) {
			t.Errorf("isValidFileType rejects supported extension %q", ext)
		}
		if err := validateImportFileType(ext); err != nil {
			t.Errorf("validateImportFileType(%q) = %v, want nil", ext, err)
		}
	}
}

func TestIsDataTableFileType(t *testing.T) {
	for _, ext := range []string{"csv", "xlsx", "xls", ".XLSX"} {
		if !isDataTableFileType(ext) {
			t.Errorf("isDataTableFileType(%q) = false, want true", ext)
		}
	}
	for _, ext := range []string{"pdf", "png", "", unknownFileType} {
		if isDataTableFileType(ext) {
			t.Errorf("isDataTableFileType(%q) = true, want false", ext)
		}
	}
}
