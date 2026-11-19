package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLoadAgentHistoryAfterRewindingSteer(t *testing.T) {
	for _, checkpointed := range []bool{false, true} {
		name := "raw_history"
		if checkpointed {
			name = "checkpoint_history"
		}
		t.Run(name, func(t *testing.T) { checkRewoundSteerHistory(t, checkpointed) })
	}
}

func checkRewoundSteerHistory(t *testing.T, checkpointed bool) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "history.db")), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Message{}, &types.MessageArtifactRecord{}))
	repo := repository.NewMessageRepository(db)
	ctx := t.Context()
	const session = "rewind-session"
	const marker = "REVOKED_PROJECT_CODENAME_ORCHID"
	rows := storedTurns(3)
	steer := &types.Message{
		SessionID: session, RequestID: rows[3].RequestID, Role: "user",
		Content: "Use project codename " + marker, CreatedAt: rows[3].CreatedAt.Add(time.Second),
	}
	rows = append(rows, steer)
	for _, row := range rows {
		row.SessionID = session
		require.NoError(t, db.Create(row).Error)
	}
	if checkpointed {
		require.NoError(t, repo.UpdateMessageContextCheckpoint(ctx, session, rows[3].ID, &types.ContextCheckpoint{
			Summary: "The user requested project codename " + marker + " in turn 2.",
		}))
	}
	deleted, err := repo.DeleteMessagesFrom(ctx, session, steer.CreatedAt, steer.ID, true)
	require.NoError(t, err)
	require.Len(t, deleted, 3)
	var remaining int64
	require.NoError(t, db.Model(&types.Message{}).
		Where("id = ?", steer.ID).Count(&remaining).Error)
	require.Zero(t, remaining)
	history, _, err := LoadAgentHistory(ctx, repo, session, unlimitedBudget, false)
	require.NoError(t, err)
	text := strings.Join(contents(history), "\n")
	t.Logf("checkpointed=%t active_steer_rows=%d marker_in_next_context=%t",
		checkpointed, remaining, strings.Contains(text, marker))
	require.NotContains(t, text, marker, "rewound steer must not survive in a derived context checkpoint")
	require.Contains(t, text, "question 2", "retained history must remain available")
}
