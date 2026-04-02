package sandbox

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	saTokenPath  = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	saCACertPath = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

const (
	tokenCacheTTL    = 30 * time.Second
	maxResponseBytes = 10 * 1024 * 1024 // 10MB cap on K8s API response reads
)

// kubeClient is a lightweight Kubernetes API client using only net/http + encoding/json.
// For in-cluster use, tokenPath is set so the token is re-read periodically
// to handle bound SA token rotation (tokens expire, typically after 1 hour).
type kubeClient struct {
	apiServer     string
	token         string
	tokenPath     string
	tokenMu       sync.Mutex // guards token, tokenCachedAt
	tokenCachedAt time.Time
	httpClient    *http.Client
}

// newKubeClientInCluster reads the service account token and CA cert from the default paths
// and returns a kubeClient configured for in-cluster use.
func newKubeClientInCluster() (*kubeClient, error) {
	// Verify the token file is readable at init time.
	if _, err := os.ReadFile(saTokenPath); err != nil {
		return nil, fmt.Errorf("failed to read SA token: %w", err)
	}

	caBytes, err := os.ReadFile(saCACertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("failed to parse CA cert")
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: pool,
			},
		},
	}

	return &kubeClient{
		apiServer:  "https://kubernetes.default.svc",
		tokenPath:  saTokenPath,
		httpClient: httpClient,
	}, nil
}

// newKubeClient creates a kubeClient with the given apiServer, token, and optional httpClient.
// If httpClient is nil, http.DefaultClient is used.
func newKubeClient(apiServer, token string, httpClient *http.Client) *kubeClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &kubeClient{
		apiServer:  apiServer,
		token:      token,
		httpClient: httpClient,
	}
}

// getToken returns the current bearer token. If tokenPath is set (in-cluster mode),
// it re-reads the file periodically (every tokenCacheTTL) to handle bound SA token rotation.
func (c *kubeClient) getToken() (string, error) {
	if c.tokenPath != "" {
		c.tokenMu.Lock()
		defer c.tokenMu.Unlock()
		if c.token != "" && time.Since(c.tokenCachedAt) < tokenCacheTTL {
			return c.token, nil
		}
		data, err := os.ReadFile(c.tokenPath)
		if err != nil {
			return "", err
		}
		c.token = string(data)
		c.tokenCachedAt = time.Now()
		return c.token, nil
	}
	return c.token, nil
}

// inClusterAvailable returns true if the SA token file exists.
func inClusterAvailable() bool {
	_, err := os.Stat(saTokenPath)
	return err == nil
}

// do performs an HTTP request to the K8s API with Bearer token auth.
// Returns the response body bytes, HTTP status code, and any error.
func (c *kubeClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.apiServer+path, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	token, err := c.getToken()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read token: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response: %w", err)
	}

	return respBytes, resp.StatusCode, nil
}

// createConfigMap creates a ConfigMap in the given namespace.
func (c *kubeClient) createConfigMap(ctx context.Context, namespace, name string, data map[string]string, labels map[string]string) error {
	cm := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"labels":    labels,
		},
		"data": data,
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps", namespace)
	_, status, err := c.do(ctx, http.MethodPost, path, cm)
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fmt.Errorf("createConfigMap: unexpected status %d", status)
	}
	return nil
}

// deleteConfigMap deletes a ConfigMap by name in the given namespace.
func (c *kubeClient) deleteConfigMap(ctx context.Context, namespace, name string) error {
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps/%s", namespace, name)
	_, status, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusAccepted && status != http.StatusNoContent {
		return fmt.Errorf("deleteConfigMap: unexpected status %d", status)
	}
	return nil
}

// jobSpec holds the parameters needed to create a Kubernetes Job.
type jobSpec struct {
	Name               string
	Image              string
	Command            []string
	ConfigMapName      string
	TimeoutSeconds     int
	ServiceAccountName string
	MemoryLimit        string
	CPULimit           string
}

// jobStatus represents the current state of a Kubernetes Job.
type jobStatus struct {
	succeeded bool
	failed    bool
	active    bool
}

