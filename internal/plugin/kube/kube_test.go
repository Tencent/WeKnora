package kube

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/driver"
	"github.com/Tencent/WeKnora/internal/plugin/egress"
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
	// readyAfter is how many deployment reads see it not yet rolled out.
	readyAfter int
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
		obj, ok := f.applied[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		out := map[string]any{}
		for k, v := range obj {
			out[k] = v
		}
		switch {
		case strings.Contains(r.URL.Path, "/deployments/"):
			f.polls++
			available := 0
			if f.polls > f.readyAfter {
				available = 1
			}
			meta := map[string]any{"generation": 2}
			for k, v := range obj["metadata"].(map[string]any) {
				meta[k] = v
			}
			out["metadata"] = meta
			out["status"] = map[string]any{
				"observedGeneration": 2, "replicas": 1, "updatedReplicas": 1, "availableReplicas": available,
			}
		case strings.Contains(r.URL.Path, "/services/"):
			out["spec"] = map[string]any{"ports": []any{map[string]any{"nodePort": 30123}}}
		}
		_ = json.NewEncoder(w).Encode(out)
	case http.MethodDelete:
		f.deleted = append(f.deleted, r.URL.Path)
		delete(f.applied, r.URL.Path)
	}
}

func (f *fakeAPI) has(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.applied[path]
	return ok
}

type fakeEndpoints struct {
	mu          sync.Mutex
	url, secret string
	removed     []string
}

func (f *fakeEndpoints) ServeDeployed(_ context.Context, _ *manifest.Manifest, url, secret string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.url, f.secret = url, secret
	return nil
}

func (f *fakeEndpoints) Deactivate(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return nil
}

func newDriver(t *testing.T, api http.Handler) (*Driver, *fakeEndpoints) {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	cfg := &Config{APIURL: srv.URL, Token: "tok", Namespace: "plugins", ServiceType: "NodePort", NodeHost: "10.0.0.5"}
	ep := &fakeEndpoints{}
	d, err := New(cfg, ep)
	if err != nil {
		t.Fatal(err)
	}
	return d, ep
}

func loaded(t *testing.T, id string) *reconcile.Loaded {
	t.Helper()
	sealed, err := utils.EncryptAESGCM("sekrit", utils.GetAESKey())
	if err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		ID: id, Version: "1.2.0",
		Runtime: manifest.Runtime{
			Type: manifest.RuntimeKubernetes, Image: "ghcr.io/acme/search:1.2.0", Port: 9000,
			Resources: &manifest.Resources{CPU: "500m", Memory: "256Mi"},
		},
	}
	return &reconcile.Loaded{Manifest: m, Installed: types.InstalledPlugin{ID: m.ID, RemoteSecret: sealed}}
}

func TestDriverDeploysAndRemoves(t *testing.T) {
	api := &fakeAPI{applied: map[string]map[string]any{}}
	d, ep := newDriver(t, api)
	if err := d.Activate(context.Background(), loaded(t, "acme.search")); err != nil {
		t.Fatal(err)
	}
	if ep.url != "http://10.0.0.5:30123" || ep.secret != "sekrit" {
		t.Fatalf("served at %q with %q", ep.url, ep.secret)
	}
	name := ResourceName("acme.search")
	dep := api.applied["/apis/apps/v1/namespaces/plugins/deployments/"+name]
	if dep == nil {
		t.Fatalf("no deployment applied: %v", api.applied)
	}
	b, _ := json.Marshal(dep)
	for _, want := range []string{
		`"image":"ghcr.io/acme/search:1.2.0"`, `"value":":9000"`,
		`"secretKeyRef":{"key":"secret","name":"` + name + `"}`,
		`"memory":"256Mi"`, `"automountServiceAccountToken":false`, `"weknora.plugin/version":"1.2.0"`,
		`"terminationGracePeriodSeconds":75`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("deployment lacks %s: %s", want, b)
		}
	}
	sec := api.applied["/api/v1/namespaces/plugins/secrets/"+name]
	if sec["stringData"].(map[string]any)["secret"] != "sekrit" {
		t.Fatalf("secret = %v", sec)
	}
	svc := api.applied["/api/v1/namespaces/plugins/services/"+name]
	if svc["spec"].(map[string]any)["type"] != "NodePort" {
		t.Fatalf("service = %v", svc)
	}

	if err := d.Deactivate(context.Background(), "acme.search"); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if len(api.deleted) != 3 || len(api.applied) != 0 || len(ep.removed) != 1 {
		t.Fatalf("deleted %v, left %v, unregistered %v", api.deleted, api.applied, ep.removed)
	}
}

