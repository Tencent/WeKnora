package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestWeChatSettingsHTTPRedactionAuditAndPublicFlags(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.WeChatAuthSetting{}))
	cfg := &config.Config{}
	svc := service.NewWeChatAuthService(db, cfg, nil)
	auth := &AuthHandler{configInfo: cfg}
	audit := &capturingAuditService{}
	system := &SystemHandler{auditSvc: audit}
	NewWeChatAuthHandler(svc, auth).BindSystemHandler(system)
	r := gin.New()
	r.GET("/settings", system.GetWeChatSettings)
	r.PUT("/settings", system.UpdateWeChatSettings)
	r.GET("/auth/config", auth.GetAuthConfig)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(withActor("admin"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for _, body := range []string{`{}`, `{"version":0,"web":{}}`, `{"version":null,"web":{},"mobile":{}}`} {
		require.Equal(t, http.StatusBadRequest, request(http.MethodPut, "/settings", body).Code)
	}
	body := `{"version":0,"web":{"enabled":true,"app_id":"web-id","app_secret":"write-only-secret",` +
		`"callback_url":"https://example.com/api/v1/auth/wechat/callback"},"mobile":{"enabled":false}}`
	w := request(http.MethodPut, "/settings", body)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.NotContains(t, w.Body.String(), "write-only-secret")
	require.NotContains(t, request(http.MethodGet, "/settings", "").Body.String(), "write-only-secret")
	require.Len(t, audit.entries, 1)
	encoded, err := json.Marshal(audit.entries)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "write-only-secret")
	require.Equal(t, types.AuditAction("system.wechat_auth_updated"), audit.entries[0].Action)
	require.Equal(t, "admin", audit.entries[0].ActorUserID)
	require.Equal(t, http.StatusConflict, request(http.MethodPut, "/settings", body).Code)
	var flags map[string]any
	w = request(http.MethodGet, "/auth/config", "")
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &flags))
	require.Equal(t, true, flags["wechat_web_enabled"])
	require.Equal(t, false, flags["wechat_mobile_enabled"])
	require.NotContains(t, w.Body.String(), "web-id")
	_, err = sqlDB.Exec("UPDATE we_chat_auth_settings SET web_enabled = false WHERE id = 1")
	require.NoError(t, err)
	w = request(http.MethodGet, "/auth/config", "")
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &flags))
	require.Equal(t, false, flags["wechat_web_enabled"], "configuration must take effect without restarting")
}
