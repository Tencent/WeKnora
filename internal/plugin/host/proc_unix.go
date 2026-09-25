//go:build unix && !linux

package host

import (
	"os/exec"
	"syscall"
)

// configureChild puts the plugin in its own process group, so a signal
// meant for WeKnora's terminal does not reach it directly.
func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminate(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGTERM) }
