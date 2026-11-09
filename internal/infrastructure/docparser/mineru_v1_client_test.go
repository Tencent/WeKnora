package docparser

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMinerUV1413 is a minimal in-memory MinerU 4.1.3 task-mode api-server.
type fakeMinerUV1413 struct {
	t          *testing.T
	apiKey     string
	authHealth bool

	mu             sync.Mutex
	taskPayload    map[string]any
	uploaded       []byte
	putAuth        string
	putContentType string
	pollStatuses   []string // statuses returned by successive GET task calls
	polls          int
	deleteCalls    int
	fileError      *struct{ Code, Message string }
	zipData        []byte
}

func (f *fakeMinerUV1413) handler() http.Handler {
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	authed := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if f.apiKey != "" && r.Header.Get("Authorization") != "Bearer "+f.apiKey {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"error": map[string]string{"message": "bad key", "type": "auth_error", "code": "invalid_api_key"},
				})
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		if f.authHealth && f.apiKey != "" && r.Header.Get("Authorization") != "Bearer "+f.apiKey {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{"message": "bad key", "type": "auth_error", "code": "invalid_api_key"},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": "4.1.3"})
	})
	mux.HandleFunc("GET /v1/tasks", authed(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"tasks": []any{}})
	}))
	mux.HandleFunc("POST /v1/tasks", authed(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		f.mu.Lock()
		f.taskPayload = body
		f.mu.Unlock()
		fileName := ""
		if files, ok := body["files"].([]any); ok && len(files) > 0 {
			if m, ok := files[0].(map[string]any); ok {
				fileName, _ = m["file_name"].(string)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"task_id": "task_1", "status": "waiting",
			"files": []any{map[string]any{
				"file_id": "file-1", "file_name": fileName, "status": "waiting",
			}},
		})
	}))
	mux.HandleFunc("PUT /v1/tasks/task_1/uploads/file-1", authed(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(f.t, err)
		f.mu.Lock()
		f.uploaded = body
		f.putAuth = r.Header.Get("Authorization")
		f.putContentType = r.Header.Get("Content-Type")
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"task_id": "task_1", "file_id": "file-1", "status": "pending",
		})
	}))
	mux.HandleFunc("GET /v1/tasks/task_1", authed(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		status := "completed"
		if len(f.pollStatuses) > 0 {
			idx := f.polls
			if idx >= len(f.pollStatuses) {
				idx = len(f.pollStatuses) - 1
			}
			status = f.pollStatuses[idx]
		}
		f.polls++
		file := map[string]any{"file_id": "file-1", "file_name": "report.pdf", "status": status}
		switch status {
		case "failed":
			if f.fileError != nil {
				file["error"] = map[string]any{"code": f.fileError.Code, "message": f.fileError.Message}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"task_id": "task_1", "status": status, "files": []any{file}})
	}))
	mux.HandleFunc("DELETE /v1/tasks/task_1", authed(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.deleteCalls++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /v1/files/file-1/content", authed(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(f.zipData)
	}))
	return mux
}

func buildMinerUZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func allowLoopbackSSRF(t *testing.T) {
	t.Helper()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
}

func fastV1Client(baseURL, apiKey string) *minerUV1Client {
	c := newMinerUV1Client(baseURL, apiKey, 10*time.Second)
	c.pollInitial = time.Millisecond
	c.pollMax = 5 * time.Millisecond
	return c
}

const v1TestMarkdown = "# Report\n\n![](images/page_0_image_1.png)\n\n" +
	"<table><tr><td><img src=\"images/page_0_table_image_2_1.png\"></td></tr></table>\n"

func v1TestZip(t *testing.T) []byte {
	return buildMinerUZip(t, map[string]string{
		"markdown.md":                          v1TestMarkdown,
		"middle_json.json":                     "{}",
		"images/page_0_image_1.png":            "png-bytes-1",
		"images/page_0_table_image_2_1.png":    "png-bytes-2",
		"images/page_1_image_unreferenced.png": "unused",
	})
}

