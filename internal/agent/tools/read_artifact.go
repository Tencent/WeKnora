package tools

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ArtifactFileSource authorizes persisted files independently of sandbox paths.
// Implementations must restrict references to the bound conversation's outputs.
type ArtifactFileSource interface {
	Read(context.Context, string) ([]byte, error)
}

// WithArtifacts adds authorized persisted outputs without granting sandbox access.
func (t *ReadFileTool) WithArtifacts(source ArtifactFileSource) *ReadFileTool {
	t.artifacts = source
	t.updateDescription()
	return t
}

func (t *ReadFileTool) readArtifact(ctx context.Context, input ReadFileInput) *types.ToolResult {
	if t.artifacts == nil {
		return &types.ToolResult{Success: false, Error: "generated file access is unavailable in this conversation"}
	}
	if input.Offset < 0 || input.LineOffset < 0 || input.Limit < 0 || input.MaxBytes < 0 {
		return &types.ToolResult{Success: false, Error: "read offsets and limits must be non-negative"}
	}
	if strings.TrimSpace(input.Query) != "" && input.LineOffset != 0 {
		return &types.ToolResult{Success: false, Error: "line_offset cannot be combined with query"}
	}
	data, err := t.artifacts.Read(ctx, input.Path)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}
	}
	return renderFilePage(ctx, input, data, resolveSessionID(ctx), input.Path, types.ResourceScheme)
}
