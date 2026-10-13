package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPythonQuoteCheckAllowsPrefixedAdjacentLiterals(t *testing.T) {
	for _, src := range []string{
		`value = "prefix" r"\n"`,
		`value = "prefix" u"suffix"`,
		`value = "prefix" f"{1 + 2}"`,
		`value = "prefix" fr"{1 + 2}\n"`,
		`value = "prefix" RF"{1 + 2}\n"`,
		`value = b"prefix" br"\n"`,
		`value = B"prefix" RB"\n"`,
		`value = "prefix"r"suffix"`,
		`value = "prefix" r"""suffix"""`,
		`value = r"first" u"second" f"{3}"`,
	} {
		t.Run(src, func(t *testing.T) {
			assert.Empty(t, pythonScriptSyntaxHint("/workspace/output/result.py", src, ToolEditSandboxFile))
		})
	}
}

func TestWritePythonAdjacentLiteralsReportsSuccess(t *testing.T) {
	const src = `value = "prefix" r"\n"`
	const filePath = "/workspace/output/result.py"
	sink := &fakeSandboxFileSink{}
	args, err := json.Marshal(map[string]string{"path": filePath, "content": src})
	require.NoError(t, err)
	result, err := NewWriteSandboxFileTool(sink, 0).Execute(sandboxFileTestContext(), args)
	require.NoError(t, err)
	assert.True(t, result.Success, result.Error)
	assert.NotContains(t, result.Data, "syntax_error")
	assert.Equal(t, src, string(sink.files[filePath]))
}

func TestPythonQuoteCheckStillScansAdjacentLiteralContents(t *testing.T) {
	for _, src := range []string{
		`value = "prefix" r"unterminated`,
		`value = "prefix" f"unterminated`,
		`value = "prefix" r"suffix"broken`,
		`value = "prefix"result`,
	} {
		t.Run(src, func(t *testing.T) {
			assert.NotEmpty(t, pythonScriptSyntaxHint("/workspace/output/result.py", src, ToolEditSandboxFile))
		})
	}
}
