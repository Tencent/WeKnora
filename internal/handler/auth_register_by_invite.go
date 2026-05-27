package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// registerByInviteRequest is the body for /auth/register-by-invite.
//
// Email is collected from the invitee themselves: share-link rows have
// no specific invitee, so we cannot pre-bind any address. The token
// IS the authorisation; the email is whatever account the invitee
// wants to register.
type registerByInviteRequest struct {
	Token    string `json:"token"    binding:"required"`
	Email    string `json:"email"    binding:"required,email"`
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required,min=6"`
}

// invitationLookupResponse is the public projection of a share-link
// row returned by /auth/invitations/lookup. Kept narrow on purpose:
// just enough for the registration page to render context ("X invited
// you to Y") without exposing inviter audit fields.
type invitationLookupResponse struct {
	TenantID   uint64           `json:"tenant_id"`
	TenantName string           `json:"tenant_name,omitempty"`
	Role       types.TenantRole `json:"role"`
	ExpiresAt  string           `json:"expires_at"`
}

// invitationLookupRequest carries the token in the request body
// instead of the URL path: GET /auth/invitations/:token would have
// landed the plaintext token in access logs, browser history, and
// tracing spans. POST + body keeps the token out of those surfaces
// at the cost of "this endpoint reads, why is it POST" — worth it
// because the token is the sole authorisation for registration.
type invitationLookupRequest struct {
	Token string `json:"token" binding:"required"`
}

// LookupInvitationByToken godoc
// @Summary      解析共享邀请链接 token
// @Description  根据邀请链接中的 token 返回邀请上下文（租户名 / 角色 / 过期时间），
// @Description  供注册页展示。无认证；token 无效或被撤销返回 410。
// @Description  使用 POST + body 而非 GET + path，避免 token 落入访问日志 / 浏览器历史 / tracing。
// @Tags         认证
// @Accept       json
// @Produce      json
// @Param        request  body      invitationLookupRequest  true  "邀请 token"
// @Success      200      {object}  invitationLookupResponse
// @Failure      410      {object}  errors.AppError  "链接无效或已撤销"
// @Router       /auth/invitations/lookup [post]
func (h *AuthHandler) LookupInvitationByToken(c *gin.Context) {
	ctx := c.Request.Context()

	if h.invitationSvc == nil {
		c.Error(apperrors.NewInternalServerError("invitation service unavailable"))
		return
	}

	var req invitationLookupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("token is required").WithDetails(err.Error()))
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		c.Error(apperrors.NewValidationError("token is required"))
		return
	}

	inv, err := h.invitationSvc.LookupByToken(ctx, token)
	if err != nil {
		// Collapse "unknown / expired / revoked" into 410 to avoid
		// leaking which slot a stolen token used to occupy.
		c.Error(&apperrors.AppError{
			Code:     apperrors.ErrNotFound,
			Message:  "invitation link is invalid or has been revoked",
			HTTPCode: http.StatusGone,
		})
		return
	}

	resp := invitationLookupResponse{
		TenantID:  inv.TenantID,
		Role:      inv.Role,
		ExpiresAt: inv.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if tenant, terr := h.tenantService.GetTenantByID(ctx, inv.TenantID); terr == nil && tenant != nil {
		resp.TenantName = tenant.Name
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
	})
}

// RegisterByInvite godoc
// @Summary      使用共享链接注册
// @Description  通过 Owner 生成的共享邀请链接 token 完成注册，绕过 invite_only 模式拦截。
// @Description  注册者自填邮箱（与 token 不绑定）；注册成功后自动加入对应租户。
// @Tags         认证
// @Accept       json
// @Produce      json
// @Param        request  body      registerByInviteRequest  true  "邀请注册请求"
// @Success      201      {object}  types.LoginResponse
// @Failure      400      {object}  errors.AppError  "请求参数错误"
// @Failure      409      {object}  errors.AppError  "邮箱已注册"
// @Failure      410      {object}  errors.AppError  "链接无效或已撤销"
// @Router       /auth/register-by-invite [post]
//
// RegisterByInvite is intentionally NOT subject to the invite_only gate:
// the gate suppresses public registration, while this endpoint requires
// a valid token issued by an Owner. The token IS the authorisation.
func (h *AuthHandler) RegisterByInvite(c *gin.Context) {
	ctx := c.Request.Context()

	if h.invitationSvc == nil {
		c.Error(apperrors.NewInternalServerError("invitation service unavailable"))
		return
	}

	var req registerByInviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid registration parameters").WithDetails(err.Error()))
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Username = strings.TrimSpace(req.Username)
	// Password is intentionally NOT sanitized: SanitizeForLog strips
	// \n, \r, \t and control chars to make a string safe to write into
	// a log line — applying it to a real password would silently
	// rewrite the stored credential and lock the user out. Passwords
	// must never be logged, so they don't need that defence here.
	if req.Token == "" || req.Email == "" || req.Username == "" || req.Password == "" {
		c.Error(apperrors.NewValidationError("token, email, username and password are required"))
		return
	}

	inv, err := h.invitationSvc.LookupByToken(ctx, req.Token)
	if err != nil {
		c.Error(&apperrors.AppError{
			Code:     apperrors.ErrNotFound,
			Message:  "invitation link is invalid or has been revoked",
			HTTPCode: http.StatusGone,
		})
		return
	}

	// If the email already has an account we shouldn't quietly succeed
	// here: register would fail with a duplicate-email error, but the
	// share-link flow is "create new account + join tenant", not "join
	// my existing account". Surface 409 so the SPA can prompt them to
	// log in instead, and the invitation can be applied to their
	// existing account via /me/invitations once we wire that path.
	if existing, _ := h.userService.GetUserByEmail(ctx, req.Email); existing != nil {
		c.Error(apperrors.NewConflictError(
			"this email already has an account; please log in to join the workspace"))
		return
	}

	user, err := h.userService.Register(ctx, &types.RegisterRequest{
		Username: req.Username,
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		logger.Errorf(ctx, "register-by-invite: user create failed for %s: %v",
			secutils.SanitizeForLog(req.Email), err)
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}

	if _, err := h.invitationSvc.AcceptByToken(ctx, req.Token, user.ID); err != nil {
		// Race: link was revoked between Lookup and Accept. The user
		// account is created either way — they can still log in.
		logger.Errorf(ctx, "register-by-invite: accept failed for user %s: %v", user.ID, err)
		c.Error(&apperrors.AppError{
			Code:     apperrors.ErrNotFound,
			Message:  "invitation link is no longer valid; please log in to your new account",
			HTTPCode: http.StatusGone,
		})
		return
	}

	accessToken, refreshToken, err := h.userService.GenerateTokens(ctx, user)
	if err != nil {
		logger.Errorf(ctx, "register-by-invite: token generation failed for user %s: %v", user.ID, err)
		c.Error(apperrors.NewInternalServerError("token generation failed").WithDetails(err.Error()))
		return
	}

	tenant, _ := h.tenantService.GetTenantByID(ctx, inv.TenantID)
	c.JSON(http.StatusCreated, &types.LoginResponse{
		Success:      true,
		Message:      "Registration successful",
		User:         user,
		ActiveTenant: tenant,
		Memberships: []types.Membership{{
			TenantID:   inv.TenantID,
			TenantName: tenantNameOrEmpty(tenant),
			Role:       inv.Role,
		}},
		Token:        accessToken,
		RefreshToken: refreshToken,
	})
}
