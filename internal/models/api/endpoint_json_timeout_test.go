package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

type jsonTimeoutTransport func(*http.Request) (*http.Response, error)

func (f jsonTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPostJSONAppliesFallbackDeadline(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var requestCtx context.Context
		ep := Endpoint{Client: &http.Client{Transport: jsonTimeoutTransport(func(req *http.Request) (*http.Response, error) {
			requestCtx = req.Context()
			<-requestCtx.Done()
			return nil, requestCtx.Err()
		})}}
		err := ep.PostJSON(context.Background(), "http://127.0.0.1", map[string]any{}, nil)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, DefaultJSONTimeout, time.Since(start))
		require.ErrorIs(t, requestCtx.Err(), context.DeadlineExceeded)
	})
}

func TestPostJSONHonorsExistingDeadline(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	for _, budget := range []time.Duration{DefaultJSONTimeout / 2, DefaultJSONTimeout * 2} {
		t.Run(budget.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), budget)
				defer cancel()
				deadline, _ := ctx.Deadline()
				ep := Endpoint{Client: &http.Client{Transport: jsonTimeoutTransport(func(req *http.Request) (*http.Response, error) {
					got, ok := req.Context().Deadline()
					require.True(t, ok)
					require.Equal(t, deadline, got)
					<-req.Context().Done()
					return nil, req.Context().Err()
				})}}
				start := time.Now()
				err := ep.PostJSON(ctx, "http://127.0.0.1", nil, nil)
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Equal(t, budget, time.Since(start))
			})
		})
	}
}

func TestPostJSONWithRetrySharesFallbackDeadline(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var deadlines []time.Time
		ep := Endpoint{Client: &http.Client{Transport: jsonTimeoutTransport(func(req *http.Request) (*http.Response, error) {
			deadline, ok := req.Context().Deadline()
			require.True(t, ok)
			deadlines = append(deadlines, deadline)
			if len(deadlines) == 1 {
				time.Sleep(DefaultJSONTimeout / 2)
				return nil, errors.New("connection reset")
			}
			<-req.Context().Done()
			return nil, req.Context().Err()
		})}}
		err := ep.PostJSONWithRetry(context.Background(), "http://127.0.0.1", nil, nil,
			RetryPolicy{MaxRetries: 1, BaseDelay: DefaultJSONTimeout / 4}, "embedding")
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Len(t, deadlines, 2)
		require.Equal(t, deadlines[0], deadlines[1])
		require.Equal(t, DefaultJSONTimeout, time.Since(start))
	})
}

func TestPostJSONWithRetryCancelsBudgetAfterSuccess(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var requestCtx context.Context
	ep := Endpoint{Client: &http.Client{Transport: jsonTimeoutTransport(func(req *http.Request) (*http.Response, error) {
		requestCtx = req.Context()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})}}
	var out struct{ OK bool }
	require.NoError(t, ep.PostJSONWithRetry(context.Background(), "http://127.0.0.1", nil, &out, RetryPolicy{}, "embedding"))
	require.True(t, out.OK)
	require.ErrorIs(t, requestCtx.Err(), context.Canceled)
}
