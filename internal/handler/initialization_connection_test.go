package handler

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// TestIsReachableBadRequest covers the 400-as-reachable fallback used by
// checkChatModelConnection: a 400 from the model endpoint means the endpoint
// was reached and auth passed, only the test-call parameters were rejected.
// Regression test for #3928: since #3470 dropped go-openai, 400s surface as
// *api.HTTPError ("API request failed with status 400: ..."), so the old
// "status code: 400" string match alone can never fire.
func TestIsReachableBadRequest(t *testing.T) {
	// The exact error shape produced on the current code path: LiteLLM
	// passing through a reasoning model's max_tokens=1 rejection.
	newStyle := &api.HTTPError{
		StatusCode: 400,
		Body:       "Could not finish the message because max_tokens or model output limit was reached",
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "new api.HTTPError 400",
			err:  newStyle,
			want: true,
		},
		{
			// errors are wrapped with %w through the client layers
			// (openaicompletions.Client.Chat -> "create chat completion: %w")
			name: "wrapped api.HTTPError 400",
			err:  fmt.Errorf("create chat completion: %w", newStyle),
			want: true,
		},
		{
			// legacy go-openai wording, kept for compatibility
			name: "legacy go-openai wording",
			err:  stderrors.New(`ChatCompletion failed: error, status code: 400, message: ...`),
			want: true,
		},
		{
			name: "401 is not reachable",
			err:  &api.HTTPError{StatusCode: 401, Body: "invalid api key"},
			want: false,
		},
		{
			name: "500 is not reachable",
			err:  &api.HTTPError{StatusCode: 500, Body: "internal error"},
			want: false,
		},
		{
			name: "connection refused is not a 400",
			err:  stderrors.New(`post https://example.com/v1: dial tcp: connection refused`),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isReachableBadRequest(tc.err); got != tc.want {
				t.Errorf("isReachableBadRequest(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}

	// Sanity: the exact regression — the pre-#3470 string match cannot see
	// the new error wording, which is why the fallback went dead.
	if strings.Contains(newStyle.Error(), "status code: 400") {
		t.Errorf("expected legacy string match to miss the new wording, got match")
	}
}
