package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRecursiveItemUnchanged(t *testing.T) {
	item := &types.FetchedItem{
		Title: "linked", FileName: "folder/linked.md", Content: []byte("unchanged content"),
		Metadata: map[string]string{"recursive": "true"},
	}
	existing := &types.Knowledge{
		Title: item.Title, FileName: item.FileName, FileHash: calculateStr(string(item.Content)),
		ParseStatus: types.ParseStatusCompleted,
	}
	require.True(t, recursiveItemUnchanged(existing, item))
	existing.ParseStatus = types.ParseStatusFailed
	require.False(t, recursiveItemUnchanged(existing, item), "failed parses must be retried")
	existing.ParseStatus = types.ParseStatusCompleted
	item.Content = []byte("edited")
	require.False(t, recursiveItemUnchanged(existing, item))
	item.Content = []byte("unchanged content")
	item.FileName = "moved/linked.md"
	require.False(t, recursiveItemUnchanged(existing, item))
	item.FileName = existing.FileName
	item.Metadata = nil
	require.False(t, recursiveItemUnchanged(existing, item), "ordinary sync behaviour is unchanged")
}
