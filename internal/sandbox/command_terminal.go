package sandbox

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

// Command terminal budgets bound each submitted process tree.
const (
	DefaultTerminalTimeout     = 120 * time.Second
	DefaultTerminalCPUSeconds  = 60
	DefaultTerminalMemoryBytes = int64(512 * 1024 * 1024)
	MaxTerminalTimeout         = 30 * time.Minute
)

// CommandTerminalRequest starts exactly one audited command, never an existing shell.
// Zero limits select bounded defaults. MemoryBytes is inherited RLIMIT_AS,
// not an aggregate container memory quota.
type CommandTerminalRequest struct {
	Command     string
	Cols, Rows  uint16
	Timeout     time.Duration
	CPUSeconds  int
	MemoryBytes int64
}

// CommandTerminalExit reports the process exit and any enforced limit.
type CommandTerminalExit struct {
	ExitCode int    `json:"exit_code"`
	Reason   string `json:"reason"`
}

// CommandTerminal carries raw PTY bytes (combined stdout/stderr, possibly binary).
// Close and cancellation of the Open context terminate the command and its
// descendants. Wait only waits; cancellation of its context does not kill.
type CommandTerminal interface {
	io.ReadCloser
	Input(context.Context, []byte) error
	Resize(context.Context, uint16, uint16) error
	Interrupt(context.Context) error
	Wait(context.Context) (CommandTerminalExit, error)
}

// SessionCommandTerminalProvider uses the tenant-owned session's normal lifecycle.
// Provider IDs and process IDs never come from the caller.
type SessionCommandTerminalProvider interface {
	OpenSessionCommandTerminal(context.Context, string, CommandTerminalRequest) (CommandTerminal, error)
}

// CommandTerminalProviderFrom discovers command execution separately from the
// persistent shell exposed by SessionTerminalProvider.
func CommandTerminalProviderFrom(mgr Manager) (SessionCommandTerminalProvider, bool) {
	if provider, ok := mgr.(interface {
		SessionCommandTerminalProvider() SessionCommandTerminalProvider
	}); ok {
		terminal := provider.SessionCommandTerminalProvider()
		return terminal, terminal != nil
	}
	terminal, ok := mgr.(SessionCommandTerminalProvider)
	return terminal, ok
}

type remoteCommandTerminalProvider interface {
	OpenCommandTerminal(context.Context, RemoteSandboxHandle, CommandTerminalRequest) (CommandTerminal, error)
}

func normalizeTerminalRequest(req CommandTerminalRequest) (CommandTerminalRequest, error) {
	if strings.TrimSpace(req.Command) == "" || strings.ContainsRune(req.Command, 0) {
		return req, errors.New("sandbox: terminal command must be nonempty and contain no NUL")
	}
	if len(req.Command) > 64*1024 {
		return req, errors.New("sandbox: terminal command exceeds 64 KiB")
	}
	if req.Timeout < 0 || req.Timeout > MaxTerminalTimeout || req.CPUSeconds < 0 || req.MemoryBytes < 0 {
		return req, errors.New("sandbox: invalid terminal budget (maximum wall time is 30 minutes)")
	}
	if req.Timeout == 0 {
		req.Timeout = DefaultTerminalTimeout
	}
	if req.CPUSeconds == 0 {
		req.CPUSeconds = DefaultTerminalCPUSeconds
	}
	if req.MemoryBytes == 0 {
		req.MemoryBytes = DefaultTerminalMemoryBytes
	}
	if req.Cols == 0 {
		req.Cols = 80
	}
	if req.Rows == 0 {
		req.Rows = 24
	}
	return req, nil
}
