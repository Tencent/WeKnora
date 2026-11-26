package searchutil

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractSnippet_AroundHit(t *testing.T) {
	content := strings.Repeat("前", 80) + "部署手册" + strings.Repeat("后", 80)
	got := ExtractSnippet(content, "部署")
	assert.Contains(t, got, "部署")
	assert.True(t, strings.HasPrefix(got, "... "))
	assert.True(t, strings.HasSuffix(got, " ..."))
	assert.Less(t, len([]rune(got)), 250)
}

func TestExtractSnippet_InvalidRegex(t *testing.T) {
	assert.Empty(t, ExtractSnippet("hello world", "("))
}

func TestExtractSnippet_EmptyInputs(t *testing.T) {
	assert.Empty(t, ExtractSnippet("", "q"))
	assert.Empty(t, ExtractSnippet("body", ""))
}

func TestExtractSnippet_RuneLimits(t *testing.T) {
	before := strings.Repeat("前", 59) + "🙂"
	after := "🚀" + strings.Repeat("后", 59)
	match := strings.Repeat("中", 99) + "🧪"
	content := "outside" + before + match + "界" + after + "outside"
	assert.Equal(t, "... "+before+match+"..."+after+" ...", ExtractSnippet(content, "中+🧪界"))
}

func TestExtractSnippet_ReplacesEachInvalidByte(t *testing.T) {
	assert.Equal(t, "... 前��匹配��后 ...", ExtractSnippet("前\xff\xfe匹配\xfe\xff后", "匹配"))
}

func BenchmarkExtractSnippet_LargePage(b *testing.B) {
	content := strings.Repeat("文", 65536) + "MATCH" + strings.Repeat("字", 65536)
	b.ReportAllocs()
	for b.Loop() {
		if ExtractSnippet(content, "match") == "" {
			b.Fatal("match missing from snippet")
		}
	}
}
