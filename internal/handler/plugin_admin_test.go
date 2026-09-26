package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/market"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestPluginAdminMarket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, store := plugintest.NewMemRepo(), &plugintest.MemStore{}
	r := reconcile.New(reconcile.Options{Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir()})
	svc := install.NewService(repo, store, r, "0.5.0")
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)

	kit := plugintest.KitPackage(t, "1.1.0")
	var digest string
	if p, err := svc.Inspect(t.Context(), kit); err == nil {
		digest = p.Digest
	} else {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/kit.wkp" {
			_, _ = w.Write(kit)
			return
		}
		_, _ = w.Write([]byte(`{"schemaVersion":1,"plugins":[{"id":"acme.kit","versions":[` +
			`{"version":"1.1.0","url":"kit.wkp","digest":"` + digest + `"}]}]}`))
	}))
	defer srv.Close()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	engine := func(h *PluginAdminHandler) *gin.Engine {
		e := gin.New()
		e.Use(middleware.ErrorHandler())
		e.GET("/market", h.ListMarketPlugins)
		e.POST("/inspect", h.InspectPlugin)
		return e
	}
	var res struct {
		Data struct {
			Configured bool                  `json:"configured"`
			Plugins    []MarketPluginListing `json:"plugins"`
		} `json:"data"`
	}
	w := call(engine(NewPluginAdminHandler(svc)), http.MethodGet, "/market", "", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Data.Configured {
		t.Fatalf("unconfigured market = %s", w.Body.String())
	}

	e := engine(NewPluginAdminHandler(svc).WithMarket(market.New(srv.URL+"/index.json", "0.5.0", srv.Client())))
	w = call(e, http.MethodGet, "/market", "", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || !res.Data.Configured || len(res.Data.Plugins) != 1 ||
		res.Data.Plugins[0].InstalledVersion != "1.0.0" || res.Data.Plugins[0].Latest.Version != "1.1.0" {
		t.Fatalf("market = %s", w.Body.String())
	}

	hdr := map[string]string{"Content-Type": "application/json"}
	pkgURL := res.Data.Plugins[0].Latest.URL
	if w := call(e, http.MethodPost, "/inspect", `{"url":"`+pkgURL+`","digest":"`+digest+`"}`, hdr); w.Code != 200 {
		t.Fatalf("inspect with the listed digest = %d %s", w.Code, w.Body.String())
	}
	other := "sha256:" + strings.Repeat("0", 64)
	if w := call(e, http.MethodPost, "/inspect", `{"url":"`+pkgURL+`","digest":"`+other+`"}`, hdr); w.Code != 400 {
		t.Fatalf("inspect with another digest = %d %s", w.Code, w.Body.String())
	}
}
