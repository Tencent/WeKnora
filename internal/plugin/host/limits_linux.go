//go:build linux

package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/Tencent/WeKnora/internal/logger"
)

// cpuPeriod is the cgroup cpu.max period, in microseconds.
const cpuPeriod = 100000

// envCgroup names a cgroup v2 directory delegated to WeKnora. Each plugin
// process then runs in a child cgroup with memory.max and cpu.max set from
// its resources. Unset: memory is capped with an rlimit only and CPU is not
// capped.
const envCgroup = "WEKNORA_PLUGIN_CGROUP"

// applyLimits caps a started plugin process. Memory is capped with
// RLIMIT_DATA, which needs no privileges and covers the heap and anonymous
// mappings of Go, Python and Node; with a delegated cgroup, memory.max and
// cpu.max apply as well. Failures are logged: an unlimited plugin still
// runs. It returns a cleanup that removes the plugin's cgroup.

func applyLimits(pid int, id string, l limits) func() {
	ctx := context.Background()
	if l.memoryBytes > 0 {
		lim := &unix.Rlimit{Cur: uint64(l.memoryBytes), Max: uint64(l.memoryBytes)}
		if err := unix.Prlimit(pid, unix.RLIMIT_DATA, lim, nil); err != nil {
			logger.Warnf(ctx, "[plugin] %s: cap memory: %v", id, err)
		}
	}
	root := strings.TrimSpace(os.Getenv(envCgroup))
	if root == "" || (l.memoryBytes == 0 && l.cpuMilli == 0) {
		return func() {}
	}
	dir := filepath.Join(root, fmt.Sprintf("plugin-%d", pid))
	if err := setupCgroup(dir, pid, l); err != nil {
		logger.Warnf(ctx, "[plugin] %s: cgroup %s: %v", id, dir, err)
		_ = os.Remove(dir)
		return func() {}
	}
	return func() { _ = os.Remove(dir) }
}

func setupCgroup(dir string, pid int, l limits) error {
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	write := func(file, value string) error {
		return os.WriteFile(filepath.Join(dir, file), []byte(value), 0o644)
	}
	if l.memoryBytes > 0 {
		if err := write("memory.max", strconv.FormatInt(l.memoryBytes, 10)); err != nil {
			return err
		}
	}
	if l.cpuMilli > 0 {
		quota := l.cpuMilli * cpuPeriod / 1000
		if err := write("cpu.max", fmt.Sprintf("%d %d", quota, cpuPeriod)); err != nil {
			return err
		}
	}
	return write("cgroup.procs", strconv.Itoa(pid))
}
