//go:build desktop

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// signalRecoveredProcess kills a leftover process group after an identity
// check. Tests replace it. A failure must not stop recovery of other jobs:
// the watcher that owned the job is already gone, and a failed identity
// check is not permission to guess which process to signal.
var signalRecoveredProcess = localRecoverProcess

func localJobNotice(meta sandbox.ShellJobMeta, result interfaces.WorkResult,
	finished time.Time,
) interfaces.BackgroundNotice {
	title := strings.Join(strings.Fields(meta.Title), " ")
	if len(title) > 160 {
		title = strings.ToValidUTF8(title[:160], "")
	}
	return interfaces.BackgroundNotice{
		ID: meta.ID, Kind: "shell", FinishedAt: finished,
		Text: fmt.Sprintf("Background shell %s %s: %s. Use work_wait(wait_sec=0) to read its output.",
			meta.ID, result.State, title),
	}
}

func readLocalJobJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	// The private job directory contains metadata written by this service.
	// Legacy stdin wrappers and output baselines can exceed 64 KiB; recovery
	// must accept the same metadata as the normal ReadShellJob path.
	return json.NewDecoder(file).Decode(target)
}

// recoverJobs is the ONLY writer allowed besides the watcher: the previous
// owner crashed. Terminal jobs within NoticeRetention re-push their notice
// (the in-memory inbox was lost); older directories are deleted (D5). A
// running job is identity-checked, killed, and recorded lost.
func (s *LocalShellJobService) recoverJobs(ctx context.Context) error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, entry := range entries {
		if !entry.IsDir() || !sandbox.ValidShellJobID(entry.Name()) {
			continue
		}
		if err := s.recoverJob(ctx, entry.Name(), now); err != nil {
			logger.Warnf(ctx, "[LocalShellJob] skip recovery of %s: %v", entry.Name(), err)
		}
	}
	return nil
}

func (s *LocalShellJobService) recoverJob(ctx context.Context, name string, now time.Time) error {
	directory := filepath.Join(s.root, name)
	var meta sandbox.ShellJobMeta
	var status sandbox.ShellJobStatus
	if err := readLocalJobJSON(filepath.Join(directory, "meta.json"), &meta); err != nil {
		return err
	}
	if meta.ID != name || meta.TenantID == 0 || meta.SessionID == "" {
		return fmt.Errorf("invalid local job metadata in %s", name)
	}
	if err := readLocalJobJSON(filepath.Join(directory, "status.json"), &status); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		status = sandbox.ShellJobStatus{State: interfaces.WorkStateRunning, StartedAt: meta.CreatedAt}
	}
	if status.State == interfaces.WorkStateRunning {
		if status.PGID > 0 && status.ProcStartedAt > 0 {
			if kerr := signalRecoveredProcess(ctx, status.PGID, status.ProcStartedAt); kerr != nil {
				logger.Warnf(ctx, "[LocalShellJob] could not signal %s pid=%d: %v", name, status.PGID, kerr)
			}
		}
		ended := now
		status.State, status.EndedAt = interfaces.WorkStateLost, &ended
		if werr := sandbox.WriteShellJobJSON(filepath.Join(directory, "status.json"), status); werr != nil {
			return werr
		}
	}
	if !interfaces.IsTerminalWorkState(status.State) || status.EndedAt == nil {
		return fmt.Errorf("invalid local terminal status in %s", name)
	}
	if status.EndedAt.Before(now.Add(-interfaces.NoticeRetention)) {
		if rerr := os.RemoveAll(directory); rerr != nil {
			return rerr
		}
		return nil
	}
	j := &localJob{
		meta: meta, status: status,
		done: make(chan struct{}), reported: make(chan struct{}),
	}
	close(j.done)
	close(j.reported)
	s.jobs[meta.ID] = j
	result := ShellJobResult(sandbox.ShellJobSnapshot{
		Meta: meta, Status: status, WatcherAlive: false,
	}, true)
	// Pending lists the oldest FinishedAt first. A recovered lost job uses
	// its start time while that start is still inside retention.
	finished := *status.EndedAt
	if status.State == interfaces.WorkStateLost && !status.StartedAt.IsZero() &&
		!status.StartedAt.Before(now.Add(-interfaces.NoticeRetention)) {
		finished = status.StartedAt
	}
	if _, perr := s.bg.Inbox.Push(ctx, localRef(meta),
		localJobNotice(meta, result, finished)); perr != nil {
		return perr
	}
	return nil
}
