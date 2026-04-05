package sandbox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKubernetesSandboxType(t *testing.T) {
	s := NewKubernetesSandbox(nil, nil)
	t.Cleanup(func() { s.Cleanup(context.Background()) })
	if s.Type() != SandboxTypeKubernetes {
		t.Errorf("expected Type() = %q, got %q", SandboxTypeKubernetes, s.Type())
	}
}

func TestKubernetesSandboxScriptSizeValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxScriptSize = 100 // very small limit

	s := NewKubernetesSandbox(cfg, nil)
	t.Cleanup(func() { s.Cleanup(context.Background()) })

	// Create a script larger than MaxScriptSize
	largeContent := strings.Repeat("x", 200)

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "big.py")
	if err := os.WriteFile(scriptPath, []byte(largeContent), 0644); err != nil {
		t.Fatalf("failed to write temp script: %v", err)
	}

	ctx := context.Background()
	_, err := s.Execute(ctx, &ExecuteConfig{
		Script: scriptPath,
	})

	if err == nil {
		t.Fatal("expected error for oversized script, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("expected 'exceeds limit' in error message, got: %v", err)
	}
}

func TestKubernetesSandboxExecute(t *testing.T) {
	const mockLogOutput = "hello from kubernetes sandbox\n"
	const jobName = "test-sandbox-job"

	// Track which paths were called
	type call struct {
		method string
		path   string
	}
	var calls []call

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, call{r.Method, r.URL.Path})

		switch {
		// POST configmaps
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/configmaps"):
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))

		// POST jobs
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/jobs"):
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))

		// GET job status
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/jobs/"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": {"succeeded": 1, "failed": 0, "active": 0}}`))

		// GET pods (find job pod via label selector)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods") && !strings.Contains(r.URL.Path, "/log"):
			w.WriteHeader(http.StatusOK)
			resp := map[string]any{
				"items": []map[string]any{
					{"metadata": map[string]any{"name": "sandbox-pod-abc"}},
				},
			}
			data, _ := json.Marshal(resp)
			w.Write(data)

		// GET pod logs
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/log"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(mockLogOutput))

		// DELETE jobs
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/jobs/"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))

		// DELETE configmaps
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/configmaps/"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))

		// Access check (list configmaps with limit=1)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/configmaps") && strings.Contains(r.URL.RawQuery, "limit=1"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"items": []}`))

		default:
			t.Logf("unhandled request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newKubeClient(server.URL, "test-token", server.Client())

	cfg := DefaultConfig()
	cfg.KubeNamespace = "test-ns"
	cfg.MaxConcurrentSandboxes = 2

	s := NewKubernetesSandbox(cfg, client)
	t.Cleanup(func() { s.Cleanup(context.Background()) })

	// Create a temp script file
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "test.py")
	scriptContent := `print("hello from kubernetes sandbox")`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatalf("failed to write temp script: %v", err)
	}

	ctx := context.Background()
	result, err := s.Execute(ctx, &ExecuteConfig{
		Script: scriptPath,
	})

	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}
	if result.Stdout != mockLogOutput {
		t.Errorf("expected Stdout %q, got %q", mockLogOutput, result.Stdout)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected ExitCode=0, got %d", result.ExitCode)
	}

	// Verify at minimum: ConfigMap was created, Job was created, Job was polled, logs were fetched
	foundCMCreate := false
	foundJobCreate := false
	foundLogFetch := false
	for _, c := range calls {
		if c.method == http.MethodPost && strings.Contains(c.path, "/configmaps") {
			foundCMCreate = true
		}
		if c.method == http.MethodPost && strings.Contains(c.path, "/jobs") {
			foundJobCreate = true
		}
		if c.method == http.MethodGet && strings.Contains(c.path, "/log") {
			foundLogFetch = true
		}
	}
	if !foundCMCreate {
		t.Error("expected ConfigMap creation call")
	}
	if !foundJobCreate {
		t.Error("expected Job creation call")
	}
	if !foundLogFetch {
		t.Error("expected pod log fetch call")
	}
}

func TestKubernetesSandboxExecuteWithScriptContent(t *testing.T) {
	const mockLogOutput = "content-based execution\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/configmaps"):
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/jobs"):
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/jobs/"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": {"succeeded": 1, "failed": 0, "active": 0}}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods") && !strings.Contains(r.URL.Path, "/log"):
			w.WriteHeader(http.StatusOK)
			resp := map[string]any{
				"items": []map[string]any{
					{"metadata": map[string]any{"name": "pod-xyz"}},
				},
			}
			data, _ := json.Marshal(resp)
			w.Write(data)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/log"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(mockLogOutput))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newKubeClient(server.URL, "token", server.Client())
	s := NewKubernetesSandbox(nil, client)
	t.Cleanup(func() { s.Cleanup(context.Background()) })

	ctx := context.Background()
	result, err := s.Execute(ctx, &ExecuteConfig{
		Script:        "myscript.sh",
		ScriptContent: `echo "content-based execution"`,
	})

	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Stdout != mockLogOutput {
		t.Errorf("expected Stdout %q, got %q", mockLogOutput, result.Stdout)
	}
}

