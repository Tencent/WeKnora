// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/log"
	"github.com/hibiken/asynq/internal/rdb"
	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
)

// A Scheduler kicks off tasks at regular intervals based on the user defined schedule.
//
// Schedulers are safe for concurrent use by multiple goroutines.
type Scheduler struct {
	id string

	state *serverState

	heartbeatInterval time.Duration
	logger            *log.Logger
	client            *Client
	rdb               *rdb.RDB
	cron              *cron.Cron
	location          *time.Location
	done              chan struct{}
	wg                sync.WaitGroup
	preEnqueueFunc    func(task *Task, opts []Option)
	postEnqueueFunc   func(info *TaskInfo, err error)
	errHandler        func(task *Task, opts []Option, err error)

	// guards idmap
	mu sync.Mutex
	// idmap maps Scheduler's entry ID to cron.EntryID
	// to avoid using cron.EntryID as the public API of
	// the Scheduler.
	idmap map[string]cron.EntryID
}

const defaultHeartbeatInterval = 10 * time.Second

// NewScheduler returns a new Scheduler instance given the redis connection option.
// The parameter opts is optional, defaults will be used if opts is set to nil
func NewScheduler(r RedisConnOpt, opts *SchedulerOpts) *Scheduler {
	scheduler := newScheduler(opts)

	redisClient, ok := r.MakeRedisClient().(redis.UniversalClient)
	if !ok {
		panic(fmt.Sprintf("asynq: unsupported RedisConnOpt type %T", r))
	}

	rdb := rdb.NewRDB(redisClient)

	scheduler.rdb = rdb
	scheduler.client = &Client{broker: rdb, sharedConnection: false}

	return scheduler
}

// NewSchedulerFromRedisClient returns a new instance of Scheduler given a redis.UniversalClient
// The parameter opts is optional, defaults will be used if opts is set to nil.
// Warning: The underlying redis connection pool will not be closed by Asynq, you are responsible for closing it.
func NewSchedulerFromRedisClient(c redis.UniversalClient, opts *SchedulerOpts) *Scheduler {
	scheduler := newScheduler(opts)

	scheduler.rdb = rdb.NewRDB(c)
	scheduler.client = NewClientFromRedisClient(c)

	return scheduler
}

func newScheduler(opts *SchedulerOpts) *Scheduler {
	if opts == nil {
		opts = &SchedulerOpts{}
	}

	heartbeatInterval := opts.HeartbeatInterval
	if heartbeatInterval <= 0 {
		heartbeatInterval = defaultHeartbeatInterval
	}

	logger := log.NewLogger(opts.Logger)
	loglevel := opts.LogLevel
	if loglevel == level_unspecified {
		loglevel = InfoLevel
	}
	logger.SetLevel(toInternalLogLevel(loglevel))

	loc := opts.Location
	if loc == nil {
		loc = time.UTC
	}

	return &Scheduler{
		id:                generateSchedulerID(),
		state:             &serverState{value: srvStateNew},
		heartbeatInterval: heartbeatInterval,
		logger:            logger,
		cron:              cron.New(cron.WithLocation(loc)),
		location:          loc,
		done:              make(chan struct{}),
		preEnqueueFunc:    opts.PreEnqueueFunc,
		postEnqueueFunc:   opts.PostEnqueueFunc,
		errHandler:        opts.EnqueueErrorHandler,
		idmap:             make(map[string]cron.EntryID),
	}
}

func generateSchedulerID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s:%d:%v", host, os.Getpid(), uuid.New())
}

// SchedulerOpts specifies scheduler options.
type SchedulerOpts struct {
	// HeartbeatInterval specifies the interval between scheduler heartbeats.
	//
	// If unset, zero or a negative value, the interval is set to 10 second.
	//
	// Note: Setting this value too low may add significant load to redis.
	//
	// By default, HeartbeatInterval is set to 10 seconds.
	HeartbeatInterval time.Duration

	// Logger specifies the logger used by the scheduler instance.
	//
	// If unset, the default logger is used.
	Logger Logger

	// LogLevel specifies the minimum log level to enable.
	//
	// If unset, InfoLevel is used by default.
	LogLevel LogLevel

	// Location specifies the time zone location.
	//
	// If unset, the UTC time zone (time.UTC) is used.
	Location *time.Location

	// PreEnqueueFunc, if provided, is called before a task gets enqueued by Scheduler.
	// The callback function should return quickly to not block the current thread.
	PreEnqueueFunc func(task *Task, opts []Option)

	// PostEnqueueFunc, if provided, is called after a task gets enqueued by Scheduler.
	// The callback function should return quickly to not block the current thread.
	PostEnqueueFunc func(info *TaskInfo, err error)

	// Deprecated: Use PostEnqueueFunc instead
	// EnqueueErrorHandler gets called when scheduler cannot enqueue a registered task
	// due to an error.
	EnqueueErrorHandler func(task *Task, opts []Option, err error)
}

// enqueueJob encapsulates the job of enqueuing a task and recording the event.
type enqueueJob struct {
	id              uuid.UUID
	cronspec        string
	task            *Task
	opts            []Option
	location        *time.Location
	logger          *log.Logger
	client          *Client
	rdb             *rdb.RDB
	preEnqueueFunc  func(task *Task, opts []Option)
	postEnqueueFunc func(info *TaskInfo, err error)
	errHandler      func(task *Task, opts []Option, err error)
}

