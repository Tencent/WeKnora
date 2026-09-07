package types

import "testing"

func TestResolveKnowledgeRelativeReference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, folder, ref, wantFolder, wantFile string
		ok                                      bool
	}{
		{"same directory", "docs/guide", "./a.png", "docs/guide", "a.png", true},
		{"child directory", "docs/guide", "images/a.png", "docs/guide/images", "a.png", true},
		{"parent directory", "docs/guide", "../images/a.png", "docs/images", "a.png", true},
		{"normalizes interior parent", "docs/guide", "assets/../images/a.png", "docs/guide/images", "a.png", true},
		{"may reach knowledge root", "docs/guide", "../../a.png", "", "a.png", true},
		{"cannot leave knowledge root", "docs/guide", "../../../a.png", "", "", false},
		{"root cannot use parent", "", "../a.png", "", "", false},
		{"absolute path", "docs", "/etc/passwd", "", "", false},
		{"windows path", "docs", "C:\\Windows\\a.png", "", "", false},
		{"file URL", "docs", "file:///etc/passwd", "", "", false},
		{"resource URL", "docs", "resource://abcdefghijklmnopqrstuv", "", "", false},
		{"network URL", "docs", "https://example.com/a.png", "", "", false},
		{"anchor", "docs", "#section", "", "", false},
		{"query is deferred", "docs", "a.png?v=1", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			folder, file, ok := ResolveKnowledgeRelativeReference(tt.folder, tt.ref)
			if ok != tt.ok || folder != tt.wantFolder || file != tt.wantFile {
				t.Fatalf("ResolveKnowledgeRelativeReference(%q, %q) = (%q, %q, %v), want (%q, %q, %v)", tt.folder, tt.ref, folder, file, ok, tt.wantFolder, tt.wantFile, tt.ok)
			}
		})
	}
}
