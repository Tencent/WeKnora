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
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	// DefaultPort is where a plugin image serves unless runtime.port says.
	DefaultPort = 8080
	// fieldManager owns the applied fields.
	fieldManager   = "weknora"
	readyTimeout   = 5 * time.Minute
	pollInterval   = 2 * time.Second
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
	return cfg, nil
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
		cfg: cfg, endpoints: endpoints, timeout: readyTimeout,
		http: &http.Client{Timeout: requestTimeout, Transport: &http.Transport{TLSClientConfig: tlsCfg}},
	}, nil
}

// Name implements reconcile.Activator.
func (d *Driver) Name() string { return "kubernetes" }

// ActivatesInPlace implements reconcile.InPlaceActivator: a new version is
// rolled out over the running one.
func (d *Driver) ActivatesInPlace() {}

var nonName = regexp.MustCompile(`[^a-z0-9-]+`)

// ResourceName is the name of a plugin's Deployment, Service and Secret.
func ResourceName(pluginID string) string {
	n := "wkp-" + strings.Trim(nonName.ReplaceAllString(strings.ToLower(pluginID), "-"), "-")
	if len(n) > 50 {
		n = strings.TrimRight(n[:50], "-")
	}
	return n
}

// Activate implements reconcile.Activator: apply the plugin's resources,
// wait for the rollout and register the service. Other runtimes are
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
	name := ResourceName(m.ID)
	for _, obj := range Resources(d.cfg, m, name, secret) {
		if err := d.apply(ctx, obj); err != nil {
			return err
		}
	}
	if err := d.waitReady(ctx, name); err != nil {
		return err
	}
	url, err := d.serviceURL(ctx, name)
	if err != nil {
		return err
	}
	logger.Infof(ctx, "[plugin] kubernetes %s %s rolled out as %s/%s", m.ID, m.Version, d.cfg.Namespace, name)
	return d.endpoints.ServeDeployed(ctx, m, url, secret)
}

// Deactivate implements reconcile.Activator: unregister the service and
// delete its resources.
func (d *Driver) Deactivate(ctx context.Context, pluginID string) error {
	_ = d.endpoints.Deactivate(ctx, pluginID)
	name := ResourceName(pluginID)
	var errs []error
	for _, path := range []string{
		"/apis/apps/v1/namespaces/%s/deployments/%s",
		"/api/v1/namespaces/%s/services/%s",
		"/api/v1/namespaces/%s/secrets/%s",
	} {
		// Every node deactivates; a resource another node removed is fine.
		status, _, err := d.do(ctx, http.MethodDelete, fmt.Sprintf(path, d.cfg.Namespace, name), "", nil)
		if err != nil {
			errs = append(errs, err)
		} else if status >= 300 && status != http.StatusNotFound {
			errs = append(errs, fmt.Errorf("delete %s: HTTP %d", fmt.Sprintf(path, d.cfg.Namespace, name), status))
		}
	}
	return errors.Join(errs...)
}

// Object is one resource to apply: its API path and its manifest.
type Object struct {
	Path string
	Body map[string]any
}

// Resources are the objects a plugin runs as: a Secret with its signing
// secret, a Deployment of its image and a Service in front of it.
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
	container := map[string]any{
		"name": "plugin", "image": m.Runtime.Image, "imagePullPolicy": "IfNotPresent",
		"ports": []any{map[string]any{"name": "protocol", "containerPort": port}},
		"env": []any{
			map[string]any{"name": "WEKNORA_PLUGIN_ADDR", "value": ":" + strconv.Itoa(port)},
			map[string]any{"name": "WEKNORA_PLUGIN_SECRET", "valueFrom": map[string]any{
				"secretKeyRef": map[string]any{"name": name, "key": "secret"},
			}},
		},
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
	}
	if cfg.ImagePullSecret != "" {
		podSpec["imagePullSecrets"] = []any{map[string]any{"name": cfg.ImagePullSecret}}
	}
	selector := map[string]any{"app.kubernetes.io/name": name}
	svcPort := map[string]any{"name": "protocol", "port": 80, "targetPort": port}
	return []Object{
		{
			Path: fmt.Sprintf("/api/v1/namespaces/%s/secrets/%s", cfg.Namespace, name),
			Body: map[string]any{
				"apiVersion": "v1", "kind": "Secret", "metadata": meta(), "type": "Opaque",
				"stringData": map[string]any{"secret": secret},
			},
		},
		{
			Path: fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", cfg.Namespace, name),
			Body: map[string]any{
				"apiVersion": "apps/v1", "kind": "Deployment", "metadata": meta(),
				"spec": map[string]any{
					"replicas": 1,
					"selector": map[string]any{"matchLabels": selector},
					"template": map[string]any{
						"metadata": map[string]any{"labels": labels, "annotations": map[string]any{
							// A new version, or a rotated secret, rolls the pods.
							"weknora.plugin/version": m.Version,
							"weknora.plugin/secret":  shortHash(secret),
						}},
						"spec": podSpec,
					},
				},
			},
		},
		{
			Path: fmt.Sprintf("/api/v1/namespaces/%s/services/%s", cfg.Namespace, name),
			Body: map[string]any{
				"apiVersion": "v1", "kind": "Service", "metadata": meta(),
				"spec": map[string]any{"type": cfg.ServiceType, "selector": selector, "ports": []any{svcPort}},
			},
		},
	}
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

// waitReady waits until the Deployment's current spec is rolled out and
// serving.
func (d *Driver) waitReady(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	path := fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", d.cfg.Namespace, name)
	var last string
	for {
		status, resp, err := d.do(ctx, http.MethodGet, path, "", nil)
		if err == nil && status == http.StatusOK {
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
			if json.Unmarshal(resp, &dep) == nil {
				s := dep.Status
				if s.ObservedGeneration >= dep.Metadata.Generation && s.UpdatedReplicas >= 1 &&
					s.AvailableReplicas >= 1 && s.Replicas == s.UpdatedReplicas {
					return nil
				}
				for _, c := range s.Conditions {
					if c.Status == "False" && c.Message != "" {
						last = c.Message
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			if last == "" {
				last = "no pod became ready"
			}
			return fmt.Errorf("deployment %s/%s did not roll out: %s", d.cfg.Namespace, name, last)
		case <-time.After(pollInterval):
		}
	}
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