func (j *enqueueJob) Run() {
	if j.preEnqueueFunc != nil {
		j.preEnqueueFunc(j.task, j.opts)
	}
	info, err := j.client.Enqueue(j.task, j.opts...)
	if j.postEnqueueFunc != nil {
		j.postEnqueueFunc(info, err)
	}
	if err != nil {
		if j.errHandler != nil {
			j.errHandler(j.task, j.opts, err)
		}
		return
	}
	j.logger.Debugf("scheduler enqueued a task: %+v", info)
	event := &base.SchedulerEnqueueEvent{
		TaskID:     info.ID,
		EnqueuedAt: time.Now().In(j.location),
	}
	err = j.rdb.RecordSchedulerEnqueueEvent(j.id.String(), event)
	if err != nil {
		j.logger.Warnf("scheduler could not record enqueue event of enqueued task %s: %v", info.ID, err)
	}
}

// Register registers a task to be enqueued on the given schedule specified by the cronspec.
// It returns an ID of the newly registered entry.
func (s *Scheduler) Register(cronspec string, task *Task, opts ...Option) (entryID string, err error) {
	job := &enqueueJob{
		id:              uuid.New(),
		cronspec:        cronspec,
		task:            task,
		opts:            opts,
		location:        s.location,
		client:          s.client,
		rdb:             s.rdb,
		logger:          s.logger,
		preEnqueueFunc:  s.preEnqueueFunc,
		postEnqueueFunc: s.postEnqueueFunc,
		errHandler:      s.errHandler,
	}
	cronID, err := s.cron.AddJob(cronspec, job)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.idmap[job.id.String()] = cronID
	s.mu.Unlock()
	return job.id.String(), nil
}

// Unregister removes a registered entry by entry ID.
// Unregister returns a non-nil error if no entries were found for the given entryID.
func (s *Scheduler) Unregister(entryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cronID, ok := s.idmap[entryID]
	if !ok {
		return fmt.Errorf("asynq: no scheduler entry found")
	}
	delete(s.idmap, entryID)
	s.cron.Remove(cronID)
	return nil
}

// Run starts the scheduler until an os signal to exit the program is received.
// It returns an error if scheduler is already running or has been shutdown.
func (s *Scheduler) Run() error {
	if err := s.Start(); err != nil {
		return err
	}
	s.waitForSignals()
	s.Shutdown()
	return nil
}

// Start starts the scheduler.
// It returns an error if the scheduler is already running or has been shutdown.
func (s *Scheduler) Start() error {
	if err := s.start(); err != nil {
		return err
	}
	s.logger.Info("Scheduler starting")
	s.logger.Infof("Scheduler timezone is set to %v", s.location)
	s.cron.Start()
	s.wg.Add(1)
	go s.runHeartbeater()
	return nil
}

// Checks server state and returns an error if pre-condition is not met.
// Otherwise it sets the server state to active.
func (s *Scheduler) start() error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	switch s.state.value {
	case srvStateActive:
		return fmt.Errorf("asynq: the scheduler is already running")
	case srvStateClosed:
		return fmt.Errorf("asynq: the scheduler has already been stopped")
	}
	s.state.value = srvStateActive
	return nil
}

// Shutdown stops and shuts down the scheduler.
func (s *Scheduler) Shutdown() {
	s.state.mu.Lock()
	if s.state.value == srvStateNew || s.state.value == srvStateClosed {
		// scheduler is not running, do nothing and return.
		s.state.mu.Unlock()
		return
	}
	s.state.value = srvStateClosed
	s.state.mu.Unlock()

	s.logger.Info("Scheduler shutting down")
	close(s.done) // signal heartbeater to stop
	ctx := s.cron.Stop()
	<-ctx.Done()
	s.wg.Wait()

	s.clearHistory()
	if err := s.client.Close(); err != nil {
		s.logger.Errorf("Failed to close redis client connection: %v", err)
	}
	s.logger.Info("Scheduler stopped")
}

func (s *Scheduler) runHeartbeater() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.heartbeatInterval)
	for {
		select {
		case <-s.done:
			s.logger.Debugf("Scheduler heatbeater shutting down")
			if err := s.rdb.ClearSchedulerEntries(s.id); err != nil {
				s.logger.Errorf("Failed to clear the scheduler entries: %v", err)
			}
			ticker.Stop()
			return
		case <-ticker.C:
			s.beat()
		}
	}
}

// beat writes a snapshot of entries to redis.
func (s *Scheduler) beat() {
	var entries []*base.SchedulerEntry
	for _, entry := range s.cron.Entries() {
		job := entry.Job.(*enqueueJob)
		e := &base.SchedulerEntry{
			ID:      job.id.String(),
			Spec:    job.cronspec,
			Type:    job.task.Type(),
			Payload: job.task.Payload(),
			Opts:    stringifyOptions(job.opts),
			Next:    entry.Next,
			Prev:    entry.Prev,
		}
		entries = append(entries, e)
	}
	if err := s.rdb.WriteSchedulerEntries(s.id, entries, s.heartbeatInterval*2); err != nil {
		s.logger.Warnf("Scheduler could not write heartbeat data: %v", err)
	}
}

func stringifyOptions(opts []Option) []string {
	var res []string
	for _, opt := range opts {
		res = append(res, opt.String())
	}
	return res
}

func (s *Scheduler) clearHistory() {
	for _, entry := range s.cron.Entries() {
		job := entry.Job.(*enqueueJob)
		if err := s.rdb.ClearSchedulerHistory(job.id.String()); err != nil {
			s.logger.Warnf("Could not clear scheduler history for entry %q: %v", job.id.String(), err)
		}
	}
}

// Ping performs a ping against the redis connection.
func (s *Scheduler) Ping() error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if s.state.value == srvStateClosed {
		return nil
	}

	return s.rdb.Ping()
}
