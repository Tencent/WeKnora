//go:build windows

package host

import "os/exec"

func configureChild(*exec.Cmd) {}

// terminate kills the plugin: Windows has no SIGTERM for console-less
// children.
func terminate(cmd *exec.Cmd) error { return cmd.Process.Kill() }
