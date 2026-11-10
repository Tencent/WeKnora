package modelcontext

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ImageSourcesNote binds images to handles already present in the final model
// context. offset counts user images preceding the retrieved ones in this
// message. Unknown sources stay explicitly unavailable, never fabricated.
func (r *Registry) ImageSourcesNote(chunkIDs []string, offset int) string {
	if len(chunkIDs) == 0 {
		return ""
	}
	var note strings.Builder
	note.WriteString("\n\nRetrieved image sources (image numbers are 1-based within this message):")
	for i, id := range chunkIDs {
		if handle := r.ChunkHandle(id); handle != "" {
			fmt.Fprintf(&note, "\nImage %d: chunk %s.", offset+i+1, handle)
		} else {
			fmt.Fprintf(&note, "\nImage %d: source unavailable; do not invent a citation.", offset+i+1)
		}
	}
	return note.String()
}

// ToolImageSourcesNote is rendered beside the actual images after tool result
// rendering has registered its source handles. Stored results have no images
// and must not claim to attach them on replay.
func (r *Registry) ToolImageSourcesNote(result *types.ToolResult) string {
	if result == nil || len(result.Images) == 0 {
		return ""
	}
	ids := stringSliceValue(result.Data["context_images"])
	return r.ImageSourcesNote(ids[:min(len(ids), len(result.Images))], 0)
}
