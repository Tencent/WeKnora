//go:build linux

package host

import (
	"os/exec"
	"syscall"
)

// configureChild puts the plugin in its own process group and has the kernel
// kill it if WeKnora dies, so a crash never leaves orphaned plugins.
func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

func terminate(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGTERM) }
