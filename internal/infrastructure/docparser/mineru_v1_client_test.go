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

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMinerUV1 is a minimal in-memory MinerU 4.0 api-server.
type fakeMinerUV1 struct {
	t      *testing.T
	apiKey string

	mu            sync.Mutex
	uploadURL     string // absolute or relative; defaults to same-origin relative path
	dedupHit      bool   // POST /v1/uploads answers "completed" directly
	uploaded      []byte
	putAuth       string
	uploadMime    string
	jobPayload    map[string]any
	pollStatuses  []string // statuses returned by successive GET job calls
	polls         int
	fileError     *map[string]string
	canceled      bool
	zipData       []byte
	downloadCalls int
}

func (f *fakeMinerUV1) handler() http.Handler {
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

	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": "1.0.0"})
	})
	mux.HandleFunc("POST /v1/uploads", authed(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		f.mu.Lock()
		defer f.mu.Unlock()
		f.uploadMime, _ = body["mime_type"].(string)
		if f.dedupHit {
			writeJSON(w, http.StatusOK, map[string]any{
				"id": "upload_1", "status": "completed", "file": map[string]any{"id": "file-src"},
			})
			return
		}
		uploadURL := f.uploadURL
		if uploadURL == "" {
			uploadURL = "/v1/uploads/upload_1/content"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": "upload_1", "status": "pending", "upload_url": uploadURL, "upload_method": "PUT",
			"upload_headers": map[string]string{"Content-Type": f.uploadMime},
		})
	}))
	mux.HandleFunc("PUT /v1/uploads/upload_1/content", f.handlePut)
	mux.HandleFunc("POST /v1/uploads/upload_1/complete", authed(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": "upload_1", "status": "completed", "file": map[string]any{"id": "file-src"},
		})
	}))
	mux.HandleFunc("POST /v1/parse/jobs", authed(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
		f.mu.Lock()
		f.jobPayload = body
		f.mu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]any{"job_id": "job_1", "status": "queued"})
	}))
	mux.HandleFunc("GET /v1/parse/jobs/job_1", authed(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		status := "completed"
		if f.polls < len(f.pollStatuses) {
			status = f.pollStatuses[f.polls]
		}
		f.polls++
		file := map[string]any{"name": "report.pdf", "status": status}
		switch status {
		case "completed":
			file["output_files"] = map[string]any{"zip": map[string]any{"file_id": "file-zip", "bytes": len(f.zipData)}}
		case "failed":
			file["error"] = *f.fileError
		}
		writeJSON(w, http.StatusOK, map[string]any{"job_id": "job_1", "status": status, "files": []any{file}})
	}))
	mux.HandleFunc("DELETE /v1/parse/jobs/job_1", authed(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.canceled = true
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"job_id": "job_1", "status": "canceled"})
	}))
	mux.HandleFunc("GET /v1/parse/jobs", authed(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": []any{}})
	}))
	mux.HandleFunc("GET /v1/files/file-zip/content", authed(func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.downloadCalls++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(f.zipData)
	}))
	return mux
}

