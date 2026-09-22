package docparser

import (
	archiveZip "archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMinerUV1TestServer emulates a MinerU >= 4 V1 API server:
// health / uploads (create, PUT, complete) / parse jobs / file content.
func newMinerUV1TestServer(t *testing.T, failJob bool) *httptest.Server {
	t.Helper()
	var pollCount atomic.Int32
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"4.0.4"}`))
	})
	mux.HandleFunc("POST /v1/uploads", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		require.Equal(t, "parse", body["purpose"])
		require.NotZero(t, body["sha256sum"])
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "up-1",
			"status":      "pending",
			"upload_url":  "/v1/uploads/up-1/content",
			"upload_headers": map[string]string{"X-Custom": "v"},
			"file":        map[string]any{"id": "file-up-1"},
		})
	})
	mux.HandleFunc("PUT /v1/uploads/up-1/content", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "v", r.Header.Get("X-Custom"))
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /v1/uploads/up-1/complete", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"file":{"id":"file-1"}}`))
	})

	mux.HandleFunc("POST /v1/parse/jobs", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		files, _ := body["files"].([]any)
		require.Len(t, files, 1)
		source := files[0].(map[string]any)["source"].(map[string]any)
		require.Equal(t, "file-1", source["file_id"])
		require.Equal(t, []any{"zip"}, body["output_formats"])
		w.WriteHeader(http.StatusAccepted)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":"job-1","status":"queued"}`))
	})

	mux.HandleFunc("GET /v1/parse/jobs/job-1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if failJob {
			_, _ = w.Write([]byte(`{"job_id":"job-1","status":"failed"}`))
			return
		}
		status := "running"
		if pollCount.Add(1) >= 2 {
			status = "completed"
		}
		output := map[string]any{}
		if status == "completed" {
			output["zip"] = map[string]any{"file_id": "out-zip"}
		}
		resp, _ := json.Marshal(map[string]any{
			"job_id": "job-1", "status": status,
			"files": []map[string]any{{"name": "doc.pdf", "status": status, "output_files": output}},
		})
		_, _ = w.Write(resp)
	})

	mux.HandleFunc("GET /v1/files/out-zip/content", func(w http.ResponseWriter, _ *http.Request) {
		var buf bytes.Buffer
		zw := archiveZip.NewWriter(&buf)
		md, _ := zw.Create("markdown.md")
		_, _ = md.Write([]byte("# t\n\n![img](images/p-1.jpg)\n"))
		img, _ := zw.Create("images/p-1.jpg")
		imgBytes := []byte{0xFF, 0xD8, 0xFF}
		_, _ = img.Write(imgBytes)
		mj, _ := zw.Create("middle_json.json")
		_, _ = mj.Write([]byte("{}"))
		require.NoError(t, zw.Close())
		_, _ = w.Write(buf.Bytes())
	})

	return httptest.NewServer(mux)
}

func TestMinerUV1EndToEnd(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })

	srv := newMinerUV1TestServer(t, false)
	defer srv.Close()

	client := newMinerUV1Client(srv.URL, "", "standard", srv.Client())
	require.True(t, detectMinerUAPI(context.Background(), srv.URL, ""))

	content := []byte("%PDF-1.4 fake")
	fileID, err := client.v1Upload(context.Background(), content, "doc.pdf")
	require.NoError(t, err)
	require.Equal(t, "file-1", fileID)

	jobID, err := client.v1CreateJob(context.Background(), fileID)
	require.NoError(t, err)
	require.Equal(t, "job-1", jobID)

	artifactID, err := client.v1Poll(context.Background(), jobID, "zip")
	require.NoError(t, err)
	require.Equal(t, "out-zip", artifactID)

	artifact, err := client.v1Download(context.Background(), artifactID)
	require.NoError(t, err)
	md, images, err := minerUV1ExtractZip(artifact)
	require.NoError(t, err)
	assert.Contains(t, md, "images/p-1.jpg")
	assert.NotEmpty(t, images["images/p-1.jpg"])
	_, err = base64.StdEncoding.DecodeString(images["images/p-1.jpg"])
	assert.NoError(t, err)
}

func TestMinerUV1ParseJobFailed(t *testing.T) {
	srv := newMinerUV1TestServer(t, true)
	defer srv.Close()

	client := newMinerUV1Client(srv.URL, "", "", srv.Client())
	_, _, err := client.v1Parse(context.Background(), []byte("x"), "doc.pdf")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed")
}

func TestMinerUV1ReaderFullFlow(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })

	srv := newMinerUV1TestServer(t, false)
	defer srv.Close()

	// Reader built from overrides, as the engine does.
	reader := NewMinerUReader(map[string]string{"mineru_endpoint": srv.URL})
	res, err := reader.Read(context.Background(), &types.ReadRequest{
		FileName: "doc.pdf", FileType: "pdf", FileContent: []byte("%PDF-1.4 fake"),
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Contains(t, res.MarkdownContent, "# t")
	require.Len(t, res.ImageRefs, 1)
	assert.Equal(t, "images/p-1.jpg", res.ImageRefs[0].Filename)
	assert.Equal(t, []byte{0xFF, 0xD8, 0xFF}, res.ImageRefs[0].ImageData)
}

func TestMinerUV1LegacyServerFallsBackToFileParse(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })

	// Legacy server: no /v1/health (404 like real MinerU < 4), only
	// /docs + /file_parse. One handler serves both paths.
	mux := http.NewServeMux()
	called := atomic.Bool{}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/health":
			w.WriteHeader(http.StatusNotFound)
			return
		case r.URL.Path == "/docs":
			_, _ = w.Write([]byte("<html>swagger</html>"))
			return
		case r.URL.Path == "/file_parse" && r.Method == http.MethodPost:
			called.Store(true)
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":{"doc":{"md_content":"# legacy","images":{}}}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	require.False(t, detectMinerUAPI(context.Background(), srv.URL, ""))

	reader := NewMinerUReader(map[string]string{"mineru_endpoint": srv.URL})
	res, err := reader.Read(context.Background(), &types.ReadRequest{
		FileName: "doc.pdf", FileType: "pdf", FileContent: []byte("%PDF-1.4 legacy"),
	})
	require.NoError(t, err)
	assert.True(t, called.Load())
	assert.Contains(t, res.MarkdownContent, "# legacy")
}
