package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type setUserStatusService struct {
	interfaces.UserService
	returnUser *types.User
	err        error
	gotID      string
	gotActor   string
	gotActive  bool
}

func (s *setUserStatusService) SetUserActive(
	_ context.Context, userID, actorID string, active bool,
) (*types.User, error) {
	s.gotID, s.gotActor, s.gotActive = userID, actorID, active
	return s.returnUser, s.err
}

func statusRouter(h *SystemHandler, actorID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, actorID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.POST("/system/admin/users/status", h.SetUserStatus)
	return r
}

func TestSetUserStatus_Success(t *testing.T) {
	svc := &setUserStatusService{returnUser: &types.User{ID: "u2", Username: "bob", IsActive: false}}
	h := &SystemHandler{userSvc: svc}
	r := statusRouter(h, "admin-1")

	body, _ := json.Marshal(map[string]any{"user_id": "u2", "is_active": false})
	req := httptest.NewRequest(http.MethodPost, "/system/admin/users/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d %s", w.Code, w.Body.String())
	}
	if svc.gotID != "u2" || svc.gotActor != "admin-1" || svc.gotActive {
		t.Fatalf("wrong args: %+v", svc)
	}
}

func TestSetUserStatus_GuardErrors400(t *testing.T) {
	for _, sentinel := range []error{service.ErrCannotDisableSelf, service.ErrCannotDisableLastAdmin} {
		svc := &setUserStatusService{err: sentinel}
		h := &SystemHandler{userSvc: svc}
		r := statusRouter(h, "admin-1")
		body, _ := json.Marshal(map[string]any{"user_id": "x", "is_active": false})
		req := httptest.NewRequest(http.MethodPost, "/system/admin/users/status", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%v: expected 400 got %d", sentinel, w.Code)
		}
	}
}

func TestSetUserStatus_NotFound404(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "users.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.User{}))
	svc := service.NewUserService(nil, repository.NewUserRepository(db), nil, nil, nil, nil)
	h := &SystemHandler{userSvc: svc}
	r := statusRouter(h, "admin-1")
	body, _ := json.Marshal(map[string]any{"user_id": "ghost", "is_active": true})
	req := httptest.NewRequest(http.MethodPost, "/system/admin/users/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 got %d", w.Code)
	}
}

func TestSetUserStatus_RequiresExplicitStatus(t *testing.T) {
	for _, body := range []string{
		`{"user_id":"u2"}`,
		`{"user_id":"u2","is_active":null}`,
		`{"user_id":"u2","is_active":"false"}`,
	} {
		t.Run(body, func(t *testing.T) {
			svc := &setUserStatusService{}
			r := statusRouter(&SystemHandler{userSvc: svc}, "admin-1")
			req := httptest.NewRequest(http.MethodPost, "/system/admin/users/status", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Empty(t, svc.gotID, "invalid requests must not change account status")
		})
	}
}
