package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDoKeepsHeadersOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, nil)
	require.NoError(t, err)
	_, err = Endpoint{Client: srv.Client()}.Do(req)

	var httpErr *HTTPError
	require.True(t, errors.As(err, &httpErr))
	require.Equal(t, http.StatusTooManyRequests, httpErr.StatusCode)
	require.Equal(t, 12*time.Second, httpErr.RetryAfter())
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"absent", "", 0},
		{"seconds", "30", 30 * time.Second},
		{"seconds with spaces", " 5 ", 5 * time.Second},
		{"zero", "0", 0},
		{"negative", "-3", 0},
		{"http date", now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{"http date in the past", now.Add(-time.Minute).Format(http.TimeFormat), 0},
		{"garbage", "tomorrow", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, parseRetryAfter(tc.value, now))
		})
	}
}

func TestRetryAfterOnNilError(t *testing.T) {
	var e *HTTPError
	require.Zero(t, e.RetryAfter())
	require.Zero(t, (&HTTPError{StatusCode: 503}).RetryAfter())
}
