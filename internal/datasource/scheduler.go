package datasource

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/robfig/cron/v3"
)

// Scheduler manages cron-based periodic sync for data sources.
//
// robfig/cron fires at absolute wall-clock times (e.g. "0 0 * * * *" always fires
// at the top of every hour regardless of when the process started). So multiple
// instances will fire at the same moment. Dedup is handled by two layers:
//
//  1. HasRunningSync — if a previous sync is still running, skip (prevent overlap).
//  2. asynq.TaskID  — deterministic ID per (dataSourceID, minute). Redis ensures
//     only one task with a given ID is enqueued. Losers get ErrTaskIDConflict.
type Scheduler struct {
	cron         *cron.Cron
	dsRepo       interfaces.DataSourceRepository
	syncLogRepo  interfaces.SyncLogRepository
	taskEnqueuer interfaces.TaskEnqueuer

	mu      sync.Mutex
	entries map[string]cron.EntryID // dataSourceID → cron entry ID
}

// NewScheduler creates a new Scheduler.
func NewScheduler(
	dsRepo interfaces.DataSourceRepository,
	syncLogRepo interfaces.SyncLogRepository,
	taskEnqueuer interfaces.TaskEnqueuer,
) *Scheduler {
	return &Scheduler{
		cron: cron.New(cron.WithSeconds(), cron.WithChain(
			cron.Recover(cron.DefaultLogger),
		)),
		dsRepo:       dsRepo,
		syncLogRepo:  syncLogRepo,
		taskEnqueuer: taskEnqueuer,
		entries:      make(map[string]cron.EntryID),
	}
}

// Start loads all active data sources from the database and registers their
// cron schedules. Then starts the cron runner in the background.
func (s *Scheduler) Start(ctx context.Context) error {
	dataSources, err := s.dsRepo.FindActive(ctx)
	if err != nil {
		return fmt.Errorf("load active data sources: %w", err)
	}

	for _, ds := range dataSources {
		if ds.SyncSchedule == "" {
			continue
		}
		if err := s.addEntry(ds); err != nil {
			logger.Warnf(ctx, "[Scheduler] failed to register cron for ds=%s schedule=%q: %v",
				ds.ID, ds.SyncSchedule, err)
		}
	}

	s.cron.Start()
	logger.Infof(ctx, "[Scheduler] started with %d cron entries", len(s.entries))
	return nil
}

// Stop gracefully stops the cron runner and waits for running jobs to finish.
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
}

// AddOrUpdate registers (or re-registers) a cron entry for the given data source.
func (s *Scheduler) AddOrUpdate(ds *types.DataSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entryID, ok := s.entries[ds.ID]; ok {
		s.cron.Remove(entryID)
		delete(s.entries, ds.ID)
	}

	if ds.Status != types.DataSourceStatusActive || ds.SyncSchedule == "" {
		return nil
	}

	return s.addEntryLocked(ds)
}

// Remove removes the cron entry for a data source.
func (s *Scheduler) Remove(dataSourceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entryID, ok := s.entries[dataSourceID]; ok {
		s.cron.Remove(entryID)
		delete(s.entries, dataSourceID)
	}
}

func (s *Scheduler) addEntry(ds *types.DataSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addEntryLocked(ds)
}

func (s *Scheduler) addEntryLocked(ds *types.DataSource) error {
	dsID := ds.ID
	tenantID := ds.TenantID

	entryID, err := s.cron.AddFunc(ds.SyncSchedule, func() {
		s.triggerSync(dsID, tenantID)
	})
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", ds.SyncSchedule, err)
	}

	s.entries[dsID] = entryID
	return nil
}

// triggerSync is called by the cron runner on each tick.
//
// Layer 1 — DB: if a previous sync is still running, skip. This prevents
// overlap when a sync takes longer than the cron interval.
//
// Layer 2 — Redis: deterministic asynq.TaskID = "dssync:<dsID>:<minute>".
// Since robfig/cron fires at absolute wall-clock times, all instances trigger
// at the same minute. The first Enqueue wins; others get ErrTaskIDConflict.
func (s *Scheduler) triggerSync(dataSourceID string, tenantID uint64) {
	ctx := context.Background()

	ds, err := s.dsRepo.FindByID(ctx, dataSourceID)
	if err != nil || ds == nil || ds.Status != types.DataSourceStatusActive {
		logger.Infof(ctx, "[Scheduler] skipping sync for ds=%s (not active or not found)", dataSourceID)
		return
	}

	// Layer 1: prevent overlap with a still-running sync
	if running, _ := s.syncLogRepo.HasRunningSync(ctx, dataSourceID); running {
		logger.Infof(ctx, "[Scheduler] skipping sync for ds=%s (previous sync still running)", dataSourceID)
		return
	}

	syncLog := &types.SyncLog{
		DataSourceID: dataSourceID,
		TenantID:     tenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	if err := s.syncLogRepo.Create(ctx, syncLog); err != nil {
		logger.Errorf(ctx, "[Scheduler] failed to create sync log for ds=%s: %v", dataSourceID, err)
		return
	}

	payload := &types.DataSourceSyncPayload{
		DataSourceID: dataSourceID,
		TenantID:     tenantID,
		SyncLogID:    syncLog.ID,
		ForceFull:    false,
	}
	payloadJSON, _ := json.Marshal(payload)
	task := asynq.NewTask(types.TypeDataSourceSync, payloadJSON)

	// Layer 2: deterministic TaskID — all instances in the same minute produce the same ID
	taskID := fmt.Sprintf("dssync:%s:%s", dataSourceID, time.Now().UTC().Truncate(time.Minute).Format("200601021504"))

	_, err = s.taskEnqueuer.Enqueue(task,
		asynq.Queue("low"),
		asynq.TaskID(taskID),
	)
	if err != nil {
		if err == asynq.ErrTaskIDConflict {
			logger.Infof(ctx, "[Scheduler] sync already enqueued by another instance for ds=%s", dataSourceID)
			syncLog.Status = types.SyncLogStatusCanceled
			now := time.Now().UTC()
			syncLog.FinishedAt = &now
			syncLog.ErrorMessage = "deduplicated: another instance enqueued first"
			_ = s.syncLogRepo.Update(ctx, syncLog)
			return
		}
		logger.Errorf(ctx, "[Scheduler] failed to enqueue sync task for ds=%s: %v", dataSourceID, err)
		syncLog.Status = types.SyncLogStatusFailed
		now := time.Now().UTC()
		syncLog.FinishedAt = &now
		syncLog.ErrorMessage = fmt.Sprintf("enqueue failed: %v", err)
		_ = s.syncLogRepo.Update(ctx, syncLog)
		return
	}

	logger.Infof(ctx, "[Scheduler] sync task enqueued for ds=%s syncLog=%s", dataSourceID, syncLog.ID)
}

// EntryCount returns the number of active cron entries (for testing/monitoring).
func (s *Scheduler) EntryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
