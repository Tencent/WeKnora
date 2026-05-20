// Package client uses an opt-in slog logger for SDK-internal trace output.
// Default behavior writes to io.Discard so SDK consumers (CLI, server) never
// see SDK trace output on stdout/stderr. Embedders enable debug output by
// calling SetDebugLevel("debug") at startup.
package client

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

var debugLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// SetDebugLevel replaces the SDK's internal debug logger so callers can
// programmatically control SDK trace output (e.g., the CLI's --log-level
// flag). Accepted levels: "debug" / "info" / "warn" / "error" (case-
// insensitive); any other value (including "") disables output entirely
// (writes to io.Discard).
//
// Not safe for concurrent use while SDK calls are in flight — call once
// at startup before any client method is invoked.
func SetDebugLevel(level string) {
	switch strings.ToLower(level) {
	case "debug":
		debugLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	case "info":
		debugLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	case "warn":
		debugLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	default:
		// "error" or unrecognized → silent. SDK only emits Debug calls today,
		// so error-level discards everything in practice.
		debugLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
}
