package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/plugin/driver"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/market"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
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
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })

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

type fakeAudienceTenants struct{ all []*types.Tenant }

func (f fakeAudienceTenants) SearchTenants(
	_ context.Context, keyword string, tenantID uint64, _, _ int,
) ([]*types.Tenant, int64, error) {
	var out []*types.Tenant
	for _, t := range f.all {
		if (tenantID == 0 || t.ID == tenantID) && strings.Contains(t.Name, keyword) {
			out = append(out, t)
		}
	}
	return out, int64(len(out)), nil
}

func (f fakeAudienceTenants) GetTenantsByIDs(_ context.Context, ids []uint64) (map[uint64]*types.Tenant, error) {
	out := map[uint64]*types.Tenant{}
	for _, t := range f.all {
		for _, id := range ids {
			if t.ID == id {
				out[id] = t
			}
		}
	}
	return out, nil
}

func TestPluginAudienceTenants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewPluginAdminHandler(nil).WithTenants(fakeAudienceTenants{all: []*types.Tenant{
		{ID: 7, Name: "Sales", Description: "secret"}, {ID: 8, Name: "Support"},
	}})
	e := gin.New()
	e.Use(middleware.ErrorHandler())
	e.GET("/tenants", h.ListPluginAudienceTenants)
	get := func(q string) string {
		w := call(e, http.MethodGet, "/tenants"+q, "", nil)
		return w.Body.String()
	}
	if got := get("?keyword=Sup"); !strings.Contains(got, `"name":"Support"`) || strings.Contains(got, "Sales") {
		t.Fatalf("keyword = %s", got)
	}
	if got := get("?keyword=7"); !strings.Contains(got, `"id":7`) || strings.Contains(got, `"id":8`) {
		t.Fatalf("by id = %s", got)
	}
	if got := get("?ids=8,7,x"); got != `{"data":[{"id":8,"name":"Support"},{"id":7,"name":"Sales"}],"success":true}` {
		t.Fatalf("ids = %s", got)
	}
	if got := get(""); strings.Contains(got, "secret") {
		t.Fatalf("leaks tenant fields: %s", got)
	}
}

// A system admin sees where a plugin runs whatever workspace they are in:
// the state comes from the installed plugin, not the workspace's catalog.
func TestPluginAdminInstances(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, store := plugintest.NewMemRepo(), &plugintest.MemStore{}
	r := reconcile.New(reconcile.Options{Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir()})
	svc := install.NewService(repo, store, r, "0.5.0")
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	engine := func(h *PluginAdminHandler) *gin.Engine {
		e := gin.New()
		e.Use(middleware.ErrorHandler())
		e.GET("/plugins/:id/instances", h.GetPluginInstances)
		return e
	}
	var res struct {
		Data PluginInstancesDTO `json:"data"`
	}
	e := engine(NewPluginAdminHandler(svc).WithDrivers(driver.NewSet(r.Driver(manifest.RuntimeDeclarative))))
	w := call(e, http.MethodGet, "/plugins/acme.kit/instances", "", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != http.StatusOK ||
		len(res.Data.Instances) != 1 || res.Data.Instances[0].Version != "1.0.0" ||
		res.Data.Instances[0].State != driver.StateReady {
		t.Fatalf("instances = %d %s", w.Code, w.Body.String())
	}
	if w := call(e, http.MethodGet, "/plugins/acme.missing/instances", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("missing plugin = %d %s", w.Code, w.Body.String())
	}

	// A runtime without a driver says why it lists nothing.
	w = call(engine(NewPluginAdminHandler(svc).WithDrivers(driver.NewSet())), http.MethodGet,
		"/plugins/acme.kit/instances", "", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != http.StatusOK ||
		len(res.Data.Instances) != 0 || res.Data.InstanceError == "" {
		t.Fatalf("no driver = %d %s", w.Code, w.Body.String())
	}
}

// statusDriver reports fixed instances for one runtime.
type statusDriver struct {
	rt        manifest.RuntimeType
	instances []driver.InstanceStatus
}

func (d statusDriver) Type() manifest.RuntimeType                     { return d.rt }
func (statusDriver) Ensure(context.Context, *manifest.Manifest) error { return nil }
func (statusDriver) Remove(context.Context, string, string) error     { return nil }
func (statusDriver) Resolve(context.Context, string, uint64) (driver.Endpoint, error) {
	return driver.Endpoint{}, nil
}

func (d statusDriver) Status(context.Context, string) ([]driver.InstanceStatus, error) {
	return d.instances, nil
}

// The admin list flags a plugin by its least controlled instance: one
// plugin host without the sandbox is enough for the grant not to hold.
func TestPluginAdminListsEgress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo, store := plugintest.NewMemRepo(), &plugintest.MemStore{}
	r := reconcile.New(reconcile.Options{Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir()})
	svc := install.NewService(repo, store, r, "0.5.0")
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	row, _ := repo.GetPlugin(t.Context(), "acme.kit")
	row.Runtime = string(manifest.RuntimeHost)
	_ = repo.SavePlugin(t.Context(), row)

	h := NewPluginAdminHandler(svc).WithDrivers(driver.NewSet(statusDriver{
		rt: manifest.RuntimeHost, instances: []driver.InstanceStatus{
			{Node: "app/1"},
			{Node: "plugin-host:a/1", Egress: driver.EgressSandboxed},
			{Node: "plugin-host:b/1", Egress: driver.EgressProxy},
		},
	}))
	e := gin.New()
	e.Use(middleware.ErrorHandler())
	e.GET("/plugins", h.ListInstalledPlugins)
	e.GET("/plugins/:id", h.GetInstalledPlugin)
	var list struct {
		Data []struct {
			ID     string            `json:"id"`
			Egress driver.EgressMode `json:"egress"`
		} `json:"data"`
	}
	w := call(e, http.MethodGet, "/plugins", "", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Data) != 1 ||
		list.Data[0].ID != "acme.kit" || list.Data[0].Egress != driver.EgressProxy {
		t.Fatalf("list = %s", w.Body.String())
	}
	var one struct {
		Data struct {
			ActiveVersion string            `json:"active_version"`
			Egress        driver.EgressMode `json:"egress"`
		} `json:"data"`
	}
	w = call(e, http.MethodGet, "/plugins/acme.kit", "", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &one); err != nil || one.Data.ActiveVersion != "1.0.0" ||
		one.Data.Egress != driver.EgressProxy {
		t.Fatalf("get = %s", w.Body.String())
	}
}

func TestWeakestEgress(t *testing.T) {
	for _, tc := range []struct {
		modes []driver.EgressMode
		want  driver.EgressMode
	}{
		{nil, ""},
		{[]driver.EgressMode{"", ""}, ""},
		{[]driver.EgressMode{driver.EgressSandboxed, ""}, driver.EgressSandboxed},
		{[]driver.EgressMode{driver.EgressNetworkPolicy, driver.EgressSandboxed}, driver.EgressNetworkPolicy},
		{[]driver.EgressMode{driver.EgressSandboxed, driver.EgressProxy}, driver.EgressProxy},
		{[]driver.EgressMode{driver.EgressUnmanaged, driver.EgressProxy}, driver.EgressUnmanaged},
	} {
		var instances []driver.InstanceStatus
		for _, m := range tc.modes {
			instances = append(instances, driver.InstanceStatus{Egress: m})
		}
		if got := weakestEgress(instances); got != tc.want {
			t.Errorf("weakestEgress(%v) = %q, want %q", tc.modes, got, tc.want)
		}
	}
}
