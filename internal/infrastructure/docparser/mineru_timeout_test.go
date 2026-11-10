package docparser

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestMinerUTimeoutConfig(t *testing.T) {
	for _, tt := range []struct {
		name, value string
		want        time.Duration
	}{
		{"empty", "", defaultMinerUTimeout},
		{"whitespace", "  ", defaultMinerUTimeout},
		{"minutes", "90m", 90 * time.Minute},
		{"trimmed", " 5400s ", 90 * time.Minute},
		{"invalid", "invalid", defaultMinerUTimeout},
		{"unitless", "5400", defaultMinerUTimeout},
		{"zero", "0s", defaultMinerUTimeout},
		{"negative", "-1s", defaultMinerUTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WEKNORA_MINERU_TIMEOUT", tt.value)
			assert.Equal(t, tt.want, NewMinerUReader(nil).timeout)
		})
	}
}

// The configured timeout, not the old hardcoded 1000s, must end a parse on
// both protocols, and it must fire before the caller's own deadline.
func TestMinerUReaderHonorsConfiguredTimeout(t *testing.T) {
	allowLoopbackSSRF(t)
	req := &types.ReadRequest{FileContent: []byte("%PDF"), FileName: "a.pdf", FileType: "pdf"}

	t.Run("v1", func(t *testing.T) {
		t.Setenv("WEKNORA_MINERU_TIMEOUT", "200ms")
		statuses := make([]string, 1000)
		for i := range statuses {
			statuses[i] = "running"
		}
		fake := &fakeMinerUV1{t: t, pollStatuses: statuses}
		server := httptest.NewServer(fake.handler())
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := NewMinerUReader(map[string]string{"mineru_endpoint": server.URL}).Read(ctx, req)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NoError(t, ctx.Err(), "the configured timeout, not the caller's deadline, should stop the parse")
		assert.True(t, fake.canceled, "abandoned job should be canceled on the server")
	})

	t.Run("legacy", func(t *testing.T) {
		t.Setenv("WEKNORA_MINERU_TIMEOUT", "100ms")
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/health" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		defer server.Close()
		defer close(release)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := NewMinerUReader(map[string]string{"mineru_endpoint": server.URL}).Read(ctx, req)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NoError(t, ctx.Err(), "the configured timeout, not the caller's deadline, should stop the parse")
	})
}

func TestMinerUCloudTimeoutConfig(t *testing.T) {
	t.Setenv("WEKNORA_MINERU_CLOUD_TIMEOUT", "")
	assert.Equal(t, defaultCloudTimeout, NewMinerUCloudReader(nil).timeout)
	t.Setenv("WEKNORA_MINERU_CLOUD_TIMEOUT", "45m")
	assert.Equal(t, 45*time.Minute, NewMinerUCloudReader(nil).timeout)
	t.Setenv("WEKNORA_MINERU_CLOUD_TIMEOUT", "0s")
	assert.Equal(t, defaultCloudTimeout, NewMinerUCloudReader(nil).timeout)
}

func TestMinerUCloudPollStopsAtConfiguredTimeout(t *testing.T) {
	allowLoopbackSSRF(t)
	t.Setenv("WEKNORA_MINERU_CLOUD_TIMEOUT", "200ms")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"extract_result":[{"state":"running"}]}}`)
	}))
	defer server.Close()

	reader := NewMinerUCloudReader(nil)
	reader.baseURL = server.URL
	reader.pollInterval = 10 * time.Millisecond

	start := time.Now()
	_, _, _, err := reader.pollBatchResult(context.Background(), "batch-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("timed out after %s", 200*time.Millisecond))
	assert.Less(t, time.Since(start), 5*time.Second)
}
