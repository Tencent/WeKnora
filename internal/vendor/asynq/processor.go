// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq/internal/base"
	asynqcontext "github.com/hibiken/asynq/internal/context"
	"github.com/hibiken/asynq/internal/errors"
	"github.com/hibiken/asynq/internal/log"
	"github.com/hibiken/asynq/internal/timeutil"
	"golang.org/x/time/rate"
)

type processor struct {
	logger *log.Logger
	broker base.Broker
	clock  timeutil.Clock

	handler   Handler
	baseCtxFn func() context.Context

	queueConfig map[string]int

	// orderedQueues is set only in strict-priority mode.
	orderedQueues []string

	taskCheckInterval time.Duration
	retryDelayFunc    RetryDelayFunc
	isFailureFunc     func(error) bool

	errHandler      ErrorHandler
	shutdownTimeout time.Duration

	// channel via which to send sync requests to syncer.
	syncRequestCh chan<- *syncRequest

	// rate limiter to prevent spamming logs with a bunch of errors.
	errLogLimiter *rate.Limiter

	// sema is a counting semaphore to ensure the number of active workers
	// does not exceed the limit.
	sema chan struct{}

	// channel to communicate back to the long running "processor" goroutine.
	// once is used to send value to the channel only once.
	done chan struct{}
	once sync.Once

	// quit channel is closed when the shutdown of the "processor" goroutine starts.
	quit chan struct{}

	// abort channel communicates to the in-flight worker goroutines to stop.
	abort chan struct{}

	// cancelations is a set of cancel functions for all active tasks.
	cancelations *base.Cancelations

	starting chan<- *workerInfo
	finished chan<- *base.TaskMessage
}

type processorParams struct {
	logger            *log.Logger
	broker            base.Broker
	baseCtxFn         func() context.Context
	retryDelayFunc    RetryDelayFunc
	taskCheckInterval time.Duration
	isFailureFunc     func(error) bool
	syncCh            chan<- *syncRequest
	cancelations      *base.Cancelations
	concurrency       int
	queues            map[string]int
	strictPriority    bool
	errHandler        ErrorHandler
	shutdownTimeout   time.Duration
	starting          chan<- *workerInfo
	finished          chan<- *base.TaskMessage
}

// newProcessor constructs a new processor.
func newProcessor(params processorParams) *processor {
	queues := normalizeQueues(params.queues)
	orderedQueues := []string(nil)
	if params.strictPriority {
		orderedQueues = sortByPriority(queues)
	}
	return &processor{
		logger:            params.logger,
		broker:            params.broker,
		baseCtxFn:         params.baseCtxFn,
		clock:             timeutil.NewRealClock(),
		queueConfig:       queues,
		orderedQueues:     orderedQueues,
		taskCheckInterval: params.taskCheckInterval,
		retryDelayFunc:    params.retryDelayFunc,
		isFailureFunc:     params.isFailureFunc,
		syncRequestCh:     params.syncCh,
		cancelations:      params.cancelations,
		errLogLimiter:     rate.NewLimiter(rate.Every(3*time.Second), 1),
		sema:              make(chan struct{}, params.concurrency),
		done:              make(chan struct{}),
		quit:              make(chan struct{}),
		abort:             make(chan struct{}),
		errHandler:        params.errHandler,
		handler:           HandlerFunc(func(ctx context.Context, t *Task) error { return fmt.Errorf("handler not set") }),
		shutdownTimeout:   params.shutdownTimeout,
		starting:          params.starting,
		finished:          params.finished,
	}
}

// Note: stops only the "processor" goroutine, does not stop workers.
// It's safe to call this method multiple times.
func (p *processor) stop() {
	p.once.Do(func() {
		p.logger.Debug("Processor shutting down...")
		// Unblock if processor is waiting for sema token.
		close(p.quit)
		// Signal the processor goroutine to stop processing tasks
		// from the queue.
		p.done <- struct{}{}
	})
}

// NOTE: once shutdown, processor cannot be re-started.
func (p *processor) shutdown() {
	p.stop()

	time.AfterFunc(p.shutdownTimeout, func() { close(p.abort) })

	p.logger.Info("Waiting for all workers to finish...")
	// block until all workers have released the token
	for i := 0; i < cap(p.sema); i++ {
		p.sema <- struct{}{}
	}
	p.logger.Info("All workers have finished")
}

