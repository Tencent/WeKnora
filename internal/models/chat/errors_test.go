package chat

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyStatusBody(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		kind   ErrorKind
	}{
		{"401 → auth", http.StatusUnauthorized, `{"error":"invalid api key"}`, ErrAuth},
		{"403 → auth", http.StatusForbidden, "forbidden", ErrAuth},
		{"429 → rate limited", http.StatusTooManyRequests, `{"error":"rate limit"}`, ErrRateLimited},
		{"500 → upstream", http.StatusInternalServerError, "internal error", ErrProviderUpstream},
		{"503 → upstream", http.StatusServiceUnavailable, "overloaded", ErrProviderUpstream},
		{
			"400 context length → context exceeded", http.StatusBadRequest,
			`This model's maximum context length is 8192 tokens`, ErrContextExceeded,
		},
		{
			"400 context_length_exceeded → context exceeded", http.StatusBadRequest,
			`{"error":{"code":"context_length_exceeded"}}`, ErrContextExceeded,
		},
		{
			"400 dashscope input range → context exceeded", http.StatusBadRequest,
			`Range of input length should be [1, 30000]`, ErrContextExceeded,
		},
		{
			"400 content policy → content policy", http.StatusBadRequest,
			`{"error":{"code":"content_policy_violation"}}`, ErrContentPolicy,
		},
		{"400 other → upstream non-retryable", http.StatusBadRequest, `{"error":"bad param"}`, ErrProviderUpstream},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pe := classifyStatusBody(tt.status, tt.body)
			assert.Equal(t, tt.kind, pe.Kind)
			assert.Equal(t, tt.status, pe.Status)
		})
	}
}

func TestProviderErrorIsRetryable(t *testing.T) {
	assert.True(t, (&ProviderError{Kind: ErrRateLimited, Status: 429}).IsRetryable())
	assert.True(t, (&ProviderError{Kind: ErrTimeout}).IsRetryable())
	assert.True(t, (&ProviderError{Kind: ErrProviderUpstream, Status: 500}).IsRetryable())
	assert.True(t, (&ProviderError{Kind: ErrProviderUpstream, Status: 0}).IsRetryable(),
		"network-level failures retry")
	assert.False(t, (&ProviderError{Kind: ErrAuth, Status: 401}).IsRetryable(),
		"401 fails fast — no quota burn")
	assert.False(t, (&ProviderError{Kind: ErrContextExceeded, Status: 400}).IsRetryable(),
		"context-exceeded fails identically every time")
	assert.False(t, (&ProviderError{Kind: ErrContentPolicy, Status: 400}).IsRetryable())
	assert.False(t, (&ProviderError{Kind: ErrProviderUpstream, Status: 400}).IsRetryable(),
		"other 4xx fail fast")
	assert.False(t, (&ProviderError{Kind: ErrProviderUpstream, Status: -1}).IsRetryable(),
		"caller-canceled contexts never retry")
}

func TestClassifyError(t *testing.T) {
	t.Run("openai APIError classifies by status", func(t *testing.T) {
		apiErr := &openai.APIError{HTTPStatusCode: 429, Message: "rate limited"}
		pe := ClassifyError(apiErr)
		assert.Equal(t, ErrRateLimited, pe.Kind)
		require.True(t, errors.Is(ClassifyError(apiErr), apiErr) ||
			pe.Err == nil || pe.Err == apiErr || pe.Err == error(apiErr))
	})

	t.Run("provider error passes through unchanged", func(t *testing.T) {
		orig := &ProviderError{Kind: ErrAuth, Status: 401}
		assert.Same(t, orig, ClassifyError(orig))
	})

	t.Run("deadline exceeded → timeout", func(t *testing.T) {
		pe := ClassifyError(context.DeadlineExceeded)
		assert.Equal(t, ErrTimeout, pe.Kind)
		assert.True(t, pe.IsRetryable())
	})

	t.Run("canceled → non-retryable upstream", func(t *testing.T) {
		pe := ClassifyError(context.Canceled)
		assert.Equal(t, ErrProviderUpstream, pe.Kind)
		assert.False(t, pe.IsRetryable())
	})

	t.Run("generic error → network-level upstream", func(t *testing.T) {
		pe := ClassifyError(errors.New("connection refused"))
		assert.Equal(t, ErrProviderUpstream, pe.Kind)
		assert.Equal(t, 0, pe.Status)
		assert.True(t, pe.IsRetryable())
	})
}

func TestWithProviderRetry(t *testing.T) {
	t.Run("retries rate-limited then succeeds", func(t *testing.T) {
		calls := 0
		result, err := withProviderRetry(context.Background(), func(context.Context) (int, error) {
			calls++
			if calls < 2 {
				return 0, &ProviderError{Kind: ErrRateLimited, Status: 429}
			}
			return 42, nil
		})
		require.NoError(t, err)
		assert.Equal(t, 42, result)
		assert.Equal(t, 2, calls)
	})

	t.Run("auth fails fast after one attempt", func(t *testing.T) {
		calls := 0
		start := time.Now()
		_, err := withProviderRetry(context.Background(), func(context.Context) (int, error) {
			calls++
			return 0, &ProviderError{Kind: ErrAuth, Status: 401}
		})
		require.Error(t, err)
		assert.Equal(t, 1, calls, "401 must not burn retries")
		assert.Less(t, time.Since(start), invokeRetryBaseDelay, "no backoff sleep on fast-fail")
	})

	t.Run("exhausts retry budget on persistent rate limit", func(t *testing.T) {
		calls := 0
		_, err := withProviderRetry(context.Background(), func(context.Context) (int, error) {
			calls++
			return 0, &ProviderError{Kind: ErrRateLimited, Status: 429}
		})
		require.Error(t, err)
		assert.Equal(t, invokeRetryMax+1, calls)
	})

	t.Run("canceled context stops retrying", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		_, err := withProviderRetry(ctx, func(context.Context) (int, error) {
			calls++
			return 0, &ProviderError{Kind: ErrRateLimited, Status: 429}
		})
		require.Error(t, err)
		assert.Equal(t, 1, calls)
	})

	t.Run("error preserving via unwrap", func(t *testing.T) {
		sentinel := errors.New("upstream exploded")
		_, err := withProviderRetry(context.Background(), func(context.Context) (int, error) {
			return 0, &ProviderError{Kind: ErrProviderUpstream, Status: 500, Err: sentinel}
		})
		require.Error(t, err)
		require.True(t, errors.Is(err, sentinel))
	})
}
