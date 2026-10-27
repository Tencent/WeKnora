package service

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func init() {
	_ = os.Setenv("JWT_SECRET", "test-jwt-secret-for-login-identifier-tests")
}

// loginIdentifierRepo models the users table as two independently addressable
// columns and records which one was consulted. Counting the lookups is the
// point: it is what distinguishes "the fallback resolved the user by username"
// from "some lookup happened to succeed".
type loginIdentifierRepo struct {
	interfaces.UserRepository
	users        []*types.User
	emailLookups int
	nameLookups  int
}

func (r *loginIdentifierRepo) GetUserByEmail(_ context.Context, email string) (*types.User, error) {
	r.emailLookups++
	for _, user := range r.users {
		if user.Email == email {
			return user, nil
		}
	}
	return nil, errors.New("user not found")
}

func (r *loginIdentifierRepo) GetUserByUsername(_ context.Context, username string) (*types.User, error) {
	r.nameLookups++
	for _, user := range r.users {
		if user.Username == username {
			return user, nil
		}
	}
	return nil, errors.New("user not found")
}

// loginTestMemberService gives the fixture an active home membership, so Login
// exercises tenant resolution instead of falling into the stale-home repair
// path. Kept local to this file: the shared provisioning stubs are asserted on
// elsewhere and should not grow behaviour for this test's sake.
type loginTestMemberService struct {
	interfaces.TenantMemberService
	role types.TenantRole
}

func (s *loginTestMemberService) GetMembership(
	_ context.Context, userID string, tenantID uint64,
) (*types.TenantMember, error) {
	return &types.TenantMember{
		UserID:   userID,
		TenantID: tenantID,
		Role:     s.role,
		Status:   types.TenantMemberStatusActive,
	}, nil
}

func (s *loginTestMemberService) ListByUser(context.Context, string) ([]*types.TenantMember, error) {
	return nil, nil
}

func loginTestUser(t *testing.T) *types.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-horse-battery"), bcrypt.DefaultCost)
	require.NoError(t, err)
	return &types.User{
		ID:           "user-1",
		Username:     "admin",
		Email:        "admin@example.com",
		PasswordHash: string(hash),
		IsActive:     true,
		TenantID:     1,
	}
}

func newLoginTestService(repo interfaces.UserRepository) *userService {
	return &userService{
		userRepo:      repo,
		tokenRepo:     &stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}},
		tenantService: &provisioningTenantService{},
		memberService: &loginTestMemberService{role: types.TenantRoleOwner},
	}
}

// A bare username used to die in request validation ("Field validation for
// 'Email' failed on the 'email' tag"), so the credentials were never checked
// and the caller only saw a generic "Invalid login parameters".
func TestLoginAcceptsUsernameInPlaceOfEmail(t *testing.T) {
	repo := &loginIdentifierRepo{users: []*types.User{loginTestUser(t)}}
	svc := newLoginTestService(repo)

	resp, err := svc.Login(context.Background(), &types.LoginRequest{
		Email:    "admin",
		Password: "correct-horse-battery",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Success, "username must authenticate the same account as its email")
	assert.Equal(t, 1, repo.emailLookups, "email column is still tried first")
	assert.Equal(t, 1, repo.nameLookups, "username fallback must resolve the user")
}

func TestLoginStillAcceptsEmail(t *testing.T) {
	repo := &loginIdentifierRepo{users: []*types.User{loginTestUser(t)}}
	svc := newLoginTestService(repo)

	resp, err := svc.Login(context.Background(), &types.LoginRequest{
		Email:    "admin@example.com",
		Password: "correct-horse-battery",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Equal(t, 1, repo.emailLookups)
	assert.Equal(t, 0, repo.nameLookups, "an email hit must not fall through to the username column")
}

// The fallback is keyed on the "@" in the input, so an unknown address stays a
// single failed lookup instead of turning into a second, pointless query.
func TestLoginDoesNotFallBackForEmailShapedInput(t *testing.T) {
	repo := &loginIdentifierRepo{users: []*types.User{loginTestUser(t)}}
	svc := newLoginTestService(repo)

	resp, err := svc.Login(context.Background(), &types.LoginRequest{
		Email:    "nobody@example.com",
		Password: "correct-horse-battery",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Equal(t, 1, repo.emailLookups)
	assert.Equal(t, 0, repo.nameLookups)
}

func TestLoginRejectsUnknownUsername(t *testing.T) {
	repo := &loginIdentifierRepo{users: []*types.User{loginTestUser(t)}}
	svc := newLoginTestService(repo)

	resp, err := svc.Login(context.Background(), &types.LoginRequest{
		Email:    "ghost",
		Password: "correct-horse-battery",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Equal(t, 1, repo.nameLookups)
}

// Relaxing the input field must not relax authentication: a username hit still
// goes through bcrypt, and a wrong password is still refused.
func TestLoginByUsernameStillChecksPassword(t *testing.T) {
	repo := &loginIdentifierRepo{users: []*types.User{loginTestUser(t)}}
	svc := newLoginTestService(repo)

	resp, err := svc.Login(context.Background(), &types.LoginRequest{
		Email:    "admin",
		Password: "wrong-password",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Equal(t, "Invalid email or password", resp.Message)
}

func TestLoginByUsernameRejectsInactiveAccount(t *testing.T) {
	user := loginTestUser(t)
	user.IsActive = false
	repo := &loginIdentifierRepo{users: []*types.User{user}}
	svc := newLoginTestService(repo)

	resp, err := svc.Login(context.Background(), &types.LoginRequest{
		Email:    "admin",
		Password: "correct-horse-battery",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Equal(t, "Account is disabled", resp.Message)
}