func (p *processor) start(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-p.done:
				p.logger.Debug("Processor done")
				return
			default:
				p.exec()
			}
		}
	}()
}

// exec pulls a task out of the queue and starts a worker goroutine to
// process the task.
func (p *processor) exec() {
	select {
	case <-p.quit:
		return
	case p.sema <- struct{}{}: // acquire token
		qnames := p.queues()
		msg, leaseExpirationTime, err := p.broker.Dequeue(qnames...)
		switch {
		case errors.Is(err, errors.ErrNoProcessableTask):
			p.logger.Debug("All queues are empty")
			// Queues are empty, this is a normal behavior.
			// Sleep to avoid slamming redis and let scheduler move tasks into queues.
			// Note: We are not using blocking pop operation and polling queues instead.
			// This adds significant load to redis.
			jitter := rand.N(p.taskCheckInterval)
			time.Sleep(p.taskCheckInterval/2 + jitter)
			<-p.sema // release token
			return
		case err != nil:
			if p.errLogLimiter.Allow() {
				p.logger.Errorf("Dequeue error: %v", err)
			}
			<-p.sema // release token
			return
		}

		lease := base.NewLease(leaseExpirationTime)
		deadline := p.computeDeadline(msg)
		p.starting <- &workerInfo{msg, time.Now(), deadline, lease}
		go func() {
			defer func() {
				p.finished <- msg
				<-p.sema // release token
			}()

			ctx, cancel := asynqcontext.New(p.baseCtxFn(), msg, deadline)
			p.cancelations.Add(msg.ID, cancel)
			defer func() {
				cancel()
				p.cancelations.Delete(msg.ID)
			}()

			// check context before starting a worker goroutine.
			select {
			case <-ctx.Done():
				// already canceled (e.g. deadline exceeded).
				p.handleFailedMessage(ctx, lease, msg, ctx.Err())
				return
			default:
			}

			resCh := make(chan error, 1)
			go func() {
				task := newTask(
					msg.Type,
					msg.Payload,
					&ResultWriter{
						id:     msg.ID,
						qname:  msg.Queue,
						broker: p.broker,
						ctx:    ctx,
					},
				)
				task.headers = msg.Headers
				resCh <- p.perform(ctx, task)
			}()

			select {
			case <-p.abort:
				// time is up, push the message back to queue and quit this worker goroutine.
				p.logger.Warnf("Quitting worker. task id=%s", msg.ID)
				p.requeue(lease, msg)
				return
			case <-lease.Done():
				cancel()
				p.handleFailedMessage(ctx, lease, msg, ErrLeaseExpired)
				return
			case <-ctx.Done():
				p.handleFailedMessage(ctx, lease, msg, ctx.Err())
				return
			case resErr := <-resCh:
				if resErr != nil {
					p.handleFailedMessage(ctx, lease, msg, resErr)
					return
				}
				p.handleSucceededMessage(lease, msg)
			}
		}()
	}
}

func (p *processor) requeue(l *base.Lease, msg *base.TaskMessage) {
	if !l.IsValid() {
		// If lease is not valid, do not write to redis; Let recoverer take care of it.
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), l.Deadline())
	defer cancel()
	err := p.broker.Requeue(ctx, msg)
	if err != nil {
		p.logger.Errorf("Could not push task id=%s back to queue: %v", msg.ID, err)
	} else {
		p.logger.Infof("Pushed task id=%s back to queue", msg.ID)
	}
}

func (p *processor) handleSucceededMessage(l *base.Lease, msg *base.TaskMessage) {
	if msg.Retention > 0 {
		p.markAsComplete(l, msg)
	} else {
		p.markAsDone(l, msg)
	}
}

func (p *processor) markAsComplete(l *base.Lease, msg *base.TaskMessage) {
	if !l.IsValid() {
		// If lease is not valid, do not write to redis; Let recoverer take care of it.
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), l.Deadline())
	defer cancel()
	err := p.broker.MarkAsComplete(ctx, msg)
	if err != nil {
		errMsg := fmt.Sprintf("Could not move task id=%s type=%q from %q to %q:  %+v",
			msg.ID, msg.Type, base.ActiveKey(msg.Queue), base.CompletedKey(msg.Queue), err)
		p.logger.Warnf("%s; Will retry syncing", errMsg)
		p.syncRequestCh <- &syncRequest{
			fn: func() error {
				return p.broker.MarkAsComplete(ctx, msg)
			},
			errMsg:   errMsg,
			deadline: l.Deadline(),
		}
	}
}

