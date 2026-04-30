package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestOIDCRedirectCallbackUsesTicketRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := newOIDCCallbackTicketStore(nil)
	handler := &AuthHandler{
		userService: &testUserService{
			loginWithOIDC: func(ctx context.Context, code, redirectURI string) (*types.OIDCCallbackResponse, error) {
				if code != "auth-code" {
					t.Fatalf("code = %q, want %q", code, "auth-code")
				}
				return &types.OIDCCallbackResponse{
					Success:      true,
					Token:        "access-token",
					RefreshToken: "refresh-token",
				}, nil
			},
		},
		oidcTicketStore: store,
	}

	state := mustEncodeOIDCState(t, &oidcStatePayload{
		Nonce:       "nonce",
		RedirectURI: "https://kb.uimpcloud.com/api/v1/auth/oidc/callback",
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?code=auth-code&state="+url.QueryEscape(state), nil)

	handler.OIDCRedirectCallback(ctx)

	if got, want := recorder.Code, http.StatusFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}

	location := recorder.Header().Get("Location")
	if !strings.HasPrefix(location, "/#oidc_callback_ticket=") {
		t.Fatalf("Location = %q, want oidc_callback_ticket redirect", location)
	}
	if strings.Contains(location, "oidc_result=") {
		t.Fatalf("Location = %q, should not contain legacy oidc_result", location)
	}

	ticket := strings.TrimPrefix(location, "/#oidc_callback_ticket=")
	resp, err := store.Consume(context.Background(), ticket)
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if got, want := resp.Token, "access-token"; got != want {
		t.Fatalf("stored token = %q, want %q", got, want)
	}
}

func TestExchangeOIDCCallbackTicketSingleUse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := newOIDCCallbackTicketStore(nil)
	ticket, err := store.Save(context.Background(), &types.OIDCCallbackResponse{
		Success:      true,
		Token:        "access-token",
		RefreshToken: "refresh-token",
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	handler := &AuthHandler{oidcTicketStore: store}

	first := performOIDCTicketExchange(t, handler, ticket)
	if got, want := first.Code, http.StatusOK; got != want {
		t.Fatalf("first exchange status = %d, want %d", got, want)
	}

	var successResp types.OIDCCallbackResponse
	if err := json.Unmarshal(first.Body.Bytes(), &successResp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !successResp.Success || successResp.Token != "access-token" {
		t.Fatalf("first exchange body = %+v, want success with token", successResp)
	}

	second := performOIDCTicketExchange(t, handler, ticket)
	if got, want := second.Code, http.StatusGone; got != want {
		t.Fatalf("second exchange status = %d, want %d", got, want)
	}

	var goneResp types.OIDCCallbackResponse
	if err := json.Unmarshal(second.Body.Bytes(), &goneResp); err != nil {
		t.Fatalf("json.Unmarshal() second error = %v", err)
	}
	if goneResp.Success {
		t.Fatalf("second exchange success = true, want false")
	}
}

func TestExchangeOIDCCallbackTicketExpired(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now()
	store := &redisOIDCCallbackTicketStore{
		fallback: &memoryOIDCCallbackTicketStore{
			entries: make(map[string]memoryOIDCCallbackTicketEntry),
			ttl:     time.Minute,
			now:     func() time.Time { return now },
		},
	}

	ticket, err := store.Save(context.Background(), &types.OIDCCallbackResponse{
		Success: true,
		Token:   "access-token",
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	now = now.Add(2 * time.Minute)

	handler := &AuthHandler{oidcTicketStore: store}
	recorder := performOIDCTicketExchange(t, handler, ticket)
	if got, want := recorder.Code, http.StatusGone; got != want {
		t.Fatalf("expired exchange status = %d, want %d", got, want)
	}
}

func performOIDCTicketExchange(t *testing.T, handler *AuthHandler, ticket string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(&types.OIDCCallbackExchangeRequest{Ticket: ticket})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/oidc/callback/exchange", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.ExchangeOIDCCallbackTicket(ctx)
	return recorder
}

func mustEncodeOIDCState(t *testing.T, payload *oidcStatePayload) string {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

type testUserService struct {
	loginWithOIDC func(ctx context.Context, code, redirectURI string) (*types.OIDCCallbackResponse, error)
}

func (s *testUserService) Register(ctx context.Context, req *types.RegisterRequest) (*types.User, error) {
	panic("unexpected call to Register")
}

func (s *testUserService) Login(ctx context.Context, req *types.LoginRequest) (*types.LoginResponse, error) {
	panic("unexpected call to Login")
}

func (s *testUserService) GetOIDCAuthorizationURL(ctx context.Context, redirectURI string) (*types.OIDCAuthURLResponse, error) {
	panic("unexpected call to GetOIDCAuthorizationURL")
}

func (s *testUserService) LoginWithOIDC(ctx context.Context, code, redirectURI string) (*types.OIDCCallbackResponse, error) {
	if s.loginWithOIDC == nil {
		panic("unexpected call to LoginWithOIDC")
	}
	return s.loginWithOIDC(ctx, code, redirectURI)
}

func (s *testUserService) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	panic("unexpected call to GetUserByID")
}

func (s *testUserService) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	panic("unexpected call to GetUserByEmail")
}

func (s *testUserService) GetUserByUsername(ctx context.Context, username string) (*types.User, error) {
	panic("unexpected call to GetUserByUsername")
}

func (s *testUserService) GetUserByTenantID(ctx context.Context, tenantID uint64) (*types.User, error) {
	panic("unexpected call to GetUserByTenantID")
}

func (s *testUserService) UpdateUser(ctx context.Context, user *types.User) error {
	panic("unexpected call to UpdateUser")
}

func (s *testUserService) DeleteUser(ctx context.Context, id string) error {
	panic("unexpected call to DeleteUser")
}

func (s *testUserService) ChangePassword(ctx context.Context, userID string, oldPassword, newPassword string) error {
	panic("unexpected call to ChangePassword")
}

func (s *testUserService) ValidatePassword(ctx context.Context, userID string, password string) error {
	panic("unexpected call to ValidatePassword")
}

func (s *testUserService) GenerateTokens(ctx context.Context, user *types.User) (accessToken, refreshToken string, err error) {
	panic("unexpected call to GenerateTokens")
}

func (s *testUserService) ValidateToken(ctx context.Context, token string) (*types.User, error) {
	panic("unexpected call to ValidateToken")
}

func (s *testUserService) RefreshToken(ctx context.Context, refreshToken string) (accessToken, newRefreshToken string, err error) {
	panic("unexpected call to RefreshToken")
}

func (s *testUserService) RevokeToken(ctx context.Context, token string) error {
	panic("unexpected call to RevokeToken")
}

func (s *testUserService) GetCurrentUser(ctx context.Context) (*types.User, error) {
	panic("unexpected call to GetCurrentUser")
}

func (s *testUserService) SearchUsers(ctx context.Context, query string, limit int) ([]*types.User, error) {
	panic("unexpected call to SearchUsers")
}
