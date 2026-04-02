package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient creates a kubeClient pointing at the given httptest server URL.
func newTestClient(server *httptest.Server) *kubeClient {
	return newKubeClient(server.URL, "test-token", server.Client())
}

func TestKubeClientCreateConfigMap(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path

		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	ctx := context.Background()

	data := map[string]string{"script.py": "print('hello')"}
	labels := map[string]string{"app": "test"}

	err := client.createConfigMap(ctx, "test-ns", "my-cm", data, labels)
	if err != nil {
		t.Fatalf("createConfigMap failed: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/api/v1/namespaces/test-ns/configmaps" {
		t.Errorf("unexpected path: %s", gotPath)
	}

	meta, ok := gotBody["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata not found in body")
	}
	if meta["name"] != "my-cm" {
		t.Errorf("unexpected name: %v", meta["name"])
	}
	if meta["namespace"] != "test-ns" {
		t.Errorf("unexpected namespace: %v", meta["namespace"])
	}
}

func TestKubeClientDeleteConfigMap(t *testing.T) {
	var gotMethod, gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	err := client.deleteConfigMap(context.Background(), "test-ns", "my-cm")
	if err != nil {
		t.Fatalf("deleteConfigMap failed: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotPath != "/api/v1/namespaces/test-ns/configmaps/my-cm" {
		t.Errorf("unexpected path: %s", gotPath)
	}
}

func TestKubeClientCreateJob(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path

		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	ctx := context.Background()

	spec := &jobSpec{
		Name:               "sandbox-abc123",
		Image:              "python:3.11-slim",
		Command:            []string{"python", "/workspace/script.py"},
		ConfigMapName:      "cm-abc123",
		TimeoutSeconds:     60,
		ServiceAccountName: "sandbox-runner",
		MemoryLimit:        "256Mi",
		CPULimit:           "500m",
	}

	err := client.createJob(ctx, "test-ns", spec)
	if err != nil {
		t.Fatalf("createJob failed: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/apis/batch/v1/namespaces/test-ns/jobs" {
		t.Errorf("unexpected path: %s", gotPath)
	}

	if gotBody["apiVersion"] != "batch/v1" {
		t.Errorf("unexpected apiVersion: %v", gotBody["apiVersion"])
	}

	meta, _ := gotBody["metadata"].(map[string]any)
	if meta["name"] != "sandbox-abc123" {
		t.Errorf("unexpected job name: %v", meta["name"])
	}

	// Verify security settings in pod template
	jobSpec, _ := gotBody["spec"].(map[string]any)
	template, _ := jobSpec["template"].(map[string]any)
	podSpec, _ := template["spec"].(map[string]any)

	if podSpec["automountServiceAccountToken"] != false {
		t.Errorf("automountServiceAccountToken should be false")
	}
	if podSpec["restartPolicy"] != "Never" {
		t.Errorf("restartPolicy should be Never, got: %v", podSpec["restartPolicy"])
	}

	podSC, _ := podSpec["securityContext"].(map[string]any)
	if podSC["runAsNonRoot"] != true {
		t.Errorf("runAsNonRoot should be true")
	}

	containers, _ := podSpec["containers"].([]any)
	if len(containers) == 0 {
		t.Fatal("no containers in job spec")
	}
	container, _ := containers[0].(map[string]any)
	csc, _ := container["securityContext"].(map[string]any)

	if csc["allowPrivilegeEscalation"] != false {
		t.Errorf("allowPrivilegeEscalation should be false")
	}
	if csc["readOnlyRootFilesystem"] != true {
		t.Errorf("readOnlyRootFilesystem should be true")
	}
	caps, _ := csc["capabilities"].(map[string]any)
	drop, _ := caps["drop"].([]any)
	if len(drop) == 0 || drop[0] != "ALL" {
		t.Errorf("capabilities.drop should contain ALL, got: %v", drop)
	}

	// Verify volume mounts
	mounts, _ := container["volumeMounts"].([]any)
	if len(mounts) < 2 {
		t.Errorf("expected at least 2 volume mounts, got %d", len(mounts))
	}

	// Verify pod labels include job-name
	templateMeta, _ := template["metadata"].(map[string]any)
	podLabels, _ := templateMeta["labels"].(map[string]any)
	if podLabels["batch.kubernetes.io/job-name"] != "sandbox-abc123" {
		t.Errorf("pod label batch.kubernetes.io/job-name should match job name")
	}
}

func TestKubeClientGetJobStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"status": {
				"succeeded": 1,
				"failed": 0,
				"active": 0
			}
		}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	status, err := client.getJobStatus(context.Background(), "test-ns", "my-job")
	if err != nil {
		t.Fatalf("getJobStatus failed: %v", err)
	}

	if !status.succeeded {
		t.Errorf("expected succeeded=true")
	}
	if status.failed {
		t.Errorf("expected failed=false")
	}
	if status.active {
		t.Errorf("expected active=false")
	}
}

func TestKubeClientGetJobStatusActive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": {"succeeded": 0, "failed": 0, "active": 1}}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	status, err := client.getJobStatus(context.Background(), "test-ns", "my-job")
	if err != nil {
		t.Fatalf("getJobStatus failed: %v", err)
	}

	if status.succeeded || status.failed {
		t.Errorf("expected only active=true")
	}
	if !status.active {
		t.Errorf("expected active=true")
	}
}

func TestKubeClientGetPodLogs(t *testing.T) {
	const logContent = "hello from sandbox\nline 2\n"
	var gotQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(logContent))
	}))
	defer server.Close()

	client := newTestClient(server)
	logs, err := client.getPodLogs(context.Background(), "test-ns", "my-pod", 1024)
	if err != nil {
		t.Fatalf("getPodLogs failed: %v", err)
	}

	if logs != logContent {
		t.Errorf("unexpected logs: %q", logs)
	}

	if !strings.Contains(gotQuery, "limitBytes=1024") {
		t.Errorf("expected limitBytes=1024 in query, got: %s", gotQuery)
	}
}

