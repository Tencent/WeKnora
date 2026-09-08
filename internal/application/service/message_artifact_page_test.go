package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type artifactPageMessageRepo struct {
	interfaces.MessageRepository
	rows      []types.SessionArtifactMessage
	hasMore   bool
	cursor    *types.SessionArtifactCursor
	limit     int
	callCount int
}

func (r *artifactPageMessageRepo) ListSessionArtifactMessages(
	_ context.Context,
	_ string,
	cursor *types.SessionArtifactCursor,
	limit int,
) ([]types.SessionArtifactMessage, bool, error) {
	r.callCount++
	r.cursor = cursor
	r.limit = limit
	return r.rows, r.hasMore, nil
}

func TestListSessionArtifactMessagesEncodesStableCursor(t *testing.T) {
	createdAt := time.Date(2026, 9, 8, 10, 11, 12, 123000000, time.UTC)
	repo := &artifactPageMessageRepo{
		rows: []types.SessionArtifactMessage{{
			MessageID: "message-1",
			CreatedAt: createdAt,
			Artifacts: types.MessageArtifacts{{FileName: "report.csv"}},
		}},
		hasMore: true,
	}
	svc := &messageService{messageRepo: repo}

	page, err := svc.ListSessionArtifactMessages(context.Background(), "session-1", "", 0)
	require.NoError(t, err)
	require.Equal(t, 50, repo.limit)
	require.True(t, page.HasMore)
	require.NotEmpty(t, page.NextCursor)

	decoded, err := decodeArtifactCursor(page.NextCursor)
	require.NoError(t, err)
	require.Equal(t, createdAt, decoded.CreatedAt)
	require.Equal(t, "message-1", decoded.MessageID)
}

func TestListSessionArtifactMessagesRejectsMalformedCursor(t *testing.T) {
	repo := &artifactPageMessageRepo{}
	svc := &messageService{messageRepo: repo}

	_, err := svc.ListSessionArtifactMessages(
		context.Background(), "session-1", "not-base64!", 10,
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrInvalidArtifactCursor))
	require.Zero(t, repo.callCount)
}