func TestMinerUV1ClientParseFullFlow(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1413{
		t:            t,
		apiKey:       "server-key",
		pollStatuses: []string{"pending", "running", "completed"},
		zipData:      v1TestZip(t),
	}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	client := fastV1Client(server.URL, "server-key")
	md, refs, err := client.Parse(context.Background(), []byte("%PDF-1.7 body"), "report.pdf",
		minerUV1ParseOptions{Tier: "advanced", OCRMode: "txt"})
	require.NoError(t, err)

	assert.Equal(t, v1TestMarkdown, md)
	require.Len(t, refs, 2)
	byRef := map[string]types.ImageRef{}
	for _, ref := range refs {
		byRef[ref.OriginalRef] = ref
	}
	assert.Equal(t, []byte("png-bytes-1"), byRef["images/page_0_image_1.png"].ImageData)
	assert.Equal(t, []byte("png-bytes-2"), byRef["images/page_0_table_image_2_1.png"].ImageData)

	assert.Equal(t, []byte("%PDF-1.7 body"), fake.uploaded)
	assert.Equal(t, "Bearer server-key", fake.putAuth, "same-origin upload must carry the API key")
	assert.Equal(t, "application/octet-stream", fake.putContentType)
	assert.Equal(t, 3, fake.polls)
	assert.Zero(t, fake.deleteCalls, "a completed task must not be canceled")

	assert.Equal(t, "advanced", fake.taskPayload["tier"])
	assert.Equal(t, "txt", fake.taskPayload["ocr_mode"])
	files := fake.taskPayload["files"].([]any)
	entry := files[0].(map[string]any)
	assert.Equal(t, "report.pdf", entry["file_name"])
	assert.Equal(t, float64(len("%PDF-1.7 body")), entry["bytes"])
}

func TestMinerUV1ClientOmitsTierForServerDefault(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1413{t: t, apiKey: "k", pollStatuses: []string{"completed"}, zipData: v1TestZip(t)}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	_, _, err := fastV1Client(server.URL, "k").Parse(context.Background(), []byte("x"), "a.pdf",
		minerUV1ParseOptions{})
	require.NoError(t, err)
	assert.NotContains(t, fake.taskPayload, "tier")
}

