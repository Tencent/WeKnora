package sandbox

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenSandboxClientCreateSandbox(t *testing.T) {
	const wantAPIKey = "test-api-key"
	const fakeSandboxID = "sb-abc123"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sandboxes" {
			t.Errorf("unexpected path: got %s, want /v1/sandboxes", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: got %s, want POST", r.Method)
		}
		gotKey := r.Header.Get("OPEN-SANDBOX-API-KEY")
		if gotKey != wantAPIKey {
			t.Errorf("unexpected API key header: got %q, want %q", gotKey, wantAPIKey)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		// image should be {"uri": "..."}
		img, ok := body["image"].(map[string]any)
		if !ok {
			t.Errorf("expected image to be object, got %T", body["image"])
		} else if img["uri"] != "python:3.11" {
			t.Errorf("unexpected image uri: got %v", img["uri"])
		}
		// entrypoint should be an array
		if _, ok := body["entrypoint"]; !ok {
			t.Error("expected entrypoint in request body")
		}
		// resourceLimits should be present
		if _, ok := body["resourceLimits"]; !ok {
			t.Error("expected resourceLimits in request body")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(openSandboxCreateResponse{ID: fakeSandboxID})
	}))
	defer server.Close()

	client := newOpenSandboxClient(server.URL, wantAPIKey)
	resp, err := client.createSandbox(t.Context(), "python:3.11", 60*time.Second)
	if err != nil {
		t.Fatalf("createSandbox returned error: %v", err)
	}
	if resp.ID != fakeSandboxID {
		t.Errorf("ID: got %q, want %q", resp.ID, fakeSandboxID)
	}
}

func TestOpenSandboxClientGetExecdURL(t *testing.T) {
	const fakeSandboxID = "sb-abc123"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/endpoints/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openSandboxEndpointResponse{
			Endpoint: "localhost:8080/sandboxes/" + fakeSandboxID + "/proxy/44772",
		})
	}))
	defer server.Close()

	client := newOpenSandboxClient(server.URL, "key")
	url, err := client.getExecdURL(t.Context(), fakeSandboxID)
	if err != nil {
		t.Fatalf("getExecdURL returned error: %v", err)
	}
	if !strings.HasPrefix(url, "http://") {
		t.Errorf("expected http:// prefix, got: %s", url)
	}
	if !strings.Contains(url, fakeSandboxID) {
		t.Errorf("expected sandbox ID in URL, got: %s", url)
	}
}

func TestOpenSandboxClientUploadFile(t *testing.T) {
	const wantFilename = "script.py"
	const wantContent = "print('hello')"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files/upload" {
			t.Errorf("unexpected path: got %s, want /files/upload", r.URL.Path)
		}

		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			t.Errorf("expected multipart content-type, got %q", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		mr := multipart.NewReader(r.Body, params["boundary"])
		gotMetadata := false
		gotFile := false
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("read multipart: %v", err)
				break
			}
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "metadata":
				gotMetadata = true
				var meta map[string]string
				if err := json.Unmarshal(data, &meta); err != nil {
					t.Errorf("metadata not valid JSON: %v", err)
				} else if !strings.Contains(meta["path"], wantFilename) {
					t.Errorf("metadata path: got %q, want to contain %q", meta["path"], wantFilename)
				}
			case "file":
				gotFile = true
				if string(data) != wantContent {
					t.Errorf("file content: got %q, want %q", string(data), wantContent)
				}
			}
		}
		if !gotMetadata {
			t.Error("missing metadata part")
		}
		if !gotFile {
			t.Error("missing file part")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newOpenSandboxClient("http://unused", "key")
	err := client.uploadFile(t.Context(), server.URL, wantFilename, wantContent)
	if err != nil {
		t.Fatalf("uploadFile returned error: %v", err)
	}
}

func TestOpenSandboxClientExecuteCommand(t *testing.T) {
	const wantCommand = "python3 /workspace/script.py"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/command" {
			t.Errorf("unexpected path: got %s, want /command", r.URL.Path)
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if body["command"] != wantCommand {
			t.Errorf("command: got %q, want %q", body["command"], wantCommand)
		}

		// Return streaming JSONL response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"type":"init","text":"abc123","timestamp":1000}` + "\n\n"))
		w.Write([]byte(`{"type":"stdout","text":"hello world\n","timestamp":1001}` + "\n\n"))
		w.Write([]byte(`{"type":"execution_complete","execution_time":5,"timestamp":1002}` + "\n\n"))
	}))
	defer server.Close()

	client := newOpenSandboxClient("http://unused", "key")
	resp, err := client.executeCommand(t.Context(), server.URL, wantCommand)
	if err != nil {
		t.Fatalf("executeCommand returned error: %v", err)
	}
	if resp.Stdout != "hello world\n" {
		t.Errorf("Stdout: got %q, want %q", resp.Stdout, "hello world\n")
	}
	if resp.ExitCode != 0 {
		t.Errorf("ExitCode: got %d, want 0", resp.ExitCode)
	}
}

func TestOpenSandboxClientExecuteCommandError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"type":"stderr","text":"error msg","timestamp":1000}` + "\n"))
		w.Write([]byte(`{"type":"error","timestamp":1001,"error":{"ename":"CommandExecError","evalue":"2","traceback":["exit status 2"]}}` + "\n"))
	}))
	defer server.Close()

	client := newOpenSandboxClient("http://unused", "key")
	resp, err := client.executeCommand(t.Context(), server.URL, "bad-cmd")
	if err != nil {
		t.Fatalf("executeCommand returned error: %v", err)
	}
	if resp.Stderr != "error msg" {
		t.Errorf("Stderr: got %q, want %q", resp.Stderr, "error msg")
	}
	if resp.ExitCode != 2 {
		t.Errorf("ExitCode: got %d, want 2", resp.ExitCode)
	}
}

func TestOpenSandboxClientDeleteSandbox(t *testing.T) {
	const wantID = "sb-xyz789"
	const wantAPIKey = "delete-key"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/v1/sandboxes/" + wantID
		if r.URL.Path != wantPath {
			t.Errorf("unexpected path: got %s, want %s", r.URL.Path, wantPath)
		}
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method: got %s, want DELETE", r.Method)
		}
		gotKey := r.Header.Get("OPEN-SANDBOX-API-KEY")
		if gotKey != wantAPIKey {
			t.Errorf("API key header: got %q, want %q", gotKey, wantAPIKey)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newOpenSandboxClient(server.URL, wantAPIKey)
	err := client.deleteSandbox(t.Context(), wantID)
	if err != nil {
		t.Fatalf("deleteSandbox returned error: %v", err)
	}
}

func TestOpenSandboxClientHealthCheck(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				t.Errorf("unexpected path: got %s, want /health", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"healthy"}`))
		}))
		defer server.Close()

		client := newOpenSandboxClient(server.URL, "key")
		if !client.healthCheck(t.Context()) {
			t.Error("healthCheck returned false, want true")
		}
	})

	t.Run("unhealthy", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		client := newOpenSandboxClient(server.URL, "key")
		if client.healthCheck(t.Context()) {
			t.Error("healthCheck returned true, want false")
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		client := newOpenSandboxClient("http://127.0.0.1:1", "key")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if client.healthCheck(ctx) {
			t.Error("healthCheck returned true for unreachable server")
		}
	})
}
