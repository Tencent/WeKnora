package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTerminalMemoryExitMapping(t *testing.T) {
	require.Equal(t, CommandTerminalExit{ExitCode: 200, Reason: "memory_limit"}, terminalExitFor(200))
	for _, code := range []int{0, 1, 73, 137} {
		require.Equal(t, CommandTerminalExit{ExitCode: code, Reason: "exited"}, terminalExitFor(code))
	}
}
