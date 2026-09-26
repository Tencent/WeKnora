//go:build linux

package host

import (
	"context"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/Tencent/WeKnora/internal/logger"
)

// hideOnce makes WeKnora non-dumpable before its first plugin starts.
var hideOnce sync.Once

// hideFromPlugins keeps plugins, which run as WeKnora's user, out of its
// /proc entries: a non-dumpable process's environ, mem and maps belong to
// root, so a plugin cannot read SYSTEM_AES_KEY or the database password
// from /proc/<ppid>/environ. It also turns off WeKnora's core dumps.
func hideFromPlugins() {
	hideOnce.Do(func() {
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
			logger.Warnf(context.Background(), "[plugin] could not hide WeKnora's /proc entries from plugins: %v", err)
		}
	})
}

// configureChild puts the plugin in its own process group and has the kernel
// kill it if WeKnora dies, so a crash never leaves orphaned plugins.
func configureChild(cmd *exec.Cmd) {
	hideFromPlugins()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

func terminate(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGTERM) }
