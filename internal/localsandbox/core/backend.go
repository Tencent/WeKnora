package core

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ErrUnsupportedPlatform is returned by NewBackend where no OS-enforced
// sandbox is implemented. Callers must not fall back to running unsandboxed.
var ErrUnsupportedPlatform = errors.New("localsandbox: no sandbox backend on this platform")

// Command is one execution request. Argv is passed as a slice rather than a
// command string so paths containing spaces need no quoting anywhere.
type Command struct {
	Argv  []string
	Env   map[string]string
	Cwd   string
	Stdin []byte
}

// Validate reports whether the command has a non-empty argv.
func (c Command) Validate() error {
	if len(c.Argv) == 0 || c.Argv[0] == "" {
		return fmt.Errorf("localsandbox: command argv is empty")
	}
	return nil
}

// Prepared is an opaque handle to one compiled policy. macOS stores the sbpl
// text and its -D parameters; Windows will store a restricted token handle,
// capability SIDs and refreshed ACL state. Callers must not inspect it — that
// opacity is what lets the two platforms differ completely.
type Prepared interface {
	// Fingerprint reports which Policy this was prepared from.
	Fingerprint() string
	// Close releases resources held since preparation.
	Close() error
}

// Process is a running sandboxed command.
type Process interface {
	Stdout() io.Reader
	Stderr() io.Reader
	// Wait blocks until exit. Cancelling ctx terminates the whole process tree.
	Wait(ctx context.Context) (ExitStatus, error)
	// Kill terminates the whole process tree, not just the root process.
	Kill() error
	PID() int
}

// Backend is the contract every platform implementation satisfies.
type Backend interface {
	Name() string
	// Available reports whether this machine can enforce the sandbox. It must
	// not modify anything.
	Available() error
	// EnsureReady performs one-time installation. No-op on macOS; on Windows
	// it will prompt for elevation.
	EnsureReady(ctx context.Context) error
	// Prepare compiles a policy into its platform-native form.
	Prepare(ctx context.Context, p Policy) (Prepared, error)
	// Spawn starts a process under a prepared policy.
	Spawn(ctx context.Context, prep Prepared, cmd Command) (Process, error)
	// TearDown removes persistent state created by EnsureReady. No-op on macOS.
	TearDown(ctx context.Context) error
}
