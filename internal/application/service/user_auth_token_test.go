package service

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/Tencent/WeKnora/internal/types"
)

func init() {
	_ = os.Setenv("JWT_SECRET", "test-jwt-secret-for-user-auth-token-tests")
}

type stubAuthTokenRepo struct {
	tokens         map[string]*types.AuthToken
	revokedUserIDs []string
}

func (s *stubAuthTokenRepo) CreateToken(context.Context, *types.AuthToken) error { return nil }
func (s *stubAuthTokenRepo) GetTokenByValue(_ context.Context, tokenValue string) (*types.AuthToken, error) {
	token, ok := s.tokens[tokenValue]
	if !ok {
		return nil, errors.New("token not found")
	}
	return token, nil
}
func (s *stubAuthTokenRepo) GetTokensByUserID(context.Context, string) ([]*types.AuthToken, error) {
	return nil, nil
}
func (s *stubAuthTokenRepo) UpdateToken(context.Context, *types.AuthToken) error { return nil }
func (s *stubAuthTokenRepo) DeleteToken(context.Context, string) error           { return nil }
func (s *stubAuthTokenRepo) DeleteExpiredTokens(context.Context) error           { return nil }
func (s *stubAuthTokenRepo) RevokeTokensByUserID(_ context.Context, userID string) error {
	s.revokedUserIDs = append(s.revokedUserIDs, userID)
	return nil
}

type stubUserRepoForAuth struct {
	users       map[string]*types.User
	updateCalls int
}

func (s *stubUserRepoForAuth) CreateUser(context.Context, *types.User) error { return nil }
func (s *stubUserRepoForAuth) GetUserByID(_ context.Context, id string) (*types.User, error) {
	user, ok := s.users[id]
	if !ok {
		return nil, errors.New("user not found")
	}
	return user, nil
}
func (s *stubUserRepoForAuth) GetUsersByIDs(context.Context, []string) (map[string]*types.User, error) {
	return nil, nil
}
func (s *stubUserRepoForAuth) GetUserByEmail(context.Context, string) (*types.User, error) {
	return nil, nil
}
func (s *stubUserRepoForAuth) GetUserByUsername(context.Context, string) (*types.User, error) {
	return nil, nil
}
func (s *stubUserRepoForAuth) GetUserByTenantID(context.Context, uint64) (*types.User, error) {
	return nil, nil
}
func (s *stubUserRepoForAuth) UpdateUser(context.Context, *types.User) error {
	s.updateCalls++
	return nil
}
func (s *stubUserRepoForAuth) DeleteUser(context.Context, string) error { return nil }
func (s *stubUserRepoForAuth) ListUsers(context.Context, int, int) ([]*types.User, error) {
	return nil, nil
}
func (s *stubUserRepoForAuth) ListSystemAdmins(context.Context, int, int) ([]*types.User, int64, error) {
	return nil, 0, nil
}
func (s *stubUserRepoForAuth) RevokeSystemAdmin(context.Context, string, string) (*types.User, error) {
	return nil, nil
}
func (s *stubUserRepoForAuth) SearchUsers(context.Context, string, int) ([]*types.User, error) {
	return nil, nil
}

func newAuthTestUserService(tokenRepo *stubAuthTokenRepo) *userService {
	return &userService{
		userRepo: &stubUserRepoForAuth{
			users: map[string]*types.User{
				"user-1": {ID: "user-1", TenantID: 1},
			},
		},
		tokenRepo: tokenRepo,
	}
}

func signTestJWT(claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(getJwtSecret()))
	if err != nil {
		panic(err)
	}
	return signed
}

