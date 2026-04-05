package sandbox

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenSandboxSandboxType(t *testing.T) {
	s := NewOpenSandboxSandbox(nil)
	if got := s.Type(); got != SandboxTypeOpenSandbox {
		t.Errorf("Type() = %q, want %q", got, SandboxTypeOpenSandbox)
	}
}

func TestOpenSandboxSandboxExecute(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	const fakeSandboxID = "sb-test-001"
	const wantStdout = "hello from opensandbox\n"
	const wantExitCode = 0

	// Server 2: execd server (handles file upload, command execution, and ping)
	execdServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ping" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)

		case r.URL.Path == "/files/upload" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)

		case r.URL.Path == "/command" && r.Method == http.MethodPost:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("execd: decode command body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			cmd := body["command"]
			if !strings.Contains(cmd, "/workspace/") {
				t.Errorf("execd: command %q does not reference /workspace/", cmd)
			}
			// Return streaming JSONL response
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"type":"stdout","text":%q,"timestamp":1000}`+"\n\n", wantStdout)
			fmt.Fprintf(w, `{"type":"execution_complete","execution_time":5,"timestamp":1001}`+"\n\n")

		default:
			t.Errorf("execd: unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer execdServer.Close()

	// Server 1: lifecycle server (handles sandbox create/delete/endpoints)
	lifecycleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/sandboxes" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(openSandboxCreateResponse{ID: fakeSandboxID})

		case strings.Contains(r.URL.Path, "/endpoints/") && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			// Return execd server URL as endpoint (strip http:// prefix since client adds it back)
			ep := strings.TrimPrefix(execdServer.URL, "http://")
			json.NewEncoder(w).Encode(openSandboxEndpointResponse{Endpoint: ep})

		case strings.HasPrefix(r.URL.Path, "/v1/sandboxes/") && r.Method == http.MethodDelete:
			gotID := strings.TrimPrefix(r.URL.Path, "/v1/sandboxes/")
			if gotID != fakeSandboxID {
				t.Errorf("lifecycle: delete called with id %q, want %q", gotID, fakeSandboxID)
			}
			w.WriteHeader(http.StatusNoContent)

		default:
			t.Errorf("lifecycle: unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer lifecycleServer.Close()

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "test_script.py")
	if err := os.WriteFile(scriptPath, []byte("print('hello from opensandbox')"), 0644); err != nil {
		t.Fatalf("write temp script: %v", err)
	}

	cfg := DefaultConfig()
	cfg.OpenSandboxAPIURL = lifecycleServer.URL
	cfg.OpenSandboxAPIKey = "test-key"

	sb := NewOpenSandboxSandbox(cfg)

	result, err := sb.Execute(t.Context(), &ExecuteConfig{
		Script: scriptPath,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if result.Stdout != wantStdout {
		t.Errorf("Stdout: got %q, want %q", result.Stdout, wantStdout)
	}
	if result.ExitCode != wantExitCode {
		t.Errorf("ExitCode: got %d, want %d", result.ExitCode, wantExitCode)
	}
}
