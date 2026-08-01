// Copyright 2022 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// PeriodicTaskManager manages scheduling of periodic tasks.
// It syncs scheduler's entries by calling the config provider periodically.
type PeriodicTaskManager struct {
	s            *Scheduler
	p            PeriodicTaskConfigProvider
	syncInterval time.Duration
	done         chan (struct{})
	wg           sync.WaitGroup
	m            map[string]string // map[hash]entryID
}

type PeriodicTaskManagerOpts struct {
	// Required: must be non nil
	PeriodicTaskConfigProvider PeriodicTaskConfigProvider

	// Optional: if RedisUniversalClient is nil must be non nil
	RedisConnOpt RedisConnOpt

	// Optional: if RedisUniversalClient is non nil, RedisConnOpt is ignored.
	RedisUniversalClient redis.UniversalClient

	// Optional: scheduler options
	*SchedulerOpts

	// Optional: default is 3m
	SyncInterval time.Duration
}

const defaultSyncInterval = 3 * time.Minute

// NewPeriodicTaskManager returns a new PeriodicTaskManager instance.
// The given opts should specify the RedisConnOp and PeriodicTaskConfigProvider at minimum.
func NewPeriodicTaskManager(opts PeriodicTaskManagerOpts) (*PeriodicTaskManager, error) {
	if opts.PeriodicTaskConfigProvider == nil {
		return nil, fmt.Errorf("PeriodicTaskConfigProvider cannot be nil")
	}
	if opts.RedisConnOpt == nil && opts.RedisUniversalClient == nil {
		return nil, fmt.Errorf("RedisConnOpt/RedisUniversalClient cannot be nil")
	}
	var scheduler *Scheduler
	if opts.RedisUniversalClient != nil {
		scheduler = NewSchedulerFromRedisClient(opts.RedisUniversalClient, opts.SchedulerOpts)
	} else {
		scheduler = NewScheduler(opts.RedisConnOpt, opts.SchedulerOpts)
	}

	syncInterval := opts.SyncInterval
	if syncInterval == 0 {
		syncInterval = defaultSyncInterval
	}
	return &PeriodicTaskManager{
		s:            scheduler,
		p:            opts.PeriodicTaskConfigProvider,
		syncInterval: syncInterval,
		done:         make(chan struct{}),
		m:            make(map[string]string),
	}, nil
}

// PeriodicTaskConfigProvider provides configs for periodic tasks.
// GetConfigs will be called by a PeriodicTaskManager periodically to
// sync the scheduler's entries with the configs returned by the provider.
type PeriodicTaskConfigProvider interface {
	GetConfigs() ([]*PeriodicTaskConfig, error)
}

// PeriodicTaskConfig specifies the details of a periodic task.
type PeriodicTaskConfig struct {
	Cronspec string   // required: must be non empty string
	Task     *Task    // required: must be non nil
	Opts     []Option // optional: can be nil
}

