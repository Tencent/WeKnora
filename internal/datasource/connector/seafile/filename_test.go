package seafile

import (
	"strings"
	"testing"
)

func TestKnowledgeFileName(t *testing.T) {
	if got := knowledgeFileName("01-机场线", "/产品/产品A/需求: v2.docx"); got != "01-机场线/产品/产品A/需求_ v2.docx" {
		t.Fatalf("filename = %q", got)
	}
	p := "/" + strings.Repeat("层级/", 20) + "说明.pdf"
	if got := knowledgeFileName("资料库", p); got != "资料库"+p {
		t.Fatalf("deep filename was truncated: %q", got)
	}
}

func TestExternalIDAndFingerprint(t *testing.T) {
	if got := externalID(testRepoID, "/产品/a:b.pdf"); got != "seafile:"+testRepoID+":/产品/a:b.pdf" {
		t.Fatalf("externalID = %q", got)
	}
	for _, tc := range []struct {
		oid         string
		mtime, size int64
		want        string
	}{
		{"abc123", 123, 456, "o:abc123|m:123|s:456"},
		{"", 0, 0, "o:|m:0|s:0"},
	} {
		if got := fingerprint(tc.oid, tc.mtime, tc.size); got != tc.want {
			t.Fatalf("fingerprint = %q, want %q", got, tc.want)
		}
	}
}

func TestValidEntryName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"说明.pdf", true},
		{"需求: v2.docx", true},
		{"a%2Fb.pdf", true},
		{".hidden.pdf", true},
		{" spaced .pdf", true},
		{"", false},
		{".", false},
		{"..", false},
		{"../outside.pdf", false},
		{"/absolute.pdf", false},
		{"a/b.pdf", false},
		{"a\\b.pdf", false},
		{"a\x00b.pdf", false},
		{"a\nb.pdf", false},
		{"a\rb.pdf", false},
		{"a\tb.pdf", false},
		{"a\x7fb.pdf", false},
	} {
		if got := validEntryName(tc.name); got != tc.want {
			t.Errorf("validEntryName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
