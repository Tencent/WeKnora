package handler

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// bootstrapSystemAdminEmailEnv is the same variable cmd/server/bootstrap.go
// reads at startup. Handler cannot import package main, so the name is
// duplicated. TrimSpace is the only normalization that hook applies;
// the sign-up match below is also case-insensitive.
const bootstrapSystemAdminEmailEnv = "WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL"

// maxClosedRegisterBodyBytes caps the body read while registration is
// closed. The app port is published directly, so an nginx client_max_body_size
// does not cover it. Over the limit is the same 403 as malformed JSON.
const maxClosedRegisterBodyBytes int64 = 64 << 10

// bootstrapAdminSignupMu serializes the empty-admin check and the create
// on this process. Another replica can pass the same check before either
// insert commits. Both inserts use the trimmed env email, so the
// case-sensitive users.email unique index lets one win and Register rolls
// the loser's new tenant back. A different casing is a different unique
// index key: if both replicas observe no LOWER(email) match before either
// commits, each can insert a system administrator. There is no
// cross-replica lock.
var bootstrapAdminSignupMu sync.Mutex

// registerWhenClosed handles POST /auth/register when the resolved mode is
// not self_serve. Binding and the password policy run before the bootstrap
// decision so their status codes do not reveal the configured email.
func (h *AuthHandler) registerWhenClosed(c *gin.Context) {
	ctx := c.Request.Context()
	body, err := readClosedRegisterBody(c.Request)
	if err != nil {
		rejectClosedRegistration(c)
		return
	}
	normalized, err := trimRegisterEmailJSON(body)
	if err != nil {
		rejectClosedRegistration(c)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(normalized))
	c.Request.ContentLength = int64(len(normalized))

	var req types.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		rejectClosedRegistration(c)
		return
	}
	req.Username = secutils.SanitizeForLog(req.Username)
	req.Email = secutils.SanitizeForLog(req.Email)
	if req.Username == "" || req.Email == "" || req.Password == "" {
		rejectClosedRegistration(c)
		return
	}
	// Same 400 for every closed-registration email. A weak password must
	// not confirm that this address is the bootstrap email or that the
	// administrator slot is open.
	if err := service.ValidatePasswordPolicy(req.Password, h.complexPasswordEnabled(ctx)); err != nil {
		logger.Error(ctx, "Invalid password policy")
		_ = c.Error(errors.NewValidationError(err.Error()))
		return
	}

	configured := strings.TrimSpace(os.Getenv(bootstrapSystemAdminEmailEnv))
	if configured == "" || !strings.EqualFold(strings.TrimSpace(req.Email), configured) {
		rejectClosedRegistration(c)
		return
	}

	bootstrapAdminSignupMu.Lock()
	defer bootstrapAdminSignupMu.Unlock()

	if !h.bootstrapAdminSlotOpen(ctx, configured) {
		rejectClosedRegistration(c)
		return
	}
	// Re-check immediately before insert. The mutex makes this redundant
	// in one process. Across replicas it sees an administrator only after
	// that other insert has committed. See bootstrapAdminSignupMu.
	if _, total, listErr := h.userService.ListSystemAdmins(ctx, 0, 1); listErr != nil || total > 0 {
		rejectClosedRegistration(c)
		return
	}

	// Store the trimmed env value so startup's exact lookup finds this row.
	// The flag is persisted by the same insert. A failed insert leaves no
	// user; Register deletes the personal tenant it created for that attempt.
	req.Email = secutils.SanitizeForLog(configured)
	req.BootstrapSystemAdmin = true
	req.TenantProvisioning = h.resolveDefaultTenantMode(ctx)
	user, err := h.userService.Register(ctx, &req)
	if err != nil {
		logger.Errorf(ctx, "Failed to register bootstrap system administrator: %v", err)
		rejectClosedRegistration(c)
		return
	}

	logger.Infof(ctx, "User registered successfully: %s", secutils.SanitizeForLog(user.Email))
	c.JSON(http.StatusCreated, &types.RegisterResponse{
		Success: true,
		Message: "Registration successful",
		User:    user,
	})
}

func rejectClosedRegistration(c *gin.Context) {
	logger.Warn(c.Request.Context(), "Public registration rejected by auth.registration_mode")
	_ = c.Error(errors.NewForbiddenError("Public registration is disabled"))
}

// bootstrapAdminSlotOpen is called with bootstrapAdminSignupMu held.
// Occupancy uses LOWER(email) = LOWER(?), so an active or inactive row in
// any ASCII casing blocks the sign-up. The existing row is not updated.
func (h *AuthHandler) bootstrapAdminSlotOpen(ctx context.Context, configured string) bool {
	if h == nil || h.userService == nil {
		return false
	}
	_, total, err := h.userService.ListSystemAdmins(ctx, 0, 1)
	if err != nil || total > 0 {
		return false
	}
	user, lookupErr := h.userService.GetUserByEmailCaseInsensitive(ctx, configured)
	if lookupErr != nil {
		return bootstrapUserNotFound(lookupErr)
	}
	return user == nil
}

func bootstrapUserNotFound(err error) bool {
	if err == nil {
		return false
	}
	return stderrors.Is(err, service.ErrUserNotFound) ||
		strings.Contains(strings.ToLower(err.Error()), "user not found")
}

func readClosedRegisterBody(r *http.Request) ([]byte, error) {
	if r == nil || r.Body == nil {
		return nil, stderrors.New("missing request body")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxClosedRegisterBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, stderrors.New("empty request body")
	}
	return body, nil
}

// trimRegisterEmailJSON trims the email on every closed-registration body
// so a surrounding space is not itself a signal of whether the address
// matches the bootstrap email. Other fields are left unchanged.
func trimRegisterEmailJSON(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if email, ok := payload["email"].(string); ok {
		payload["email"] = strings.TrimSpace(email)
	}
	return json.Marshal(payload)
}
