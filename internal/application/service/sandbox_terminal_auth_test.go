package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type terminalAuthUserStub struct {
	interfaces.UserService
	user  *types.User
	token *types.AuthToken
	// userFail / tokenFail stand in for a database that is momentarily
	// unreachable, as opposed to a row that is genuinely gone.
	userFail  error
	tokenFail error
}

func (s *terminalAuthUserStub) GetUserByID(context.Context, string) (*types.User, error) {
	if s.userFail != nil {
		return nil, s.userFail
	}
	if s.user == nil {
		return nil, apprepo.ErrUserNotFound
	}
	return s.user, nil
}

func (s *terminalAuthUserStub) GetAccessTokenByID(context.Context, string) (*types.AuthToken, error) {
	if s.tokenFail != nil {
		return nil, s.tokenFail
	}
	if s.token == nil {
		return nil, apprepo.ErrTokenNotFound
	}
	return s.token, nil
}

type terminalAuthMemberStub struct {
	interfaces.TenantMemberService
	member *types.TenantMember
	fail   error
}

func (s *terminalAuthMemberStub) GetMembership(context.Context, string, uint64) (*types.TenantMember, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	return s.member, nil
}

type terminalAuthSessionStub struct {
	interfaces.SessionService
	err error
}

func (s *terminalAuthSessionStub) GetOwnedSession(context.Context, string) (*types.Session, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &types.Session{ID: "sess-9"}, nil
}

func validTerminalAuthInputs() (
	*terminalAuthUserStub,
	*terminalAuthMemberStub,
	*terminalAuthSessionStub,
	SandboxTerminalTicketClaims,
) {
	users := &terminalAuthUserStub{
		user: &types.User{ID: "user-1", IsActive: true},
		token: &types.AuthToken{
			ID:        "tok-abc",
			UserID:    "user-1",
			TokenType: "access_token",
			ExpiresAt: time.Now().Add(time.Hour),
		},
	}
	members := &terminalAuthMemberStub{
		member: &types.TenantMember{
			UserID:   "user-1",
			TenantID: 42,
			Status:   types.TenantMemberStatusActive,
		},
	}
	sessions := &terminalAuthSessionStub{}
	claims := SandboxTerminalTicketClaims{
		UserID:    "user-1",
		TenantID:  42,
		SessionID: "sess-9",
		TokenID:   "tok-abc",
	}
	return users, members, sessions, claims
}

func handshakeAuth(
	users interfaces.UserService,
	members interfaces.TenantMemberService,
	sessions interfaces.SessionService,
	claims SandboxTerminalTicketClaims,
	rbacEnforced bool,
) (*types.User, error) {
	return CheckSandboxTerminalAuth(context.Background(), users, members, sessions, claims, rbacEnforced, true)
}

func TestCheckSandboxTerminalAuthAllowsCurrentIdentity(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	user, err := handshakeAuth(users, members, sessions, claims, true)
	require.NoError(t, err)
	// The handshake installs its auth session from this user rather than
	// looking it up again.
	require.NotNil(t, user)
	require.Equal(t, "user-1", user.ID)
}

func TestCheckSandboxTerminalAuthDeniesRevokedToken(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	users.token.IsRevoked = true
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, ErrTerminalAuthDenied)
}

func TestCheckSandboxTerminalAuthDeniesExpiredToken(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	users.token.ExpiresAt = time.Now().Add(-time.Minute)
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, ErrTerminalAuthDenied)
}

// Silent access-token rotation leaves the minting row expired but unrevoked.
// The open PTY must not treat that as logout.
func TestCheckSandboxTerminalAuthRecheckAllowsExpiredUnrevokedToken(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	users.token.ExpiresAt = time.Now().Add(-time.Minute)
	user, err := CheckSandboxTerminalAuth(context.Background(), users, members, sessions, claims, true, false)
	require.NoError(t, err)
	require.Equal(t, "user-1", user.ID)
}

