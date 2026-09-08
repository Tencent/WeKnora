package repository

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestListSessionArtifactMessagesUsesStableCursorAndFiltersEmptyRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Message{}))

	firstTime := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	laterTime := firstTime.Add(time.Second)
	withArtifact := func(name string, createdAt time.Time) *types.Message {
		return &types.Message{
			SessionID: "session-1",
			Role:      "assistant",
			CreatedAt: createdAt,
			Artifacts: types.MessageArtifacts{{FileName: name}},
		}
	}
	first := withArtifact("first.txt", firstTime)
	second := withArtifact("second.txt", firstTime)
	later := withArtifact("later.txt", laterTime)
	rows := []*types.Message{
		first,
		{SessionID: "session-1", Role: "assistant", CreatedAt: firstTime},
		{SessionID: "session-1", Role: "user", CreatedAt: firstTime, Artifacts: types.MessageArtifacts{{FileName: "user.txt"}}},
		{SessionID: "other-session", Role: "assistant", CreatedAt: firstTime, Artifacts: types.MessageArtifacts{{FileName: "foreign.txt"}}},
		second,
		later,
	}
	for _, row := range rows {
		require.NoError(t, db.Create(row).Error)
	}

	expected := []*types.Message{first, second, later}
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].CreatedAt.Equal(expected[j].CreatedAt) {
			return expected[i].ID < expected[j].ID
		}
		return expected[i].CreatedAt.Before(expected[j].CreatedAt)
	})

	repo := NewMessageRepository(db)
	page1, hasMore, err := repo.ListSessionArtifactMessages(
		context.Background(), "session-1", nil, 1,
	)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Len(t, page1, 1)
	require.Equal(t, expected[0].ID, page1[0].MessageID)
	require.Equal(t, expected[0].Artifacts, page1[0].Artifacts)

	cursor := &types.SessionArtifactCursor{
		CreatedAt: page1[0].CreatedAt,
		MessageID: page1[0].MessageID,
	}
	page2, hasMore, err := repo.ListSessionArtifactMessages(
		context.Background(), "session-1", cursor, 2,
	)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Len(t, page2, 2)
	require.Equal(t, expected[1].ID, page2[0].MessageID)
	require.Equal(t, expected[2].ID, page2[1].MessageID)
}
