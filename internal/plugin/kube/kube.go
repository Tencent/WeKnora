// Package kube runs plugins of runtime.type kubernetes: WeKnora deploys the
// package's container image as a Deployment with a Service in its own
// namespace, then reaches it like a remote plugin. The platform, not an
// administrator, owns the service: it holds the signing secret, rolls out
// new versions and removes the resources when the plugin goes.
//
// It talks to the Kubernetes API directly (server-side apply) with the pod's
// service account, or with WEKNORA_PLUGIN_K8S_* settings outside a cluster.
package kube

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/driver"
	"github.com/Tencent/WeKnora/internal/plugin/egress"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	// DefaultPort is where a plugin image serves unless runtime.port says.
	DefaultPort = 8080
	// fieldManager owns the applied fields.
	fieldManager = "weknora"
	readyTimeout = 5 * time.Minute
	pollInterval = 2 * time.Second
	// stuckInterval is how often a rollout that overran readyTimeout is
	// checked again: a fixed pull secret or a new node may still let it
	// finish.
	stuckInterval  = 30 * time.Second
	requestTimeout = 30 * time.Second
	serviceAcct    = "/var/run/secrets/kubernetes.io/serviceaccount"
)

// Config is how to reach the cluster and where plugins go.
type Config struct {
	// APIURL is the API server ("https://10.0.0.1:443").
	APIURL string
	Token  string
	// CA verifies the API server; empty uses the system roots.
	CA []byte
	// Insecure skips verifying the API server (local clusters only).
	Insecure  bool
	Namespace string
	// ServiceType is ClusterIP (WeKnora runs in the cluster) or NodePort
	// (it runs outside and reaches NodeHost:<node port>).
	ServiceType string
	NodeHost    string
	// ImagePullSecret, when set, is used to pull plugin images.
	ImagePullSecret string
	// HostAPIURL is where plugins reach the Host API
	// (WEKNORA_PLUGIN_HOST_API_URL); pods reach it without the egress proxy.
	HostAPIURL string
	// EgressPort, when set, is the port app nodes serve the cluster egress
	// proxy on (egress.ClusterProxy): plugin pods get it as HTTP(S)_PROXY, at
	// the Host API's host, with their own credentials.
	EgressPort int
	// EgressKey derives each plugin's proxy password (egress.ProxyKey).
	EgressKey []byte
	// NetworkPolicy, when set, confines each plugin's pods with a
	// NetworkPolicy to DNS and the app pods, so the egress proxy is their
	// only way out.
	NetworkPolicy *NetworkPolicy
}

// NetworkPolicy is what a plugin's NetworkPolicy lets its pods reach.
type NetworkPolicy struct {
	// AppNamespace and AppLabels select WeKnora's app pods: the Host API and
	// the egress proxy, and the only callers of a plugin.
	AppNamespace string
	AppLabels    map[string]string
	// AppPort is the port the app pods serve HTTP on (the Host API): a
	// NetworkPolicy matches the pod's port, not the Service's.
	AppPort int
	// DNSNamespaceLabels and DNSPodLabels select the cluster DNS pods.
	DNSNamespaceLabels map[string]string
	DNSPodLabels       map[string]string
}