func TestCheckSandboxTerminalAuthRecheckStillDeniesRevokedToken(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	users.token.IsRevoked = true
	users.token.ExpiresAt = time.Now().Add(-time.Minute)
	_, err := CheckSandboxTerminalAuth(context.Background(), users, members, sessions, claims, true, false)
	require.ErrorIs(t, err, ErrTerminalAuthDenied)
}

func TestCheckSandboxTerminalAuthDeniesTokenOwnedBySomeoneElse(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	users.token.UserID = "other-user"
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, ErrTerminalAuthDenied)
}

func TestCheckSandboxTerminalAuthDeniesRefreshToken(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	users.token.TokenType = "refresh_token"
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, ErrTerminalAuthDenied)
}

func TestCheckSandboxTerminalAuthDeniesMissingToken(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	users.token = nil
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, ErrTerminalAuthDenied)
}

func TestCheckSandboxTerminalAuthDeniesDeletedUser(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	users.user = nil
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, ErrTerminalAuthDenied)
}

// A database that is briefly unreachable must not read as "access revoked":
// the recheck runs on a timer against every open terminal, so a fail-closed
// lookup would disconnect all of them at once.
func TestCheckSandboxTerminalAuthPropagatesTokenLookupError(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	lookupErr := errors.New("auth_tokens db down")
	users.tokenFail = lookupErr
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, lookupErr)
	require.False(t, errors.Is(err, ErrTerminalAuthDenied))
}

func TestCheckSandboxTerminalAuthPropagatesUserLookupError(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	lookupErr := errors.New("users db down")
	users.userFail = lookupErr
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, lookupErr)
	require.False(t, errors.Is(err, ErrTerminalAuthDenied))
}

func TestCheckSandboxTerminalAuthPropagatesSessionLookupError(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	lookupErr := errors.New("sessions db down")
	sessions.err = lookupErr
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, lookupErr)
	require.False(t, errors.Is(err, ErrTerminalAuthDenied))
}

func TestCheckSandboxTerminalAuthDeniesInactiveUser(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	users.user.IsActive = false
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, ErrTerminalAuthDenied)
}

func TestCheckSandboxTerminalAuthDeniesMissingMembershipWhenRBACOn(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	members.member = nil
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, ErrTerminalAuthDenied)
}

func TestCheckSandboxTerminalAuthAllowsMissingMembershipWhenRBACOff(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	members.member = nil
	_, err := handshakeAuth(users, members, sessions, claims, false)
	require.NoError(t, err)
}

func TestCheckSandboxTerminalAuthAllowsSuperuserWithoutMembership(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	users.user.CanAccessAllTenants = true
	members.member = nil
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.NoError(t, err)
}

func TestCheckSandboxTerminalAuthDeniesSuspendedMembership(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	members.member.Status = types.TenantMemberStatusSuspended
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, ErrTerminalAuthDenied)
}

func TestCheckSandboxTerminalAuthPropagatesMembershipLookupError(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	lookupErr := errors.New("membership db down")
	members.fail = lookupErr
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, lookupErr)
	require.False(t, errors.Is(err, ErrTerminalAuthDenied))
}

func TestCheckSandboxTerminalAuthDeniesDeletedSession(t *testing.T) {
	users, members, sessions, claims := validTerminalAuthInputs()
	sessions.err = apperrors.ErrSessionNotFound
	_, err := handshakeAuth(users, members, sessions, claims, true)
	require.ErrorIs(t, err, ErrTerminalAuthDenied)
}

func TestAccessTokenStillActive(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	live := &types.AuthToken{
		ID:        "tok-1",
		UserID:    "user-1",
		TokenType: "access_token",
		ExpiresAt: now.Add(time.Hour),
	}
	require.NoError(t, AssertAccessTokenStillActive(live, "user-1", now))

	revoked := *live
	revoked.IsRevoked = true
	require.ErrorIs(t, AssertAccessTokenStillActive(&revoked, "user-1", now), ErrTerminalAuthDenied)

	expired := *live
	expired.ExpiresAt = now
	require.ErrorIs(t, AssertAccessTokenStillActive(&expired, "user-1", now), ErrTerminalAuthDenied)
}