func (f *fakeMinerUV1) handlePut(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(r.Body)
	require.NoError(f.t, err)
	f.mu.Lock()
	f.uploaded = data
	f.putAuth = r.Header.Get("Authorization")
	f.mu.Unlock()
	w.WriteHeader(http.StatusOK)
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
	fake := &fakeMinerUV1{
		t:            t,
		apiKey:       "server-key",
		pollStatuses: []string{"queued", "running", "completed"},
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
	assert.Equal(t, "image/png", byRef["images/page_0_image_1.png"].MimeType)

	assert.Equal(t, []byte("%PDF-1.7 body"), fake.uploaded)
	assert.Equal(t, "Bearer server-key", fake.putAuth, "same-origin upload must carry the API key")
	assert.Equal(t, "application/pdf", fake.uploadMime)
	assert.Equal(t, 3, fake.polls)
	assert.False(t, fake.canceled)

	assert.Equal(t, "advanced", fake.jobPayload["tier"])
	assert.Equal(t, "txt", fake.jobPayload["ocr_mode"])
	assert.Equal(t, []any{"zip"}, fake.jobPayload["output_formats"])
	files := fake.jobPayload["files"].([]any)
	source := files[0].(map[string]any)["source"].(map[string]any)
	assert.Equal(t, map[string]any{"type": "file_id", "file_id": "file-src"}, source)
}

func TestMinerUV1ClientOmitsTierForServerDefault(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1{t: t, zipData: v1TestZip(t)}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	_, _, err := fastV1Client(server.URL, "").Parse(context.Background(), []byte("x"), "a.pdf",
		minerUV1ParseOptions{OCRMode: "auto"})
	require.NoError(t, err)
	_, hasTier := fake.jobPayload["tier"]
	assert.False(t, hasTier)
}

func TestMinerUV1ClientCrossOriginUploadOmitsAPIKey(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1{t: t, apiKey: "server-key", zipData: v1TestZip(t)}

	// A different port on the same host is a different origin, like a
	// pre-signed object-storage URL handed out by the official API.
	storage := httptest.NewServer(http.HandlerFunc(fake.handlePut))
	defer storage.Close()
	fake.uploadURL = storage.URL + "/v1/uploads/upload_1/content?Signature=abc"

	server := httptest.NewServer(fake.handler())
	defer server.Close()

	_, _, err := fastV1Client(server.URL, "server-key").Parse(context.Background(), []byte("bytes"), "a.pdf",
		minerUV1ParseOptions{})
	require.NoError(t, err)
	assert.Equal(t, []byte("bytes"), fake.uploaded)
	assert.Empty(t, fake.putAuth, "API key must not leak to a cross-origin upload URL")
}

func TestMinerUV1ClientDedupSkipsByteUpload(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1{t: t, dedupHit: true, zipData: v1TestZip(t)}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	_, _, err := fastV1Client(server.URL, "").Parse(context.Background(), []byte("bytes"), "a.pdf",
		minerUV1ParseOptions{})
	require.NoError(t, err)
	assert.Nil(t, fake.uploaded)
}

func TestMinerUV1ClientFailedJobReportsFileError(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1{
		t:            t,
		pollStatuses: []string{"failed"},
		fileError:    &map[string]string{"code": "parse_failed", "message": "corrupt pdf"},
	}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	_, _, err := fastV1Client(server.URL, "").Parse(context.Background(), []byte("x"), "a.pdf",
		minerUV1ParseOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse_failed: corrupt pdf")
	assert.Zero(t, fake.downloadCalls)
}

func TestMinerUV1ClientCancelsJobOnTimeout(t *testing.T) {
	allowLoopbackSSRF(t)
	statuses := make([]string, 1000)
	for i := range statuses {
		statuses[i] = "running"
	}
	fake := &fakeMinerUV1{t: t, pollStatuses: statuses}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	client := fastV1Client(server.URL, "")
	client.jobTimeout = 100 * time.Millisecond
	_, _, err := client.Parse(context.Background(), []byte("x"), "a.pdf", minerUV1ParseOptions{})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, fake.canceled, "abandoned job should be canceled on the server")
}

func TestMinerUV1ClientAPIErrorMessage(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUV1{t: t, apiKey: "server-key"}
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
		name    string
		status  int
		body    string
		want    minerUProtocol
		wantErr string
	}{
		{name: "v1 healthy", status: 200, body: `{"status":"ok","version":"1.0.0"}`, want: minerUProtocolV1},
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
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			got, err := detectMinerUProtocol(context.Background(), http.DefaultClient, server.URL)
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
	fake := &fakeMinerUV1{t: t, apiKey: "server-key", zipData: v1TestZip(t)}
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
	assert.Equal(t, "standard", fake.jobPayload["tier"])
	assert.Equal(t, "ocr", fake.jobPayload["ocr_mode"])
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
	fake := &fakeMinerUV1{t: t, apiKey: "server-key"}
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

	open := &fakeMinerUV1{t: t}
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