// createJob creates a Kubernetes batch/v1 Job from the given jobSpec.
func (c *kubeClient) createJob(ctx context.Context, namespace string, spec *jobSpec) error {
	ttl := int32(120)
	backoffLimit := int32(0)
	activeDeadlineSeconds := int64(spec.TimeoutSeconds)
	if activeDeadlineSeconds <= 0 {
		activeDeadlineSeconds = 300
	}
	runAsUser := int64(1000)
	runAsGroup := int64(1000)
	runAsNonRoot := true
	allowPrivEsc := false
	readOnlyRootfs := true

	job := map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"name":      spec.Name,
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/component":  "sandbox",
				"app.kubernetes.io/managed-by": "weknora",
			},
		},
		"spec": map[string]any{
			"backoffLimit":            backoffLimit,
			"ttlSecondsAfterFinished": ttl,
			"activeDeadlineSeconds":   activeDeadlineSeconds,
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": map[string]string{
						"app.kubernetes.io/component":  "sandbox",
						"app.kubernetes.io/managed-by": "weknora",
						"batch.kubernetes.io/job-name": spec.Name,
					},
				},
				"spec": map[string]any{
					"restartPolicy":                "Never",
					"automountServiceAccountToken": false,
					"serviceAccountName":           spec.ServiceAccountName,
					"securityContext": map[string]any{
						"runAsUser":    runAsUser,
						"runAsGroup":   runAsGroup,
						"runAsNonRoot": runAsNonRoot,
						"seccompProfile": map[string]any{
							"type": "RuntimeDefault",
						},
					},
					"volumes": []map[string]any{
						{
							"name": "workspace",
							"configMap": map[string]any{
								"name": spec.ConfigMapName,
							},
						},
						{
							"name": "tmp",
							"emptyDir": map[string]any{
								"medium":    "Memory",
								"sizeLimit": "64Mi",
							},
						},
					},
					"containers": []map[string]any{
						{
							"name":    "sandbox",
							"image":   spec.Image,
							"command": spec.Command,
							"securityContext": map[string]any{
								"allowPrivilegeEscalation": allowPrivEsc,
								"readOnlyRootFilesystem":   readOnlyRootfs,
								"runAsUser":                runAsUser,
								"runAsGroup":               runAsGroup,
								"runAsNonRoot":             runAsNonRoot,
								"capabilities": map[string]any{
									"drop": []string{"ALL"},
								},
								"seccompProfile": map[string]any{
									"type": "RuntimeDefault",
								},
							},
							"resources": map[string]any{
								"limits": map[string]any{
									"memory": spec.MemoryLimit,
									"cpu":    spec.CPULimit,
								},
							},
							"volumeMounts": []map[string]any{
								{
									"name":      "workspace",
									"mountPath": "/workspace",
									"readOnly":  true,
								},
								{
									"name":      "tmp",
									"mountPath": "/tmp",
								},
							},
						},
					},
				},
			},
		},
	}

	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", namespace)
	_, status, err := c.do(ctx, http.MethodPost, path, job)
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fmt.Errorf("createJob: unexpected status %d", status)
	}
	return nil
}

// getJobStatus returns the current status of a Kubernetes Job.
func (c *kubeClient) getJobStatus(ctx context.Context, namespace, name string) (*jobStatus, error) {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", namespace, name)
	body, status, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("getJobStatus: unexpected status %d", status)
	}

	var result struct {
		Status struct {
			Succeeded int32 `json:"succeeded"`
			Failed    int32 `json:"failed"`
			Active    int32 `json:"active"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("getJobStatus: failed to parse response: %w", err)
	}

	return &jobStatus{
		succeeded: result.Status.Succeeded > 0,
		failed:    result.Status.Failed > 0,
		active:    result.Status.Active > 0,
	}, nil
}

// findJobPod returns the name of the first pod belonging to the given job.
func (c *kubeClient) findJobPod(ctx context.Context, namespace, jobName string) (string, error) {
	q := url.Values{}
	q.Set("labelSelector", "batch.kubernetes.io/job-name="+jobName)
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods?%s", namespace, q.Encode())
	body, status, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("findJobPod: unexpected status %d", status)
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("findJobPod: failed to parse response: %w", err)
	}

	if len(result.Items) == 0 {
		return "", fmt.Errorf("findJobPod: no pods found for job %s", jobName)
	}

	return result.Items[0].Metadata.Name, nil
}

// getPodLogs returns the logs of a pod, optionally limiting the response size.
func (c *kubeClient) getPodLogs(ctx context.Context, namespace, podName string, limitBytes int64) (string, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/log", namespace, podName)
	if limitBytes > 0 {
		path += "?limitBytes=" + strconv.FormatInt(limitBytes, 10)
	}

	body, status, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("getPodLogs: unexpected status %d", status)
	}

	return string(body), nil
}

// deleteJob deletes a Kubernetes Job with cascading deletion via Background propagation policy.
func (c *kubeClient) deleteJob(ctx context.Context, namespace, name string) error {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s?propagationPolicy=Background", namespace, name)
	_, status, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusAccepted && status != http.StatusNoContent {
		return fmt.Errorf("deleteJob: unexpected status %d", status)
	}
	return nil
}

// configMapEntry holds a ConfigMap's name and labels.
type configMapEntry struct {
	name   string
	labels map[string]string
}

// listConfigMapsWithLabels returns ConfigMap entries (name + labels) matching the label selector.
func (c *kubeClient) listConfigMapsWithLabels(ctx context.Context, namespace, labelSelector string) ([]configMapEntry, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps", namespace)
	if labelSelector != "" {
		q := url.Values{}
		q.Set("labelSelector", labelSelector)
		path += "?" + q.Encode()
	}

	body, status, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("listConfigMapsWithLabels: unexpected status %d", status)
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("listConfigMapsWithLabels: failed to parse response: %w", err)
	}

	entries := make([]configMapEntry, 0, len(result.Items))
	for _, item := range result.Items {
		entries = append(entries, configMapEntry{
			name:   item.Metadata.Name,
			labels: item.Metadata.Labels,
		})
	}
	return entries, nil
}

// checkAccess returns true if the client can operate in the given namespace.
// It verifies by listing configmaps (which only requires namespace-scoped RBAC).
func (c *kubeClient) checkAccess(ctx context.Context, namespace string) bool {
	path := fmt.Sprintf("/api/v1/namespaces/%s/configmaps?limit=1", namespace)
	_, status, err := c.do(ctx, http.MethodGet, path, nil)
	return err == nil && status == http.StatusOK
}