func TestValidateTokenRejectsRefreshToken(t *testing.T) {
	ctx := context.Background()
	tokenRepo := &stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}}
	svc := newAuthTestUserService(tokenRepo)

	refreshJWT := signTestJWT(jwt.MapClaims{
		"user_id": "user-1",
		"type":    "refresh",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	tokenRepo.tokens[refreshJWT] = &types.AuthToken{
		UserID:    "user-1",
		Token:     refreshJWT,
		TokenType: "refresh_token",
	}

	_, _, err := svc.ValidateToken(ctx, refreshJWT)
	if err == nil || err.Error() != "refresh token cannot be used as access token" {
		t.Fatalf("ValidateToken(refresh JWT) err = %v, want refresh rejection", err)
	}

	legacyRefresh := signTestJWT(jwt.MapClaims{
		"user_id": "user-1",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	tokenRepo.tokens[legacyRefresh] = &types.AuthToken{
		UserID:    "user-1",
		Token:     legacyRefresh,
		TokenType: "refresh_token",
	}

	_, _, err = svc.ValidateToken(ctx, legacyRefresh)
	if err == nil || err.Error() != "refresh token cannot be used as access token" {
		t.Fatalf("ValidateToken(legacy refresh in DB) err = %v, want refresh rejection", err)
	}
}

func TestRefreshTokenRejectsAccessTokenRecord(t *testing.T) {
	ctx := context.Background()
	tokenRepo := &stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}}
	svc := newAuthTestUserService(tokenRepo)

	refreshJWT := signTestJWT(jwt.MapClaims{
		"user_id": "user-1",
		"type":    "refresh",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	tokenRepo.tokens[refreshJWT] = &types.AuthToken{
		UserID:    "user-1",
		Token:     refreshJWT,
		TokenType: "access_token",
	}

	_, _, err := svc.RefreshToken(ctx, refreshJWT)
	if err == nil || err.Error() != "not a refresh token" {
		t.Fatalf("RefreshToken(access token record) err = %v, want not a refresh token", err)
	}
}

func TestLogoutRevokesAllUserTokens(t *testing.T) {
	ctx := context.Background()
	tokenRepo := &stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}}
	svc := newAuthTestUserService(tokenRepo)

	expiredAccess := signTestJWT(jwt.MapClaims{
		"user_id": "user-1",
		"type":    "access",
		"exp":     time.Now().Add(-time.Hour).Unix(),
	})

	if err := svc.Logout(ctx, expiredAccess); err != nil {
		t.Fatalf("Logout(expired access token) err = %v", err)
	}
	if len(tokenRepo.revokedUserIDs) != 1 || tokenRepo.revokedUserIDs[0] != "user-1" {
		t.Fatalf("RevokeTokensByUserID calls = %v, want [user-1]", tokenRepo.revokedUserIDs)
	}
}

func TestAdminResetPasswordHashesPasswordAndRevokesSessions(t *testing.T) {
	ctx := context.Background()
	tokenRepo := &stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}}
	svc := newAuthTestUserService(tokenRepo)
	repo := svc.userRepo.(*stubUserRepoForAuth)

	if err := svc.AdminResetPassword(ctx, "user-1", "NewSecure9"); err != nil {
		t.Fatalf("AdminResetPassword() err = %v", err)
	}
	if repo.updateCalls != 1 {
		t.Fatalf("UpdateUser calls = %d, want 1", repo.updateCalls)
	}
	user := repo.users["user-1"]
	if user.PasswordHash == "NewSecure9" || user.PasswordHash == "" {
		t.Fatalf("password was not stored as a hash")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("NewSecure9")); err != nil {
		t.Fatalf("stored hash does not match new password: %v", err)
	}
	if len(tokenRepo.revokedUserIDs) != 1 || tokenRepo.revokedUserIDs[0] != "user-1" {
		t.Fatalf("RevokeTokensByUserID calls = %v, want [user-1]", tokenRepo.revokedUserIDs)
	}
}

func TestAdminResetPasswordRejectsWeakPasswordBeforeWrite(t *testing.T) {
	tokenRepo := &stubAuthTokenRepo{tokens: map[string]*types.AuthToken{}}
	svc := newAuthTestUserService(tokenRepo)
	repo := svc.userRepo.(*stubUserRepoForAuth)

	err := svc.AdminResetPassword(context.Background(), "user-1", "password")
	if !errors.Is(err, ErrPasswordPolicy) {
		t.Fatalf("AdminResetPassword() err = %v, want ErrPasswordPolicy", err)
	}
	if repo.updateCalls != 0 || len(tokenRepo.revokedUserIDs) != 0 {
		t.Fatalf("weak password caused side effects: updates=%d revocations=%v", repo.updateCalls, tokenRepo.revokedUserIDs)
	}
}

func TestUserIDFromSignedTokenAcceptsExpiredToken(t *testing.T) {
	expired := signTestJWT(jwt.MapClaims{
		"user_id": "user-1",
		"type":    "access",
		"exp":     time.Now().Add(-time.Hour).Unix(),
	})

	userID, err := userIDFromSignedToken(expired)
	if err != nil {
		t.Fatalf("userIDFromSignedToken(expired) err = %v", err)
	}
	if userID != "user-1" {
		t.Fatalf("userIDFromSignedToken(expired) = %q, want user-1", userID)
	}
}