func TestKubernetesSandboxExecuteJobFailed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/configmaps"):
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/jobs"):
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/jobs/"):
			// Job failed
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": {"succeeded": 0, "failed": 1, "active": 0}}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods") && !strings.Contains(r.URL.Path, "/log"):
			w.WriteHeader(http.StatusOK)
			resp := map[string]any{
				"items": []map[string]any{
					{"metadata": map[string]any{"name": "failed-pod"}},
				},
			}
			data, _ := json.Marshal(resp)
			w.Write(data)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/log"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("error output\n"))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newKubeClient(server.URL, "token", server.Client())
	s := NewKubernetesSandbox(nil, client)
	t.Cleanup(func() { s.Cleanup(context.Background()) })

	ctx := context.Background()
	result, err := s.Execute(ctx, &ExecuteConfig{
		Script:        "script.py",
		ScriptContent: `raise Exception("fail")`,
	})

	if err != nil {
		t.Fatalf("Execute should not return Go error for failed jobs, got: %v", err)
	}
	if result.ExitCode != 1 {
		t.Errorf("expected ExitCode=1 for failed job, got %d", result.ExitCode)
	}
}

func TestKubernetesSandboxExecuteWithArgsAndStdin(t *testing.T) {
	var capturedJobBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/configmaps"):
			// Verify stdin file is included in ConfigMap data
			body, _ := io.ReadAll(r.Body)
			var cm map[string]any
			json.Unmarshal(body, &cm)
			data, _ := cm["data"].(map[string]any)
			if _, ok := data[".stdin"]; !ok {
				t.Error("expected .stdin key in ConfigMap data")
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/jobs"):
			capturedJobBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/jobs/"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": {"succeeded": 1, "failed": 0, "active": 0}}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods") && !strings.Contains(r.URL.Path, "/log"):
			w.WriteHeader(http.StatusOK)
			resp := map[string]any{"items": []map[string]any{{"metadata": map[string]any{"name": "pod-1"}}}}
			data, _ := json.Marshal(resp)
			w.Write(data)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/log"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("output\n"))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newKubeClient(server.URL, "token", server.Client())
	s := NewKubernetesSandbox(nil, client)
	t.Cleanup(func() { s.Cleanup(context.Background()) })

	_, err := s.Execute(context.Background(), &ExecuteConfig{
		Script:        "script.py",
		ScriptContent: `import sys; print(sys.stdin.read())`,
		Args:          []string{"--format", "json"},
		Stdin:         `{"key": "value"}`,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	// Verify the Job command uses sh -c with stdin piping
	var job map[string]any
	json.Unmarshal(capturedJobBody, &job)
	spec := job["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	containers := spec["containers"].([]any)
	container := containers[0].(map[string]any)
	command := container["command"].([]any)

	if len(command) != 3 || command[0] != "sh" || command[1] != "-c" {
		t.Fatalf("expected sh -c command for stdin mode, got: %v", command)
	}
	cmdStr := command[2].(string)
	if !strings.Contains(cmdStr, ".stdin") {
		t.Errorf("expected .stdin in command, got: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "--format") {
		t.Errorf("expected --format arg in command, got: %s", cmdStr)
	}
}

func TestKubernetesSandboxExecuteExecFormWithoutStdin(t *testing.T) {
	var capturedJobBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/configmaps"):
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/jobs"):
			capturedJobBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/jobs/"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status": {"succeeded": 1, "failed": 0, "active": 0}}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pods") && !strings.Contains(r.URL.Path, "/log"):
			w.WriteHeader(http.StatusOK)
			resp := map[string]any{"items": []map[string]any{{"metadata": map[string]any{"name": "pod-1"}}}}
			data, _ := json.Marshal(resp)
			w.Write(data)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/log"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok\n"))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newKubeClient(server.URL, "token", server.Client())
	s := NewKubernetesSandbox(nil, client)
	t.Cleanup(func() { s.Cleanup(context.Background()) })

	_, err := s.Execute(context.Background(), &ExecuteConfig{
		Script:        "script.py",
		ScriptContent: `print("ok")`,
		Args:          []string{"--verbose"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	// Verify exec-form (no shell wrapping) when there's no stdin
	var job map[string]any
	json.Unmarshal(capturedJobBody, &job)
	spec := job["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	containers := spec["containers"].([]any)
	container := containers[0].(map[string]any)
	command := container["command"].([]any)

	// Should be exec-form: ["python3", "/workspace/script.py", "--verbose"]
	if len(command) < 3 {
		t.Fatalf("expected at least 3 elements in exec-form command, got: %v", command)
	}
	if command[0] == "sh" {
		t.Errorf("expected exec-form (no shell) without stdin, got sh -c: %v", command)
	}
	if command[0] != "python3" {
		t.Errorf("expected python3 interpreter, got: %v", command[0])
	}
	found := false
	for _, c := range command {
		if c == "--verbose" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected --verbose in command args, got: %v", command)
	}
}

func TestKubernetesSandboxCleanup(t *testing.T) {
	s := NewKubernetesSandbox(nil, nil)
	if err := s.Cleanup(context.Background()); err != nil {
		t.Errorf("Cleanup should return nil, got: %v", err)
	}
}
