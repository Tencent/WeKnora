package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type resetPasswordUserService struct {
	interfaces.UserService
	target      *types.User
	lookupCalls int
	resetCalls  int
	resetUserID string
}

func (s *resetPasswordUserService) GetUserByEmail(context.Context, string) (*types.User, error) {
	s.lookupCalls++
	return s.target, nil
}

func (s *resetPasswordUserService) AdminResetPassword(_ context.Context, userID, _ string) error {
	s.resetCalls++
	s.resetUserID = userID
	return nil
}

func passwordResetRouter(h *SystemHandler, actorID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, actorID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.POST("/system/admin/users/reset-password", h.ResetUserPassword)
	return r
}

func performPasswordReset(t *testing.T, r *gin.Engine, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/system/admin/users/reset-password", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestResetUserPasswordResetsOtherUserAndAuditsWithoutSecret(t *testing.T) {
	users := &resetPasswordUserService{target: &types.User{
		ID: "target-user", Username: "alice", Email: "alice@example.com",
	}}
	audits := &capturingAuditService{}
	h := &SystemHandler{userSvc: users, auditSvc: audits}

	w := performPasswordReset(t, passwordResetRouter(h, "admin-user"), map[string]string{
		"email": "alice@example.com", "new_password": "FreshPass9",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if users.resetCalls != 1 || users.resetUserID != "target-user" {
		t.Fatalf("reset calls=%d user=%q", users.resetCalls, users.resetUserID)
	}
	if len(audits.entries) != 1 || audits.entries[0].Action != types.AuditActionSystemUserPasswordReset {
		t.Fatalf("unexpected audit entries: %+v", audits.entries)
	}
	if strings.Contains(string(audits.entries[0].Details), "FreshPass9") {
		t.Fatal("audit details leaked the new password")
	}
}

func TestResetUserPasswordRejectsSelfReset(t *testing.T) {
	users := &resetPasswordUserService{target: &types.User{
		ID: "admin-user", Username: "admin", Email: "admin@example.com",
	}}
	h := &SystemHandler{userSvc: users}

	w := performPasswordReset(t, passwordResetRouter(h, "admin-user"), map[string]string{
		"email": "admin@example.com", "new_password": "FreshPass9",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if users.resetCalls != 0 {
		t.Fatalf("self reset reached service %d times", users.resetCalls)
	}
}

func TestResetUserPasswordRejectsWeakPasswordBeforeUserLookup(t *testing.T) {
	users := &resetPasswordUserService{target: &types.User{ID: "target-user"}}
	h := &SystemHandler{userSvc: users}

	w := performPasswordReset(t, passwordResetRouter(h, "admin-user"), map[string]string{
		"email": "alice@example.com", "new_password": "password",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if users.lookupCalls != 0 || users.resetCalls != 0 {
		t.Fatalf("weak password caused side effects: lookups=%d resets=%d", users.lookupCalls, users.resetCalls)
	}
}
