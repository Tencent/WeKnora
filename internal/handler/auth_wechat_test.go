package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubWeChatService struct {
	store    func(context.Context, *types.LoginResponse, string) (string, error)
	complete func(context.Context, string, string) (*service.WeChatWebResult, error)
	create   func(context.Context, string, string) (*types.WeChatAttemptResponse, error)
	exchange func(
		context.Context, string, *types.WeChatExchangeRequest, bool, types.TenantProvisioningMode,
	) (*types.LoginResponse, bool, error)
}

func (s *stubWeChatService) StoreWebResult(
	ctx context.Context,
	response *types.LoginResponse,
	verifier string,
) (string, error) {
	return s.store(ctx, response, verifier)
}

func (s *stubWeChatService) CompleteWebResult(
	ctx context.Context,
	ticket, verifier string,
) (*service.WeChatWebResult, error) {
	return s.complete(ctx, ticket, verifier)
}

func (s *stubWeChatService) CreateAttempt(
	ctx context.Context,
	client, challenge string,
) (*types.WeChatAttemptResponse, error) {
	return s.create(ctx, client, challenge)
}

func (s *stubWeChatService) WebAuthorizationURL(*types.WeChatAttemptResponse) (string, error) {
	return "https://open.weixin.qq.com/connect/qrconnect?state=state", nil
}

func (s *stubWeChatService) Exchange(
	ctx context.Context,
	client string,
	req *types.WeChatExchangeRequest,
	allow bool,
	mode types.TenantProvisioningMode,
) (*types.LoginResponse, bool, error) {
	return s.exchange(ctx, client, req, allow, mode)
}

func weChatTestRouter(s *stubWeChatService, mode string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture())
	h := &WeChatAuthHandler{
		service: s,
		auth:    &AuthHandler{configInfo: &config.Config{Auth: &config.AuthConfig{RegistrationMode: mode}}},
	}
	r.GET("/api/v1/auth/wechat/start", h.Start)
	r.GET("/api/v1/auth/wechat/callback", h.Callback)
	r.POST("/api/v1/auth/wechat/exchange", h.Exchange)
	r.POST("/api/v1/auth/wechat/complete", h.Complete)
	r.POST("/api/v1/auth/wechat/attempts", h.CreateAttempt)
	return r
}

func TestWeChatWebBrowserBindingAndSafePayload(t *testing.T) {
	var challenge string
	var calls int
	s := &stubWeChatService{
		store: func(_ context.Context, response *types.LoginResponse, verifier string) (string, error) {
			require.Equal(t, "access", response.Token)
			require.Equal(t, challenge, service.WeChatChallenge(verifier))
			return "ticket", nil
		},
		complete: func(_ context.Context, ticket, verifier string) (*service.WeChatWebResult, error) {
			require.Equal(t, "ticket", ticket)
			require.Equal(t, challenge, service.WeChatChallenge(verifier))
			return &service.WeChatWebResult{Success: true, Token: "access", RefreshToken: "refresh"}, nil
		},
		create: func(_ context.Context, client, ch string) (*types.WeChatAttemptResponse, error) {
			require.Equal(t, "web", client)
			challenge = ch
			return &types.WeChatAttemptResponse{AttemptID: "attempt", State: "state"}, nil
		},
		exchange: func(
			_ context.Context, client string, req *types.WeChatExchangeRequest,
			allow bool, _ types.TenantProvisioningMode,
		) (*types.LoginResponse, bool, error) {
			calls++
			require.Equal(t, "web", client)
			require.Equal(t, "attempt", req.AttemptID)
			require.Equal(t, "state", req.State)
			require.Equal(t, challenge, service.WeChatChallenge(req.CodeVerifier))
			require.False(t, allow)
			return &types.LoginResponse{
				Success:      true,
				Token:        "access",
				RefreshToken: "refresh",
				User:         &types.User{ID: "user", PasswordHash: "do-not-expose"},
				ActiveTenant: &types.Tenant{
					ID: 1,
					Credentials: &types.CredentialsConfig{
						WeKnoraCloud: &types.WeKnoraCloudCredentials{AppSecret: "tenant-secret"},
					},
				},
			}, false, nil
		},
	}
	r := weChatTestRouter(s, config.AuthRegistrationModeInviteOnly)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://example.com/api/v1/auth/wechat/start", nil))
	require.Equal(t, http.StatusFound, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	require.True(t, cookies[0].Secure)
	require.True(t, cookies[0].HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
	require.Equal(t, weChatCookiePath, cookies[0].Path)
	for _, withCookie := range []bool{false, true} {
		w = httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodGet,
			"https://example.com/api/v1/auth/wechat/callback?code=code&state=state",
			nil,
		)
		if withCookie {
			req.AddCookie(cookies[0])
		}
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusFound, w.Code)
		if !withCookie {
			require.Zero(t, calls)
			require.Contains(t, w.Header().Get("Location"), "wechat_error=")
			continue
		}
		require.Equal(t, 1, calls)
		require.Equal(t, "/#wechat_ticket=ticket", w.Header().Get("Location"))
		require.NotContains(t, w.Header().Get("Location"), "access")
		require.Equal(t, -1, w.Result().Cookies()[0].MaxAge)
		resultCookie := w.Result().Cookies()[1]
		require.True(t, resultCookie.HttpOnly)
		require.True(t, resultCookie.Secure)
		for _, bound := range []bool{false, true} {
			completeReq := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/auth/wechat/complete",
				strings.NewReader(`{"ticket":"ticket"}`),
			)
			if bound {
				completeReq.AddCookie(resultCookie)
			}
			completeResponse := httptest.NewRecorder()
			r.ServeHTTP(completeResponse, completeReq)
			if bound {
				require.Equal(t, 200, completeResponse.Code)
				require.Contains(t, completeResponse.Body.String(), `"token":"access"`)
				require.Equal(t, -1, completeResponse.Result().Cookies()[0].MaxAge)
			} else {
				require.Equal(t, 401, completeResponse.Code)
			}
		}
	}
}

