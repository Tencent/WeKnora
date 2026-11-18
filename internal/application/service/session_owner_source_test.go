package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestCreateSessionStampsOwnerSourceServerSide(t *testing.T) {
	repo := &recordingSessionRepo{}
	svc := &sessionService{sessionRepo: repo}
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "u-1")
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "u-1"})
	ctx = types.WithPrincipalAuthSource(ctx, types.AuthSourceWeb)

	_, err := svc.CreateSession(ctx, &types.Session{
		TenantID: 1, UserID: "u-1", OwnerAuthSource: "api_signed_token",
	})
	require.NoError(t, err)
	require.Equal(t, types.AuthSourceWeb, repo.created.OwnerAuthSource)
}

type recordingSessionRepo struct {
	interfaces.SessionRepository
	created *types.Session
}

func (r *recordingSessionRepo) Create(_ context.Context, s *types.Session) (*types.Session, error) {
	copied := *s
	r.created = &copied
	return &copied, nil
}
