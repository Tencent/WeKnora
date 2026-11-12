package confluence

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfluenceFullSyncKeepsFailedPageForDeletionReconciliation(t *testing.T) {
	const deleted = "deleted"
	const surviving = "surviving"
	previous := streamCursor(map[string]string{deleted: "v:1", surviving: "v:1"})
	first := &streamAPI{
		pages:      []streamPage{{id: deleted, title: "Old", version: 2}, {id: surviving, title: "Other", version: 1}},
		bodyStatus: map[string]int{deleted: http.StatusForbidden},
	}
	partial := &captureHandler{}
	next, err := newStreamConnector(first).FetchFullStream(t.Context(), streamConfig(), previous, partial)
	require.NoError(t, err)
	require.Len(t, partial.items, 2)
	require.Equal(t, deleted, partial.items[0].ExternalID)
	require.Empty(t, partial.items[0].Content)
	require.False(t, partial.items[0].IsDeleted)

	second := &streamAPI{pages: []streamPage{{id: surviving, title: "Other", version: 1}}}
	result := &captureHandler{}
	_, err = newStreamConnector(second).FetchStream(t.Context(), streamConfig(), next, result)
	require.NoError(t, err)
	require.Len(t, result.items, 1, "the previously indexed page needs a deletion event")
	require.Equal(t, deleted, result.items[0].ExternalID)
	require.True(t, result.items[0].IsDeleted)
	require.Zero(t, second.bodyCalls, "the unchanged page should not be fetched")
}
