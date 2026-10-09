package modelcontext

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestImageSourcesUseOnlyKnownHandlesAndAccountForUserImages(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterChunk(ChunkReference{ChunkID: "known-source"})
	note := registry.ImageSourcesNote([]string{"known-source", "unverified-source"}, 2)
	require.Contains(t, note, "Image 3: chunk c1.")
	require.Contains(t, note, "Image 4: source unavailable")
	require.NotContains(t, note, "unverified-source")
	require.NotContains(t, note, "known-source")
	require.Empty(t, registry.ChunkHandle("unverified-source"))
	stored := &types.ToolResult{Data: map[string]interface{}{"context_images": []string{"known-source"}}}
	require.Empty(t, registry.ToolImageSourcesNote(stored), "stored history has no image bytes")
	var missing *Registry
	require.Contains(t, missing.ImageSourcesNote([]string{"known-source"}, 0), "source unavailable")
}
