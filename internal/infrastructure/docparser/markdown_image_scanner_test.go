package docparser

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestScanMarkdownImageTargets(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		targets []string
	}{
		{
			name:    "without title",
			input:   `before ![a](images/a.png) after`,
			targets: []string{`images/a.png`},
		},
		{
			name:    "double quoted title",
			input:   `![a](images/a.png "图")`,
			targets: []string{`images/a.png "图"`},
		},
		{
			name:    "single quoted title",
			input:   `![a](images/a.png '图')`,
			targets: []string{`images/a.png '图'`},
		},
		{
			name:    "parenthesized title",
			input:   `![a](images/a.png (图))`,
			targets: []string{`images/a.png (图)`},
		},
		{
			name:    "title containing right paren",
			input:   `![a](images/a.png "阶段 1) 结果")`,
			targets: []string{`images/a.png "阶段 1) 结果"`},
		},
		{
			name:    "title containing both parens",
			input:   `![a](images/a.png '阶段 (1) 结果')`,
			targets: []string{`images/a.png '阶段 (1) 结果'`},
		},
		{
			name:    "escaped quote in title",
			input:   `![a](images/a.png "阶段 \"1\"")`,
			targets: []string{`images/a.png "阶段 \"1\""`},
		},
		{
			name:    "multiline title",
			input:   "![a](images/a.png\n  \"多行 title\")",
			targets: []string{"images/a.png\n  \"多行 title\""},
		},
		{
			name:    "spaced path",
			input:   `![a](images/第 1 页.png)`,
			targets: []string{`images/第 1 页.png`},
		},
		{
			name:    "path containing balanced parens",
			input:   `![a](images/a_(1).png "title")`,
			targets: []string{`images/a_(1).png "title"`},
		},
		{
			name:    "angle destination",
			input:   `![a](<images/a b.png> "title")`,
			targets: []string{`<images/a b.png> "title"`},
		},
		{
			name:    "malformed image is skipped",
			input:   `![a](images/a.png "unterminated title) after ![b](images/b.png)`,
			targets: []string{`images/b.png`},
		},
		{
			name:    "escaped image marker is skipped",
			input:   `\![a](images/a.png) ![b](images/b.png)`,
			targets: []string{`images/b.png`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans := scanMarkdownImageTargets(tt.input)
			if len(spans) != len(tt.targets) {
				t.Fatalf("got %d spans, want %d: %#v", len(spans), len(tt.targets), spans)
			}
			for i, span := range spans {
				got := tt.input[span.TargetStart:span.TargetEnd]
				if got != tt.targets[i] {
					t.Fatalf("target %d = %q, want %q", i, got, tt.targets[i])
				}
			}
		})
	}
}

func TestSplitMarkdownImageTarget(t *testing.T) {
	refMap := map[string]types.ImageRef{
		"images/a.png":          {OriginalRef: "images/a.png"},
		"images/第 1 页.png":      {OriginalRef: "images/第 1 页.png"},
		"images/a_(1).png":      {OriginalRef: "images/a_(1).png"},
		"images/第 1 页 (测试).gif": {OriginalRef: "images/第 1 页 (测试).gif"},
	}

	tests := []struct {
		name     string
		raw      string
		wantPath string
		wantText string
		wantOK   bool
	}{
		{
			name:     "without title",
			raw:      `images/a.png`,
			wantPath: `images/a.png`,
			wantText: `local://stored`,
			wantOK:   true,
		},
		{
			name:     "double quoted title",
			raw:      `images/a.png "图"`,
			wantPath: `images/a.png`,
			wantText: `local://stored "图"`,
			wantOK:   true,
		},
		{
			name:     "single quoted title",
			raw:      `images/a.png '图'`,
			wantPath: `images/a.png`,
			wantText: `local://stored '图'`,
			wantOK:   true,
		},
		{
			name:     "parenthesized title",
			raw:      `images/a.png (阶段 (1) 结果)`,
			wantPath: `images/a.png`,
			wantText: `local://stored (阶段 (1) 结果)`,
			wantOK:   true,
		},
		{
			name:     "multiline title",
			raw:      "images/a.png\n  \"多行 title\"",
			wantPath: `images/a.png`,
			wantText: "local://stored\n  \"多行 title\"",
			wantOK:   true,
		},
		{
			name:     "path with spaces wins without title",
			raw:      `images/第 1 页.png`,
			wantPath: `images/第 1 页.png`,
			wantText: `local://stored`,
			wantOK:   true,
		},
		{
			name:     "path with balanced parens",
			raw:      `images/a_(1).png "title"`,
			wantPath: `images/a_(1).png`,
			wantText: `local://stored "title"`,
			wantOK:   true,
		},
		{
			name:     "angle destination replaces only inner path",
			raw:      `<images/第 1 页 (测试).gif> "阶段 1) 图片"`,
			wantPath: `images/第 1 页 (测试).gif`,
			wantText: `<local://stored> "阶段 1) 图片"`,
			wantOK:   true,
		},
		{
			name:   "unknown reference",
			raw:    `images/missing.png "title"`,
			wantOK: false,
		},
		{
			name:   "blank line in title is invalid",
			raw:    "images/a.png \"line1\n\nline2\"",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, pathStart, pathEnd, ok := splitMarkdownImageTarget(tt.raw, refMap)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if path != tt.wantPath {
				t.Fatalf("path = %q, want %q", path, tt.wantPath)
			}
			replaced := tt.raw[:pathStart] + "local://stored" + tt.raw[pathEnd:]
			if replaced != tt.wantText {
				t.Fatalf("replacement = %q, want %q", replaced, tt.wantText)
			}
			if strings.Contains(replaced, tt.wantPath) {
				t.Fatalf("replacement still contains original path: %q", replaced)
			}
		})
	}
}
