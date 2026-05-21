//go:build windows

package sandbox

import (
	"os/exec"
)

func setupProcessGroup(cmd *exec.Cmd) {
	// Windows does not support Setpgid; process groups are managed via Job Objects
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
