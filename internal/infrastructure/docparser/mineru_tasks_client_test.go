package docparser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMinerUTasks follows the published MinerU 4.1.0 single-file contract.
// In particular it deliberately has no standalone uploads or Jobs routes.
type fakeMinerUTasks struct {
	t          *testing.T
	prefix     string
	cacheHit   bool
	uploadURL  string
	resultURL  string
	fileError  bool
	running    bool
	zipData    []byte
	mu         sync.Mutex
	manifest   map[string]any
	puts       int
	polls      int
	cancels    int
	downloads  int
	uploaded   []byte
	unexpected []string
}

func (f *fakeMinerUTasks) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSON := func(status int, body any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			assert.NoError(f.t, json.NewEncoder(w).Encode(body))
		}
		if r.URL.Path == f.prefix+"/v1/health" {
			writeJSON(http.StatusOK, map[string]any{"status": "ok", "version": "4.1.4"})
			return
		}
		assert.Equal(f.t, "Bearer server-key", r.Header.Get("Authorization"), r.URL.Path)
		switch r.Method + " " + strings.TrimPrefix(r.URL.Path, f.prefix) {
		case "POST /v1/tasks":
			assert.Equal(f.t, "application/json", r.Header.Get("Content-Type"))
			assert.NoError(f.t, json.NewDecoder(r.Body).Decode(&f.manifest))
			file := map[string]any{"file_id": "file_1", "file_name": "report.pdf", "status": "pending"}
			status := "pending"
			if !f.cacheHit {
				status = "waiting-file"
				file["status"] = status
				file["upload"] = map[string]any{
					"url":     stringOr(f.uploadURL, f.prefix+"/v1/tasks/task_1/uploads/file_1"),
					"headers": map[string]string{"X-Upload-Token": "upload-token"},
				}
			}
			writeJSON(http.StatusAccepted, map[string]any{"task_id": "task_1", "status": status, "files": []any{file}})
		case "PUT /v1/tasks/task_1/uploads/file_1":
			f.puts++
			assert.Equal(f.t, "upload-token", r.Header.Get("X-Upload-Token"))
			var err error
			f.uploaded, err = io.ReadAll(r.Body)
			assert.NoError(f.t, err)
			writeJSON(http.StatusOK, map[string]any{"task_id": "task_1", "file_id": "file_1", "status": "pending"})
		case "GET /v1/tasks/task_1":
			f.polls++
			status := "completed"
			file := map[string]any{
				"file_id": "file_1", "file_name": "report.pdf", "status": status,
				"result_url": stringOr(f.resultURL, f.prefix+"/v1/files/file_1/content"),
			}
			if f.fileError {
				status = "failed"
				file = map[string]any{
					"file_id": "file_1", "status": status,
					"error": map[string]string{"code": "parse_failed", "message": "corrupt pdf"},
				}
			} else if f.running {
				status = "running"
				file = map[string]any{"file_id": "file_1", "status": status}
			}
			writeJSON(http.StatusOK, map[string]any{"task_id": "task_1", "status": status, "files": []any{file}})
		case "GET /v1/files/file_1/content":
			f.downloads++
			_, _ = w.Write(f.zipData)
		case "POST /v1/tasks/task_1/cancel":
			f.cancels++
			writeJSON(http.StatusOK, map[string]any{"task_id": "task_1", "status": "canceled"})
		default:
			f.unexpected = append(f.unexpected, r.Method+" "+r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func TestMinerUReaderTasksFullFlow(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUTasks{t: t, prefix: "/proxy/mineru", zipData: v1TestZip(t)}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	reader := NewMinerUReader(map[string]string{
		"mineru_endpoint": server.URL + fake.prefix, "mineru_server_api_key": "server-key",
		"mineru_tier": "advanced", "mineru_parse_method": "txt",
	})
	content := []byte("%PDF-1.7 body")
	result, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: content, FileName: "dir/report.pdf", FileType: "pdf",
	})
	require.NoError(t, err)
	require.Empty(t, result.Error)
	assert.Equal(t, v1TestMarkdown, result.MarkdownContent)
	require.Len(t, result.ImageRefs, 2)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Equal(t, content, fake.uploaded)
	assert.Equal(t, 1, fake.puts)
	assert.Equal(t, 1, fake.polls)
	assert.Equal(t, 1, fake.downloads)
	assert.Empty(t, fake.unexpected)
	assert.Equal(t, "advanced", fake.manifest["tier"])
	assert.Equal(t, "txt", fake.manifest["ocr_mode"])
	assert.Equal(t, []any{"default"}, fake.manifest["output_formats"])
	sum := sha256.Sum256(content)
	assert.Equal(t, []any{map[string]any{
		"file_name": "report.pdf", "bytes": float64(len(content)),
		"sha256sum": hex.EncodeToString(sum[:]),
	}}, fake.manifest["files"])
}