// Deactivating a plugin the driver did not deploy (a host plugin, one
// whose name collides) touches neither the cluster nor the endpoints.
func TestDeactivateLeavesOtherPluginsAlone(t *testing.T) {
	api := &fakeAPI{applied: map[string]map[string]any{}}
	d, ep := newDriver(t, api)
	if err := d.Activate(context.Background(), loaded(t, "acme.foo-bar")); err != nil {
		t.Fatal(err)
	}
	if err := d.Deactivate(context.Background(), "acme.host"); err != nil {
		t.Fatal(err)
	}
	if len(api.deleted) != 0 || len(ep.removed) != 0 {
		t.Fatalf("deactivating another plugin deleted %v, unregistered %v", api.deleted, ep.removed)
	}

	// Resources under a colliding name that belong to another plugin stay.
	path := "/apis/apps/v1/namespaces/plugins/deployments/" + ResourceName("acme.foo-bar")
	if err := d.deleteResources(context.Background(), "acme-foo.bar", ResourceName("acme.foo-bar")); err != nil {
		t.Fatal(err)
	}
	if !api.has(path) {
		t.Fatal("another plugin's deployment was deleted")
	}
}

// Resources an earlier version created under the old name are removed
// once the plugin runs under the new one.
func TestLegacyResourcesAreRemoved(t *testing.T) {
	api := &fakeAPI{applied: map[string]map[string]any{}}
	d, _ := newDriver(t, api)
	legacy := legacyResourceName("acme.search")
	l := loaded(t, "acme.search")
	for _, obj := range Resources(d.cfg, l.Manifest, legacy, "old") {
		api.applied[obj.Path] = obj.Body
	}
	// And a resource of another plugin that happens to have the same old name.
	other := "/api/v1/namespaces/plugins/secrets/" + legacyResourceName("acme-search.x")
	api.applied[other] = map[string]any{"metadata": map[string]any{
		"annotations": map[string]any{"weknora.plugin/id": "acme-search.x"},
	}}
	if err := d.Activate(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if api.has("/apis/apps/v1/namespaces/plugins/deployments/"+legacy) ||
		api.has("/api/v1/namespaces/plugins/secrets/"+legacy) {
		t.Fatalf("legacy resources survived: %v", api.applied)
	}
	if !api.has(other) {
		t.Fatal("another plugin's resource was removed")
	}
}

// A rollout that is not done at once finishes in the background: Activate
// returns at once with a pending error and the plugin reports ready later.
func TestRolloutFinishesInTheBackground(t *testing.T) {
	api := &fakeAPI{applied: map[string]map[string]any{}, readyAfter: 2}
	d, ep := newDriver(t, api)
	l := loaded(t, "acme.search")
	reports := make(chan error, 4)
	l.Report = func(healthy bool, err error) {
		if healthy {
			err = nil
		} else if err == nil {
			err = errors.New("unhealthy")
		}
		reports <- err
	}
	err := d.Activate(context.Background(), l)
	var pending *reconcile.PendingError
	if !errors.As(err, &pending) {
		t.Fatalf("Activate = %v, want pending", err)
	}
	select {
	case err := <-reports:
		if err != nil {
			t.Fatalf("report = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the rollout never reported")
	}
	ep.mu.Lock()
	defer ep.mu.Unlock()
	if ep.url == "" {
		t.Fatal("ready but not served")
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
	ep := &fakeEndpoints{}
	d, _ := New(&Config{APIURL: srv.URL, Token: "t", Namespace: "p", ServiceType: "ClusterIP"}, ep)
	d.timeout, d.stuck = 100*time.Millisecond, time.Hour
	sealed, _ := utils.EncryptAESGCM("s", utils.GetAESKey())
	m := &manifest.Manifest{ID: "acme.x", Runtime: manifest.Runtime{Type: manifest.RuntimeKubernetes, Image: "x"}}
	reports := make(chan error, 4)
	l := &reconcile.Loaded{
		Manifest: m, Installed: types.InstalledPlugin{RemoteSecret: sealed},
		Report: func(_ bool, err error) { reports <- err },
	}
	if err := d.Activate(context.Background(), l); err == nil {
		t.Fatal("a rollout still going must be pending")
	}
	select {
	case err := <-reports:
		if err == nil || !strings.Contains(err.Error(), "ImagePullBackOff") {
			t.Fatalf("stuck rollout: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stuck rollout was never reported")
	}
	// Deactivating stops the background wait.
	if err := d.Deactivate(context.Background(), "acme.x"); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.rollouts) != 0 {
		t.Fatal("the rollout was not stopped")
	}
}

func TestResourceName(t *testing.T) {
	long := "acme.very-long-plugin-name-that-goes-on-and-on-and-on-forever-and-ever"
	if got := ResourceName(long); len(got) > 63 || !strings.HasPrefix(got, "wkp-acme-very-long") {
		t.Fatalf("name = %q", got)
	}
	if ResourceName(long) == ResourceName(long+"x") {
		t.Fatal("IDs sharing a long prefix share a name")
	}
	if ResourceName("acme.foo-bar") == ResourceName("acme-foo.bar") {
		t.Fatal("IDs that read alike share a name")
	}
	if !dnsLabel.MatchString(ResourceName("a.b")) {
		t.Fatalf("name = %q", ResourceName("a.b"))
	}
}

var dnsLabel = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

func confinedConfig() *Config {
	return &Config{
		Namespace: "plugins", ServiceType: "ClusterIP",
		HostAPIURL: "http://app.weknora.svc:80", EgressPort: 8090, EgressKey: []byte("key"),
		NetworkPolicy: &NetworkPolicy{
			AppNamespace: "weknora", AppLabels: map[string]string{"app.kubernetes.io/component": "app"},
			AppPort:            8080,
			DNSNamespaceLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"},
			DNSPodLabels:       map[string]string{"k8s-app": "kube-dns"},
		},
	}
}

func objectKinds(objs []Object) string {
	var kinds []string
	for _, o := range objs {
		kinds = append(kinds, o.Body["kind"].(string))
	}
	return strings.Join(kinds, ",")
}

// With the network policy on, a plugin's pods reach only DNS and the app
// pods, and are handed the egress proxy with their own credentials, which
// stay in the Secret.
func TestResourcesConfineEgress(t *testing.T) {
	cfg := confinedConfig()
	m := loaded(t, "acme.search").Manifest
	name := ResourceName(m.ID)
	objs := Resources(cfg, m, name, "sekrit")
	// The policy is in place before any pod starts.
	if got := objectKinds(objs); got != "Secret,NetworkPolicy,Deployment,Service" {
		t.Fatalf("objects = %s", got)
	}
	np := objs[1]
	if np.Path != "/apis/networking.k8s.io/v1/namespaces/plugins/networkpolicies/"+name {
		t.Fatalf("policy path = %s", np.Path)
	}
	b, _ := json.Marshal(np.Body["spec"])
	app := `{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"weknora"}},` +
		`"podSelector":{"matchLabels":{"app.kubernetes.io/component":"app"}}}`
	want := `{"egress":[` +
		`{"ports":[{"port":53,"protocol":"UDP"},{"port":53,"protocol":"TCP"}],"to":[` +
		`{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"kube-system"}},` +
		`"podSelector":{"matchLabels":{"k8s-app":"kube-dns"}}}]},` +
		`{"ports":[{"port":8080,"protocol":"TCP"},{"port":8090,"protocol":"TCP"}],"to":[` + app + `]}],` +
		`"ingress":[{"from":[` + app + `],"ports":[{"port":9000,"protocol":"TCP"}]}],` +
		`"podSelector":{"matchLabels":{"app.kubernetes.io/name":"` + name + `"}},` +
		`"policyTypes":["Ingress","Egress"]}`
	if string(b) != want {
		t.Fatalf("policy spec =\n%s\nwant\n%s", b, want)
	}
	if np.Body["metadata"].(map[string]any)["annotations"].(map[string]any)["weknora.plugin/id"] != m.ID {
		t.Fatalf("policy metadata = %v", np.Body["metadata"])
	}

	proxy := "http://acme.search:" + egress.Password(cfg.EgressKey, m.ID) + "@app.weknora.svc:8090"
	if got := objs[0].Body["stringData"].(map[string]any)["egress-proxy"]; got != proxy {
		t.Fatalf("secret egress-proxy = %v, want %s", got, proxy)
	}
	dep, _ := json.Marshal(objs[2].Body)
	if strings.Contains(string(dep), egress.Password(cfg.EgressKey, m.ID)) {
		t.Fatal("the proxy password is inline in the Deployment")
	}
	for _, v := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		ref := `{"name":"` + v + `","valueFrom":{"secretKeyRef":{"key":"egress-proxy","name":"` + name + `"}}}`
		if !strings.Contains(string(dep), ref) {
			t.Fatalf("deployment lacks %s: %s", ref, dep)
		}
	}
	for _, want := range []string{
		`{"name":"NO_PROXY","value":"app.weknora.svc,localhost,127.0.0.1"}`, `"weknora.plugin/egress":"`,
	} {
		if !strings.Contains(string(dep), want) {
			t.Fatalf("deployment lacks %s: %s", want, dep)
		}
	}
}

// Without egress settings a plugin runs as before: no policy, no proxy.
func TestResourcesWithoutEgressControl(t *testing.T) {
	cfg := &Config{Namespace: "plugins", ServiceType: "ClusterIP", HostAPIURL: "http://app.weknora.svc"}
	objs := Resources(cfg, loaded(t, "acme.search").Manifest, "n", "s")
	if got := objectKinds(objs); got != "Secret,Deployment,Service" {
		t.Fatalf("objects = %s", got)
	}
	b, _ := json.Marshal(objs)
	if strings.Contains(string(b), "PROXY") || strings.Contains(string(b), "egress") {
		t.Fatalf("proxy settings without an egress proxy: %s", b)
	}
	if cfg.Egress() != driver.EgressUnmanaged {
		t.Fatalf("egress = %s", cfg.Egress())
	}
	cfg.EgressPort, cfg.EgressKey = 8090, []byte("k")
	objs = Resources(cfg, loaded(t, "acme.search").Manifest, "n", "s")
	if got := objectKinds(objs); got != "Secret,Deployment,Service" || cfg.Egress() != driver.EgressProxy {
		t.Fatalf("proxy only: objects = %s, egress = %s", got, cfg.Egress())
	}
}

func TestDeactivateDeletesTheNetworkPolicy(t *testing.T) {
	api := &fakeAPI{applied: map[string]map[string]any{}}
	srv := httptest.NewServer(api)
	defer srv.Close()
	cfg := confinedConfig()
	cfg.APIURL, cfg.Token = srv.URL, "tok"
	d, err := New(cfg, &fakeEndpoints{})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Activate(context.Background(), loaded(t, "acme.search")); err != nil {
		t.Fatal(err)
	}
	policy := networkPolicyPath("plugins", ResourceName("acme.search"))
	if !api.has(policy) {
		t.Fatalf("no network policy applied: %v", api.applied)
	}
	if err := d.Deactivate(context.Background(), "acme.search"); err != nil {
		t.Fatal(err)
	}
	if len(api.deleted) != 4 || api.deleted[3] != policy || len(api.applied) != 0 {
		t.Fatalf("deleted %v (the policy last), left %v", api.deleted, api.applied)
	}
}

// Turning the policy off removes the one an earlier activation left, which
// would cut the pods off now that they get no proxy; a platform that grants
// no access to network policies is fine.
func TestDisabledPolicyIsRemoved(t *testing.T) {
	api := &fakeAPI{applied: map[string]map[string]any{}}
	d, _ := newDriver(t, api)
	l := loaded(t, "acme.search")
	stale := networkPolicyPath("plugins", ResourceName("acme.search"))
	api.applied[stale] = map[string]any{"metadata": map[string]any{
		"annotations": map[string]any{"weknora.plugin/id": "acme.search"},
	}}
	if err := d.Activate(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if api.has(stale) {
		t.Fatal("the stale network policy survived")
	}

	forbidden := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/networkpolicies/") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		api.ServeHTTP(w, r)
	})
	d, _ = newDriver(t, forbidden)
	if err := d.Activate(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if err := d.Deactivate(context.Background(), "acme.search"); err != nil {
		t.Fatalf("deactivate without access to network policies: %v", err)
	}
}

type fakeStatus struct{ driver.Driver }

func (fakeStatus) Status(context.Context, string) ([]driver.InstanceStatus, error) {
	return []driver.InstanceStatus{{Node: "a"}, {Node: "b", Egress: driver.EgressUnmanaged}}, nil
}

func TestStatusesReportTheEgressMode(t *testing.T) {
	d, err := New(confinedConfig(), &fakeEndpoints{})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := d.Statuses(fakeStatus{}).Status(context.Background(), "acme.search")
	for _, s := range out {
		if s.Egress != driver.EgressNetworkPolicy {
			t.Fatalf("%s: egress = %q", s.Node, s.Egress)
		}
	}
}

func TestEgressConfigFromEnv(t *testing.T) {
	base := map[string]string{
		"WEKNORA_PLUGIN_K8S_NAMESPACE": "plugins", "WEKNORA_PLUGIN_K8S_API": "https://k8s",
		"WEKNORA_PLUGIN_K8S_TOKEN":    "t",
		"WEKNORA_PLUGIN_HOST_API_URL": "http://app.weknora.svc:80",
	}
	for name, tc := range map[string]struct {
		env     map[string]string
		wantErr string
		check   func(*Config) bool
	}{
		"off": {check: func(c *Config) bool { return c.EgressPort == 0 && c.NetworkPolicy == nil }},
		"proxy only": {
			env:   map[string]string{"WEKNORA_PLUGIN_K8S_EGRESS_PORT": "8090"},
			check: func(c *Config) bool { return c.EgressPort == 8090 && c.NetworkPolicy == nil },
		},
		"bad port": {env: map[string]string{"WEKNORA_PLUGIN_K8S_EGRESS_PORT": "x"}, wantErr: "must be a port"},
		"no host api": {
			env:     map[string]string{"WEKNORA_PLUGIN_K8S_EGRESS_PORT": "8090", "WEKNORA_PLUGIN_HOST_API_URL": ""},
			wantErr: "HOST_API_URL",
		},
		"policy without proxy": {
			env: map[string]string{"WEKNORA_PLUGIN_K8S_NETWORK_POLICY": "1"}, wantErr: "EGRESS_PORT",
		},
		"policy without app labels": {
			env: map[string]string{
				"WEKNORA_PLUGIN_K8S_NETWORK_POLICY": "1", "WEKNORA_PLUGIN_K8S_EGRESS_PORT": "8090",
			},
			wantErr: "APP_LABELS",
		},
		"policy with node ports": {
			env: map[string]string{
				"WEKNORA_PLUGIN_K8S_NETWORK_POLICY": "1", "WEKNORA_PLUGIN_K8S_EGRESS_PORT": "8090",
				"WEKNORA_PLUGIN_K8S_SERVICE_TYPE": "NodePort", "WEKNORA_PLUGIN_K8S_NODE_HOST": "10.0.0.1",
			},
			wantErr: "ClusterIP",
		},
		"policy": {
			env: map[string]string{
				"WEKNORA_PLUGIN_K8S_NETWORK_POLICY": "true", "WEKNORA_PLUGIN_K8S_EGRESS_PORT": "8090",
				"WEKNORA_PLUGIN_K8S_APP_LABELS":    "app.kubernetes.io/instance=wk, app.kubernetes.io/component=app",
				"WEKNORA_PLUGIN_K8S_APP_NAMESPACE": "weknora", "WEKNORA_PLUGIN_K8S_DNS_POD_LABELS": "-",
			},
			check: func(c *Config) bool {
				np := c.NetworkPolicy
				return np != nil && np.AppNamespace == "weknora" && len(np.AppLabels) == 2 &&
					np.AppLabels["app.kubernetes.io/component"] == "app" && len(np.DNSPodLabels) == 0 &&
					np.DNSNamespaceLabels["kubernetes.io/metadata.name"] == "kube-system"
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range base {
				t.Setenv(k, v)
			}
			for _, k := range []string{
				"WEKNORA_PLUGIN_K8S_EGRESS_PORT", "WEKNORA_PLUGIN_K8S_NETWORK_POLICY", "WEKNORA_PLUGIN_K8S_APP_LABELS",
				"WEKNORA_PLUGIN_K8S_APP_NAMESPACE", "WEKNORA_PLUGIN_K8S_DNS_POD_LABELS",
				"WEKNORA_PLUGIN_K8S_DNS_NAMESPACE_LABELS", "WEKNORA_PLUGIN_K8S_SERVICE_TYPE",
			} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := ConfigFromEnv()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %s", err, tc.wantErr)
				}
				return
			}
			if err != nil || !tc.check(cfg) {
				t.Fatalf("cfg = %+v, err = %v", cfg, err)
			}
		})
	}
}
