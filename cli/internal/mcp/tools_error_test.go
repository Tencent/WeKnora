package mcp

import (
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/output"
	sdk "github.com/Tencent/WeKnora/client"
)

// TestToolErrorResult_TypedError_PopulatesStructuredContent verifies the
// helper maps a typed cmdutil.Error to MCP StructuredContent as *output.ErrDetail.
func TestToolErrorResult_TypedError_PopulatesStructuredContent(t *testing.T) {
	err := cmdutil.NewError(cmdutil.CodeAuthUnauthenticated, "no creds")
	res := toolErrorResult(err)
	if !res.IsError {
		t.Errorf("expected IsError true")
	}
	if res.StructuredContent == nil {
		t.Fatal("expected StructuredContent populated")
	}
	sc, ok := res.StructuredContent.(*output.ErrDetail)
	if !ok {
		t.Fatalf("StructuredContent has unexpected type %T", res.StructuredContent)
	}
	if sc.Type != "auth.unauthenticated" {
		t.Errorf("type: got %v want auth.unauthenticated", sc.Type)
	}
	// retry_command should fall back to "weknora auth login" via cmdutil default
	if sc.RetryCommand != "weknora auth login" {
		t.Errorf("retry_command: got %v", sc.RetryCommand)
	}
}

// TestToolErrorResult_TextFallback verifies the human-readable Content[0]
// contains code + message + (hint? + retry?) lines.
func TestToolErrorResult_TextFallback(t *testing.T) {
	err := cmdutil.NewError(cmdutil.CodeKBIDRequired, "kb_id is required")
	res := toolErrorResult(err)
	if len(res.Content) == 0 {
		t.Fatal("expected Content[0]")
	}
	// Assertion is best-effort on the text shape; exact prose may vary
	// with hint/retry defaults. Lock the type code prefix.
	text := contentText(t, res.Content[0])
	if !strings.Contains(text, "local.kb_id_required: kb_id is required") {
		t.Errorf("text fallback missing code:message; got %q", text)
	}
}

// contentText extracts the Text field from an mcpsdk.Content value.
// Lives in the test file to avoid leaking helpers into production.
func contentText(t *testing.T, c any) string {
	t.Helper()
	// Direct assertion to the concrete type used by toolErrorResult.
	if tc, ok := c.(*mcpsdk.TextContent); ok {
		return tc.Text
	}
	// Fallback: some go-sdk versions may expose a GetText() method.
	if tc, ok := c.(interface{ GetText() string }); ok {
		return tc.GetText()
	}
	t.Fatalf("cannot extract text from Content of type %T", c)
	return ""
}

// TestToolErrorResult_IsErrorFlagSet verifies IsError is always true.
func TestToolErrorResult_IsErrorFlagSet(t *testing.T) {
	res := toolErrorResult(cmdutil.NewError(cmdutil.CodeInputMissingFlag, "doc_id is required"))
	if !res.IsError {
		t.Error("expected IsError=true")
	}
}

// TestToolErrorResult_ContentPresent verifies Content slice is non-empty.
func TestToolErrorResult_ContentPresent(t *testing.T) {
	res := toolErrorResult(cmdutil.NewError(cmdutil.CodeInputInvalidArgument, "page_size must be in 1..1000"))
	if len(res.Content) == 0 {
		t.Error("expected at least one Content item")
	}
}

// TestToolErrorResult_NetworkError verifies a network error code round-trips
// through toolErrorResult with the correct type in StructuredContent.
func TestToolErrorResult_NetworkError(t *testing.T) {
	err := cmdutil.NewError(cmdutil.CodeNetworkError, "dial failed")
	res := toolErrorResult(err)
	if !res.IsError {
		t.Error("expected IsError=true")
	}
	sc, ok := res.StructuredContent.(*output.ErrDetail)
	if !ok {
		t.Fatalf("StructuredContent has unexpected type %T", res.StructuredContent)
	}
	if sc.Type != "network.error" {
		t.Errorf("type: got %v want network.error", sc.Type)
	}
}

// Suppress unused-import warning if sdk isn't used in this file.
var _ = sdk.KnowledgeBase{}
