package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLoadAgentHistoryAfterDeletingCoveredTurn(t *testing.T) {
	for _, checkpointed := range []bool{false, true} {
		name := "raw_history"
		if checkpointed {
			name = "checkpoint_history"
		}
		t.Run(name, func(t *testing.T) { checkDeletedTurnHistory(t, checkpointed) })
	}
}

func checkDeletedTurnHistory(t *testing.T, checkpointed bool) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "history.db")), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Message{}, &types.MessageArtifactRecord{}))
	repo := repository.NewMessageRepository(db)
	ctx := t.Context()
	const session = "deletion-session"
	const marker = "REVOKED_PROJECT_CODENAME_ORCHID"
	rows := storedTurns(3)
	rows[0].Content = "My private project is " + marker
	rows[1].Content = "Acknowledged " + marker
	for _, row := range rows {
		row.SessionID = session
		require.NoError(t, db.Create(row).Error)
	}
	if checkpointed {
		require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, session, rows[3].ID, &types.ContextCheckpoint{
			Summary: "The user's private project is " + marker + ". They also asked question 2.",
		}))
	}
	require.NoError(t, repo.DeleteMessage(ctx, session, rows[0].ID))
	require.NoError(t, repo.DeleteMessage(ctx, session, rows[1].ID))
	var remaining int64
	require.NoError(t, db.Model(&types.Message{}).
		Where("id IN ?", []string{rows[0].ID, rows[1].ID}).Count(&remaining).Error)
	require.Zero(t, remaining)
	history, _, err := LoadAgentHistory(ctx, repo, session, unlimitedBudget, false)
	require.NoError(t, err)
	text := strings.Join(contents(history), "\n")
	t.Logf("checkpointed=%t deleted_turn_rows=%d marker_in_next_context=%t",
		checkpointed, remaining, strings.Contains(text, marker))
	require.NotContains(t, text, marker, "deleted conversation must not survive in a derived context checkpoint")
}
