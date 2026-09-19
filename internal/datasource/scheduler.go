package datasource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// Scheduler manages cron-based periodic sync for data sources.
//
// robfig/cron fires at absolute wall-clock times (e.g. "0 0 * * * *" always fires
// at the top of every hour regardless of when the process started). So multiple
// instances will fire at the same moment. Dedup is handled by two layers:
//
//  1. Trigger lock serializes check+create+enqueue across manual/scheduled nodes.
//  2. HasRunningSync prevents enqueue while an earlier run is still active.
//  3. Per-run TaskID identifies one SyncLog and never blocks future runs.
type Scheduler struct {
	cron            *cron.Cron
	dsRepo          interfaces.DataSourceRepository
	syncLogRepo     interfaces.SyncLogRepository
	taskEnqueuer    interfaces.TaskEnqueuer
	syncCoordinator *SyncCoordinator
	dispatcher      *SyncOutboxDispatcher
	dispatchCancel  context.CancelFunc
	dispatchWG      sync.WaitGroup

	mu      sync.Mutex
	entries map[string]cron.EntryID // dataSourceID → cron entry ID
}

// NewScheduler creates a new Scheduler.
func NewScheduler(
	dsRepo interfaces.DataSourceRepository,
	syncLogRepo interfaces.SyncLogRepository,
	taskEnqueuer interfaces.TaskEnqueuer,
) *Scheduler {
	return NewSchedulerWithCoordinator(dsRepo, syncLogRepo, taskEnqueuer, NewSyncCoordinator(nil))
}

// NewSchedulerWithCoordinator is the production constructor. The coordinator
// is shared with DataSourceService so manual and scheduled triggers contend on
// the same local/Redis trigger lock.
func NewSchedulerWithCoordinator(
	dsRepo interfaces.DataSourceRepository,
	syncLogRepo interfaces.SyncLogRepository,
	taskEnqueuer interfaces.TaskEnqueuer,
	coordinator *SyncCoordinator,
) *Scheduler {
	if coordinator == nil {
		coordinator = NewSyncCoordinator(nil)
	}
	return &Scheduler{
		cron: cron.New(cron.WithSeconds(), cron.WithChain(
			cron.Recover(cron.DefaultLogger),
		)),
		dsRepo:          dsRepo,
		syncLogRepo:     syncLogRepo,
		taskEnqueuer:    taskEnqueuer,
		syncCoordinator: coordinator,
		dispatcher:      NewSyncOutboxDispatcher(syncLogRepo, taskEnqueuer),
		entries:         make(map[string]cron.EntryID),
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
	s.startOutboxDispatcher()
	logger.Infof(ctx, "[Scheduler] started with %d cron entries", len(s.entries))
	return nil
}

// Stop gracefully stops the cron runner and waits for running jobs to finish.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	cancelDispatch := s.dispatchCancel
	s.dispatchCancel = nil
	s.mu.Unlock()
	if cancelDispatch != nil {
		cancelDispatch()
		s.dispatchWG.Wait()
	}
	ctx := s.cron.Stop()
	<-ctx.Done()
}

func (s *Scheduler) startOutboxDispatcher() {
	s.mu.Lock()
	if s.dispatchCancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.dispatchCancel = cancel
	s.dispatchWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.dispatchWG.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			if _, err := s.dispatcher.DispatchPending(ctx, defaultOutboxBatchSize); err != nil && ctx.Err() == nil {
				logger.Warnf(ctx, "[Scheduler] sync outbox dispatch pass failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
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
// The trigger lock makes the DB running check and task creation one serialized
// critical section across application instances.
func (s *Scheduler) triggerSync(dataSourceID string, tenantID uint64) {
	ctx := context.Background()
	triggerLock, lockErr := s.syncCoordinator.TryAcquireTrigger(ctx, dataSourceID)
	if lockErr != nil {
		if errors.Is(lockErr, ErrSyncAlreadyRunning) {
			logger.Infof(ctx, "[Scheduler] sync trigger already being coordinated for ds=%s", dataSourceID)
		}
		return
	}
	defer func() { _ = triggerLock.Release() }()
	ctx = triggerLock.Context()

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
		ID:           uuid.NewString(),
		DataSourceID: dataSourceID,
		TenantID:     tenantID,
		Status:       types.SyncLogStatusPending,
		StartedAt:    time.Now().UTC(),
	}
	payload := &types.DataSourceSyncPayload{
		DataSourceID: dataSourceID,
		TenantID:     tenantID,
		SyncLogID:    syncLog.ID,
		ForceFull:    false,
		Trigger:      "schedule",
	}
	langfuse.InjectTracing(ctx, payload)
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		logger.Errorf(ctx, "[Scheduler] encode sync payload for ds=%s: %v", dataSourceID, err)
		return
	}
	syncLog.TaskID = SyncTaskID(dataSourceID, syncLog.ID)
	syncLog.TaskPayload = types.JSON(payloadJSON)
	if err := s.syncLogRepo.Create(ctx, syncLog); err != nil {
		logger.Errorf(ctx, "[Scheduler] failed to create sync outbox row for ds=%s: %v", dataSourceID, err)
		return
	}
	if _, err := s.dispatcher.Dispatch(ctx, syncLog); err != nil {
		logger.Warnf(ctx, "[Scheduler] sync persisted for retry but immediate dispatch failed: ds=%s log=%s err=%v", dataSourceID, syncLog.ID, err)
	}

	logger.Infof(ctx, "[Scheduler] sync task enqueued for ds=%s syncLog=%s", dataSourceID, syncLog.ID)
}

// EntryCount returns the number of active cron entries (for testing/monitoring).
func (s *Scheduler) EntryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
