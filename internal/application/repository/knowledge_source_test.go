package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeSourceConcurrentEditorsHaveOneWinner(t *testing.T) {
	db, repo, before, _ := replacementFixture(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// SQLite serializes writers. Use one connection so contenders wait for
	// the preceding SQL update instead of failing with SQLITE_BUSY.
	sqlDB.SetMaxOpenConns(1)
	type result struct {
		updated bool
		err     error
	}
	results := make(chan result, 8)
	for i := range 8 {
		go func() {
			updated, err := repo.UpdateKnowledgeSourceIfUnchanged(context.Background(), before, map[string]interface{}{
				"file_path": fmt.Sprintf("editor-%d.md", i), "parse_status": types.ParseStatusPending,
			})
			results <- result{updated, err}
		}()
	}
	winners := 0
	for range 8 {
		r := <-results
		require.NoError(t, r.err)
		if r.updated {
			winners++
		}
	}
	require.Equal(t, 1, winners)
}

func TestKnowledgeSourcePublishChecksSnapshotAndState(t *testing.T) {
	for _, change := range []string{"none", "path", "hash", "updated", "tenant", "processing", "deleting"} {
		t.Run(change, func(t *testing.T) {
			_, repo, before, _ := replacementFixture(t)
			ctx := context.Background()
			switch change {
			case "path":
				require.NoError(t, repo.UpdateKnowledgeColumn(ctx, before.ID, "file_path", "another.md"))
			case "hash":
				require.NoError(t, repo.UpdateKnowledgeColumn(ctx, before.ID, "file_hash", "another-hash"))
			case "updated":
				require.NoError(t, repo.UpdateKnowledgeColumn(ctx, before.ID,
					"updated_at", before.UpdatedAt.Add(time.Second)))
			case "tenant":
				before.TenantID++
			case "processing", "deleting":
				require.NoError(t, repo.UpdateKnowledgeColumn(ctx, before.ID, "parse_status", change))
			}
			values := map[string]interface{}{"file_path": "edited.md", "parse_status": types.ParseStatusPending}
			updated, err := repo.UpdateKnowledgeSourceIfUnchanged(ctx, before, values)
			require.NoError(t, err)
			require.Equal(t, change == "none", updated)
			if updated {
				// A second editor with the same snapshot cannot overwrite the winner.
				updated, err = repo.UpdateKnowledgeSourceIfUnchanged(ctx, before,
					map[string]interface{}{"file_path": "loser.md"})
				require.NoError(t, err)
				require.False(t, updated)
				current, err := repo.GetKnowledgeByID(ctx, before.TenantID, before.ID)
				require.NoError(t, err)
				require.Equal(t, "edited.md", current.FilePath)
			}
		})
	}
}
