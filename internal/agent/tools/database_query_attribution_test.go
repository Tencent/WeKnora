package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryReferencesChunksCannotBeBypassedByProjectionShape(t *testing.T) {
	for _, query := range []string{
		"SELECT c.id, c.chunk_index FROM chunks c",
		"SELECT c.content FROM chunks c",
		"SELECT CONCAT(c.content, '') AS excerpt FROM chunks c",
		"SELECT * FROM chunks",
		"SELECT c FROM chunks c",
		"SELECT row_to_json(c) FROM chunks c",
		"SELECT COUNT(*) FROM chunks",
	} {
		require.True(t, queryReferencesChunks(query), query)
	}

	for _, query := range []string{
		"SELECT k.description AS content FROM knowledges k",
		"SELECT COUNT(*) FROM knowledges",
	} {
		require.False(t, queryReferencesChunks(query), query)
	}
}
