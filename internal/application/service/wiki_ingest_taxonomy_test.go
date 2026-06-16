package service

import (
	"context"
	"strings"
	"testing"
)

func TestSplitCategoryLine(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantCat  []string
		wantRest string
	}{
		{
			name:     "two level category",
			content:  "CATEGORY: 组织 / 企业\n# 标题\n正文",
			wantCat:  []string{"组织", "企业"},
			wantRest: "# 标题\n正文",
		},
		{
			name:     "single level category",
			content:  "CATEGORY: 人物\n# 陈洋",
			wantCat:  []string{"人物"},
			wantRest: "# 陈洋",
		},
		{
			name:     "empty category keeps existing (nil) and strips line",
			content:  "CATEGORY:\n# 标题",
			wantCat:  nil,
			wantRest: "# 标题",
		},
		{
			name:     "no category line left untouched",
			content:  "# 标题\n正文",
			wantCat:  nil,
			wantRest: "# 标题\n正文",
		},
		{
			name:     "fullwidth colon and separators",
			content:  "CATEGORY：组织｜企业\n正文",
			wantCat:  []string{"组织", "企业"},
			wantRest: "正文",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat, rest := splitCategoryLine(tt.content)
			if strings.Join(cat, "/") != strings.Join(tt.wantCat, "/") {
				t.Fatalf("splitCategoryLine() cat = %v, want %v", cat, tt.wantCat)
			}
			if rest != tt.wantRest {
				t.Fatalf("splitCategoryLine() rest = %q, want %q", rest, tt.wantRest)
			}
		})
	}
}

func TestFormatExistingTaxonomyForPrompt(t *testing.T) {
	got := formatExistingTaxonomyForPrompt([][]string{
		{"春节", "传统习俗"},
		{"春节", "文化习俗", "节日习俗"},
		{"春节习俗"},
		{"产品定位"},
	})
	want := "产品定位\n" +
		"春节\n" +
		"  传统习俗\n" +
		"  文化习俗\n" +
		"    节日习俗\n" +
		"春节习俗"
	if got != want {
		t.Fatalf("formatExistingTaxonomyForPrompt():\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatExistingTaxonomyForPromptEmpty(t *testing.T) {
	if got := formatExistingTaxonomyForPrompt(nil); got != "" {
		t.Fatalf("formatExistingTaxonomyForPrompt(nil) = %q, want empty", got)
	}
}

func TestDedupeWikiCategoryPaths(t *testing.T) {
	got := dedupeWikiCategoryPaths([][]string{
		{"春节", "传统习俗"},
		{" 春节 ", "传统习俗"},
		{"春节", "文化习俗"},
		{"", "空"},
		{},
	}, 10)
	if len(got) != 3 {
		t.Fatalf("dedupeWikiCategoryPaths() len = %d, want 3: %v", len(got), got)
	}
	if got[0][0] != "春节" || got[0][1] != "传统习俗" {
		t.Fatalf("dedupeWikiCategoryPaths()[0] = %v", got[0])
	}
	if got[1][1] != "文化习俗" {
		t.Fatalf("dedupeWikiCategoryPaths()[1] = %v", got[1])
	}
}

func TestResolveExistingTaxonomyForPrompt(t *testing.T) {
	t.Run("nil batch context", func(t *testing.T) {
		got := resolveExistingTaxonomyForPrompt(t.Context(), nil)
		if got == "" {
			t.Fatal("expected fallback text for nil batch context")
		}
	})
	t.Run("batch context with taxonomy", func(t *testing.T) {
		batchCtx := &WikiBatchContext{
			ExistingTaxonomy: func(ctx context.Context) string {
				return "春节\n  传统习俗"
			},
		}
		got := resolveExistingTaxonomyForPrompt(t.Context(), batchCtx)
		if got != "春节\n  传统习俗" {
			t.Fatalf("resolveExistingTaxonomyForPrompt() = %q", got)
		}
	})
}

func TestRemapCategoryPath(t *testing.T) {
	remap := map[string][]string{
		categoryPathKey([]string{"春节习俗"}):         {"春节", "传统习俗"},
		categoryPathKey([]string{"组织单位", "致谢人员"}): {"组织"},
	}

	tests := []struct {
		name    string
		current []string
		want    []string
		wantHit bool
	}{
		{
			name:    "single-label synonym remapped",
			current: []string{"春节习俗"},
			want:    []string{"春节", "传统习俗"},
			wantHit: true,
		},
		{
			name:    "document-role path collapsed to coarse folder",
			current: []string{"组织单位", "致谢人员"},
			want:    []string{"组织"},
			wantHit: true,
		},
		{
			name:    "noise normalized before matching",
			current: []string{"实体/春节习俗"},
			want:    []string{"春节", "传统习俗"},
			wantHit: true,
		},
		{
			name:    "unrelated path untouched",
			current: []string{"人物"},
			wantHit: false,
		},
		{
			name:    "empty path untouched",
			current: nil,
			wantHit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, hit := remapCategoryPath(tt.current, remap)
			if hit != tt.wantHit {
				t.Fatalf("remapCategoryPath() hit = %v, want %v", hit, tt.wantHit)
			}
			if !hit {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("remapCategoryPath() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("remapCategoryPath()[%d] = %q, want %q (full=%v)", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}
