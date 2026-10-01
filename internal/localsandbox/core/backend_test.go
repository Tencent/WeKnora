package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandRequiresArgv(t *testing.T) {
	require.Error(t, Command{Cwd: "/tmp"}.Validate())
	require.NoError(t, Command{Argv: []string{"/bin/echo", "hi"}, Cwd: "/tmp"}.Validate())
}
