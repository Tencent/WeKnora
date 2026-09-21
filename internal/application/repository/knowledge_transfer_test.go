package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestTransferCheckpointReturnsStoredTimestampPrecision(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	// Emulate a database that stores fewer fractional digits than time.Now.
	require.NoError(t, db.Exec(`CREATE TRIGGER truncate_checkpoint_time AFTER UPDATE ON knowledges
        BEGIN UPDATE knowledges SET updated_at = strftime('%Y-%m-%d %H:%M:%S', NEW.updated_at) || '+00:00'
        WHERE id = NEW.id; END`).Error)
	row := &types.Knowledge{ID: "doc", TenantID: 7, KnowledgeBaseID: "source", ParseStatus: types.ParseStatusCompleted}
	require.NoError(t, db.Omit("custom_metadata").Create(row).Error)
	repo := NewKnowledgeRepository(db)
	before, err := repo.GetKnowledgeByID(context.Background(), 7, "doc")
	require.NoError(t, err)
	after := *before
	after.ParseStatus = types.ParseStatusProcessing
	require.NoError(t, repo.UpdateKnowledgeForTransfer(context.Background(), before, &after))
	require.Zero(t, after.UpdatedAt.Nanosecond())
	final := after
	final.KnowledgeBaseID = "target"
	final.ParseStatus = types.ParseStatusCompleted
	require.NoError(t, repo.UpdateKnowledgeForTransfer(context.Background(), &after, &final))
}
