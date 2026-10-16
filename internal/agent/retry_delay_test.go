package agent

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/require"
)

func TestLLMRetryDelayHonoursRetryAfter(t *testing.T) {
	limited := func(retryAfter string) error {
		return fmt.Errorf("chat: %w", &api.HTTPError{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{retryAfter}},
		})
	}
	for _, tc := range []struct {
		name    string
		err     error
		attempt int
		want    time.Duration
	}{
		{"no header keeps the linear backoff", &api.HTTPError{StatusCode: 503}, 2, 2 * time.Second},
		{"untyped error keeps the linear backoff", errors.New("timeout"), 1, time.Second},
		{"retry-after longer than the backoff wins", limited("7"), 1, 7 * time.Second},
		{"retry-after shorter than the backoff is not a speed-up", limited("1"), 3, 3 * time.Second},
		{"retry-after is capped", limited("3600"), 1, maxLLMRetryAfter},
		{"malformed retry-after is ignored", limited("soon"), 1, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, llmRetryDelay(tc.err, tc.attempt))
		})
	}
}
