package service

import (
	"strings"
	"testing"
)

func TestSanitizeGeneratedTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		raw           string
		want          string
		wantTruncated bool
	}{
		{
			name: "plain title is kept as is",
			raw:  "保单理赔流程咨询",
			want: "保单理赔流程咨询",
		},
		{
			name: "thinking prefix and surrounding whitespace are dropped",
			raw:  "<think>\n\n</think>  Claim filing steps \n",
			want: "Claim filing steps",
		},
		{
			name:          "over-long ascii title is truncated",
			raw:           strings.Repeat("a", 300),
			want:          strings.Repeat("a", maxSessionTitleRunes),
			wantTruncated: true,
		},
		{
			name:          "over-long cjk title is truncated by rune, not byte",
			raw:           strings.Repeat("保", 300),
			want:          strings.Repeat("保", maxSessionTitleRunes),
			wantTruncated: true,
		},
		{
			name: "title exactly at the limit is not truncated",
			raw:  strings.Repeat("保", maxSessionTitleRunes),
			want: strings.Repeat("保", maxSessionTitleRunes),
		},
		{
			name: "wrapping quotes some models add are dropped",
			raw:  "“询问一加一等于多少”",
			want: "询问一加一等于多少",
		},
		{
			name: "empty completion stays empty",
			raw:  "   ",
			want: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, truncated := sanitizeGeneratedTitle(tt.raw)
			if got != tt.want {
				t.Fatalf("title = %q, want %q", got, tt.want)
			}
			if truncated != tt.wantTruncated {
				t.Fatalf("truncated = %v, want %v", truncated, tt.wantTruncated)
			}
			if len([]rune(got)) > maxSessionTitleRunes {
				t.Fatalf("title still exceeds %d runes: %d", maxSessionTitleRunes, len([]rune(got)))
			}
		})
	}
}

// The database column is VARCHAR(255) in every shipped migration; guard the
// constant so nobody raises it past what the column can hold.
func TestMaxSessionTitleRunesFitsColumn(t *testing.T) {
	t.Parallel()
	// Worst case for UTF-8 is 4 bytes per rune, but the column counts characters
	// in PostgreSQL and bytes in some engines, so keep a conservative bound.
	if maxSessionTitleRunes > 255 {
		t.Fatalf("maxSessionTitleRunes=%d exceeds the sessions.title column limit", maxSessionTitleRunes)
	}
}

func TestLooksLikeLeakedAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		title string
		want  bool
	}{
		// Observed leaks: the model answered instead of titling.
		{name: "bare number", title: "42", want: true},
		{name: "number with unit", title: "8848米", want: true},
		{name: "decimal", title: "3.14", want: true},
		{name: "percent", title: "98%", want: true},
		{name: "equation", title: "6×7=42", want: true},
		{name: "equation with spaces", title: "6 * 7 = 42", want: true},
		{name: "english yes sentence", title: "Yes, they were both American.", want: true},
		{name: "bare no", title: "No", want: true},
		{name: "chinese yes", title: "是的", want: true},
		{name: "quoted number", title: "\"42\"", want: true},

		// Legitimate titles must never match.
		{name: "question rephrase", title: "六乘以七等于多少", want: false},
		{name: "topic title", title: "法国首都城市查询", want: false},
		{name: "english topic title", title: "France capital question", want: false},
		{name: "cjk topic with digits inside", title: "2026年预算规划讨论", want: false},
		{name: "year-scoped topic title", title: "2026年规划", want: false},
		{name: "name-like title", title: "42号公约解读", want: false},
		{name: "empty", title: "   ", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := looksLikeLeakedAnswer(tt.title); got != tt.want {
				t.Fatalf("looksLikeLeakedAnswer(%q) = %v, want %v", tt.title, got, tt.want)
			}
		})
	}
}

func TestFallbackSessionTitleFromQuestion(t *testing.T) {
	t.Parallel()

	t.Run("short question is kept as is", func(t *testing.T) {
		t.Parallel()
		if got := fallbackSessionTitleFromQuestion("法国的首都是哪里？"); got != "法国的首都是哪里？" {
			t.Fatalf("fallback = %q", got)
		}
	})

	t.Run("multiline question uses only the first line", func(t *testing.T) {
		t.Parallel()
		if got := fallbackSessionTitleFromQuestion("帮我看看这段代码\nfunc main() {}"); got != "帮我看看这段代码" {
			t.Fatalf("fallback = %q", got)
		}
	})

	t.Run("long question is truncated by rune and trimmed", func(t *testing.T) {
		t.Parallel()
		got := fallbackSessionTitleFromQuestion(strings.Repeat("问", maxSessionTitleRunes+50))
		if len([]rune(got)) != maxSessionTitleRunes {
			t.Fatalf("fallback rune count = %d, want %d", len([]rune(got)), maxSessionTitleRunes)
		}
	})

	t.Run("empty question yields empty title", func(t *testing.T) {
		t.Parallel()
		if got := fallbackSessionTitleFromQuestion("  \n "); got != "" {
			t.Fatalf("fallback = %q, want empty", got)
		}
	})
}