func TestKubeClientGetPodLogsNoLimit(t *testing.T) {
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("logs"))
	}))
	defer server.Close()

	client := newTestClient(server)
	_, err := client.getPodLogs(context.Background(), "test-ns", "my-pod", 0)
	if err != nil {
		t.Fatalf("getPodLogs failed: %v", err)
	}

	if strings.Contains(gotPath, "limitBytes") {
		t.Errorf("expected no limitBytes in query when limit=0, got: %s", gotPath)
	}
}

func TestKubeClientDeleteJob(t *testing.T) {
	var gotMethod, gotPath, gotQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	err := client.deleteJob(context.Background(), "test-ns", "my-job")
	if err != nil {
		t.Fatalf("deleteJob failed: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotPath != "/apis/batch/v1/namespaces/test-ns/jobs/my-job" {
		t.Errorf("unexpected path: %s", gotPath)
	}
	if !strings.Contains(gotQuery, "propagationPolicy=Background") {
		t.Errorf("expected propagationPolicy=Background in query, got: %s", gotQuery)
	}
}

func TestKubeClientFindJobPod(t *testing.T) {
	var gotQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"items": [
				{"metadata": {"name": "my-job-abc12"}},
				{"metadata": {"name": "my-job-xyz99"}}
			]
		}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	podName, err := client.findJobPod(context.Background(), "test-ns", "my-job")
	if err != nil {
		t.Fatalf("findJobPod failed: %v", err)
	}

	if podName != "my-job-abc12" {
		t.Errorf("expected first pod 'my-job-abc12', got %q", podName)
	}

	if !strings.Contains(gotQuery, "labelSelector") {
		t.Errorf("expected labelSelector in query, got: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "my-job") {
		t.Errorf("expected job name in query selector, got: %s", gotQuery)
	}
}

func TestKubeClientFindJobPodNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"items": []}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	_, err := client.findJobPod(context.Background(), "test-ns", "missing-job")
	if err == nil {
		t.Fatal("expected error when no pods found")
	}
}

func TestKubeClientListConfigMapsWithLabels(t *testing.T) {
	var gotQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"items": [
				{"metadata": {"name": "cm-one", "labels": {"env": "test"}}},
				{"metadata": {"name": "cm-two", "labels": {"env": "prod"}}}
			]
		}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	entries, err := client.listConfigMapsWithLabels(context.Background(), "test-ns", "app=sandbox")
	if err != nil {
		t.Fatalf("listConfigMapsWithLabels failed: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].name != "cm-one" || entries[1].name != "cm-two" {
		t.Errorf("unexpected names: %v", entries)
	}
	if entries[0].labels["env"] != "test" {
		t.Errorf("expected label env=test on first entry, got: %v", entries[0].labels)
	}
	if !strings.Contains(gotQuery, "labelSelector") {
		t.Errorf("expected labelSelector in query, got: %s", gotQuery)
	}
}

func TestKubeClientCheckAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"items": []}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	if !client.checkAccess(context.Background(), "test-ns") {
		t.Errorf("expected checkAccess to return true")
	}
}

func TestKubeClientCheckAccessDenied(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := newTestClient(server)
	if client.checkAccess(context.Background(), "test-ns") {
		t.Errorf("expected checkAccess to return false on 403")
	}
}

func TestKubeClientBearerToken(t *testing.T) {
	var gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"items": []}`))
	}))
	defer server.Close()

	client := newKubeClient(server.URL, "my-secret-token", server.Client())
	_, err := client.listConfigMapsWithLabels(context.Background(), "ns", "")
	if err != nil {
		t.Fatalf("listConfigMapsWithLabels failed: %v", err)
	}

	expected := "Bearer my-secret-token"
	if gotAuth != expected {
		t.Errorf("expected Authorization header %q, got %q", expected, gotAuth)
	}
}

func TestInClusterAvailable(t *testing.T) {
	// In test environments, SA token is not present — expect false.
	result := inClusterAvailable()
	if result {
		t.Log("inClusterAvailable returned true — running inside a K8s cluster")
	} else {
		t.Log("inClusterAvailable returned false — expected outside cluster")
	}
}
