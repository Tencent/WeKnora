package docparser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestPaddleOCRVLCloudTimeoutConfig(t *testing.T) {
	for _, tt := range []struct {
		name, value string
		want        time.Duration
	}{
		{"empty", "", paddleOCRVLCloudTimeout},
		{"whitespace", "  ", paddleOCRVLCloudTimeout},
		{"minutes", "90m", 90 * time.Minute},
		{"trimmed", " 5400s ", 90 * time.Minute},
		{"invalid", "invalid", paddleOCRVLCloudTimeout},
		{"unitless", "5400", paddleOCRVLCloudTimeout},
		{"zero", "0s", paddleOCRVLCloudTimeout},
		{"negative", "-1s", paddleOCRVLCloudTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WEKNORA_PADDLEOCR_VL_CLOUD_TIMEOUT", tt.value)
			assert.Equal(t, tt.want, NewPaddleOCRVLCloudReader(nil).timeout)
		})
	}
}

// The configured timeout, not the old hardcoded 600s, must end a job that the
// cloud keeps reporting as running.
func TestPaddleOCRVLCloudPollStopsAtConfiguredTimeout(t *testing.T) {
	allowLoopbackSSRF(t)
	t.Setenv("WEKNORA_PADDLEOCR_VL_CLOUD_TIMEOUT", "200ms")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"state":"running"}}`)
	}))
	defer server.Close()

	reader := NewPaddleOCRVLCloudReader(nil)
	reader.baseURL = server.URL
	reader.pollInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Watchdog: without the configured budget this loop keeps polling for the
	// old hardcoded 600s, which must fail the test instead of hanging it.
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, pollErr := reader.pollJob(ctx, "job-1")
		done <- pollErr
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), fmt.Sprintf("timed out after %s", 200*time.Millisecond))
		assert.Less(t, time.Since(start), 5*time.Second)
	case <-time.After(5 * time.Second):
		t.Fatalf("poll loop ignored WEKNORA_PADDLEOCR_VL_CLOUD_TIMEOUT")
	}
}

func TestWeKnoraCloudPollTimeoutConfig(t *testing.T) {
	for _, tt := range []struct {
		name, value string
		want        time.Duration
	}{
		{"empty", "", defaultWeKnoraCloudPollTimeout},
		{"minutes", "90m", 90 * time.Minute},
		{"trimmed", " 3600s ", time.Hour},
		{"invalid", "invalid", defaultWeKnoraCloudPollTimeout},
		{"zero", "0s", defaultWeKnoraCloudPollTimeout},
		{"negative", "-1s", defaultWeKnoraCloudPollTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WEKNORA_WEKNORACLOUD_TIMEOUT", tt.value)
			reader, err := NewWeKnoraCloudSignedDocumentReader("app-id", "api-key")
			require.NoError(t, err)
			assert.Equal(t, tt.want, reader.pollTimeout)
		})
	}
}

// weKnoraCloudPollTransport answers every poll with "still running" so the
// configured poll budget is the only thing that can end the loop.
type weKnoraCloudPollTransport struct {
	polls atomic.Int64
}

func (t *weKnoraCloudPollTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.polls.Add(1)
	body := `{"task_id":"task-1","status":"processing","progress":0.1}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// The configured budget, not the old hardcoded 20m, must end the poll loop
// whether or not the caller has a deadline of its own. Ingestion always calls
// with a (much longer) DocReader deadline, so that case must not bypass it.
func TestWeKnoraCloudPollStopsAtConfiguredTimeout(t *testing.T) {
	for _, tt := range []struct {
		name      string
		parentCtx func() (context.Context, context.CancelFunc)
	}{
		{"no parent deadline", func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		}},
		{"longer parent deadline", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Minute)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WEKNORA_WEKNORACLOUD_TIMEOUT", "300ms")
			reader, err := NewWeKnoraCloudSignedDocumentReader("app-id", "api-key")
			require.NoError(t, err)

			transport := &weKnoraCloudPollTransport{}
			reader.client = &http.Client{Transport: transport}
			reader.initialPollInterval = 5 * time.Millisecond
			reader.maxPollInterval = 10 * time.Millisecond

			ctx, cancel := tt.parentCtx()
			defer cancel()

			done := make(chan error, 1)
			start := time.Now()
			go func() {
				_, pollErr := reader.pollTaskResult(ctx, "task-1")
				done <- pollErr
			}()

			select {
			case pollErr := <-done:
				require.Error(t, pollErr)
				assert.True(t, errors.Is(pollErr, context.DeadlineExceeded), "err = %v", pollErr)
				assert.Less(t, time.Since(start), 5*time.Second)
				assert.Greater(t, transport.polls.Load(), int64(1),
					"the job must actually be polled before the budget ends it")
			case <-time.After(5 * time.Second):
				t.Fatalf("poll loop ignored WEKNORA_WEKNORACLOUD_TIMEOUT after %d polls", transport.polls.Load())
			}
		})
	}
}

// The submit POST carries the whole file in one multipart body, so the
// configured budget must bound it too; the old fixed 60s client ceiling failed
// every scan larger than ~100MB (#5589).
func TestPaddleOCRVLCloudSubmitStopsAtConfiguredTimeout(t *testing.T) {
	allowLoopbackSSRF(t)
	t.Setenv("WEKNORA_PADDLEOCR_VL_CLOUD_TIMEOUT", "200ms")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second) // slower than the configured budget
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"jobId":"job-1"}}`)
	}))
	defer server.Close()

	reader := NewPaddleOCRVLCloudReader(nil)
	reader.baseURL = server.URL

	// Watchdog: with the old hardcoded 60s client the goroutine would still be
	// waiting when this fires.
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, submitErr := reader.submitJob(context.Background(), &types.ReadRequest{
			FileName: "big.pdf",
			FileType: ".pdf",
		}, []byte("%PDF-1.7 big scan"))
		done <- submitErr
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		// The transport's deadline wording varies by platform and code path
		// ("context deadline exceeded", "net/http: request canceled
		// (Client.Timeout exceeded ...)", or a mix), so assert the wrap
		// prefix plus that the error is a net timeout, not exact text.
		assert.Contains(t, err.Error(), "HTTP request")
		var netErr net.Error
		require.ErrorAs(t, err, &netErr)
		assert.True(t, netErr.Timeout())
		assert.Less(t, time.Since(start), 5*time.Second)
	case <-time.After(5 * time.Second):
		t.Fatalf("submit ignored WEKNORA_PADDLEOCR_VL_CLOUD_TIMEOUT")
	}
}

func TestPaddleOCRVLCloudSubmitSucceedsWithinConfiguredTimeout(t *testing.T) {
	allowLoopbackSSRF(t)
	t.Setenv("WEKNORA_PADDLEOCR_VL_CLOUD_TIMEOUT", "10s")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"jobId":"job-42"}}`)
	}))
	defer server.Close()

	reader := NewPaddleOCRVLCloudReader(nil)
	reader.baseURL = server.URL

	jobID, err := reader.submitJob(context.Background(), &types.ReadRequest{
		FileName: "small.pdf",
		FileType: ".pdf",
	}, []byte("%PDF-1.7"))
	require.NoError(t, err)
	assert.Equal(t, "job-42", jobID)
}
