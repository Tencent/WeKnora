package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetMessagesByRequestIDsStaysInsideSession(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	require.NoError(t, db.AutoMigrate(&types.Session{}))
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).
		Create(&types.Session{ID: "parent", TenantID: 1}).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{ID: "fork", TenantID: 1}).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Message{
		ID: "p-u", SessionID: "parent", RequestID: "req", Role: "user", CreatedAt: at,
	}).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Message{
		ID: "p-a", SessionID: "parent", RequestID: "req", Role: "assistant", CreatedAt: at.Add(time.Second),
	}).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Message{
		ID: "f-u", SessionID: "fork", RequestID: "req", Role: "user", CreatedAt: at,
	}).Error)
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Message{
		ID: "f-a", SessionID: "fork", RequestID: "req", Role: "assistant", CreatedAt: at.Add(time.Second),
	}).Error)

	got, err := repo.GetMessagesByRequestIDs(ctx, "fork", []string{"req"})
	require.NoError(t, err)
	require.Equal(t, []string{"f-u", "f-a"}, messageIDsFromWithSession(got))

	empty, err := repo.GetMessagesByRequestIDs(ctx, "", []string{"req"})
	require.NoError(t, err)
	require.Empty(t, empty)
}

func messageIDsFromWithSession(messages []*types.MessageWithSession) []string {
	ids := make([]string, 0, len(messages))
	for _, m := range messages {
		if m != nil {
			ids = append(ids, m.ID)
		}
	}
	return ids
}
