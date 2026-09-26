package kube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

type fakeAPI struct {
	mu      sync.Mutex
	applied map[string]map[string]any
	deleted []string
	polls   int
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		if r.Header.Get("Content-Type") != "application/apply-patch+yaml" ||
			r.URL.Query().Get("fieldManager") != "weknora" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		f.applied[r.URL.Path] = body
		_, _ = w.Write(b)
	case http.MethodGet:
		switch {
		case strings.Contains(r.URL.Path, "/deployments/"):
			f.polls++
			available := 0
			if f.polls > 1 {
				available = 1
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"metadata": map[string]any{"generation": 2},
				"status": map[string]any{
					"observedGeneration": 2, "replicas": 1, "updatedReplicas": 1, "availableReplicas": available,
				},
			})
		case strings.Contains(r.URL.Path, "/services/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"spec": map[string]any{"ports": []any{map[string]any{"nodePort": 30123}}},
			})
		}
	case http.MethodDelete:
		f.deleted = append(f.deleted, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

type fakeEndpoints struct {
	url, secret string
	removed     []string
}

func (f *fakeEndpoints) ServeDeployed(_ context.Context, _ *manifest.Manifest, url, secret string) error {
	f.url, f.secret = url, secret
	return nil
}

func (f *fakeEndpoints) Deactivate(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
}

func TestDriverDeploysAndRemoves(t *testing.T) {
	api := &fakeAPI{applied: map[string]map[string]any{}}
	srv := httptest.NewServer(api)
	defer srv.Close()
	cfg := &Config{APIURL: srv.URL, Token: "tok", Namespace: "plugins", ServiceType: "NodePort", NodeHost: "10.0.0.5"}
	ep := &fakeEndpoints{}
	d, err := New(cfg, ep)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := utils.EncryptAESGCM("sekrit", utils.GetAESKey())
	if err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		ID: "acme.search", Version: "1.2.0",
		Runtime: manifest.Runtime{
			Type: manifest.RuntimeKubernetes, Image: "ghcr.io/acme/search:1.2.0", Port: 9000,
			Resources: &manifest.Resources{CPU: "500m", Memory: "256Mi"},
		},
	}
	l := &reconcile.Loaded{Manifest: m, Installed: types.InstalledPlugin{ID: m.ID, RemoteSecret: sealed}}
	if err := d.Activate(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if ep.url != "http://10.0.0.5:30123" || ep.secret != "sekrit" {
		t.Fatalf("served at %q with %q", ep.url, ep.secret)
	}
	dep := api.applied["/apis/apps/v1/namespaces/plugins/deployments/wkp-acme-search"]
	if dep == nil {
		t.Fatalf("no deployment applied: %v", api.applied)
	}
	b, _ := json.Marshal(dep)
	for _, want := range []string{
		`"image":"ghcr.io/acme/search:1.2.0"`, `"value":":9000"`,
		`"secretKeyRef":{"key":"secret","name":"wkp-acme-search"}`,
		`"memory":"256Mi"`, `"automountServiceAccountToken":false`, `"weknora.plugin/version":"1.2.0"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("deployment lacks %s: %s", want, b)
		}
	}
	sec := api.applied["/api/v1/namespaces/plugins/secrets/wkp-acme-search"]
	if sec["stringData"].(map[string]any)["secret"] != "sekrit" {
		t.Fatalf("secret = %v", sec)
	}
	svc := api.applied["/api/v1/namespaces/plugins/services/wkp-acme-search"]
	if svc["spec"].(map[string]any)["type"] != "NodePort" {
		t.Fatalf("service = %v", svc)
	}

	if err := d.Deactivate(context.Background(), "acme.search"); err != nil {
		t.Fatalf("deactivate (resources already gone): %v", err)
	}
	if len(api.deleted) != 3 || len(ep.removed) != 1 {
		t.Fatalf("deleted %v, unregistered %v", api.deleted, ep.removed)
	}
}

func TestDriverReportsAStuckRollout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"metadata": map[string]any{"generation": 1},
				"status": map[string]any{"observedGeneration": 1, "conditions": []any{map[string]any{
					"type": "Available", "status": "False", "message": "ImagePullBackOff: image not found",
				}}},
			})
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	d, _ := New(&Config{APIURL: srv.URL, Token: "t", Namespace: "p", ServiceType: "ClusterIP"}, &fakeEndpoints{})
	d.timeout = 3 * time.Second
	sealed, _ := utils.EncryptAESGCM("s", utils.GetAESKey())
	m := &manifest.Manifest{ID: "acme.x", Runtime: manifest.Runtime{Type: manifest.RuntimeKubernetes, Image: "x"}}
	l := &reconcile.Loaded{Manifest: m, Installed: types.InstalledPlugin{RemoteSecret: sealed}}
	err := d.Activate(context.Background(), l)
	if err == nil || !strings.Contains(err.Error(), "ImagePullBackOff") {
		t.Fatalf("stuck rollout: %v", err)
	}
}

func TestResourceName(t *testing.T) {
	if got := ResourceName("Acme.Very_Long.plugin-name.that.goes.on.and.on.and.on.forever"); len(got) > 50 ||
		!strings.HasPrefix(got, "wkp-acme-very-long") || strings.HasSuffix(got, "-") {
		t.Fatalf("name = %q", got)
	}
}
