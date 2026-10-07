package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/Tencent/WeKnora/internal/types"
)

// errTokenWrite stands in for a failed auth_tokens insert: statement timeout,
// exhausted connection pool, connection reset. It is always transient
// infrastructure, never caused by the request.
var errTokenWrite = errors.New("auth_tokens write failed: database is locked")

func persistedTestUser(t *testing.T, tenantID uint64, email, password string) *types.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	return &types.User{
		ID:           "user-1",
		TenantID:     tenantID,
		Email:        email,
		IsActive:     true,
		PasswordHash: string(hash),
	}
}

// A token the database has no row for is rejected by ValidateToken on the very
// next request, and the client is told its session was revoked. Login must
// therefore fail — and must not hand out the signed pair — when either insert
// fails.
func TestLoginFailsWhenTokensCannotBePersisted(t *testing.T) {
	ctx := context.Background()
	tokenRepo := &stubAuthTokenRepo{
		tokens:     map[string]*types.AuthToken{},
		createErrs: []error{errTokenWrite},
	}
	svc := newAuthTestUserService(tokenRepo)
	user := persistedTestUser(t, 0, "user@example.com", "Secure9!")
	svc.userRepo.(*stubUserRepoForAuth).users["user-1"] = user

	resp, err := svc.Login(ctx, &types.LoginRequest{Email: user.Email, Password: "Secure9!"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.False(t, resp.Success,
		"login reported success although the token rows were never written")
	require.Empty(t, resp.Token)
	require.Empty(t, resp.RefreshToken)
	require.Equal(t, "Login failed", resp.Message)
}

// Either half of the pair failing must abort issuance: both rows are checked
// before the caller sees a token.
func TestGenerateTokensFailsWhenEitherTokenCannotBePersisted(t *testing.T) {
	ctx := context.Background()
	user := persistedTestUser(t, 1, "user@example.com", "Secure9!")

	for _, tt := range []struct {
		name       string
		createErrs []error
		wantErr    string
	}{
		{"access token", []error{errTokenWrite}, "persist access token"},
		{"refresh token", []error{nil, errTokenWrite}, "persist refresh token"},
		{"both", []error{errTokenWrite, errTokenWrite}, "persist access token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tokenRepo := &stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}, createErrs: tt.createErrs}
			svc := newAuthTestUserService(tokenRepo)

			access, refresh, err := svc.GenerateTokens(ctx, user)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
			require.Empty(t, access, "a token that was not persisted must not be handed out")
			require.Empty(t, refresh, "a token that was not persisted must not be handed out")
		})
	}
}

// The normal path must be untouched: both rows land before the pair is
// returned, and the access token validates afterwards.
func TestGenerateTokensPersistsPairBeforeReturning(t *testing.T) {
	ctx := context.Background()
	tokenRepo := &stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}}
	svc := newAuthTestUserService(tokenRepo)
	user := persistedTestUser(t, 1, "user@example.com", "Secure9!")

	access, refresh, err := svc.GenerateTokens(ctx, user)
	require.NoError(t, err)
	require.NotEmpty(t, access)
	require.NotEmpty(t, refresh)
	require.Equal(t, 2, tokenRepo.createCalls)
	require.Contains(t, tokenRepo.tokens, access)
	require.Contains(t, tokenRepo.tokens, refresh)
	require.Equal(t, "access_token", tokenRepo.tokens[access].TokenType)
	require.Equal(t, "refresh_token", tokenRepo.tokens[refresh].TokenType)

	got, tenantID, err := svc.ValidateToken(ctx, access)
	require.NoError(t, err)
	require.Equal(t, user.ID, got.ID)
	require.Equal(t, uint64(1), tenantID)
}

// A failed lookup is an infrastructure error, not a revocation. Reporting it as
// "token is revoked" sent operators chasing a session revocation that never
// happened, which is exactly the misleading half of the login-token defect.
func TestValidateTokenReportsLookupFailureTruthfully(t *testing.T) {
	ctx := context.Background()
	lookupErr := errors.New("connection reset by peer")
	tokenRepo := &stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}, getErr: lookupErr}
	svc := newAuthTestUserService(tokenRepo)

	access := signTestJWT(jwt.MapClaims{
		"user_id": "user-1", "type": "access", "exp": time.Now().Add(time.Hour).Unix(),
	})
	_, _, err := svc.ValidateToken(ctx, access)
	require.Error(t, err)
	require.ErrorIs(t, err, lookupErr)
	require.NotContains(t, err.Error(), "token is revoked")

	refresh := signTestJWT(jwt.MapClaims{
		"user_id": "user-1", "type": "refresh", "exp": time.Now().Add(time.Hour).Unix(),
	})
	_, _, err = svc.RefreshToken(ctx, refresh)
	require.Error(t, err)
	require.ErrorIs(t, err, lookupErr)
	require.NotContains(t, err.Error(), "revoked")
}

// The revocation message must survive where it is true.
func TestValidateTokenStillReportsRevokedTokens(t *testing.T) {
	ctx := context.Background()
	access := signTestJWT(jwt.MapClaims{
		"user_id": "user-1", "type": "access", "exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenRepo := &stubAuthTokenRepo{tokens: map[string]*types.AuthToken{
		access: {ID: "tok-1", UserID: "user-1", Token: access, TokenType: "access_token", IsRevoked: true},
	}}
	svc := newAuthTestUserService(tokenRepo)

	_, _, err := svc.ValidateToken(ctx, access)
	require.Error(t, err)
	require.Contains(t, err.Error(), "token is revoked")
}
