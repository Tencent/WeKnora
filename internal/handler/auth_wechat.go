package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

const (
	weChatCookieName       = "__Host-weknora_wechat"
	weChatCookiePath       = "/"
	weChatResultCookieName = "__Host-weknora_wechat_result"
	weChatResultCookiePath = "/"
)

type weChatAuthService interface {
	StoreWebResult(context.Context, *types.LoginResponse, string) (string, error)
	CompleteWebResult(context.Context, string, string) (*service.WeChatWebResult, error)
	CreateAttempt(context.Context, string, string) (*types.WeChatAttemptResponse, error)
	WebAuthorizationURL(*types.WeChatAttemptResponse) (string, error)
	Exchange(
		context.Context,
		string,
		*types.WeChatExchangeRequest,
		bool,
		types.TenantProvisioningMode,
	) (*types.LoginResponse, bool, error)
}

// WeChatAuthHandler exposes website and native authentication endpoints.
type WeChatAuthHandler struct {
	service weChatAuthService
	auth    *AuthHandler
}

// NewWeChatAuthHandler wires login and public capability discovery.
func NewWeChatAuthHandler(s *service.WeChatAuthService, auth *AuthHandler) *WeChatAuthHandler {
	auth.weChatService = s
	return &WeChatAuthHandler{service: s, auth: auth}
}

// CreateAttempt is the native SDK entry point. The client retains its random
// verifier locally and sends only BASE64URL(SHA256(verifier)) at this stage.
func (h *WeChatAuthHandler) CreateAttempt(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req struct {
		CodeChallenge string `json:"code_challenge" binding:"required,len=43"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewValidationError("Invalid WeChat login challenge"))
		return
	}
	a, err := h.service.CreateAttempt(c.Request.Context(), "mobile", req.CodeChallenge)
	if err != nil {
		weChatHTTPError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": a})
}

// Exchange returns a WeKnora session for a native authorization code.
func (h *WeChatAuthHandler) Exchange(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req types.WeChatExchangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewValidationError("Invalid WeChat login request"))
		return
	}
	response, isNew, err := h.exchange(c, "mobile", &req)
	if err != nil {
		weChatHTTPError(c, err)
		return
	}
	safe := dto.NewAuthLoginResponse(response)
	c.JSON(http.StatusOK, gin.H{
		"success": true, "access_token": safe.Token, "refresh_token": safe.RefreshToken,
		"user": safe.User, "active_tenant": safe.ActiveTenant, "memberships": safe.Memberships, "is_new_user": isNew,
	})
}

func (h *WeChatAuthHandler) exchange(
	c *gin.Context,
	client string,
	req *types.WeChatExchangeRequest,
) (*types.LoginResponse, bool, error) {
	ctx := c.Request.Context()
	return h.service.Exchange(ctx, client, req,
		h.auth.resolveRegistrationMode(ctx) == config.AuthRegistrationModeSelfServe,
		h.auth.resolveDefaultTenantMode(ctx))
}

// startWebAttempt uses the configured HTTPS callback, never a caller-provided
// Host or redirect URI. A host-only HttpOnly cookie binds the attempt to this
// browser; starting another attempt replaces it.
func (h *WeChatAuthHandler) startWebAttempt(c *gin.Context) (*types.WeChatAttemptResponse, error) {
	verifier, err := service.WeChatRandomVerifier()
	if err != nil {
		return nil, err
	}
	a, err := h.service.CreateAttempt(c.Request.Context(), "web", service.WeChatChallenge(verifier))
	if err != nil {
		return nil, err
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(
		weChatCookieName,
		a.AttemptID+"."+verifier,
		int(service.WeChatAttemptTTL.Seconds()),
		weChatCookiePath,
		"",
		true,
		true,
	)
	return a, nil
}

// Start redirects the whole page to WeChat's hosted QR code page.
func (h *WeChatAuthHandler) Start(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	a, err := h.startWebAttempt(c)
	if err != nil {
		weChatHTTPError(c, err)
		return
	}
	authorizationURL, err := h.service.WebAuthorizationURL(a)
	if err != nil {
		weChatHTTPError(c, err)
		return
	}
	c.Redirect(http.StatusFound, authorizationURL)
}

// CreateWebAttempt returns the parameters for a QR code embedded in the login
// page. After the user confirms on their phone, WeChat navigates the top
// window to the same callback used by Start.
func (h *WeChatAuthHandler) CreateWebAttempt(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	a, err := h.startWebAttempt(c)
	if err != nil {
		weChatHTTPError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"app_id":       a.AppID,
		"scope":        a.Scope,
		"state":        a.State,
		"redirect_uri": a.CallbackURL,
		"expires_at":   a.ExpiresAt,
	}})
}

// Callback completes browser authorization using the initiating browser cookie.
func (h *WeChatAuthHandler) Callback(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	cookie, err := c.Cookie(weChatCookieName)
	parts := strings.Split(cookie, ".")
	if err != nil || len(parts) != 2 {
		c.Redirect(http.StatusFound, "/#wechat_error=invalid_attempt")
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(weChatCookieName, "", -1, weChatCookiePath, "", true, true)
	response, _, err := h.exchange(c, "web", &types.WeChatExchangeRequest{
		AttemptID: parts[0], CodeVerifier: parts[1], State: c.Query("state"), Code: c.Query("code"),
	})
	if err != nil {
		code := "login_failed"
		switch {
		case errors.Is(err, service.ErrWeChatRegistration):
			code = "registration_disabled"
		case errors.Is(err, service.ErrWeChatAttempt):
			code = "invalid_attempt"
		}
		c.Redirect(http.StatusFound, "/#wechat_error="+code)
		return
	}
	ticket, err := h.service.StoreWebResult(c.Request.Context(), response, parts[1])
	if err != nil {
		c.Redirect(http.StatusFound, "/#wechat_error=login_failed")
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(weChatResultCookieName, parts[1], 60, weChatResultCookiePath, "", true, true)
	c.Redirect(http.StatusFound, "/#wechat_ticket="+ticket)
}

// Complete returns credentials only to the browser that initiated authorization.
// A copied callback URL is insufficient without its HttpOnly cookie.
func (h *WeChatAuthHandler) Complete(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req struct {
		Ticket string `json:"ticket" binding:"required,max=36"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewValidationError("Invalid WeChat completion ticket"))
		return
	}
	verifier, err := c.Cookie(weChatResultCookieName)
	if err != nil {
		weChatHTTPError(c, service.ErrWeChatAttempt)
		return
	}
	response, err := h.service.CompleteWebResult(c.Request.Context(), req.Ticket, verifier)
	if err != nil {
		weChatHTTPError(c, err)
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(weChatResultCookieName, "", -1, weChatResultCookiePath, "", true, true)
	c.JSON(http.StatusOK, response)
}

func weChatHTTPError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrWeChatDisabled),
		errors.Is(err, service.ErrWeChatRegistration),
		errors.Is(err, service.ErrWeChatAccount):
		_ = c.Error(apperrors.NewForbiddenError(err.Error()))
	case errors.Is(err, service.ErrWeChatAttempt), errors.Is(err, service.ErrWeChatCode):
		_ = c.Error(apperrors.NewUnauthorizedError(err.Error()))
	case errors.Is(err, service.ErrWeChatUnavailable):
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": err.Error()})
	default:
		// Database errors can contain external identity values; do not expose
		// them or request details in public responses.
		_ = c.Error(apperrors.NewInternalServerError("WeChat login failed"))
	}
}
