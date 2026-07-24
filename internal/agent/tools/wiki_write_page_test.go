package tools

import "testing"

func TestNormalizeAndValidateWikiSlug(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "already valid", in: "entity/acme-corp", want: "entity/acme-corp"},
		{name: "lowercased and spaces", in: "Entity/Acme Corp", want: "entity/acme-corp"},
		{name: "trimmed", in: "  concept/rag  ", want: "concept/rag"},
		{name: "cjk kept", in: "entity/上海中心大厦", want: "entity/上海中心大厦"},
		{name: "uuid summary", in: "summary/07a20bb1-a662-47cf-9929-06fb5d5b5b5e", want: "summary/07a20bb1-a662-47cf-9929-06fb5d5b5b5e"},
		{name: "empty", in: "   ", wantErr: true},
		{name: "leading slash", in: "/entity/x", wantErr: true},
		{name: "trailing slash", in: "entity/x/", wantErr: true},
		{name: "double slash", in: "entity//x", wantErr: true},
		{name: "invalid char", in: "entity/x!y", wantErr: true},
		{name: "invalid space-only becomes empty", in: "  ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeAndValidateWikiSlug(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got slug %q", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeAndValidateWikiSlug(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsSummaryNamespace(t *testing.T) {
	if !isSummaryNamespace("summary/abc") {
		t.Fatal("summary/abc must be in the summary namespace")
	}
	if isSummaryNamespace("summary") {
		t.Fatal("bare 'summary' (no slash) must not count as the summary namespace")
	}
	if isSummaryNamespace("entity/summary-of-x") {
		t.Fatal("entity/summary-of-x must not count as the summary namespace")
	}
}
