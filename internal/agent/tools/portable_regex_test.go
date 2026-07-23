package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDatabaseRegexToolsRejectNonPortableSyntax(t *testing.T) {
	tests := []struct {
		name    string
		execute func() (*types.ToolResult, error)
	}{
		{
			name: "grep_chunks",
			execute: func() (*types.ToolResult, error) {
				return NewGrepChunksTool(nil, nil).Execute(
					context.Background(),
					json.RawMessage(`{"query":"\\d+"}`),
				)
			},
		},
		{
			name: "wiki_search",
			execute: func() (*types.ToolResult, error) {
				return NewWikiSearchTool(nil, nil, nil, nil).Execute(
					context.Background(),
					json.RawMessage(`{"queries":["\\d+"]}`),
				)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := testCase.execute()
			if err == nil {
				t.Fatal("expected non-portable regex to return an error")
			}
			if result == nil || result.Success {
				t.Fatalf("expected failed tool result, got %#v", result)
			}
			if !strings.Contains(result.Error, "portable") {
				t.Fatalf("expected actionable portable-regex error, got %q", result.Error)
			}
		})
	}
}
