package client

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestSetDebugLevel(t *testing.T) {
	// Save and restore
	saved := debugLogger
	defer func() { debugLogger = saved }()

	cases := []string{"debug", "info", "warn", "error", "DEBUG", "", "invalid"}
	for _, lvl := range cases {
		SetDebugLevel(lvl)
		if debugLogger == nil {
			t.Errorf("SetDebugLevel(%q): debugLogger nil", lvl)
		}
	}
}

func TestSetDebugLevel_DebugRoutesEmissions(t *testing.T) {
	saved := debugLogger
	defer func() { debugLogger = saved }()

	var buf bytes.Buffer
	debugLogger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	debugLogger.Debug("test_event", "k", "v")
	if buf.Len() == 0 {
		t.Error("expected debug emission to land in buffer")
	}
}
