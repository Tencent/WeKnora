package chunker

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHeadingScansRespectFences(t *testing.T) {
	cases := []struct {
		name, open, body, close string
	}{
		{"backticks", "```go", "# Fake\n## Fake sub", "```"},
		{"tildes", "~~~markdown", "# Fake\n## Fake sub", "~~~"},
		{"long backticks", "````markdown", "```\n# Fake\n## Fake sub", "````"},
		{"long tildes", "~~~~", "~~~\n# Fake\n## Fake sub", "~~~~~"},
		{"mismatched closer", "```", "~~~\n# Fake\n## Fake sub", "```"},
		{"closer with text", "```", "``` trailing\n# Fake\n## Fake sub", "```"},
		{"indented closer", "```", "    ```\n# Fake\n## Fake sub", "```"},
		{"tab indented closer", "```", "\t```\n# Fake\n## Fake sub", "```"},
		{"indented fences", "   ~~~go", "# Fake\n## Fake sub", "  ~~~~ \t"},
		{"backtick info", "```go title=example", "# Fake\n## Fake sub", "```"},
		{"tilde info with backtick", "~~~go `example`", "# Fake\n## Fake sub", "~~~"},
		{"unclosed", "~~~", "# Fake\n## Fake sub", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := "# Real\n" + tc.open + "\n" + tc.body
			wantTotal := 1
			if tc.close != "" {
				doc += "\n" + tc.close + "\n# After\n## Real sub"
				wantTotal = 3
			}
			p := ProfileDocument(doc)
			if p.MdHeadingTotal != wantTotal || p.MdHeadingCounts[1] != wantTotal/2+1 ||
				p.MdHeadingCounts[2] != wantTotal/2 {
				t.Errorf("unexpected heading counts: %v (total %d)", p.MdHeadingCounts, p.MdHeadingTotal)
			}
			codeChars := 0
			for _, line := range strings.Split(tc.body, "\n") {
				codeChars += utf8.RuneCountInString(line)
			}
			wantRatio := float64(codeChars) / float64(utf8.RuneCountInString(doc))
			if !p.HasCode || math.Abs(p.CodeRatio-wantRatio) > 1e-12 {
				t.Errorf("HasCode=%v CodeRatio=%g, want true and %g", p.HasCode, p.CodeRatio, wantRatio)
			}

			bounds := findHeadingBoundaries(doc, 1)
			if len(bounds) != wantTotal/2+1 || bounds[0].line != "# Real" {
				t.Errorf("unexpected boundaries: %+v", bounds)
			}
			if tc.close != "" && len(bounds) == 2 {
				wantOffset := utf8.RuneCountInString(doc[:strings.Index(doc, "# After")])
				if bounds[1].line != "# After" || bounds[1].runeStart != wantOffset {
					t.Errorf("unexpected closing boundary: %+v, want offset %d", bounds[1], wantOffset)
				}
			}

			seed := NewHeadingHierarchy()
			seed.Observe("# Real")
			bcs := sectionBreadcrumbs([]rune(doc), 1, *seed)
			observeSubHeadings([]rune(doc), 1, seed)
			wantBreadcrumb := "# Real"
			if tc.close != "" {
				wantBreadcrumb += "\n## Real sub"
			}
			if seed.BreadcrumbWithHashes() != wantBreadcrumb || len(bcs) != wantTotal/2+1 ||
				bcs[len(bcs)-1].breadcrumb != wantBreadcrumb {
				t.Errorf("unexpected heading context: %q, breadcrumbs: %+v", seed.BreadcrumbWithHashes(), bcs)
			}
		})
	}
}

func TestHeadingScansRejectInvalidFenceOpeners(t *testing.T) {
	for _, open := range []string{"``", "~~", "    ```", "\t~~~", "```go `example`", "`~`"} {
		t.Run(open, func(t *testing.T) {
			doc := open + "\n# Real\n## Real sub"
			p := ProfileDocument(doc)
			if p.HasCode || p.CodeRatio != 0 || p.MdHeadingTotal != 2 {
				t.Errorf("invalid opener hid headings or counted code: %+v", p)
			}
			bounds := findHeadingBoundaries(doc, 1)
			if len(bounds) != 2 || bounds[1].line != "# Real" {
				t.Errorf("unexpected boundaries: %+v", bounds)
			}
			h := NewHeadingHierarchy()
			h.Observe("# Seed")
			bcs := sectionBreadcrumbs([]rune(doc), 1, *h)
			observeSubHeadings([]rune(doc), 1, h)
			if h.BreadcrumbWithHashes() != "# Seed\n## Real sub" || len(bcs) != 2 {
				t.Errorf("unexpected context: %q, breadcrumbs: %+v", h.BreadcrumbWithHashes(), bcs)
			}
		})
	}
}

func TestSplitByHeadings_FencedSubHeadingContext(t *testing.T) {
	for _, fence := range []string{"~~~", "````"} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fence+newline, func(t *testing.T) {
				filler := strings.Repeat("中文 clause body sentence. ", 25)
				doc := "# Real\n" + filler + "\n## Section\n" + filler + "\n" +
					fence + "markdown\n```\n### Fake\n" + filler + "\n" + fence + "\n" +
					filler + "AFTER_CODE_MARKER\n" + filler + "\n### Real sub\n" + filler +
					"REAL_SUB_MARKER\n" + filler + "\n## Next\n" + filler + "\n## Last\n" + filler
				doc = strings.ReplaceAll(doc, "\n", newline)
				cfg := SplitterConfig{ChunkSize: 200, ChunkOverlap: 20, Separators: []string{". "}}
				chunks := splitByHeadingsImpl(doc, cfg, nil)
				if len(chunks) < 4 {
					t.Fatalf("expected large section to be sub-split, got %d chunks", len(chunks))
				}
				runes := []rune(doc)
				seenCode, seenSub := false, false
				for i, c := range chunks {
					if c.Seq != i || c.Start < 0 || c.End < c.Start || c.End > len(runes) {
						t.Fatalf("invalid chunk position or sequence: %+v", c)
					}
					if c.End-c.Start != utf8.RuneCountInString(c.Content) || string(runes[c.Start:c.End]) != c.Content {
						t.Errorf("chunk %d does not match its source slice", i)
					}
					if strings.Contains(c.ContextHeader, "Fake") {
						t.Errorf("chunk %d contains code heading in context: %q", i, c.ContextHeader)
					}
					if strings.Contains(c.Content, "AFTER_CODE_MARKER") {
						seenCode = true
						if c.ContextHeader != "# Real\n## Section" {
							t.Errorf("context after code = %q", c.ContextHeader)
						}
					}
					if strings.Contains(c.Content, "REAL_SUB_MARKER") {
						seenSub = true
						if c.ContextHeader != "# Real\n## Section\n### Real sub" {
							t.Errorf("context after real sub-heading = %q", c.ContextHeader)
						}
					}
				}
				if !seenCode || !seenSub {
					t.Error("marker content lost during chunking")
				}
			})
		}
	}
}
