package docparser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fastTasksClient(endpoint string) *minerUTasksClient {
	return &minerUTasksClient{fastV1Client(endpoint, "server-key")}
}

func TestMinerUTasksCacheHitOmitsUploadAndTier(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUTasks{t: t, cacheHit: true, zipData: v1TestZip(t)}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	md, _, _, err := fastTasksClient(server.URL).ParseWithLayout(
		context.Background(), []byte("pdf"), "report.pdf",
		minerUV1ParseOptions{})
	require.NoError(t, err)
	assert.Equal(t, v1TestMarkdown, md)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Zero(t, fake.puts)
	assert.Nil(t, fake.uploaded)
	assert.NotContains(t, fake.manifest, "tier")
	assert.Empty(t, fake.unexpected)
}

func TestMinerUTasksFileErrorInSuccessfulHTTPResponse(t *testing.T) {
	allowLoopbackSSRF(t)
	fake := &fakeMinerUTasks{t: t, cacheHit: true, fileError: true}
	server := httptest.NewServer(fake.handler())
	defer server.Close()
	_, _, _, err := fastTasksClient(server.URL).ParseWithLayout(
		context.Background(), []byte("pdf"), "report.pdf",
		minerUV1ParseOptions{})
	require.ErrorContains(t, err, "parse_failed: corrupt pdf")
	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Zero(t, fake.downloads)
	assert.Zero(t, fake.cancels)
}

func TestMinerUTasksCancel(t *testing.T) {
	allowLoopbackSSRF(t)
	for _, callerCancel := range []bool{false, true} {
		name := "timeout"
		if callerCancel {
			name = "caller"
		}
		t.Run(name, func(t *testing.T) {
			fake := &fakeMinerUTasks{t: t, cacheHit: true, running: true}
			polled := make(chan struct{}, 1)
			handler := fake.handler()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler.ServeHTTP(w, r)
				if r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/task_1" {
					select {
					case polled <- struct{}{}:
					default:
					}
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := fastTasksClient(server.URL)
			if !callerCancel {
				client.jobTimeout = 200 * time.Millisecond
			}
			if callerCancel {
				go func() {
					select {
					case <-polled:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			_, _, _, err := client.ParseWithLayout(ctx, []byte("pdf"), "report.pdf", minerUV1ParseOptions{})
			if callerCancel {
				assert.ErrorIs(t, err, context.Canceled)
			} else {
				assert.ErrorIs(t, err, context.DeadlineExceeded)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			assert.Equal(t, 1, fake.cancels, "use POST /cancel even after the parse context expires")
			assert.Empty(t, fake.unexpected)
		})
	}
}

func TestMinerUTasksReturnedURLsKeepCredentialsIsolated(t *testing.T) {
	allowLoopbackSSRF(t)
	for _, redirect := range []bool{false, true} {
		name := "direct"
		if redirect {
			name = "redirect"
		}
		t.Run(name, func(t *testing.T) {
			zipData := v1TestZip(t)
			var puts, gets atomic.Int32
			storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Empty(t, r.Header.Get("Authorization"), "same host on a different port is cross-origin")
				if r.Method == http.MethodPut {
					puts.Add(1)
					assert.Equal(t, "upload-token", r.Header.Get("X-Upload-Token"))
					w.WriteHeader(http.StatusNoContent) // Object storage need not return JSON.
				} else {
					gets.Add(1)
					_, _ = w.Write(zipData)
				}
			}))
			defer storage.Close()
			fake := &fakeMinerUTasks{
				t: t, uploadURL: storage.URL + "/put?signature=x", resultURL: storage.URL + "/result?signature=y",
			}
			if redirect {
				fake.resultURL = "/redirect"
			}
			handler := fake.handler()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirect" {
					assert.Equal(t, "Bearer server-key", r.Header.Get("Authorization"))
					http.Redirect(w, r, storage.URL+"/result", http.StatusFound)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			md, _, _, err := fastTasksClient(server.URL).ParseWithLayout(
				context.Background(), []byte("pdf"), "report.pdf",
				minerUV1ParseOptions{})
			require.NoError(t, err)
			assert.Equal(t, v1TestMarkdown, md)
			assert.Equal(t, int32(1), puts.Load())
			assert.Equal(t, int32(1), gets.Load())
		})
	}
}

func TestMinerUTasksBudgetsAndSSRF(t *testing.T) {
	allowLoopbackSSRF(t)
	for _, scenario := range []string{
		"zip limit", "upload scheme", "result scheme", "upload SSRF", "result SSRF", "redirect SSRF", "upload redirect",
	} {
		t.Run(scenario, func(t *testing.T) {
			fake := &fakeMinerUTasks{t: t, cacheHit: true, zipData: v1TestZip(t)}
			if scenario == "upload scheme" {
				fake.cacheHit = false
				fake.uploadURL = "file:///etc/passwd"
			}
			if scenario == "result scheme" {
				fake.resultURL = "file:///etc/passwd"
			}
			if scenario == "upload SSRF" {
				fake.cacheHit = false
				fake.uploadURL = "http://169.254.169.254/latest/meta-data"
			}
			if scenario == "result SSRF" {
				fake.resultURL = "http://169.254.169.254/latest/meta-data"
			}
			if scenario == "redirect SSRF" {
				fake.resultURL = "/redirect"
			}
			if scenario == "upload redirect" {
				fake.cacheHit = false
				fake.uploadURL = "/redirect"
			}
			handler := fake.handler()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirect" {
					http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusTemporaryRedirect)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			client := fastTasksClient(server.URL)
			if scenario == "zip limit" {
				client.maxZipBytes = 10
			}
			_, _, _, err := client.ParseWithLayout(
				context.Background(), []byte("pdf"), "report.pdf",
				minerUV1ParseOptions{})
			require.Error(t, err)
			switch scenario {
			case "zip limit":
				assert.ErrorContains(t, err, "artifact exceeds 10 bytes")
			case "upload scheme", "result scheme":
				assert.ErrorContains(t, err, "unsupported scheme")
			default:
				assert.True(t,
					strings.Contains(err.Error(), "SSRF") || strings.Contains(err.Error(), "cross-origin"), err)
			}
			if !fake.cacheHit {
				fake.mu.Lock()
				assert.Equal(t, 1, fake.cancels, "cancel an admitted task when its upload fails")
				fake.mu.Unlock()
			}
		})
	}
}

