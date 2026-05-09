// Package client uses an opt-in slog logger for SDK-internal trace output.
// Default behavior writes to io.Discard so SDK consumers (CLI, server) never
// see SDK trace output on stdout/stderr. Set WEKNORA_SDK_DEBUG=1 to route
// trace events to stderr.
package client

import (
	"io"
	"log/slog"
	"os"
)

var debugLogger = newDebugLogger()

func newDebugLogger() *slog.Logger {
	if os.Getenv("WEKNORA_SDK_DEBUG") == "1" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
