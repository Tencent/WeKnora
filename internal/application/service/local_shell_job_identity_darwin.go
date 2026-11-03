//go:build desktop && darwin

package service

import (
	"context"
	"errors"
	"syscall"

	"github.com/shirou/gopsutil/v3/process"
)

func localProcessStartedAt(ctx context.Context, pid int) (int64, error) {
	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0, err
	}
	return proc.CreateTimeWithContext(ctx)
}

// localRecoverProcess kills a leftover job process group only when the process
// at pgid still is the one the dead watcher recorded (PID-reuse guard).
func localRecoverProcess(ctx context.Context, pgid int, expected int64) error {
	actual, err := localProcessStartedAt(ctx, pgid)
	if errors.Is(err, process.ErrorProcessNotRunning) {
		return nil
	}
	if err != nil {
		return err
	}
	if actual != expected {
		return nil // reused PID: do not signal
	}
	group, err := syscall.Getpgid(pgid)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	if group != pgid {
		return nil
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
