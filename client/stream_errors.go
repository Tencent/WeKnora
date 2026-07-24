package client

import (
	"errors"
	"fmt"
	"strings"
)

// ErrSSEStreamTerminal marks a terminal response_type=error, done=true frame
// from an SSE stream. The stream delivered the frame to the callback before
// returning the error.
var ErrSSEStreamTerminal = errors.New("SSE stream terminal error")

// SSEStreamError is returned when the server emits a terminal error frame on
// an SSE stream.
type SSEStreamError struct {
	Content string
}

func (e *SSEStreamError) Error() string {
	return fmt.Sprintf("SSE stream error: %s", e.Content)
}

func (e *SSEStreamError) Unwrap() error {
	return ErrSSEStreamTerminal
}

// NewSSEStreamError builds the terminal SSE stream error returned by the
// streaming readers after delivering the error frame to the callback.
func NewSSEStreamError(content string) error {
	return &SSEStreamError{Content: content}
}

// IsSSEStreamError reports whether err is a terminal SSE stream error from the
// SDK readers, including wrapped *SSEStreamError values and legacy
// fmt.Errorf("SSE stream error: ...") chains.
func IsSSEStreamError(err error) bool {
	var sse *SSEStreamError
	if errors.As(err, &sse) {
		return true
	}
	for err != nil {
		if strings.HasPrefix(err.Error(), "SSE stream error: ") {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
