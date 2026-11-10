package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubChangePasswordUserService struct {
	interfaces.UserService
	getCurrentUser func(ctx context.Context) (*types.User, error)
	changePassword func(ctx context.Context, userID, oldPassword, newPassword string) error
}

func (s *stubChangePasswordUserService) GetCurrentUser(ctx context.Context) (*types.User, error) {
	return s.getCurrentUser(ctx)
}

func (s *stubChangePasswordUserService) ChangePassword(
	ctx context.Context,
	userID, oldPassword, newPassword string,
) error {
	return s.changePassword(ctx, userID, oldPassword, newPassword)
}

func changePasswordErrorCapture() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 {
			return
		}
		err := c.Errors.Last().Err
		if appErr, ok := err.(*apperrors.AppError); ok {
			c.JSON(appErr.HTTPCode, gin.H{
				"success": false,
				"error": gin.H{
					"code":    appErr.Code,
					"message": appErr.Message,
					"details": appErr.Details,
				},
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func newChangePasswordTestRouter(h *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(changePasswordErrorCapture())
	r.POST("/auth/change-password", h.ChangePassword)
	return r
}

func doChangePassword(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/change-password", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestChangePassword_MapsInvalidOldPasswordDetail(t *testing.T) {
	h := &AuthHandler{
		userService: &stubChangePasswordUserService{
			getCurrentUser: func(ctx context.Context) (*types.User, error) {
				return &types.User{ID: "user-1", Email: "alice@example.com"}, nil
			},
			changePassword: func(ctx context.Context, userID, oldPassword, newPassword string) error {
				return service.ErrInvalidOldPassword
			},
		},
	}

	w := doChangePassword(t, newChangePasswordTestRouter(h), map[string]string{
		"old_password": "wrong",
		"new_password": "NewSecure9",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var resp struct {
		Error struct {
			Details string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error.Details != service.DetailInvalidOldPassword {
		t.Fatalf("details = %q, want %q", resp.Error.Details, service.DetailInvalidOldPassword)
	}
}

func TestChangePassword_MapsSamePasswordDetail(t *testing.T) {
	h := &AuthHandler{
		userService: &stubChangePasswordUserService{
			getCurrentUser: func(ctx context.Context) (*types.User, error) {
				return &types.User{ID: "user-1", Email: "alice@example.com"}, nil
			},
			changePassword: func(ctx context.Context, userID, oldPassword, newPassword string) error {
				return service.ErrSamePassword
			},
		},
	}

	w := doChangePassword(t, newChangePasswordTestRouter(h), map[string]string{
		"old_password": "OldSecure9",
		"new_password": "OldSecure9",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var resp struct {
		Error struct {
			Details string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error.Details != service.DetailSamePassword {
		t.Fatalf("details = %q, want %q", resp.Error.Details, service.DetailSamePassword)
	}
}

func TestChangePassword_MapsPasswordPolicyDetail(t *testing.T) {
	h := &AuthHandler{
		userService: &stubChangePasswordUserService{
			getCurrentUser: func(_ context.Context) (*types.User, error) {
				return &types.User{ID: "user-1", Email: "alice@example.com"}, nil
			},
			changePassword: func(_ context.Context, _, _, _ string) error {
				return service.ErrComplexPasswordPolicy
			},
		},
	}

	w := doChangePassword(t, newChangePasswordTestRouter(h), map[string]string{
		"old_password": "OldSecure9",
		"new_password": "NewSecure9",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	var resp struct {
		Error struct {
			Details string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error.Details != service.DetailPasswordPolicy {
		t.Fatalf("details = %q, want %q", resp.Error.Details, service.DetailPasswordPolicy)
	}
}
