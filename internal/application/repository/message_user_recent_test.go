package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetRecentUserMessagesFiltersRoleBeforeLimit(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	base := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	for i, role := range []string{"user", "assistant", "user", "assistant", "assistant", "user"} {
		seedMessage(t, db, fmt.Sprintf("m%d", i+1), "s1", role, base.Add(time.Duration(i)*time.Second))
	}
	seedMessage(t, db, "other", "s2", "user", base.Add(time.Minute))

	got, err := repo.GetRecentUserMessages(context.Background(), "s1", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"m3", "m6"}, messageIDs(got))
}