func TestMinerUV1ClientTaskFailedReportsFileError(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1413{
		t:            t,
		apiKey:       "k",
		pollStatuses: []string{"failed"},
		fileError:    &struct{ Code, Message string }{"unsupported_type", "office files need tier>=standard"},
	}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	_, _, err := fastV1Client(server.URL, "k").Parse(context.Background(), []byte("x"), "a.docx",
		minerUV1ParseOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported_type: office files need tier>=standard")
}

func TestMinerUV1ClientCancelsTaskOnTimeout(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1413{
		t:            t,
		apiKey:       "k",
		pollStatuses: []string{"running"}, // never terminal within the budget
	}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	client := fastV1Client(server.URL, "k")
	client.jobTimeout = 40 * time.Millisecond
	_, _, err := client.Parse(context.Background(), []byte("x"), "a.pdf", minerUV1ParseOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
	assert.Equal(t, 1, fake.deleteCalls, "timeout must best-effort cancel the task")
}

func TestMinerUV1ClientAPIErrorMessage(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1413{t: t, apiKey: "server-key"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	_, _, err := fastV1Client(server.URL, "wrong").Parse(context.Background(), []byte("x"), "a.pdf",
		minerUV1ParseOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "invalid_api_key: bad key")
}

func TestDetectMinerUProtocol(t *testing.T) {
	allowLoopbackSSRF(t)
	tests := []struct {
		name       string
		apiKey     string
		requireKey string
		status     int
		body       string
		want       minerUProtocol
		wantErr    string
	}{
		{name: "v1 healthy", status: 200, body: `{"status":"ok","version":"1.0.0"}`, want: minerUProtocolV1},
		{
			name:       "v1 healthy behind gateway with api key",
			apiKey:     "test-key",
			requireKey: "test-key",
			status:     200,
			body:       `{"status":"ok"}`,
			want:       minerUProtocolV1,
		},
		{
			name:       "v1 behind gateway missing api key",
			requireKey: "test-key",
			status:     401,
			body: `{"error":{"message":"Authentication Error, No api key passed in.",` +
				`"type":"auth_error","code":"401"}}`,
			want:    minerUProtocolV1,
			wantErr: "requires authentication, please configure API key",
		},
		{
			name:       "v1 behind gateway invalid api key",
			apiKey:     "bad-key",
			requireKey: "test-key",
			status:     401,
			body:       `{"error":{"message":"Invalid API key","type":"auth_error","code":"401"}}`,
			want:       minerUProtocolV1,
			wantErr:    "401: Invalid API key",
		},
		{name: "legacy 404", status: 404, body: `{"detail":"Not Found"}`, want: minerUProtocolLegacy},
		{name: "legacy 405", status: 405, body: ``, want: minerUProtocolLegacy},
		{
			name:   "v1 preloading models",
			status: 503,
			body: `{"error":{"message":"weights missing","type":"engine_error",` +
				`"code":"model_preload_files_missing"}}`,
			want:    minerUProtocolV1,
			wantErr: "model_preload_files_missing: weights missing",
		},
		{name: "proxy error", status: 502, body: `bad gateway`, wantErr: "status 502"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1/health", r.URL.Path)
				if tt.requireKey != "" {
					auth := r.Header.Get("Authorization")
					if auth != "Bearer "+tt.requireKey {
						w.WriteHeader(http.StatusUnauthorized)
						if tt.body != "" {
							_, _ = w.Write([]byte(tt.body))
						}
						return
					}
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			got, err := detectMinerUProtocol(
				context.Background(), http.DefaultClient, server.URL, tt.apiKey)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMinerUReaderUsesV1WhenAvailable(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1413{t: t, apiKey: "server-key", zipData: v1TestZip(t)}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	reader := NewMinerUReader(map[string]string{
		"mineru_endpoint":       server.URL,
		"mineru_server_api_key": "server-key",
		"mineru_tier":           "Standard",
		"mineru_parse_method":   "ocr",
		// Legacy-only knobs are ignored by the V1 path.
		"mineru_model":          "pipeline",
		"mineru_enable_formula": "false",
	})
	result, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("%PDF"),
		FileName:    "dir/report.pdf",
		FileType:    "pdf",
	})
	require.NoError(t, err)
	require.Empty(t, result.Error)
	assert.True(t, strings.HasPrefix(result.MarkdownContent, "# Report"))
	assert.Len(t, result.ImageRefs, 2)
	assert.Equal(t, "standard", fake.taskPayload["tier"])
	assert.Equal(t, "ocr", fake.taskPayload["ocr_mode"])
}

func TestResolveMinerUTier(t *testing.T) {
	assert.Equal(t, "", resolveMinerUTier(""))
	assert.Equal(t, "flash", resolveMinerUTier(" Flash "))
	assert.Equal(t, "advanced", resolveMinerUTier("advanced"))
	assert.Equal(t, "", resolveMinerUTier("pipeline"), "legacy backend names are not tiers")
	assert.Equal(t, "", resolveMinerUTier("auto"))
}

func TestPingMinerUV1(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1413{t: t, apiKey: "server-key"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	ok, msg := PingMinerU(server.URL, "server-key")
	assert.True(t, ok, msg)

	ok, msg = PingMinerU(server.URL, "wrong")
	assert.False(t, ok)
	assert.Contains(t, msg, "API Key 无效")

	ok, msg = PingMinerU(server.URL, "")
	assert.False(t, ok)
	assert.Contains(t, msg, "配置 API Key")

	// Gateway requiring authentication on /v1/health
	gwFake := &fakeMinerUV1413{t: t, apiKey: "server-key", authHealth: true}
	gwServer := httptest.NewServer(gwFake.handler())
	defer gwServer.Close()

	ok, msg = PingMinerU(gwServer.URL, "server-key")
	assert.True(t, ok, msg)

	ok, msg = PingMinerU(gwServer.URL, "wrong")
	assert.False(t, ok)
	assert.Contains(t, msg, "401")

	open := &fakeMinerUV1413{t: t}
	openServer := httptest.NewServer(open.handler())
	defer openServer.Close()
	ok, msg = PingMinerU(openServer.URL, "")
	assert.True(t, ok, msg)
}

func TestExtractMarkdownZipResolvesEncodedAndNestedPaths(t *testing.T) {
	data := buildMinerUZip(t, map[string]string{
		"report/report.md":        "![](images/%E7%AC%AC%201%20%E9%A1%B5.png)\n![](./images/b.jpg)",
		"report/images/第 1 页.png": "p1",
		"report/images/b.jpg":     "p2",
	})
	md, refs, err := extractMarkdownZip(data, "test")
	require.NoError(t, err)
	assert.Contains(t, md, "images/")
	require.Len(t, refs, 2)
	assert.Equal(t, []byte("p1"), refs[0].ImageData)
	assert.Equal(t, "image/jpeg", refs[1].MimeType)
}

func TestMinerUV1APIError_LongNonJSONBodyStaysValidUTF8(t *testing.T) {
	// 500 bytes into this body falls inside a three-byte character.
	body := []byte("<h1>" + strings.Repeat("服务暂时不可用", 30) + "</h1>")
	require.Greater(t, len(body), 500)

	err := newMinerUV1APIError(http.StatusBadGateway, body)
	assert.True(t, utf8.ValidString(err.Error()), "error is not valid UTF-8: %q", err.Error())
	assert.True(t, strings.HasSuffix(err.msg, "..."))
	assert.LessOrEqual(t, len(err.msg), 500+len("..."))
}

func TestMinerUV1MimeTypeXlsx(t *testing.T) {
	cases := map[string]string{
		"book.xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"BOOK.XLSX": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	}
	for name, want := range cases {
		if got := minerUV1MimeType(name); got != want {
			t.Errorf("minerUV1MimeType(%q) = %q, want %q", name, got, want)
		}
	}
}
