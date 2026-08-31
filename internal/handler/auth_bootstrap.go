package handler

import (
	"context"
	stderrors "errors"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const bootstrapSystemAdminEmailEnv = "WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL"

// bootstrapSystemAdminMu closes the check-then-promote window for concurrent
// requests handled by one process. The startup hook remains best-effort, but
// the public bootstrap endpoint must not create two administrators when a
// user double-clicks the first-account form.
var bootstrapSystemAdminMu sync.Mutex

type bootstrapSystemAdminRequest struct {
	Username string `json:"username" binding:"required,min=2,max=50"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8,max=32"`
}

// BootstrapSystemAdmin creates (or promotes) the one configured bootstrap
// account and signs it in. It is intentionally independent of the ordinary
// registration gate: DISABLE_REGISTRATION=true is commonly enabled on the
// first deployment specifically to prevent arbitrary public sign-ups, while
// WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL names the single account allowed to
// initialize platform administration.
//
// The endpoint is safe to leave enabled after the first login. Once an
// administrator exists, every subsequent request is rejected and the config
// endpoint hides the bootstrap entry point.
func (h *AuthHandler) BootstrapSystemAdmin(c *gin.Context) {
	ctx := c.Request.Context()
	configuredEmail := strings.TrimSpace(os.Getenv(bootstrapSystemAdminEmailEnv))
	if configuredEmail == "" {
		c.Error(apperrors.NewNotFoundError("system-admin bootstrap is not configured"))
		return
	}
	if h.userService == nil {
		c.Error(apperrors.NewServiceUnavailableError("system-admin bootstrap is unavailable"))
		return
	}

	var req bootstrapSystemAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid bootstrap parameters").WithDetails(err.Error()))
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	if !strings.EqualFold(req.Email, configuredEmail) {
		c.Error(apperrors.NewForbiddenError("system-admin bootstrap is not available for this email"))
		return
	}
	// Use the deployment-configured spelling for lookup and login. This keeps
	// bootstrap idempotent when an existing account uses mixed-case email.
	req.Email = configuredEmail
	if err := service.ValidatePasswordPolicy(req.Password); err != nil {
		c.Error(apperrors.NewValidationError(err.Error()))
		return
	}

	bootstrapSystemAdminMu.Lock()
	defer bootstrapSystemAdminMu.Unlock()

	_, total, err := h.userService.ListSystemAdmins(ctx, 0, 1)
	if err != nil {
		logger.Errorf(ctx, "system-admin bootstrap: failed to list administrators: %v", err)
		c.Error(apperrors.NewServiceUnavailableError("system-admin bootstrap is unavailable"))
		return
	}
	if total > 0 {
		c.Error(apperrors.NewConflictError("a system administrator is already configured"))
		return
	}

	user, err := h.userService.GetUserByEmail(ctx, req.Email)
	if err != nil && !isBootstrapUserNotFound(err) {
		logger.Errorf(ctx, "system-admin bootstrap: failed to find %s: %v", req.Email, err)
		c.Error(apperrors.NewServiceUnavailableError("system-admin bootstrap is unavailable"))
		return
	}

	created := user == nil
	if created {
		user, err = h.userService.Register(ctx, &types.RegisterRequest{
			Username:           req.Username,
			Email:              req.Email,
			Password:           req.Password,
			TenantProvisioning: h.resolveDefaultTenantMode(ctx),
		})
		if err != nil {
			logger.Errorf(ctx, "system-admin bootstrap: failed to create user: %v", err)
			c.Error(apperrors.NewBadRequestError(err.Error()))
			return
		}
	} else {
		// An operator may have created the configured account before enabling
		// invite-only registration. Require its password before promoting it.
		if !user.IsActive {
			c.Error(apperrors.NewForbiddenError("bootstrap account is inactive"))
			return
		}
		if err := h.userService.ValidatePassword(ctx, user.ID, req.Password); err != nil {
			c.Error(apperrors.NewUnauthorizedError("invalid bootstrap credentials"))
			return
		}
	}
	if user == nil {
		logger.Error(ctx, "system-admin bootstrap: user service returned a nil user")
		c.Error(apperrors.NewInternalServerError("system-admin bootstrap failed"))
		return
	}

	user.IsSystemAdmin = true
	if err := h.userService.UpdateUser(ctx, user); err != nil {
		logger.Errorf(ctx, "system-admin bootstrap: failed to promote user %s: %v", user.ID, err)
		c.Error(apperrors.NewInternalServerError("failed to configure system administrator").WithDetails(err.Error()))
		return
	}

	loginResponse, err := h.userService.Login(ctx, &types.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil || loginResponse == nil || !loginResponse.Success {
		if err != nil {
			logger.Errorf(ctx, "system-admin bootstrap: failed to sign in user %s: %v", user.ID, err)
		} else {
			logger.Errorf(ctx, "system-admin bootstrap: sign-in failed for user %s", user.ID)
		}
		c.Error(apperrors.NewInternalServerError("system-admin bootstrap sign-in failed"))
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, dto.NewAuthLoginResponse(loginResponse))
}

func (h *AuthHandler) bootstrapSystemAdminAvailable(ctx context.Context) bool {
	if strings.TrimSpace(os.Getenv(bootstrapSystemAdminEmailEnv)) == "" || h.userService == nil {
		return false
	}
	if _, total, err := h.userService.ListSystemAdmins(ctx, 0, 1); err != nil || total > 0 {
		return false
	}
	user, err := h.userService.GetUserByEmail(ctx, strings.TrimSpace(os.Getenv(bootstrapSystemAdminEmailEnv)))
	return (err == nil && user != nil) || isBootstrapUserNotFound(err)
}

func isBootstrapUserNotFound(err error) bool {
	if err == nil {
		return false
	}
	return stderrors.Is(err, apprepo.ErrUserNotFound) ||
		strings.Contains(strings.ToLower(err.Error()), "user not found")
}
