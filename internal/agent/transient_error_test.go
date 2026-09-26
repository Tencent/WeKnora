package agent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// A chunk the endpoint or a proxy mangled used to end the round outright: the
// failure carried neither a type nor a marker the retry classifier knew, so
// think.go never retried it and the turn fell through to the no-tool
// degradation path — on the user's side, a round in which no tool was called.
func TestIsTransientErrorCorruptStreamChunkIsRetryable(t *testing.T) {
	// What the client loops wrap a bad frame into. api.StreamAssembler.Fail
	// puts exactly this text into StreamResponse.Content.
	decodeErr := fmt.Errorf("decode stream chunk: %w: %w",
		api.ErrCorruptStreamChunk, errors.New("invalid character '<' looking for beginning of value"))
	// The agent rebuilds an error from that content, so only the text is left.
	flattened := errors.New("LLM stream error: " + decodeErr.Error())

	require.Contains(t, flattened.Error(), types.StreamChunkCorruptError,
		"the marker has to survive the error-chunk flattening to be of any use")

	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "typed: the error value still carries the sentinel",
			err:  decodeErr,
			want: true,
		},
		{
			name: "flattened: only the marker text made it through",
			err:  flattened,
			want: true,
		},
		{
			name: "flattened: a tool call that would not decode",
			err: errors.New("LLM stream error: decode tool call 0: " + types.StreamChunkCorruptError +
				": unexpected end of JSON input"),
			want: true,
		},
		{
			// The exact shape this failure had before the marker existed. It
			// names the corruption but nothing tells the classifier that the
			// request is worth sending again, so the round ended here.
			name: "no marker: the undecorated completions frame",
			err: errors.New("LLM stream error: decode stream chunk: " +
				"invalid character '<' looking for beginning of value"),
			want: false,
		},
		{
			name: "no marker: the undecorated SSE frame",
			err:  errors.New("LLM stream error: decode SSE response: unexpected end of JSON input"),
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isTransientError(tc.err))
		})
	}
}