// ConfigFromEnv reads the configuration: WEKNORA_PLUGIN_K8S_NAMESPACE turns
// the driver on. Inside a pod the service account supplies the API server
// and credentials; WEKNORA_PLUGIN_K8S_API, _TOKEN and _CA override them.
func ConfigFromEnv() (*Config, error) {
	ns := strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_K8S_NAMESPACE"))
	if ns == "" {
		return nil, nil
	}
	cfg := &Config{
		Namespace:       ns,
		APIURL:          strings.TrimSuffix(strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_K8S_API")), "/"),
		Token:           strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_K8S_TOKEN")),
		ServiceType:     strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_K8S_SERVICE_TYPE")),
		NodeHost:        strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_K8S_NODE_HOST")),
		ImagePullSecret: strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_K8S_IMAGE_PULL_SECRET")),
		Insecure:        os.Getenv("WEKNORA_PLUGIN_K8S_INSECURE") == "1",
		HostAPIURL:      strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_HOST_API_URL")),
	}
	if cfg.APIURL == "" {
		host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
		if host == "" {
			return nil, errors.New("WEKNORA_PLUGIN_K8S_NAMESPACE is set but WeKnora is not in a pod; " +
				"set WEKNORA_PLUGIN_K8S_API and WEKNORA_PLUGIN_K8S_TOKEN")
		}
		cfg.APIURL = "https://" + host + ":" + port
	}
	if cfg.Token == "" {
		b, err := os.ReadFile(serviceAcct + "/token")
		if err != nil {
			return nil, fmt.Errorf("read the service account token: %w", err)
		}
		cfg.Token = strings.TrimSpace(string(b))
	}
	caPath := strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_K8S_CA"))
	if caPath == "" && os.Getenv("WEKNORA_PLUGIN_K8S_API") == "" {
		caPath = serviceAcct + "/ca.crt"
	}
	if caPath != "" {
		b, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("read the cluster CA: %w", err)
		}
		cfg.CA = b
	}
	switch cfg.ServiceType {
	case "":
		cfg.ServiceType = "ClusterIP"
	case "ClusterIP":
	case "NodePort":
		if cfg.NodeHost == "" {
			return nil, errors.New("WEKNORA_PLUGIN_K8S_SERVICE_TYPE=NodePort needs WEKNORA_PLUGIN_K8S_NODE_HOST")
		}
	default:
		return nil, fmt.Errorf("WEKNORA_PLUGIN_K8S_SERVICE_TYPE must be ClusterIP or NodePort, got %q", cfg.ServiceType)
	}
	if err := egressFromEnv(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// egressFromEnv reads how plugin pods reach the network:
// WEKNORA_PLUGIN_K8S_EGRESS_PORT hands them the cluster egress proxy, and
// WEKNORA_PLUGIN_K8S_NETWORK_POLICY=1 makes it their only way out.
func egressFromEnv(cfg *Config) error {
	if raw := strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_K8S_EGRESS_PORT")); raw != "" {
		if cfg.EgressPort = EgressPortFromEnv(); cfg.EgressPort == 0 && raw != "0" {
			return fmt.Errorf("WEKNORA_PLUGIN_K8S_EGRESS_PORT must be a port, got %q", raw)
		}
	}
	if cfg.EgressPort > 0 && hostOf(cfg.HostAPIURL) == "" {
		return errors.New("WEKNORA_PLUGIN_K8S_EGRESS_PORT needs WEKNORA_PLUGIN_HOST_API_URL: " +
			"plugin pods reach the egress proxy at the Host API's host")
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_K8S_NETWORK_POLICY"))) {
	case "", "0", "false":
		return nil
	}
	if cfg.EgressPort == 0 {
		return errors.New("WEKNORA_PLUGIN_K8S_NETWORK_POLICY needs WEKNORA_PLUGIN_K8S_EGRESS_PORT: " +
			"the egress proxy is the plugins' only way out")
	}
	if cfg.ServiceType != "ClusterIP" {
		return errors.New("WEKNORA_PLUGIN_K8S_NETWORK_POLICY needs WeKnora in the cluster (ClusterIP services)")
	}
	np := &NetworkPolicy{
		AppNamespace: strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_K8S_APP_NAMESPACE")),
		AppPort:      DefaultPort,
	}
	var err error
	if np.AppLabels, err = labelsFromEnv("WEKNORA_PLUGIN_K8S_APP_LABELS", ""); err != nil {
		return err
	}
	if len(np.AppLabels) == 0 {
		return errors.New("WEKNORA_PLUGIN_K8S_NETWORK_POLICY needs WEKNORA_PLUGIN_K8S_APP_LABELS, " +
			"the labels of WeKnora's app pods")
	}
	if np.AppNamespace == "" {
		b, err := os.ReadFile(serviceAcct + "/namespace")
		if err != nil {
			return errors.New("WEKNORA_PLUGIN_K8S_NETWORK_POLICY needs WEKNORA_PLUGIN_K8S_APP_NAMESPACE outside a pod")
		}
		np.AppNamespace = strings.TrimSpace(string(b))
	}
	if np.DNSNamespaceLabels, err = labelsFromEnv("WEKNORA_PLUGIN_K8S_DNS_NAMESPACE_LABELS",
		"kubernetes.io/metadata.name=kube-system"); err != nil {
		return err
	}
	if np.DNSPodLabels, err = labelsFromEnv("WEKNORA_PLUGIN_K8S_DNS_POD_LABELS", "k8s-app=kube-dns"); err != nil {
		return err
	}
	cfg.NetworkPolicy = np
	return nil
}

// EgressPortFromEnv is WEKNORA_PLUGIN_K8S_EGRESS_PORT: the port app nodes
// serve the cluster egress proxy on, or 0.
func EgressPortFromEnv() int {
	port, err := strconv.Atoi(strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_K8S_EGRESS_PORT")))
	if err != nil || port < 0 || port > 65535 {
		return 0
	}
	return port
}

// labelsFromEnv reads "key=value,key=value"; "-" is no labels.
func labelsFromEnv(name, def string) (map[string]string, error) {
	raw, ok := os.LookupEnv(name)
	if raw = strings.TrimSpace(raw); !ok || raw == "" {
		raw = def
	}
	out := map[string]string{}
	if raw == "-" {
		return out, nil
	}
	for _, kv := range strings.Split(raw, ",") {
		if kv = strings.TrimSpace(kv); kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("%s: %q is not key=value", name, kv)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// proxyURL is the egress proxy a plugin's pods are handed, with the
// plugin's credentials; empty when pods get no proxy.
func (cfg *Config) proxyURL(pluginID string) string {
	if cfg.EgressPort == 0 || len(cfg.EgressKey) == 0 || hostOf(cfg.HostAPIURL) == "" {
		return ""
	}
	u := url.URL{
		Scheme: "http", User: url.UserPassword(pluginID, egress.Password(cfg.EgressKey, pluginID)),
		Host: net.JoinHostPort(hostOf(cfg.HostAPIURL), strconv.Itoa(cfg.EgressPort)),
	}
	return u.String()
}

// Egress is how the driver holds plugin pods to their egress grant.
func (cfg *Config) Egress() driver.EgressMode {
	switch {
	case cfg.NetworkPolicy != nil:
		return driver.EgressNetworkPolicy
	case cfg.EgressPort > 0 && len(cfg.EgressKey) > 0:
		return driver.EgressProxy
	default:
		return driver.EgressUnmanaged
	}
}

// Endpoints is where the driver hands a deployed plugin over: the remote
// plugin manager, which checks and health-watches it.
type Endpoints interface {
	ServeDeployed(ctx context.Context, m *manifest.Manifest, url, secret string) error
	Deactivate(ctx context.Context, pluginID string) error
}

// Driver is the kubernetes runtime as a reconcile.Activator. It must come
// right after the remote plugin manager, before the activators that call
// plugins.
type Driver struct {
	cfg       *Config
	http      *http.Client
	endpoints Endpoints
	timeout   time.Duration
	stuck     time.Duration

	mu sync.Mutex
	// owned are the plugins this driver deployed: it deactivates only
	// those, not every plugin the reconciler unloads.
	owned map[string]bool
	// rollouts cancels a plugin's rollout still finishing in the
	// background.
	rollouts map[string]context.CancelFunc
	// serveMu orders registering a service against unregistering it, so a
	// rollout that finishes as its plugin is deactivated leaves nothing.
	serveMu sync.Mutex
}

// New creates the driver.
func New(cfg *Config, endpoints Endpoints) (*Driver, error) {
	// Insecure is an explicit opt-in for local clusters.
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.Insecure} //nolint:gosec // opt-in
	if len(cfg.CA) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(cfg.CA) {
			return nil, errors.New("the cluster CA is not PEM")
		}
		tlsCfg.RootCAs = pool
	}
	return &Driver{
		cfg: cfg, endpoints: endpoints, timeout: readyTimeout, stuck: stuckInterval,
		http:  &http.Client{Timeout: requestTimeout, Transport: &http.Transport{TLSClientConfig: tlsCfg}},
		owned: map[string]bool{}, rollouts: map[string]context.CancelFunc{},
	}, nil
}

// Name implements reconcile.Activator.
func (d *Driver) Name() string { return "kubernetes" }

// ActivatesInPlace implements reconcile.InPlaceActivator: a new version is
// rolled out over the running one.
func (d *Driver) ActivatesInPlace() {}

var nonName = regexp.MustCompile(`[^a-z0-9-]+`)

// ResourceName is the name of a plugin's Deployment, Service and Secret: a
// readable prefix and a hash of the whole ID, so IDs that read alike
// (acme.foo-bar, acme-foo.bar) or share a long prefix never share one.
func ResourceName(pluginID string) string {
	n := strings.Trim(nonName.ReplaceAllString(strings.ToLower(pluginID), "-"), "-")
	if len(n) > 40 {
		n = strings.TrimRight(n[:40], "-")
	}
	sum := sha256.Sum256([]byte(pluginID))
	return "wkp-" + n + "-" + hex.EncodeToString(sum[:5])
}

// legacyResourceName is the name earlier versions gave a plugin's
// resources; they are removed once the plugin runs under its new name.
func legacyResourceName(pluginID string) string {
	n := "wkp-" + strings.Trim(nonName.ReplaceAllString(strings.ToLower(pluginID), "-"), "-")
	if len(n) > 50 {
		n = strings.TrimRight(n[:50], "-")
	}
	return n
}

// Activate implements reconcile.Activator: apply the plugin's resources and
// register the service. A rollout that is not done at once finishes in the
// background: Activate returns a reconcile.PendingError and the plugin
// reports ready through Loaded.Report, so a slow image pull holds up
// neither the reconciler nor the node's startup. Other runtimes are
// ignored.
func (d *Driver) Activate(ctx context.Context, l *reconcile.Loaded) error {
	m := l.Manifest
	if m.Runtime.Type != manifest.RuntimeKubernetes {
		return nil
	}
	secret, err := utils.DecryptStoredSecret(l.Installed.RemoteSecret)
	if err != nil || secret == "" {
		return fmt.Errorf("the plugin's signing secret is not readable: %v", err)
	}
	d.stopRollout(m.ID)
	d.mu.Lock()
	d.owned[m.ID] = true
	d.mu.Unlock()
	name := ResourceName(m.ID)
	for _, obj := range Resources(d.cfg, m, name, secret) {
		if err := d.apply(ctx, obj); err != nil {
			return err
		}
	}
	if d.cfg.NetworkPolicy == nil {
		// A policy from when they were on would cut the pods off from the
		// network now that they get no proxy.
		if err := d.deleteResource(ctx, m.ID, networkPolicyPath(d.cfg.Namespace, name)); err != nil {
			logger.Warnf(ctx, "[plugin] kubernetes %s: remove its network policy: %v", m.ID, err)
		}
	}
	if ready, _, err := d.rolledOut(ctx, name); err == nil && ready {
		// Nothing changed since it last rolled out (a node restarting).
		return d.serve(ctx, m, name, secret)
	}
	rctx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.rollouts[m.ID] = cancel
	d.mu.Unlock()
	go d.rollout(rctx, l, name, secret)
	return reconcile.Pending(fmt.Errorf("rolling out %s/%s", d.cfg.Namespace, name))
}

// rollout waits for a plugin's Deployment in the background and serves it
// once it is ready, reporting a rollout that overran the timeout (and keeps
// waiting for it).
func (d *Driver) rollout(ctx context.Context, l *reconcile.Loaded, name, secret string) {
	report := func(healthy bool, err error) {
		if l.Report != nil {
			l.Report(healthy, err)
		}
	}
	interval := pollInterval
	for {
		err := d.waitReady(ctx, name, interval)
		if err == nil {
			err = d.serve(ctx, l.Manifest, name, secret)
		}
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			d.finishRollout(ctx, l.Manifest.ID)
			report(true, nil)
			return
		}
		logger.Warnf(ctx, "[plugin] kubernetes %s: %v", l.Manifest.ID, err)
		report(false, err)
		interval = d.stuck
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// serve registers a rolled-out plugin with the remote plugin manager and
// removes what an earlier version left under the legacy name.
func (d *Driver) serve(ctx context.Context, m *manifest.Manifest, name, secret string) error {
	url, err := d.serviceURL(ctx, name)
	if err != nil {
		return err
	}
	d.serveMu.Lock()
	if err := ctx.Err(); err != nil {
		d.serveMu.Unlock()
		return err
	}
	err = d.endpoints.ServeDeployed(ctx, m, url, secret)
	d.serveMu.Unlock()
	if err != nil {
		return err
	}
	logger.Infof(ctx, "[plugin] kubernetes %s %s rolled out as %s/%s", m.ID, m.Version, d.cfg.Namespace, name)
	if legacy := legacyResourceName(m.ID); legacy != name {
		if err := d.deleteResources(ctx, m.ID, legacy); err != nil {
			logger.Warnf(ctx, "[plugin] kubernetes %s: remove resources under the old name %s: %v", m.ID, legacy, err)
		}
	}
	return nil
}

func (d *Driver) stopRollout(pluginID string) {
	d.mu.Lock()
	cancel := d.rollouts[pluginID]
	delete(d.rollouts, pluginID)
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// finishRollout forgets a rollout that completed, unless a newer one
// replaced it.
func (d *Driver) finishRollout(ctx context.Context, pluginID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ctx.Err() == nil {
		delete(d.rollouts, pluginID)
	}
}

// Deactivate implements reconcile.Activator: unregister the service and
// delete its resources. Plugins this driver did not deploy are left alone.
func (d *Driver) Deactivate(ctx context.Context, pluginID string) error {
	d.stopRollout(pluginID)
	d.mu.Lock()
	owned := d.owned[pluginID]
	delete(d.owned, pluginID)
	d.mu.Unlock()
	if !owned {
		return nil
	}
	d.serveMu.Lock()
	_ = d.endpoints.Deactivate(ctx, pluginID)
	d.serveMu.Unlock()
	err := d.deleteResources(ctx, pluginID, ResourceName(pluginID))
	if legacy := legacyResourceName(pluginID); legacy != ResourceName(pluginID) {
		err = errors.Join(err, d.deleteResources(ctx, pluginID, legacy))
	}
	return err
}

// resourcePaths are the API paths of a plugin's resources, by name. The
// NetworkPolicy goes last, so no pod outlives it.
var resourcePaths = []string{
	"/apis/apps/v1/namespaces/%s/deployments/%s",
	"/api/v1/namespaces/%s/services/%s",
	"/api/v1/namespaces/%s/secrets/%s",
	"/apis/networking.k8s.io/v1/namespaces/%s/networkpolicies/%s",
}

func networkPolicyPath(namespace, name string) string {
	return fmt.Sprintf(resourcePaths[3], namespace, name)
}

// deleteResources deletes the resources named name that belong to the
// plugin: another plugin's, under a name that collided, stay.
func (d *Driver) deleteResources(ctx context.Context, pluginID, name string) error {
	var errs []error
	for _, p := range resourcePaths {
		errs = append(errs, d.deleteResource(ctx, pluginID, fmt.Sprintf(p, d.cfg.Namespace, name)))
	}
	return errors.Join(errs...)
}

func (d *Driver) deleteResource(ctx context.Context, pluginID, path string) error {
	status, resp, err := d.do(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return err
	}
	// Every node deactivates; a resource another node removed is fine.
	if status == http.StatusNotFound {
		return nil
	}
	// Without network policies the platform may not grant access to them.
	if status == http.StatusForbidden && d.cfg.NetworkPolicy == nil && strings.Contains(path, "/networkpolicies/") {
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("read %s: HTTP %d", path, status)
	}
	var obj struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if json.Unmarshal(resp, &obj) != nil || obj.Metadata.Annotations["weknora.plugin/id"] != pluginID {
		return nil
	}
	status, _, err = d.do(ctx, http.MethodDelete, path, "", nil)
	if err != nil {
		return err
	}
	if status >= 300 && status != http.StatusNotFound {
		return fmt.Errorf("delete %s: HTTP %d", path, status)
	}
	return nil
}

// Object is one resource to apply: its API path and its manifest.
type Object struct {
	Path string
	Body map[string]any
}

// Resources are the objects a plugin runs as: a Secret with its signing
// secret (and egress proxy URL), the NetworkPolicy that confines it when
// the platform asks for one, a Deployment of its image and a Service in
// front of it.
func Resources(cfg *Config, m *manifest.Manifest, name, secret string) []Object {
	port := m.Runtime.Port
	if port == 0 {
		port = DefaultPort
	}
	labels := map[string]any{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "weknora",
		"weknora.plugin/id":            ResourceName(m.ID),
	}
	meta := func() map[string]any {
		return map[string]any{
			"name": name, "namespace": cfg.Namespace, "labels": labels,
			"annotations": map[string]any{"weknora.plugin/id": m.ID, "weknora.plugin/version": m.Version},
		}
	}
	env := []any{
		map[string]any{"name": "WEKNORA_PLUGIN_ADDR", "value": ":" + strconv.Itoa(port)},
		map[string]any{"name": "WEKNORA_PLUGIN_SECRET", "valueFrom": map[string]any{
			"secretKeyRef": map[string]any{"name": name, "key": "secret"},
		}},
	}
	secretData := map[string]any{"secret": secret}
	podAnnotations := map[string]any{
		// A new version, or a rotated secret, rolls the pods.
		"weknora.plugin/version": m.Version,
		"weknora.plugin/secret":  shortHash(secret),
	}
	proxy := cfg.proxyURL(m.ID)
	if proxy != "" {
		// The proxy URL carries the plugin's credentials, so it stays in the
		// Secret. The Host API is reached directly.
		secretData["egress-proxy"] = proxy
		for _, v := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			env = append(env, map[string]any{"name": v, "valueFrom": map[string]any{
				"secretKeyRef": map[string]any{"name": name, "key": "egress-proxy"},
			}})
		}
		noProxy := strings.Join([]string{hostOf(cfg.HostAPIURL), "localhost", "127.0.0.1"}, ",")
		env = append(env, map[string]any{"name": "NO_PROXY", "value": noProxy},
			map[string]any{"name": "no_proxy", "value": noProxy})
		podAnnotations["weknora.plugin/egress"] = shortHash(proxy)
	}
	container := map[string]any{
		"name": "plugin", "image": m.Runtime.Image, "imagePullPolicy": "IfNotPresent",
		"ports": []any{map[string]any{"name": "protocol", "containerPort": port}},
		"env":   env,
		// Every protocol endpoint, health included, wants a signed request;
		// the kubelet can only see that the port is open. WeKnora checks
		// health and the manifest itself before it routes a call there.
		"readinessProbe": map[string]any{
			"tcpSocket":     map[string]any{"port": port},
			"periodSeconds": 5,
		},
		"securityContext": map[string]any{
			"allowPrivilegeEscalation": false, "runAsNonRoot": true,
			"capabilities": map[string]any{"drop": []any{"ALL"}},
		},
	}
	if r := m.Runtime.Resources; r != nil && (r.CPU != "" || r.Memory != "") {
		limits := map[string]any{}
		if r.CPU != "" {
			limits["cpu"] = r.CPU
		}
		if r.Memory != "" {
			limits["memory"] = r.Memory
		}
		container["resources"] = map[string]any{"limits": limits, "requests": limits}
	}
	podSpec := map[string]any{
		"containers":                   []any{container},
		"automountServiceAccountToken": false,
		// The SDKs drain calls for up to 60s on SIGTERM; the default 30s
		// would cut a rollout's in-flight parse or sync short.
		"terminationGracePeriodSeconds": 75,
	}
	if cfg.ImagePullSecret != "" {
		podSpec["imagePullSecrets"] = []any{map[string]any{"name": cfg.ImagePullSecret}}
	}
	selector := map[string]any{"app.kubernetes.io/name": name}
	svcPort := map[string]any{"name": "protocol", "port": 80, "targetPort": port}
	objs := []Object{{
		Path: fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", cfg.Namespace, name),
		Body: map[string]any{
			"apiVersion": "v1", "kind": "Secret", "metadata": meta(), "type": "Opaque",
			"stringData": secretData,
		},
	}}
	if np := cfg.NetworkPolicy; np != nil {
		// Before the Deployment: no pod starts unconfined.
		objs = append(objs, Object{
			Path: networkPolicyPath(cfg.Namespace, name),
			Body: map[string]any{
				"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": meta(),
				"spec": np.spec(selector, port, cfg.EgressPort),
			},
		})
	}
	return append(objs,
		Object{
			Path: fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", cfg.Namespace, name),
			Body: map[string]any{
				"apiVersion": "apps/v1", "kind": "Deployment", "metadata": meta(),
				"spec": map[string]any{
					"replicas": 1,
					"selector": map[string]any{"matchLabels": selector},
					"template": map[string]any{
						"metadata": map[string]any{"labels": labels, "annotations": podAnnotations},
						"spec":     podSpec,
					},
				},
			},
		},
		Object{
			Path: fmt.Sprintf("/api/v1/namespaces/%s/services/%s", cfg.Namespace, name),
			Body: map[string]any{
				"apiVersion": "v1", "kind": "Service", "metadata": meta(),
				"spec": map[string]any{"type": cfg.ServiceType, "selector": selector, "ports": []any{svcPort}},
			},
		},
	)
}

// spec is the NetworkPolicy of a plugin's pods: only the app pods call in,
// on the plugin's port; out, the pods reach DNS and the app pods' Host API
// and egress proxy, which applies the plugin's grant.
func (np *NetworkPolicy) spec(pods map[string]any, pluginPort, egressPort int) map[string]any {
	app := map[string]any{
		"namespaceSelector": map[string]any{
			"matchLabels": map[string]any{"kubernetes.io/metadata.name": np.AppNamespace},
		},
		"podSelector": map[string]any{"matchLabels": stringMap(np.AppLabels)},
	}
	dns := map[string]any{"namespaceSelector": map[string]any{"matchLabels": stringMap(np.DNSNamespaceLabels)}}
	if len(np.DNSPodLabels) > 0 {
		dns["podSelector"] = map[string]any{"matchLabels": stringMap(np.DNSPodLabels)}
	}
	tcp := func(port int) map[string]any { return map[string]any{"protocol": "TCP", "port": port} }
	return map[string]any{
		"podSelector": map[string]any{"matchLabels": pods},
		"policyTypes": []any{"Ingress", "Egress"},
		"ingress":     []any{map[string]any{"from": []any{app}, "ports": []any{tcp(pluginPort)}}},
		"egress": []any{
			map[string]any{"to": []any{dns}, "ports": []any{
				map[string]any{"protocol": "UDP", "port": 53}, tcp(53),
			}},
			map[string]any{"to": []any{app}, "ports": []any{tcp(np.AppPort), tcp(egressPort)}},
		},
	}
}

func stringMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Egress is how the driver holds plugin pods to their egress grant.
func (d *Driver) Egress() driver.EgressMode { return d.cfg.Egress() }

// Statuses reports the kubernetes plugins' instances as inner does (one per
// node that loaded the plugin), with how their pods' egress is controlled.
func (d *Driver) Statuses(inner driver.Driver) driver.Driver {
	return egressStatus{Driver: inner, mode: d.Egress()}
}

type egressStatus struct {
	driver.Driver
	mode driver.EgressMode
}

func (s egressStatus) Status(ctx context.Context, pluginID string) ([]driver.InstanceStatus, error) {
	out, err := s.Driver.Status(ctx, pluginID)
	for i := range out {
		out[i].Egress = s.mode
	}
	return out, err
}

func (d *Driver) apply(ctx context.Context, obj Object) error {
	body, err := json.Marshal(obj.Body)
	if err != nil {
		return err
	}
	status, resp, err := d.do(ctx, http.MethodPatch, obj.Path+"?fieldManager="+fieldManager+"&force=true",
		"application/apply-patch+yaml", body)
	if err != nil {
		return err
	}
	if status >= 300 {
		return fmt.Errorf("apply %s: HTTP %d: %s", obj.Path, status, apiMessage(resp))
	}
	return nil
}

// waitReady waits up to the driver's timeout until the Deployment's current
// spec is rolled out and serving, checking every interval.
func (d *Driver) waitReady(ctx context.Context, name string, interval time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	var last string
	for {
		ready, msg, err := d.rolledOut(ctx, name)
		if err == nil && ready {
			return nil
		}
		if msg != "" {
			last = msg
		}
		select {
		case <-ctx.Done():
			if last == "" {
				last = "no pod became ready"
			}
			return fmt.Errorf("deployment %s/%s did not roll out: %s", d.cfg.Namespace, name, last)
		case <-time.After(interval):
		}
	}
}

// rolledOut checks the Deployment once: whether its current spec is rolled
// out and serving, and if not, the latest reason Kubernetes gives.
func (d *Driver) rolledOut(ctx context.Context, name string) (bool, string, error) {
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", d.cfg.Namespace, name)
	status, resp, err := d.do(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return false, "", err
	}
	if status != http.StatusOK {
		return false, "", fmt.Errorf("read deployment %s: HTTP %d", name, status)
	}
	var dep struct {
		Metadata struct{ Generation int64 } `json:"metadata"`
		Status   struct {
			ObservedGeneration int64 `json:"observedGeneration"`
			Replicas           int   `json:"replicas"`
			UpdatedReplicas    int   `json:"updatedReplicas"`
			AvailableReplicas  int   `json:"availableReplicas"`
			Conditions         []struct {
				Type, Status, Reason, Message string
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal(resp, &dep); err != nil {
		return false, "", err
	}
	s := dep.Status
	if s.ObservedGeneration >= dep.Metadata.Generation && s.UpdatedReplicas >= 1 &&
		s.AvailableReplicas >= 1 && s.Replicas == s.UpdatedReplicas {
		return true, "", nil
	}
	var msg string
	for _, c := range s.Conditions {
		if c.Status == "False" && c.Message != "" {
			msg = c.Message
		}
	}
	return false, msg, nil
}

// serviceURL is where WeKnora reaches the plugin's Service.
func (d *Driver) serviceURL(ctx context.Context, name string) (string, error) {
	if d.cfg.ServiceType != "NodePort" {
		return fmt.Sprintf("http://%s.%s.svc", name, d.cfg.Namespace), nil
	}
	status, resp, err := d.do(ctx, http.MethodGet,
		fmt.Sprintf("/api/v1/namespaces/%s/services/%s", d.cfg.Namespace, name), "", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("read service %s: HTTP %d", name, status)
	}
	var svc struct {
		Spec struct {
			Ports []struct {
				NodePort int `json:"nodePort"`
			} `json:"ports"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(resp, &svc); err != nil || len(svc.Spec.Ports) == 0 || svc.Spec.Ports[0].NodePort == 0 {
		return "", fmt.Errorf("service %s has no node port", name)
	}
	return fmt.Sprintf("http://%s:%d", d.cfg.NodeHost, svc.Spec.Ports[0].NodePort), nil
}

func (d *Driver) do(ctx context.Context, method, path, ctype string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, d.cfg.APIURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+d.cfg.Token)
	req.Header.Set("Accept", "application/json")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("kubernetes API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, nil
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

func apiMessage(b []byte) string {
	var s struct{ Message string }
	if json.Unmarshal(b, &s) == nil && s.Message != "" {
		return s.Message
	}
	return strings.TrimSpace(string(b))
}