func (c *PeriodicTaskConfig) hash() string {
	h := sha256.New()
	_, _ = h.Write([]byte(c.Cronspec))
	_, _ = h.Write([]byte(c.Task.Type()))
	h.Write(c.Task.Payload())
	opts := stringifyOptions(c.Opts)
	sort.Strings(opts)
	for _, opt := range opts {
		_, _ = h.Write([]byte(opt))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func validatePeriodicTaskConfig(c *PeriodicTaskConfig) error {
	if c == nil {
		return fmt.Errorf("PeriodicTaskConfig cannot be nil")
	}
	if c.Task == nil {
		return fmt.Errorf("PeriodicTaskConfig.Task cannot be nil")
	}
	if c.Cronspec == "" {
		return fmt.Errorf("PeriodicTaskConfig.Cronspec cannot be empty")
	}
	return nil
}

// Start starts a scheduler and background goroutine to sync the scheduler with the configs
// returned by the provider.
//
// Start returns any error encountered at start up time.
func (mgr *PeriodicTaskManager) Start() error {
	if mgr.s == nil || mgr.p == nil {
		panic("asynq: cannot start uninitialized PeriodicTaskManager; use NewPeriodicTaskManager to initialize")
	}
	if err := mgr.initialSync(); err != nil {
		return fmt.Errorf("asynq: %w", err)
	}
	if err := mgr.s.Start(); err != nil {
		return fmt.Errorf("asynq: %w", err)
	}
	mgr.wg.Add(1)
	go func() {
		defer mgr.wg.Done()
		ticker := time.NewTicker(mgr.syncInterval)
		for {
			select {
			case <-mgr.done:
				mgr.s.logger.Debugf("Stopping syncer goroutine")
				ticker.Stop()
				return
			case <-ticker.C:
				mgr.sync()
			}
		}
	}()
	return nil
}

// Shutdown gracefully shuts down the manager.
// It notifies a background syncer goroutine to stop and stops scheduler.
func (mgr *PeriodicTaskManager) Shutdown() {
	close(mgr.done)
	mgr.wg.Wait()
	mgr.s.Shutdown()
}

// Run starts the manager and blocks until an os signal to exit the program is received.
// Once it receives a signal, it gracefully shuts down the manager.
func (mgr *PeriodicTaskManager) Run() error {
	if err := mgr.Start(); err != nil {
		return err
	}
	mgr.s.waitForSignals()
	mgr.Shutdown()
	mgr.s.logger.Debugf("PeriodicTaskManager exiting")
	return nil
}

func (mgr *PeriodicTaskManager) initialSync() error {
	configs, err := mgr.p.GetConfigs()
	if err != nil {
		return fmt.Errorf("initial call to GetConfigs failed: %w", err)
	}
	for _, c := range configs {
		if err := validatePeriodicTaskConfig(c); err != nil {
			return fmt.Errorf("initial call to GetConfigs contained an invalid config: %w", err)
		}
	}
	mgr.add(configs)
	return nil
}

func (mgr *PeriodicTaskManager) add(configs []*PeriodicTaskConfig) {
	for _, c := range configs {
		entryID, err := mgr.s.Register(c.Cronspec, c.Task, c.Opts...)
		if err != nil {
			mgr.s.logger.Errorf("Failed to register periodic task: cronspec=%q task=%q err=%v",
				c.Cronspec, c.Task.Type(), err)
			continue
		}
		mgr.m[c.hash()] = entryID
		mgr.s.logger.Infof("Successfully registered periodic task: cronspec=%q task=%q, entryID=%s",
			c.Cronspec, c.Task.Type(), entryID)
	}
}

func (mgr *PeriodicTaskManager) remove(removed map[string]string) {
	for hash, entryID := range removed {
		if err := mgr.s.Unregister(entryID); err != nil {
			mgr.s.logger.Errorf("Failed to unregister periodic task: %v", err)
			continue
		}
		delete(mgr.m, hash)
		mgr.s.logger.Infof("Successfully unregistered periodic task: entryID=%s", entryID)
	}
}

func (mgr *PeriodicTaskManager) sync() {
	configs, err := mgr.p.GetConfigs()
	if err != nil {
		mgr.s.logger.Errorf("Failed to get periodic task configs: %v", err)
		return
	}
	for _, c := range configs {
		if err := validatePeriodicTaskConfig(c); err != nil {
			mgr.s.logger.Errorf("Failed to sync: GetConfigs returned an invalid config: %v", err)
			return
		}
	}
	// Diff and only register/unregister the newly added/removed entries.
	removed := mgr.diffRemoved(configs)
	added := mgr.diffAdded(configs)
	mgr.remove(removed)
	mgr.add(added)
}

// diffRemoved diffs the incoming configs with the registered config and returns
// a map containing hash and entryID of each config that was removed.
func (mgr *PeriodicTaskManager) diffRemoved(configs []*PeriodicTaskConfig) map[string]string {
	newConfigs := make(map[string]string)
	for _, c := range configs {
		newConfigs[c.hash()] = "" // empty value since we don't have entryID yet
	}
	removed := make(map[string]string)
	for k, v := range mgr.m {
		// test whether existing config is present in the incoming configs
		if _, found := newConfigs[k]; !found {
			removed[k] = v
		}
	}
	return removed
}

// diffAdded diffs the incoming configs with the registered configs and returns
// a list of configs that were added.
func (mgr *PeriodicTaskManager) diffAdded(configs []*PeriodicTaskConfig) []*PeriodicTaskConfig {
	var added []*PeriodicTaskConfig
	for _, c := range configs {
		if _, found := mgr.m[c.hash()]; !found {
			added = append(added, c)
		}
	}
	return added
}
