//go:build windows

package sandbox

import "os/exec"

func setSysProcAttr(cmd *exec.Cmd) {
	// Windows does not support Setpgid; process groups are managed differently.
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
