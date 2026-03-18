package im

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// defaultMaxQueueSize is the maximum number of pending QA requests in the queue.
	defaultMaxQueueSize = 50
	// defaultMaxPerUser limits how many requests a single user can have queued.
	defaultMaxPerUser = 3
	// defaultWorkers is the default number of concurrent QA workers.
	defaultWorkers = 5
	// queueTimeout is how long a request can wait in the queue before being discarded.
	queueTimeout = 60 * time.Second
)

// qaRequest represents a QA request waiting in the queue.
type qaRequest struct {
	ctx       context.Context
	cancel    context.CancelFunc
	msg       *IncomingMessage
	session   *types.Session
	agent     *types.CustomAgent
	adapter   Adapter
	channel   *IMChannel
	channelID string

	// userKey is "channelID:userID:chatID", used for per-user limits and /stop.
	userKey    string
	enqueuedAt time.Time
}

// QueueMetrics exposes observable queue state.
type QueueMetrics struct {
	// Depth is the current number of requests waiting in the queue.
	Depth int
	// ActiveWorkers is the number of workers currently executing a QA request.
	ActiveWorkers int64
	// TotalEnqueued is the cumulative number of requests enqueued.
	TotalEnqueued int64
	// TotalProcessed is the cumulative number of requests dequeued and executed.
	TotalProcessed int64
	// TotalRejected is the cumulative number of requests rejected (queue full / per-user limit).
	TotalRejected int64
	// TotalTimeout is the cumulative number of requests discarded due to queue timeout.
	TotalTimeout int64
}

// qaQueue is a bounded, per-user-limited request queue with a fixed worker pool.
type qaQueue struct {
	mu         sync.Mutex
	cond       *sync.Cond
	queue      []*qaRequest
	maxSize    int
	maxPerUser int
	workers    int
	perUser    map[string]int // userKey → queued count
	closed     bool

	// metrics
	activeWorkers  atomic.Int64
	totalEnqueued  atomic.Int64
	totalProcessed atomic.Int64
	totalRejected  atomic.Int64
	totalTimeout   atomic.Int64

	// handler is called by workers to execute the QA request.
	handler func(req *qaRequest)
}

// newQAQueue creates a new bounded queue with the given worker count.
func newQAQueue(workers, maxSize, maxPerUser int, handler func(req *qaRequest)) *qaQueue {
	q := &qaQueue{
		queue:      make([]*qaRequest, 0, maxSize),
		maxSize:    maxSize,
		maxPerUser: maxPerUser,
		workers:    workers,
		perUser:    make(map[string]int),
		handler:    handler,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Start launches the worker goroutines and the metrics reporter. Call Stop to shut down.
func (q *qaQueue) Start(stopCh <-chan struct{}) {
	for i := 0; i < q.workers; i++ {
		go q.runWorker(i)
	}
	go q.metricsLoop(stopCh)
}

// Stop signals all workers to exit after draining.
func (q *qaQueue) Stop() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
}

// Enqueue adds a request to the queue. Returns the queue position (0-based)
// or an error if the queue is full or per-user limit is reached.
func (q *qaQueue) Enqueue(req *qaRequest) (position int, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return 0, fmt.Errorf("queue is closed")
	}

	if len(q.queue) >= q.maxSize {
		q.totalRejected.Add(1)
		return 0, fmt.Errorf("queue full (%d/%d)", len(q.queue), q.maxSize)
	}

	if q.perUser[req.userKey] >= q.maxPerUser {
		q.totalRejected.Add(1)
		return 0, fmt.Errorf("per-user queue limit reached (%d/%d)", q.perUser[req.userKey], q.maxPerUser)
	}

	req.enqueuedAt = time.Now()
	q.queue = append(q.queue, req)
	q.perUser[req.userKey]++
	q.totalEnqueued.Add(1)
	pos := len(q.queue) - 1

	q.cond.Signal()
	return pos, nil
}

// Remove cancels and removes a queued request by userKey.
// Returns true if a request was found and removed.
func (q *qaQueue) Remove(userKey string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, req := range q.queue {
		if req.userKey == userKey {
			req.cancel()
			q.queue = append(q.queue[:i], q.queue[i+1:]...)
			q.perUser[userKey]--
			if q.perUser[userKey] <= 0 {
				delete(q.perUser, userKey)
			}
			return true
		}
	}
	return false
}

// Metrics returns a snapshot of the queue's observable state.
func (q *qaQueue) Metrics() QueueMetrics {
	q.mu.Lock()
	depth := len(q.queue)
	q.mu.Unlock()

	return QueueMetrics{
		Depth:          depth,
		ActiveWorkers:  q.activeWorkers.Load(),
		TotalEnqueued:  q.totalEnqueued.Load(),
		TotalProcessed: q.totalProcessed.Load(),
		TotalRejected:  q.totalRejected.Load(),
		TotalTimeout:   q.totalTimeout.Load(),
	}
}

func (q *qaQueue) runWorker(id int) {
	for {
		req := q.dequeue()
		if req == nil {
			return // queue closed
		}

		// Skip requests that have been cancelled or timed out while queued.
		if req.ctx.Err() != nil {
			q.totalTimeout.Add(1)
			continue
		}

		waitDuration := time.Since(req.enqueuedAt)
		if waitDuration > queueTimeout {
			q.totalTimeout.Add(1)
			logger.Warnf(req.ctx, "[IM] Queue timeout: user=%s waited=%s, discarding", req.msg.UserID, waitDuration)
			_ = req.adapter.SendReply(req.ctx, req.msg, &ReplyMessage{
				Content: "您的消息等待超时，请重新发送。",
				IsFinal: true,
			})
			req.cancel()
			continue
		}

		logger.Infof(req.ctx, "[IM] Dequeued: worker=%d user=%s waited=%s depth=%d",
			id, req.msg.UserID, waitDuration, q.Metrics().Depth)

		q.activeWorkers.Add(1)
		q.handler(req)
		q.activeWorkers.Add(-1)
		q.totalProcessed.Add(1)
	}
}

func (q *qaQueue) dequeue() *qaRequest {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.queue) == 0 && !q.closed {
		q.cond.Wait()
	}

	if q.closed && len(q.queue) == 0 {
		return nil
	}

	req := q.queue[0]
	q.queue = q.queue[1:]
	q.perUser[req.userKey]--
	if q.perUser[req.userKey] <= 0 {
		delete(q.perUser, req.userKey)
	}

	return req
}

const metricsLogInterval = 30 * time.Second

// metricsLoop periodically logs queue metrics for operational visibility.
func (q *qaQueue) metricsLoop(stopCh <-chan struct{}) {
	ticker := time.NewTicker(metricsLogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m := q.Metrics()
			// Only log when there is activity to avoid noise.
			if m.Depth > 0 || m.ActiveWorkers > 0 {
				logger.Infof(context.Background(),
					"[IM] Queue metrics: depth=%d active_workers=%d enqueued=%d processed=%d rejected=%d timeout=%d",
					m.Depth, m.ActiveWorkers, m.TotalEnqueued, m.TotalProcessed, m.TotalRejected, m.TotalTimeout)
			}
		case <-stopCh:
			return
		}
	}
}