func TestDetectMinerUTasksProtocol(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		want       minerUProtocol
	}{
		{"4.0 Jobs", `{"status":"ok","version":"4.0.12"}`, minerUProtocolV1},
		{"4.1 Tasks", `{"status":"ok","version":"4.1.4"}`, minerUProtocolTasks},
		{
			"capabilities override API version",
			`{"status":"ok","version":"1.0.0","features":{"task_output_formats":["default","docx"]}}`,
			minerUProtocolTasks,
		},
		{"missing version preserves Jobs", `{"status":"ok"}`, minerUProtocolV1},
		{"unknown version preserves Jobs", `{"status":"ok","version":"development"}`, minerUProtocolV1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			got, err := detectMinerUProtocol(context.Background(), server.Client(), server.URL, "")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPingMinerUTasksUsesAuthenticatedUsage(t *testing.T) {
	allowLoopbackSSRF(t)
	for _, status := range []int{
		http.StatusOK, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/health":
					_, _ = w.Write([]byte(`{"status":"ok","version":"4.1.4"}`))
				case "/v1/usage":
					if r.Header.Get("Authorization") != "Bearer server-key" {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"access_level":"registered","usage":{}}`))
				default:
					t.Errorf("unexpected probe %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			ok, msg := PingMinerU(server.URL, "server-key")
			assert.Equal(t, status == http.StatusOK, ok, msg)
			ok, msg = PingMinerU(server.URL, "wrong")
			assert.False(t, ok)
			assert.Contains(t, msg, "API Key 无效")
			ok, msg = PingMinerU(server.URL, "")
			assert.False(t, ok)
			assert.Contains(t, msg, "配置 API Key")
		})
	}
}

func TestPingMinerUJobsRejectsMissingRoute(t *testing.T) {
	allowLoopbackSSRF(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	ok, msg := PingMinerU(server.URL, "server-key")
	assert.False(t, ok)
	assert.Contains(t, msg, "404")
}

// A waiting-file snapshot with no upload action must be polled. A later
// action can be used once, and a stale action after a successful PUT is ignored.
func TestMinerUTasksUploadsOnlyOnCurrentAction(t *testing.T) {
	allowLoopbackSSRF(t)
	var polls, puts atomic.Int32
	zipData := v1TestZip(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file := map[string]any{"file_id": "file_1", "status": "waiting-file"}
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/tasks":
		case "GET /v1/tasks/task_1":
			n := polls.Add(1)
			if n < 3 {
				file["upload"] = map[string]any{"url": "/put", "headers": map[string]string{}}
			} else {
				file["status"] = "completed"
				file["result_url"] = "/result"
			}
		case "PUT /put":
			puts.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		case "GET /result":
			_, _ = w.Write(zipData)
			return
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task_id": "task_1", "status": file["status"], "files": []any{file},
		})
	}))
	defer server.Close()
	_, _, _, err := fastTasksClient(server.URL).ParseWithLayout(
		context.Background(), []byte("pdf"), "report.pdf",
		minerUV1ParseOptions{})
	require.NoError(t, err)
	assert.Equal(t, int32(1), puts.Load())
	assert.Equal(t, int32(3), polls.Load())
}