func (p *processor) markAsDone(l *base.Lease, msg *base.TaskMessage) {
	if !l.IsValid() {
		// If lease is not valid, do not write to redis; Let recoverer take care of it.
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), l.Deadline())
	defer cancel()
	err := p.broker.Done(ctx, msg)
	if err != nil {
		errMsg := fmt.Sprintf("Could not remove task id=%s type=%q from %q err: %+v", msg.ID, msg.Type, base.ActiveKey(msg.Queue), err)
		p.logger.Warnf("%s; Will retry syncing", errMsg)
		p.syncRequestCh <- &syncRequest{
			fn: func() error {
				return p.broker.Done(ctx, msg)
			},
			errMsg:   errMsg,
			deadline: l.Deadline(),
		}
	}
}

// SkipRetry is used as a return value from Handler.ProcessTask to indicate that
// the task should not be retried and should be archived instead.
var SkipRetry = errors.New("skip retry for the task")

// RevokeTask is used as a return value from Handler.ProcessTask to indicate that
// the task should not be retried or archived.
var RevokeTask = errors.New("revoke task")

func (p *processor) handleFailedMessage(ctx context.Context, l *base.Lease, msg *base.TaskMessage, err error) {
	if p.errHandler != nil {
		p.errHandler.HandleError(ctx, NewTaskWithHeaders(msg.Type, msg.Payload, msg.Headers), err)
	}
	switch {
	case errors.Is(err, RevokeTask):
		p.logger.Warnf("revoke task id=%s", msg.ID)
		p.markAsDone(l, msg)
	case msg.Retried >= msg.Retry || errors.Is(err, SkipRetry):
		p.logger.Warnf("Retry exhausted for task id=%s", msg.ID)
		p.archive(l, msg, err)
	default:
		p.retry(l, msg, err, p.isFailureFunc(err))
	}
}

func (p *processor) retry(l *base.Lease, msg *base.TaskMessage, e error, isFailure bool) {
	if !l.IsValid() {
		// If lease is not valid, do not write to redis; Let recoverer take care of it.
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), l.Deadline())
	defer cancel()
	d := p.retryDelayFunc(msg.Retried, e, NewTaskWithHeaders(msg.Type, msg.Payload, msg.Headers))
	retryAt := time.Now().Add(d)
	err := p.broker.Retry(ctx, msg, retryAt, e.Error(), isFailure)
	if err != nil {
		errMsg := fmt.Sprintf("Could not move task id=%s from %q to %q", msg.ID, base.ActiveKey(msg.Queue), base.RetryKey(msg.Queue))
		p.logger.Warnf("%s; Will retry syncing", errMsg)
		p.syncRequestCh <- &syncRequest{
			fn: func() error {
				return p.broker.Retry(ctx, msg, retryAt, e.Error(), isFailure)
			},
			errMsg:   errMsg,
			deadline: l.Deadline(),
		}
	}
}

func (p *processor) archive(l *base.Lease, msg *base.TaskMessage, e error) {
	if !l.IsValid() {
		// If lease is not valid, do not write to redis; Let recoverer take care of it.
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), l.Deadline())
	defer cancel()
	err := p.broker.Archive(ctx, msg, e.Error())
	if err != nil {
		errMsg := fmt.Sprintf("Could not move task id=%s from %q to %q", msg.ID, base.ActiveKey(msg.Queue), base.ArchivedKey(msg.Queue))
		p.logger.Warnf("%s; Will retry syncing", errMsg)
		p.syncRequestCh <- &syncRequest{
			fn: func() error {
				return p.broker.Archive(ctx, msg, e.Error())
			},
			errMsg:   errMsg,
			deadline: l.Deadline(),
		}
	}
}

