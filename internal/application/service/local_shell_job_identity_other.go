//go:build desktop && !darwin

package service

import "context"

// This platform cannot stamp a process start time, so a crash cannot tell a
// reused pid from the original child. Leave the stamp at 0: recovery then
// does not signal, and submit does not kill a command that is already running.
func localProcessStartedAt(context.Context, int) (int64, error) { return 0, nil }

func localRecoverProcess(context.Context, int, int64) error { return nil }
