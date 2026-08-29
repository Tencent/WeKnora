package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFirstBrokenPythonQuoteCatchesNestedASCIIQuotes(t *testing.T) {
	src := "slides = [\n" +
		"    (\"这不是一个\"大干快上\"的夜晚，而是一个适合整理、沉淀、\", False),\n" +
		"]\n"
	line, ok := firstBrokenPythonQuote(src)
	require.True(t, ok)
	assert.Equal(t, 2, line)
}

func TestFirstBrokenPythonQuoteAllowsKeywordAfterString(t *testing.T) {
	_, ok := firstBrokenPythonQuote(`x = "hello" if cond else "bye"`)
	assert.False(t, ok)
}

func TestFirstBrokenPythonQuoteAllowsImplicitConcat(t *testing.T) {
	_, ok := firstBrokenPythonQuote("x = \"hello\" \"world\"\n")
	assert.False(t, ok)
}

func TestFirstBrokenPythonQuoteAllowsSingleQuotedChinese(t *testing.T) {
	_, ok := firstBrokenPythonQuote("x = '这不是一个\"大干快上\"的夜晚'\n")
	assert.False(t, ok)
}

func TestFirstBrokenPythonQuoteAllowsCornerQuotes(t *testing.T) {
	_, ok := firstBrokenPythonQuote("x = \"这不是一个「大干快上」的夜晚\"\n")
	assert.False(t, ok)
}

func TestFirstBrokenPythonQuoteAllowsFString(t *testing.T) {
	_, ok := firstBrokenPythonQuote("x = f\"hello {name}\"\n")
	assert.False(t, ok)
}

func TestFirstBrokenPythonQuoteReportsUnclosed(t *testing.T) {
	line, ok := firstBrokenPythonQuote("x = \"hello\n")
	require.True(t, ok)
	assert.Equal(t, 1, line)
}

func TestPythonScriptSyntaxHintOnlyForPy(t *testing.T) {
	src := "(\"这不是一个\"大干快上\"的夜晚\")\n"
	assert.NotEmpty(t, pythonScriptSyntaxHint("/workspace/output/x.py", src))
	assert.Empty(t, pythonScriptSyntaxHint("/workspace/output/x.md", src))
}

func TestPythonSyntaxErrorHint(t *testing.T) {
	assert.Empty(t, pythonSyntaxErrorHint("ModuleNotFoundError: docx"))
	assert.Contains(t, pythonSyntaxErrorHint("SyntaxError: invalid syntax"), "edit_sandbox_file")
}
