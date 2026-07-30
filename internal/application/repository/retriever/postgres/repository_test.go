package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPostgresEmbeddingUpsertUpdatesExistingSource(t *testing.T) {
	clause := postgresEmbeddingUpsertClause()
	require.False(t, clause.DoNothing)
	require.Len(t, clause.Columns, 2)
	require.Equal(t, "source_id", clause.Columns[0].Name)
	require.Equal(t, "source_type", clause.Columns[1].Name)

	updateColumns := make([]string, 0, len(clause.DoUpdates))
	for _, assignment := range clause.DoUpdates {
		updateColumns = append(updateColumns, assignment.Column.Name)
	}
	require.ElementsMatch(t, []string{
		"chunk_id",
		"knowledge_id",
		"knowledge_base_id",
		"tag_id",
		"content",
		"dimension",
		"embedding",
		"is_enabled",
		"updated_at",
	}, updateColumns)
}
