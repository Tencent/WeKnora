package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// switchesDown is a tenant settings store that cannot be written.
type switchesDown struct{ plugintest.MemTenantSettings }

func (*switchesDown) Upsert(context.Context, *types.PluginTenantSetting, ...string) error {
	return errors.New("db down")
}

// Registering a workspace's own plugin switches it on there; when that
// fails the request says so, and still returns the one-time secret.
func TestInstallTenantPluginSwitchesItOn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	utils.SetSSRFWhitelistFromRaw("plugins.example.com")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))

	register := func(
		settings interfaces.PluginTenantSettingRepository, id string,
	) (*httptest.ResponseRecorder, *tenancy.Service) {
		repo, store, reg := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New()
		r := reconcile.New(reconcile.Options{Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir()})
		svc := install.NewService(repo, store, r, "0.5.0").
			WithTenantPlugins(func(context.Context) bool { return true })
		ten := tenancy.NewService(reg, settings)
		h := NewTenantPluginHandler(svc, ten)
		e := gin.New()
		e.Use(middleware.ErrorHandler())
		e.POST("/tenant-plugins", func(c *gin.Context) {
			c.Set(types.TenantIDContextKey.String(), uint64(7))
			h.InstallTenantPlugin(c)
		})

		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", "p.wkp")
		_, _ = fw.Write(plugintest.Zip(t, map[string]string{"plugin.yaml": "schemaVersion: 1\nid: " + id +
			"\nversion: 1.0.0\napiVersion: weknora.plugin/v1\nname: Own\npublisher: { id: team }\n" +
			"runtime: { type: remote }\ncontributes:\n  webSearch:\n    - { id: s, name: S }\n"}))
		_ = mw.WriteField("remote_url", "https://plugins.example.com/own")
		_ = mw.Close()
		w := call(e, http.MethodPost, "/tenant-plugins", body.String(),
			map[string]string{"Content-Type": mw.FormDataContentType()})
		return w, ten
	}

	w, ten := register(&plugintest.MemTenantSettings{}, "team.search")
	if w.Code != http.StatusOK {
		t.Fatalf("register = %d %s", w.Code, w.Body)
	}
	if on, err := ten.PluginEnabled(context.Background(), 7, "team.search"); err != nil || !on {
		t.Fatalf("the workspace's own plugin is not on: %v, %v", on, err)
	}

	w, _ = register(&switchesDown{}, "team.other")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("a switch that could not be saved = %d %s", w.Code, w.Body)
	}
	var res struct {
		Error struct {
			Details struct {
				IssuedSecret string `json:"issuedSecret"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || len(res.Error.Details.IssuedSecret) != 64 {
		t.Fatalf("the error lost the one-time secret: %s", w.Body)
	}
}
