package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// createUserService records the request handed to AdminCreateUser so
// tests can pin byte-for-byte password pass-through and the resolved
// tenant provisioning mode.
type createUserService struct {
	interfaces.UserService
	createdUser     *types.User
	generated       string
	err             error
	gotReq          *types.AdminCreateUserRequest
	gotProvisioning types.TenantProvisioningMode
}

func (s *createUserService) AdminCreateUser(
	_ context.Context,
	req *types.AdminCreateUserRequest,
	provisioning types.TenantProvisioningMode,
) (*types.User, string, error) {
	record := *req
	s.gotReq = &record
	s.gotProvisioning = provisioning
	return s.createdUser, s.generated, s.err
}

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
	users := &createUserService{createdUser: &types.User{
		ID: "u1", Username: "alice", Email: "alice@example.com",
	}}
	audits := &capturingAuditService{}
	h := &SystemHandler{userSvc: users, auditSvc: audits}
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
	if users.gotReq == nil || users.gotReq.Password == nil || *users.gotReq.Password != "PlainPass9" {
		t.Fatalf("service received unexpected request: %+v", users.gotReq)
	}
	if len(audits.entries) != 1 || audits.entries[0].Action != types.AuditActionSystemUserCreated {
		t.Fatalf("expected one %s audit entry, got %+v", types.AuditActionSystemUserCreated, audits.entries)
	}
	if strings.Contains(string(audits.entries[0].Details), "PlainPass9") {
		t.Fatal("audit details leaked the password")
	}
	if !strings.Contains(string(audits.entries[0].Details), `"password_generated":false`) {
		t.Fatalf("audit details must mark password_generated=false, got %s", audits.entries[0].Details)
	}
	if !strings.Contains(string(audits.entries[0].Details), `"idempotent":false`) {
		t.Fatalf("audit details must mark idempotent=false, got %s", audits.entries[0].Details)
	}
}

func TestCreateSystemUserAutoGeneratesPasswordWhenEmpty(t *testing.T) {
	users := &createUserService{
		createdUser: &types.User{ID: "u2", Username: "bob", Email: "bob@example.com"},
		generated:   "G3n3r4t3dP4ssw0rd",
	}
	audits := &capturingAuditService{}
	h := &SystemHandler{userSvc: users, auditSvc: audits}
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
	if resp.GeneratedPassword != "G3n3r4t3dP4ssw0rd" {
		t.Fatalf("generated_password=%q, want the once-only generated password", resp.GeneratedPassword)
	}
	if users.gotReq == nil || users.gotReq.Password != nil {
		t.Fatalf("absent password must reach the service as nil, got %+v", users.gotReq)
	}
	if len(audits.entries) != 1 || audits.entries[0].Action != types.AuditActionSystemUserCreated {
		t.Fatalf("expected one %s audit entry, got %+v", types.AuditActionSystemUserCreated, audits.entries)
	}
	if !strings.Contains(string(audits.entries[0].Details), "password_generated") {
		t.Fatal("audit details must carry the password_generated flag")
	}
	if strings.Contains(string(audits.entries[0].Details), "G3n3r4t3dP4ssw0rd") {
		t.Fatal("audit details leaked the generated password")
	}
	if !strings.Contains(string(audits.entries[0].Details), `"password_generated":true`) {
		t.Fatalf("audit details must mark password_generated=true, got %s", audits.entries[0].Details)
	}
	if !strings.Contains(string(audits.entries[0].Details), `"idempotent":false`) {
		t.Fatalf("audit details must mark idempotent=false, got %s", audits.entries[0].Details)
	}
}

func TestCreateSystemUserDoesNotRewritePassword(t *testing.T) {
	// Password bytes must reach the service unmodified for valid credentials.
	users := &createUserService{createdUser: &types.User{ID: "u3", Username: "carol", Email: "carol@example.com"}}
	h := &SystemHandler{userSvc: users}
	r := createSystemUserRouter(h, "admin-user")

	for _, pw := range []string{"  PlainPass9  ", "\tPlainPass9\n"} {
		w := performCreateSystemUser(t, r, map[string]string{
			"username": "carol", "email": "carol@example.com", "password": pw,
		})
		if w.Code != http.StatusCreated {
			t.Fatalf("password=%q status=%d body=%s", pw, w.Code, w.Body.String())
		}
		if users.gotReq == nil || users.gotReq.Password == nil || *users.gotReq.Password != pw {
			t.Fatalf("password=%q service received %+v", pw, users.gotReq)
		}
	}
}