// queues returns a list of queues to query.
// Order of the queue names is based on the priority of each queue.
// Queue names is sorted by their priority level if strict-priority is true.
// If strict-priority is false, then the order of queue names are roughly based on
// the priority level but randomized in order to avoid starving low priority queues.
func (p *processor) queues() []string {
	// skip the overhead of generating a list of queue names
	// if we are processing one queue.
	if len(p.queueConfig) == 1 {
		for qname := range p.queueConfig {
			return []string{qname}
		}
	}
	if p.orderedQueues != nil {
		return p.orderedQueues
	}
	var names []string
	for qname, priority := range p.queueConfig {
		for i := 0; i < priority; i++ {
			names = append(names, qname)
		}
	}
	rand.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	return uniq(names, len(p.queueConfig))
}

// perform calls the handler with the given task.
// If the call returns without panic, it simply returns the value,
// otherwise, it recovers from panic and returns an error.
func (p *processor) perform(ctx context.Context, task *Task) (err error) {
	defer func() {
		if x := recover(); x != nil {
			p.logger.Errorf("recovering from panic. See the stack trace below for details:\n%s", string(debug.Stack()))
			_, file, line, ok := runtime.Caller(1) // skip the first frame (panic itself)
			if ok && strings.Contains(file, "runtime/") {
				// The panic came from the runtime, most likely due to incorrect
				// map/slice usage. The parent frame should have the real trigger.
				_, file, line, ok = runtime.Caller(2)
			}
			var errMsg string
			// Include the file and line number info in the error, if runtime.Caller returned ok.
			if ok {
				errMsg = fmt.Sprintf("panic [%s:%d]: %v", file, line, x)
			} else {
				errMsg = fmt.Sprintf("panic: %v", x)
			}
			err = &errors.PanicError{
				ErrMsg: errMsg,
			}
		}
	}()
	return p.handler.ProcessTask(ctx, task)
}

// uniq dedupes elements and returns a slice of unique names of length l.
// Order of the output slice is based on the input list.
func uniq(names []string, l int) []string {
	var res []string
	seen := make(map[string]struct{})
	for _, s := range names {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			res = append(res, s)
		}
		if len(res) == l {
			break
		}
	}
	return res
}

// sortByPriority returns a list of queue names sorted by
// their priority level in descending order.
func sortByPriority(qcfg map[string]int) []string {
	var queues []*queue
	for qname, n := range qcfg {
		queues = append(queues, &queue{qname, n})
	}
	sort.Sort(sort.Reverse(byPriority(queues)))
	var res []string
	for _, q := range queues {
		res = append(res, q.name)
	}
	return res
}

type queue struct {
	name     string
	priority int
}

type byPriority []*queue

func (x byPriority) Len() int           { return len(x) }
func (x byPriority) Less(i, j int) bool { return x[i].priority < x[j].priority }
func (x byPriority) Swap(i, j int)      { x[i], x[j] = x[j], x[i] }

// normalizeQueues divides priority numbers by their greatest common divisor.
func normalizeQueues(queues map[string]int) map[string]int {
	var xs []int
	for _, x := range queues {
		xs = append(xs, x)
	}
	d := gcd(xs...)
	res := make(map[string]int)
	for q, x := range queues {
		res[q] = x / d
	}
	return res
}

func gcd(xs ...int) int {
	fn := func(x, y int) int {
		for y > 0 {
			x, y = y, x%y
		}
		return x
	}
	res := xs[0]
	for i := 0; i < len(xs); i++ {
		res = fn(xs[i], res)
		if res == 1 {
			return 1
		}
	}
	return res
}

// computeDeadline returns the given task's deadline,
func (p *processor) computeDeadline(msg *base.TaskMessage) time.Time {
	if msg.Timeout == 0 && msg.Deadline == 0 {
		p.logger.Errorf("asynq: internal error: both timeout and deadline are not set for the task message: %s", msg.ID)
		return p.clock.Now().Add(defaultTimeout)
	}
	if msg.Timeout != 0 && msg.Deadline != 0 {
		deadlineUnix := math.Min(float64(p.clock.Now().Unix()+msg.Timeout), float64(msg.Deadline))
		return time.Unix(int64(deadlineUnix), 0)
	}
	if msg.Timeout != 0 {
		return p.clock.Now().Add(time.Duration(msg.Timeout) * time.Second)
	}
	return time.Unix(msg.Deadline, 0)
}

func IsPanicError(err error) bool {
	return errors.IsPanicError(err)
}
