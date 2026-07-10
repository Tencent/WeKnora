package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

// TestIsCleanShutdown pins that an MCP host disconnecting (stdin EOF) or a
// cancelled context is treated as normal termination (exit 0), while a genuine
// transport fault is not.
func TestIsCleanShutdown(t *testing.T) {
	clean := []error{
		io.EOF,
		context.Canceled,
		context.DeadlineExceeded,
		fmt.Errorf("jsonrpc: %w", io.EOF),    // wrapped EOF
		errors.New("server is closing: EOF"), // go-sdk formatted string (no unwrap)
	}
	for _, e := range clean {
		if !isCleanShutdown(e) {
			t.Errorf("isCleanShutdown(%v) = false, want true", e)
		}
	}
	notClean := []error{
		errors.New("connection reset by peer"),
		errors.New("write: broken pipe"),
		errors.New("EOF while reading header but more expected"), // EOF not at end
	}
	for _, e := range notClean {
		if isCleanShutdown(e) {
			t.Errorf("isCleanShutdown(%v) = true, want false", e)
		}
	}
}
