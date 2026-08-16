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
	"github.com/gin-gonic/gin"
)

// Contract tests for POST /system/admin/users/create.
//
// The create / audit / generated-password cases assert the final HTTP
// contract. The validation cases cover the handler's binding and trim
// checks.

func createSystemUserRouter(h *SystemHandler, actorID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, actorID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.POST("/system/admin/users/create", h.CreateSystemUser)
	return r
}

func performCreateSystemUser(t *testing.T, r *gin.Engine, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/system/admin/users/create", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCreateSystemUserCreatesUserWithExplicitPassword(t *testing.T) {
	audits := &capturingAuditService{}
	h := &SystemHandler{auditSvc: audits}
	r := createSystemUserRouter(h, "admin-user")

	w := performCreateSystemUser(t, r, map[string]string{
		"username": "alice", "email": "alice@example.com", "password": "PlainPass9",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp CreateSystemUserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.User == nil || resp.User.Username != "alice" || resp.User.Email != "alice@example.com" {
		t.Fatalf("unexpected user: %+v", resp.User)
	}
	if resp.GeneratedPassword != "" {
		t.Fatalf(
			"generated_password must be absent when the caller supplied a password, got %q",
			resp.GeneratedPassword,
		)
	}
	if len(audits.entries) != 1 || audits.entries[0].Action != types.AuditActionSystemUserCreated {
		t.Fatalf("expected one %s audit entry, got %+v", types.AuditActionSystemUserCreated, audits.entries)
	}
	if strings.Contains(string(audits.entries[0].Details), "PlainPass9") {
		t.Fatal("audit details leaked the password")
	}
}

func TestCreateSystemUserAutoGeneratesPasswordWhenEmpty(t *testing.T) {
	audits := &capturingAuditService{}
	h := &SystemHandler{auditSvc: audits}
	r := createSystemUserRouter(h, "admin-user")

	w := performCreateSystemUser(t, r, map[string]string{
		"username": "bob", "email": "bob@example.com",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp CreateSystemUserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.User == nil || resp.User.Username != "bob" {
		t.Fatalf("unexpected user: %+v", resp.User)
	}
	if resp.GeneratedPassword == "" {
		t.Fatal("generated_password must be returned exactly once when the password was left empty")
	}
	if len(audits.entries) != 1 || audits.entries[0].Action != types.AuditActionSystemUserCreated {
		t.Fatalf("expected one %s audit entry, got %+v", types.AuditActionSystemUserCreated, audits.entries)
	}
	if strings.Contains(string(audits.entries[0].Details), resp.GeneratedPassword) {
		t.Fatal("audit details leaked the generated password")
	}
}

func TestCreateSystemUserDoesNotRewritePassword(t *testing.T) {
	// Leading/trailing whitespace is part of the credential: the handler
	// must pass it through byte-for-byte, and a whitespace-only value is
	// treated as "empty" (random-password fallback) downstream and never
	// rewritten at this layer.
	h := &SystemHandler{}
	r := createSystemUserRouter(h, "admin-user")

	for _, pw := range []string{"  PlainPass9  ", "   "} {
		w := performCreateSystemUser(t, r, map[string]string{
			"username": "alice", "email": "alice@example.com", "password": pw,
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("password=%q status=%d body=%s", pw, w.Code, w.Body.String())
		}
	}
}

func TestCreateSystemUserRejectsMissingFields(t *testing.T) {
	h := &SystemHandler{}
	r := createSystemUserRouter(h, "admin-user")

	cases := []map[string]string{
		{"email": "alice@example.com"},
		{"username": "alice"},
		{"username": "", "email": "alice@example.com"},
		{"username": "alice", "email": ""},
		{"username": "   ", "email": "alice@example.com"},
		// Binding's min=2 ran on the raw JSON; the trimmed value "a" (1
		// rune) must be rejected by the post-trim re-check.
		{"username": "  a  ", "email": "alice@example.com"},
	}
	for _, body := range cases {
		w := performCreateSystemUser(t, r, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%v status=%d body=%s", body, w.Code, w.Body.String())
		}
	}
}

func TestCreateSystemUserRejectsInvalidEmail(t *testing.T) {
	h := &SystemHandler{}
	r := createSystemUserRouter(h, "admin-user")

	w := performCreateSystemUser(t, r, map[string]string{
		"username": "alice", "email": "not-an-email",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateSystemUserRejectsMalformedJSON(t *testing.T) {
	h := &SystemHandler{}
	r := createSystemUserRouter(h, "admin-user")

	req := httptest.NewRequest(http.MethodPost, "/system/admin/users/create",
		bytes.NewReader([]byte(`{"username": "alice"`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