func TestWeChatMobileExchangePolicyAndResponse(t *testing.T) {
	for _, mode := range []string{
		config.AuthRegistrationModeSelfServe,
		config.AuthRegistrationModeInviteOnly,
		config.AuthRegistrationModeInviteRegister,
	} {
		t.Run(mode, func(t *testing.T) {
			s := &stubWeChatService{
				exchange: func(
					_ context.Context, client string, _ *types.WeChatExchangeRequest,
					allow bool, _ types.TenantProvisioningMode,
				) (*types.LoginResponse, bool, error) {
					require.Equal(t, "mobile", client)
					require.Equal(t, mode == config.AuthRegistrationModeSelfServe, allow)
					return &types.LoginResponse{
						Success:      true,
						Token:        "access",
						RefreshToken: "refresh",
						Memberships:  []types.Membership{},
					}, true, nil
				},
			}
			r := weChatTestRouter(s, mode)
			body, err := json.Marshal(
				types.WeChatExchangeRequest{
					AttemptID:    "attempt",
					State:        "state",
					Code:         "code",
					CodeVerifier: strings.Repeat("x", 43),
				},
			)
			require.NoError(t, err)
			w := httptest.NewRecorder()
			r.ServeHTTP(
				w,
				httptest.NewRequest(http.MethodPost, "/api/v1/auth/wechat/exchange", strings.NewReader(string(body))),
			)
			require.Equal(t, 200, w.Code)
			require.Contains(t, w.Body.String(), `"access_token":"access"`)
			require.Contains(t, w.Body.String(), `"is_new_user":true`)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		})
	}
}

func TestWeChatPublicErrorsDoNotLeakDetails(t *testing.T) {
	s := &stubWeChatService{create: func(context.Context, string, string) (*types.WeChatAttemptResponse, error) {
		return nil, errors.New("database password secret")
	}}
	w := httptest.NewRecorder()
	weChatTestRouter(
		s,
		"",
	).ServeHTTP(w, httptest.NewRequest(
		http.MethodPost, "/api/v1/auth/wechat/attempts",
		strings.NewReader(`{"code_challenge":"`+strings.Repeat("x", 43)+`"}`),
	))
	require.Equal(t, 500, w.Code)
	require.NotContains(t, w.Body.String(), "database password")
}
