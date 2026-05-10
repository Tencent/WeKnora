// Package iostreams wraps stdout/stderr/stdin with TTY detection and color awareness.
//
// Tests swap the package-level singleton via SetForTest; production code reads
// the global IO directly. (Tests must not call SetForTest from t.Parallel(),
// since the swap is process-global.)
package iostreams

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/mattn/go-isatty"
)

// IOStreams holds standard I/O handles plus environment-derived flags.
type IOStreams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer

	stdoutTTY bool
	stderrTTY bool
}

// IO is the package-level singleton.
var IO *IOStreams = newProduction()

func newProduction() *IOStreams {
	return &IOStreams{
		In:        os.Stdin,
		Out:       os.Stdout,
		Err:       os.Stderr,
		stdoutTTY: isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()),
		stderrTTY: isatty.IsTerminal(os.Stderr.Fd()) || isatty.IsCygwinTerminal(os.Stderr.Fd()),
	}
}

// IsStdoutTTY reports whether stdout is a terminal.
func (s *IOStreams) IsStdoutTTY() bool { return s.stdoutTTY }

// IsStderrTTY reports whether stderr is a terminal.
func (s *IOStreams) IsStderrTTY() bool { return s.stderrTTY }

// ColorEnabled honors NO_COLOR (https://no-color.org) and TERM=dumb.
// Computed on demand so env changes after init are picked up by callers.
func (s *IOStreams) ColorEnabled() bool {
	if !s.stdoutTTY {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return os.Getenv("TERM") != "dumb"
}

// SetForTest replaces the package singleton with in-memory buffers for the
// duration of t. Call it once per test. Not safe under t.Parallel().
func SetForTest(t *testing.T) (out, errBuf *bytes.Buffer) {
	t.Helper()
	saved := IO
	out = &bytes.Buffer{}
	errBuf = &bytes.Buffer{}
	IO = &IOStreams{In: bytes.NewReader(nil), Out: out, Err: errBuf}
	t.Cleanup(func() { IO = saved })
	return out, errBuf
}

// SetForTestWithTTY is like SetForTest but reports IsStdoutTTY/IsStderrTTY
// = true so tests can exercise interactive code paths (confirm prompts,
// color, fancy renderers). Not safe under t.Parallel().
func SetForTestWithTTY(t *testing.T) (out, errBuf *bytes.Buffer) {
	t.Helper()
	saved := IO
	out = &bytes.Buffer{}
	errBuf = &bytes.Buffer{}
	IO = &IOStreams{
		In:        bytes.NewReader(nil),
		Out:       out,
		Err:       errBuf,
		stdoutTTY: true,
		stderrTTY: true,
	}
	t.Cleanup(func() { IO = saved })
	return out, errBuf
}
