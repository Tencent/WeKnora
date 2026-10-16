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
