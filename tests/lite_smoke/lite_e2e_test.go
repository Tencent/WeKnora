package lite_smoke_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const embeddingDims = 8

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestLiteEditionEndToEndSmoke boots a real compiled WeKnora-lite server
// subprocess (SQLite + sqlite-vec + FTS5 + local storage + in-memory stream
// manager, zero external services) and exercises the full user journey:
// health/system info -> register & login -> model creation -> KB creation ->
// Markdown upload & async indexing -> hybrid vector/FTS5 search -> streaming
// RAG chat over SSE -> persisted message history -> graceful SIGTERM shutdown.
func TestLiteEditionEndToEndSmoke(t *testing.T) {
	repoRoot := findRepoRoot(t)
	llmSrv := startLoopbackOpenAIServer(t)

	workDir := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(repoRoot, "config"), filepath.Join(workDir, "config")))
	require.NoError(t, os.Symlink(filepath.Join(repoRoot, "migrations"), filepath.Join(workDir, "migrations")))

	binPath := filepath.Join(workDir, "WeKnora-lite")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	ldflags := strings.Join([]string{
		"-X github.com/Tencent/WeKnora/internal/handler.Edition=lite",
		"-X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn",
	}, " ")
	buildCmd := exec.Command(
		"go", "build",
		"-tags", "sqlite_fts5",
		"-ldflags", ldflags,
		"-o", binPath,
		"./cmd/server",
	)
	buildCmd.Dir = repoRoot
	buildCmd.Env = append(os.Environ(),
		"CGO_ENABLED=1",
		"CGO_CFLAGS=-Wno-deprecated-declarations",
	)
	buildOut, err := buildCmd.CombinedOutput()
	require.NoErrorf(t, err, "build WeKnora-lite failed:\n%s", string(buildOut))

	port := allocateFreePort(t)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	var serverLogs syncBuffer
	srvCmd := exec.Command(binPath)
	srvCmd.Dir = workDir
	srvCmd.Stdout = &serverLogs
	srvCmd.Stderr = &serverLogs
	srvCmd.Env = append(os.Environ(),
		"ENV_FILE=",
		"REDIS_ADDR=",
		"NO_PROXY=127.0.0.1,localhost",
		"no_proxy=127.0.0.1,localhost",
		"GIN_MODE=debug",
		"LOG_LEVEL=info",
		"SERVER_HOST=127.0.0.1",
		"SERVER_PORT="+strconv.Itoa(port),
		"DB_DRIVER=sqlite",
		"DB_PATH="+filepath.Join(workDir, "data", "weknora.db"),
		"RETRIEVE_DRIVER=sqlite",
		"STORAGE_TYPE=local",
		"LOCAL_STORAGE_BASE_DIR="+filepath.Join(workDir, "data", "files"),
		"STREAM_MANAGER_TYPE=memory",
		"JWT_SECRET=lite-smoke-jwt-secret-at-least-32-bytes!!",
		"SYSTEM_AES_KEY=0123456789abcdef0123456789abcdef",
		"NEO4J_ENABLE=false",
		"WEKNORA_SANDBOX_MODE=disabled",
		"ENABLE_GRAPH_RAG=false",
		"DISABLE_REGISTRATION=false",
		"SSRF_WHITELIST=127.0.0.1,localhost",
		"CONCURRENCY_POOL_SIZE=2",
	)
	require.NoError(t, srvCmd.Start())

	var waitErr error
	done := make(chan struct{})
	go func() {
		waitErr = srvCmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			if srvCmd.Process != nil {
				_ = srvCmd.Process.Signal(syscall.SIGTERM)
			}
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				if srvCmd.Process != nil {
					_ = srvCmd.Process.Kill()
				}
				<-done
			}
		}
		if t.Failed() {
			t.Logf("WeKnora-lite server logs:\n%s", serverLogs.String())
		}
	})

	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
	waitForHealth(t, client, baseURL, done, func() error { return waitErr }, &serverLogs)

	// 1. Register + login user and verify Lite edition metadata.
	const (
		email    = "smoke@example.com"
		password = "SmokeTestPassword!2026"
	)
	doJSON(t, client, http.MethodPost, baseURL+"/api/v1/auth/register", "", map[string]any{
		"username": "smoke-admin",
		"email":    email,
		"password": password,
	}, http.StatusCreated, nil)

	var loginResp struct {
		Success      bool   `json:"success"`
		Token        string `json:"token"`
		ActiveTenant *struct {
			ID uint64 `json:"id"`
		} `json:"active_tenant"`
	}
	doJSON(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", "", map[string]any{
		"email":    email,
		"password": password,
	}, http.StatusOK, &loginResp)
	require.True(t, loginResp.Success)
	require.NotEmpty(t, loginResp.Token)
	require.NotNil(t, loginResp.ActiveTenant)
	require.NotZero(t, loginResp.ActiveTenant.ID)
	token := loginResp.Token

	var sysInfo struct {
		Success bool `json:"success"`
		Data    struct {
			Edition string `json:"edition"`
		} `json:"data"`
	}
	doJSON(t, client, http.MethodGet, baseURL+"/api/v1/system/info", token, nil, http.StatusOK, &sysInfo)
	require.Equal(t, "lite", sysInfo.Data.Edition)

	// 2. Register loopback OpenAI-compatible Embedding and KnowledgeQA models.
	embedModelID := createModel(t, client, baseURL, token, map[string]any{
		"name":         "smoke-embed-v1",
		"display_name": "Smoke Embedding",
		"type":         "Embedding",
		"source":       "remote",
		"parameters": map[string]any{
			"provider":   "openai",
			"base_url":   llmSrv.URL + "/v1",
			"api_key":    "sk-smoke-local",
			"interfaces": []string{"embeddings"},
			"embedding_parameters": map[string]any{
				"dimension": embeddingDims,
			},
		},
	})

	chatModelID := createModel(t, client, baseURL, token, map[string]any{
		"name":         "smoke-chat-v1",
		"display_name": "Smoke Chat",
		"type":         "KnowledgeQA",
		"source":       "remote",
		"parameters": map[string]any{
			"provider":   "openai",
			"base_url":   llmSrv.URL + "/v1",
			"api_key":    "sk-smoke-local",
			"interfaces": []string{"chat"},
		},
	})

	// 3. Create Knowledge Base bound to both models.
	var kbResp struct {
		Success bool `json:"success"`
		Data    struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	doJSON(t, client, http.MethodPost, baseURL+"/api/v1/knowledge-bases", token, map[string]any{
		"name":               "Lite Smoke KB",
		"description":        "End-to-end Lite smoke verification",
		"embedding_model_id": embedModelID,
		"summary_model_id":   chatModelID,
		"chunking_config": map[string]any{
			"chunk_size":    300,
			"chunk_overlap": 30,
			"separators":    []string{"\n\n", "\n"},
		},
	}, http.StatusCreated, &kbResp)
	kbID := kbResp.Data.ID
	require.NotEmpty(t, kbID)

	// 4. Upload a Markdown document containing both English & CJK tokens so
	// both sqlite-vec vector search and FTS5 bigram keyword search are exercised.
	docContent := strings.Join([]string{
		"# Aurora Borealis Deployment Guide",
		"",
		"Codename **Helios-7492** configures the zero-dependency Lite retrieval engine.",
		"离线单二进制架构内置 SQLite 向量检索与 FTS5 双字分词全文索引，无需外部容器依赖。",
	}, "\n")
	knowledgeID := uploadMarkdownDocument(t, client, baseURL, token, kbID, "helios-guide.md", docContent)
	waitKnowledgeIndexed(t, client, baseURL, token, knowledgeID, 45*time.Second)

	// 5. Verify hybrid search finds the indexed chunk for both English and CJK queries.
	rerankOff := false
	for _, query := range []string{"Helios-7492", "双字分词全文索引"} {
		var searchResp struct {
			Success bool `json:"success"`
			Data    []struct {
				ID      string `json:"id"`
				Content string `json:"content"`
			} `json:"data"`
		}
		doJSON(t, client, http.MethodPost, baseURL+"/api/v1/knowledge-search", token, map[string]any{
			"query":              query,
			"knowledge_base_ids": []string{kbID},
			"rerank":             map[string]any{"enabled": rerankOff},
		}, http.StatusOK, &searchResp)
		require.True(t, searchResp.Success, "search query=%q", query)
		require.NotEmpty(t, searchResp.Data, "expected indexed chunk for query=%q", query)
		require.Contains(t, searchResp.Data[0].Content, "Helios-7492")
	}

	// 6. Create conversation session and stream a full RAG answer via SSE.
	var sessResp struct {
		Success bool `json:"success"`
		Data    struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	doJSON(t, client, http.MethodPost, baseURL+"/api/v1/sessions", token, map[string]any{
		"title": "Lite Smoke Session",
	}, http.StatusCreated, &sessResp)
	sessionID := sessResp.Data.ID
	require.NotEmpty(t, sessionID)

	answer := streamKnowledgeQA(t, client, baseURL, token, sessionID, map[string]any{
		"query":              "What codename configures the Lite retrieval engine?",
		"knowledge_base_ids": []string{kbID},
		"summary_model_id":   chatModelID,
		"disable_title":      true,
	})
	require.Contains(t, answer, "Helios-7492")

	// 7. Verify assistant reply was persisted to SQLite message history.
	var msgResp struct {
		Success bool `json:"success"`
		Data    []struct {
			Role        string `json:"role"`
			Content     string `json:"content"`
			IsCompleted bool   `json:"is_completed"`
		} `json:"data"`
	}
	messagesURL := baseURL + "/api/v1/messages/" + sessionID + "/load?limit=10"
	doJSON(t, client, http.MethodGet, messagesURL, token, nil, http.StatusOK, &msgResp)
	require.GreaterOrEqual(t, len(msgResp.Data), 2, "expected user + assistant messages persisted")

	// 8. Graceful shutdown on SIGTERM must exit cleanly (code 0).
	require.NoError(t, srvCmd.Process.Signal(syscall.SIGTERM))
	select {
	case <-done:
		require.NoErrorf(t, waitErr, "WeKnora-lite did not exit cleanly on SIGTERM:\n%s", serverLogs.String())
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for WeKnora-lite graceful shutdown:\n%s", serverLogs.String())
	}
}

func startLoopbackOpenAIServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
			Input any    `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var inputs []string
		switch v := req.Input.(type) {
		case string:
			inputs = []string{v}
		case []any:
			for _, item := range v {
				inputs = append(inputs, fmt.Sprint(item))
			}
		default:
			inputs = []string{fmt.Sprint(v)}
		}

		type embItem struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		data := make([]embItem, len(inputs))
		for i, text := range inputs {
			data[i] = embItem{
				Object:    "embedding",
				Index:     i,
				Embedding: deterministicEmbedding(text),
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"model":  req.Model,
			"data":   data,
			"usage":  map[string]int{"prompt_tokens": 8, "total_tokens": 8},
		})
	})

	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		reply := "Helios-7492 configures the zero-dependency WeKnora Lite retrieval engine."
		for _, m := range req.Messages {
			text := fmt.Sprint(m.Content)
			if strings.Contains(text, "rewrite") || strings.Contains(text, "改写") {
				reply = "What codename configures Helios-7492?"
			}
		}

		if !req.Stream {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl-smoke",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   req.Model,
				"choices": []map[string]any{{
					"index":         0,
					"finish_reason": "stop",
					"message":       map[string]string{"role": "assistant", "content": reply},
				}},
			})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		for _, part := range []string{"Helios-7492 ", "configures WeKnora Lite."} {
			chunk, _ := json.Marshal(map[string]any{
				"id":      "chatcmpl-smoke",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   req.Model,
				"choices": []map[string]any{{
					"index": 0,
					"delta": map[string]string{"content": part},
				}},
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		doneChunk, _ := json.Marshal(map[string]any{
			"id":      "chatcmpl-smoke",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   req.Model,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         map[string]string{},
				"finish_reason": "stop",
			}},
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", doneChunk)
		if flusher != nil {
			flusher.Flush()
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func deterministicEmbedding(text string) []float32 {
	vec := make([]float32, embeddingDims)
	vec[0] = 0.75
	lower := strings.ToLower(text)
	if strings.Contains(lower, "helios") || strings.Contains(lower, "lite") {
		vec[1] = 0.65
	}
	if strings.Contains(text, "双字") || strings.Contains(text, "索引") {
		vec[2] = 0.65
	}
	return vec
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "could not locate repository root")
		dir = parent
	}
}

func allocateFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForHealth(
	t *testing.T, client *http.Client, baseURL string, done <-chan struct{}, waitErr func() error, logs *syncBuffer,
) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatalf("WeKnora-lite exited before becoming healthy (%v):\n%s", waitErr(), logs.String())
		default:
		}
		resp, err := client.Get(baseURL + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("WeKnora-lite health check timed out:\n%s", logs.String())
}

func createModel(t *testing.T, client *http.Client, baseURL, token string, payload map[string]any) string {
	t.Helper()
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	doJSON(t, client, http.MethodPost, baseURL+"/api/v1/models", token, payload, http.StatusCreated, &resp)
	require.True(t, resp.Success)
	require.NotEmpty(t, resp.Data.ID)
	return resp.Data.ID
}

func uploadMarkdownDocument(
	t *testing.T, client *http.Client, baseURL, token, kbID, filename, content string,
) string {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = io.WriteString(part, content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		fmt.Sprintf("%s/api/v1/knowledge-bases/%s/knowledge/file", baseURL, kbID),
		&body,
	)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "upload document status=%d body=%s", resp.StatusCode, string(raw))

	var out struct {
		Success bool `json:"success"`
		Data    struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.NotEmpty(t, out.Data.ID)
	return out.Data.ID
}

func waitKnowledgeIndexed(
	t *testing.T, client *http.Client, baseURL, token, knowledgeID string, timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				ParseStatus  string `json:"parse_status"`
				ErrorMessage string `json:"error_message"`
			} `json:"data"`
		}
		doJSON(t, client, http.MethodGet, baseURL+"/api/v1/knowledge/"+knowledgeID, token, nil, http.StatusOK, &resp)
		switch resp.Data.ParseStatus {
		case "completed":
			return
		case "failed":
			t.Fatalf("document indexing failed: %s", resp.Data.ErrorMessage)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for knowledge %s to finish indexing", knowledgeID)
}

func streamKnowledgeQA(
	t *testing.T, client *http.Client, baseURL, token, sessionID string, payload map[string]any,
) string {
	t.Helper()
	rawBody, err := json.Marshal(payload)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		fmt.Sprintf("%s/api/v1/knowledge-chat/%s", baseURL, sessionID),
		bytes.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("knowledge-chat status=%d body=%s", resp.StatusCode, string(body))
	}

	var answer strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev struct {
			ResponseType string `json:"response_type"`
			Content      string `json:"content"`
		}
		require.NoErrorf(t, json.Unmarshal([]byte(data), &ev), "parse SSE payload: %s", data)
		if ev.ResponseType == "error" {
			t.Fatalf("SSE stream returned error event: %s", ev.Content)
		}
		if ev.ResponseType == "answer" {
			answer.WriteString(ev.Content)
		}
	}
	require.NoError(t, scanner.Err())
	return answer.String()
}

func doJSON(
	t *testing.T, client *http.Client, method, url, token string, body any, wantStatus int, out any,
) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	respBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(
		t, wantStatus, resp.StatusCode,
		"%s %s returned %d: %s", method, url, resp.StatusCode, string(respBytes),
	)
	if out != nil {
		require.NoErrorf(t, json.Unmarshal(respBytes, out), "decode %s %s: %s", method, url, string(respBytes))
	}
}