func TestCreateSystemUserMapsEmptyPasswordTo400(t *testing.T) {
	users := &createUserService{err: service.ErrPasswordPolicy}
	h := &SystemHandler{userSvc: users}
	r := createSystemUserRouter(h, "admin-user")

	w := performCreateSystemUser(t, r, map[string]string{
		"username": "carol", "email": "carol@example.com", "password": "",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateSystemUserMapsIdentityConflictTo409(t *testing.T) {
	users := &createUserService{err: service.ErrUserIdentityConflict}
	audits := &capturingAuditService{}
	h := &SystemHandler{userSvc: users, auditSvc: audits}
	r := createSystemUserRouter(h, "admin-user")

	w := performCreateSystemUser(t, r, map[string]string{
		"username": "alice", "email": "bob@example.com",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(audits.entries) != 0 {
		t.Fatalf("conflict emitted audit entries: %+v", audits.entries)
	}
}

func TestCreateSystemUserResolvesDefaultTenantMode(t *testing.T) {
	users := &createUserService{createdUser: &types.User{ID: "u4", Username: "dave", Email: "dave@example.com"}}
	h := &SystemHandler{userSvc: users}
	r := createSystemUserRouter(h, "admin-user")

	w := performCreateSystemUser(t, r, map[string]string{
		"username": "dave", "email": "dave@example.com",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if users.gotProvisioning != types.TenantProvisioningCreatePersonal {
		t.Fatalf("provisioning=%v, want create_personal default", users.gotProvisioning)
	}
}

func TestCreateSystemUserMapsPasswordPolicyTo400(t *testing.T) {
	users := &createUserService{err: service.ErrPasswordPolicy}
	h := &SystemHandler{userSvc: users}
	r := createSystemUserRouter(h, "admin-user")

	w := performCreateSystemUser(t, r, map[string]string{
		"username": "alice", "email": "alice@example.com", "password": "password",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCreateSystemUserDuplicateIdentityReturnsExistingUser(t *testing.T) {
	// Idempotent contract: when the identity already exists the service
	// returns the existing user with ErrUserEmailExists/ErrUserUsernameExists,
	// and the handler answers 200 with the existing UserInfo, an empty
	// generated_password and an audit row marked idempotent.
	users := &createUserService{
		createdUser: &types.User{ID: "existing", Username: "alice", Email: "alice@example.com"},
		err:         service.ErrUserUsernameExists,
	}
	audits := &capturingAuditService{}
	h := &SystemHandler{userSvc: users, auditSvc: audits}
	r := createSystemUserRouter(h, "admin-user")

	w := performCreateSystemUser(t, r, map[string]string{
		"username": "alice", "email": "alice@example.com",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp CreateSystemUserResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.User == nil || resp.User.ID != "existing" {
		t.Fatalf("unexpected user: %+v", resp.User)
	}
	if resp.GeneratedPassword != "" {
		t.Fatalf("generated_password=%q, want empty for an existing user", resp.GeneratedPassword)
	}
	if len(audits.entries) != 1 || audits.entries[0].Action != types.AuditActionSystemUserCreated {
		t.Fatalf("expected one %s audit entry, got %+v", types.AuditActionSystemUserCreated, audits.entries)
	}
	if !strings.Contains(string(audits.entries[0].Details), `"idempotent":true`) {
		t.Fatalf("audit details must mark idempotent=true, got %s", audits.entries[0].Details)
	}
	if !strings.Contains(string(audits.entries[0].Details), `"password_generated":false`) {
		t.Fatalf("audit details must mark password_generated=false, got %s", audits.entries[0].Details)
	}
}

func TestCreateSystemUserMapsInternalErrorTo500(t *testing.T) {
	users := &createUserService{err: errors.New("transient db hiccup")}
	audits := &capturingAuditService{}
	h := &SystemHandler{userSvc: users, auditSvc: audits}
	r := createSystemUserRouter(h, "admin-user")

	w := performCreateSystemUser(t, r, map[string]string{
		"username": "alice", "email": "alice@example.com",
	})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(audits.entries) != 0 {
		t.Fatalf("failed creation emitted audit entries: %+v", audits.entries)
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
