package tools

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ErrArtifactUnavailable lets a resource read fall back to a separately authorized source.
var ErrArtifactUnavailable = errors.New("generated file is unavailable in this conversation")

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

func (t *ReadFileTool) readResource(ctx context.Context, input ReadFileInput) *types.ToolResult {
	knowledgeFallback := t.knowledgeImages != nil && strings.HasPrefix(input.Path, types.ResourceScheme)
	if t.artifacts == nil && !knowledgeFallback {
		return &types.ToolResult{Success: false, Error: "generated file access is unavailable in this conversation"}
	}
	if input.Offset < 0 || input.LineOffset < 0 || input.Limit < 0 || input.MaxBytes < 0 {
		return &types.ToolResult{Success: false, Error: "read offsets and limits must be non-negative"}
	}
	if strings.TrimSpace(input.Query) != "" && input.LineOffset != 0 {
		return &types.ToolResult{Success: false, Error: "line_offset cannot be combined with query"}
	}
	if t.artifacts != nil {
		data, err := t.artifacts.Read(ctx, input.Path)
		if err == nil {
			return renderFilePage(ctx, input, data, resolveSessionID(ctx), input.Path, types.ResourceScheme)
		}
		if !knowledgeFallback || !errors.Is(err, ErrArtifactUnavailable) {
			return &types.ToolResult{Success: false, Error: err.Error()}
		}
	}
	return t.readKnowledgeImage(ctx, input)
}
