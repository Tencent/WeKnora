// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq/internal/base"
	"github.com/hibiken/asynq/internal/errors"
	"github.com/hibiken/asynq/internal/rdb"
	"github.com/redis/go-redis/v9"
)

// Inspector is a client interface to inspect and mutate the state of
// queues and tasks.
type Inspector struct {
	rdb *rdb.RDB
	// When an Inspector has been created with an existing Redis connection, we do
	// not want to close it.
	sharedConnection bool
}

// New returns a new instance of Inspector.
func NewInspector(r RedisConnOpt) *Inspector {
	c, ok := r.MakeRedisClient().(redis.UniversalClient)
	if !ok {
		panic(fmt.Sprintf("inspeq: unsupported RedisConnOpt type %T", r))
	}
	inspector := NewInspectorFromRedisClient(c)
	inspector.sharedConnection = false
	return inspector
}

// NewInspectorFromRedisClient returns a new instance of Inspector given a redis.UniversalClient
// Warning: The underlying redis connection pool will not be closed by Asynq, you are responsible for closing it.
func NewInspectorFromRedisClient(c redis.UniversalClient) *Inspector {
	return &Inspector{
		rdb:              rdb.NewRDB(c),
		sharedConnection: true,
	}
}

// Close closes the connection with redis.
func (i *Inspector) Close() error {
	if i.sharedConnection {
		return fmt.Errorf("redis connection is shared so the Inspector can't be closed through asynq")
	}
	return i.rdb.Close()
}

// Queues returns a list of all queue names.
func (i *Inspector) Queues() ([]string, error) {
	return i.rdb.AllQueues()
}

// Groups returns a list of all groups within the given queue.
func (i *Inspector) Groups(queue string) ([]*GroupInfo, error) {
	stats, err := i.rdb.GroupStats(queue)
	if err != nil {
		return nil, err
	}
	var res []*GroupInfo
	for _, s := range stats {
		res = append(res, &GroupInfo{
			Group: s.Group,
			Size:  s.Size,
		})
	}
	return res, nil
}

// GroupInfo represents a state of a group at a certain time.
type GroupInfo struct {
	// Name of the group.
	Group string

	// Size is the total number of tasks in the group.
	Size int
}

// QueueInfo represents a state of a queue at a certain time.
type QueueInfo struct {
	// Name of the queue.
	Queue string

	// Total number of bytes that the queue and its tasks require to be stored in redis.
	// It is an approximate memory usage value in bytes since the value is computed by sampling.
	MemoryUsage int64

	// Latency of the queue, measured by the oldest pending task in the queue.
	Latency time.Duration

	// Size is the total number of tasks in the queue.
	// The value is the sum of Pending, Active, Scheduled, Retry, Aggregating and Archived.
	Size int

	// Groups is the total number of groups in the queue.
	Groups int

	// Number of pending tasks.
	Pending int
	// Number of active tasks.
	Active int
	// Number of scheduled tasks.
	Scheduled int
	// Number of retry tasks.
	Retry int
	// Number of archived tasks.
	Archived int
	// Number of stored completed tasks.
	Completed int
	// Number of aggregating tasks.
	Aggregating int

	// Total number of tasks being processed within the given date (counter resets daily).
	// The number includes both succeeded and failed tasks.
	Processed int
	// Total number of tasks failed to be processed within the given date (counter resets daily).
	Failed int

	// Total number of tasks processed (cumulative).
	ProcessedTotal int
	// Total number of tasks failed (cumulative).
	FailedTotal int

	// Paused indicates whether the queue is paused.
	// If true, tasks in the queue will not be processed.
	Paused bool

	// Time when this queue info snapshot was taken.
	Timestamp time.Time
}

// GetQueueInfo returns current information of the given queue.
func (i *Inspector) GetQueueInfo(queue string) (*QueueInfo, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return nil, err
	}
	stats, err := i.rdb.CurrentStats(queue)
	if err != nil {
		return nil, err
	}
	return &QueueInfo{
		Queue:          stats.Queue,
		MemoryUsage:    stats.MemoryUsage,
		Latency:        stats.Latency,
		Size:           stats.Size,
		Groups:         stats.Groups,
		Pending:        stats.Pending,
		Active:         stats.Active,
		Scheduled:      stats.Scheduled,
		Retry:          stats.Retry,
		Archived:       stats.Archived,
		Completed:      stats.Completed,
		Aggregating:    stats.Aggregating,
		Processed:      stats.Processed,
		Failed:         stats.Failed,
		ProcessedTotal: stats.ProcessedTotal,
		FailedTotal:    stats.FailedTotal,
		Paused:         stats.Paused,
		Timestamp:      stats.Timestamp,
	}, nil
}

// DailyStats holds aggregate data for a given day for a given queue.
type DailyStats struct {
	// Name of the queue.
	Queue string
	// Total number of tasks being processed during the given date.
	// The number includes both succeeded and failed tasks.
	Processed int
	// Total number of tasks failed to be processed during the given date.
	Failed int
	// Date this stats was taken.
	Date time.Time
}

// History returns a list of stats from the last n days.
func (i *Inspector) History(queue string, n int) ([]*DailyStats, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return nil, err
	}
	stats, err := i.rdb.HistoricalStats(queue, n)
	if err != nil {
		return nil, err
	}
	var res []*DailyStats
	for _, s := range stats {
		res = append(res, &DailyStats{
			Queue:     s.Queue,
			Processed: s.Processed,
			Failed:    s.Failed,
			Date:      s.Time,
		})
	}
	return res, nil
}

var (
	// ErrQueueNotFound indicates that the specified queue does not exist.
	ErrQueueNotFound = errors.New("queue not found")

	// ErrQueueNotEmpty indicates that the specified queue is not empty.
	ErrQueueNotEmpty = errors.New("queue is not empty")

	// ErrTaskNotFound indicates that the specified task cannot be found in the queue.
	ErrTaskNotFound = errors.New("task not found")
)

// DeleteQueue removes the specified queue.
//
// If force is set to true, DeleteQueue will remove the queue regardless of
// the queue size as long as no tasks are active in the queue.
// If force is set to false, DeleteQueue will remove the queue only if
// the queue is empty.
//
// If the specified queue does not exist, DeleteQueue returns ErrQueueNotFound.
// If force is set to false and the specified queue is not empty, DeleteQueue
// returns ErrQueueNotEmpty.
func (i *Inspector) DeleteQueue(queue string, force bool) error {
	err := i.rdb.RemoveQueue(queue, force)
	if errors.IsQueueNotFound(err) {
		return fmt.Errorf("%w: queue=%q", ErrQueueNotFound, queue)
	}
	if errors.IsQueueNotEmpty(err) {
		return fmt.Errorf("%w: queue=%q", ErrQueueNotEmpty, queue)
	}
	return err
}

// GetTaskInfo retrieves task information given a task id and queue name.
//
// Returns an error wrapping ErrQueueNotFound if a queue with the given name doesn't exist.
// Returns an error wrapping ErrTaskNotFound if a task with the given id doesn't exist in the queue.
func (i *Inspector) GetTaskInfo(queue, id string) (*TaskInfo, error) {
	info, err := i.rdb.GetTaskInfo(queue, id)
	switch {
	case errors.IsQueueNotFound(err):
		return nil, fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case errors.IsTaskNotFound(err):
		return nil, fmt.Errorf("asynq: %w", ErrTaskNotFound)
	case err != nil:
		return nil, fmt.Errorf("asynq: %w", err)
	}
	return newTaskInfo(info.Message, info.State, info.NextProcessAt, info.Result), nil
}

// ListOption specifies behavior of list operation.
type ListOption interface{}

// Internal list option representations.
type (
	pageSizeOpt int
	pageNumOpt  int
)

type listOption struct {
	pageSize int
	pageNum  int
}

const (
	// Page size used by default in list operation.
	defaultPageSize = 30

	// Page number used by default in list operation.
	defaultPageNum = 1
)

func composeListOptions(opts ...ListOption) listOption {
	res := listOption{
		pageSize: defaultPageSize,
		pageNum:  defaultPageNum,
	}
	for _, opt := range opts {
		switch opt := opt.(type) {
		case pageSizeOpt:
			res.pageSize = int(opt)
		case pageNumOpt:
			res.pageNum = int(opt)
		default:
			// ignore unexpected option
		}
	}
	return res
}

// PageSize returns an option to specify the page size for list operation.
//
// Negative page size is treated as zero.
func PageSize(n int) ListOption {
	if n < 0 {
		n = 0
	}
	return pageSizeOpt(n)
}

// Page returns an option to specify the page number for list operation.
// The value 1 fetches the first page.
//
// Negative page number is treated as one.
func Page(n int) ListOption {
	if n < 0 {
		n = 1
	}
	return pageNumOpt(n)
}

// ListPendingTasks retrieves pending tasks from the specified queue.
//
// By default, it retrieves the first 30 tasks.
func (i *Inspector) ListPendingTasks(queue string, opts ...ListOption) ([]*TaskInfo, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return nil, fmt.Errorf("asynq: %w", err)
	}
	opt := composeListOptions(opts...)
	pgn := rdb.Pagination{Size: opt.pageSize, Page: opt.pageNum - 1}
	infos, err := i.rdb.ListPending(queue, pgn)
	switch {
	case errors.IsQueueNotFound(err):
		return nil, fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case err != nil:
		return nil, fmt.Errorf("asynq: %w", err)
	}
	var tasks []*TaskInfo
	for _, i := range infos {
		tasks = append(tasks, newTaskInfo(
			i.Message,
			i.State,
			i.NextProcessAt,
			i.Result,
		))
	}
	return tasks, err
}

// ListActiveTasks retrieves active tasks from the specified queue.
//
// By default, it retrieves the first 30 tasks.
func (i *Inspector) ListActiveTasks(queue string, opts ...ListOption) ([]*TaskInfo, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return nil, fmt.Errorf("asynq: %w", err)
	}
	opt := composeListOptions(opts...)
	pgn := rdb.Pagination{Size: opt.pageSize, Page: opt.pageNum - 1}
	infos, err := i.rdb.ListActive(queue, pgn)
	switch {
	case errors.IsQueueNotFound(err):
		return nil, fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case err != nil:
		return nil, fmt.Errorf("asynq: %w", err)
	}
	expired, err := i.rdb.ListLeaseExpired(time.Now(), queue)
	if err != nil {
		return nil, fmt.Errorf("asynq: %w", err)
	}
	expiredSet := make(map[string]struct{}) // set of expired message IDs
	for _, msg := range expired {
		expiredSet[msg.ID] = struct{}{}
	}
	var tasks []*TaskInfo
	for _, i := range infos {
		t := newTaskInfo(
			i.Message,
			i.State,
			i.NextProcessAt,
			i.Result,
		)
		if _, ok := expiredSet[i.Message.ID]; ok {
			t.IsOrphaned = true
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// ListAggregatingTasks retrieves scheduled tasks from the specified group.
//
// By default, it retrieves the first 30 tasks.
func (i *Inspector) ListAggregatingTasks(queue, group string, opts ...ListOption) ([]*TaskInfo, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return nil, fmt.Errorf("asynq: %w", err)
	}
	opt := composeListOptions(opts...)
	pgn := rdb.Pagination{Size: opt.pageSize, Page: opt.pageNum - 1}
	infos, err := i.rdb.ListAggregating(queue, group, pgn)
	switch {
	case errors.IsQueueNotFound(err):
		return nil, fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case err != nil:
		return nil, fmt.Errorf("asynq: %w", err)
	}
	var tasks []*TaskInfo
	for _, i := range infos {
		tasks = append(tasks, newTaskInfo(
			i.Message,
			i.State,
			i.NextProcessAt,
			i.Result,
		))
	}
	return tasks, nil
}

// ListScheduledTasks retrieves scheduled tasks from the specified queue.
// Tasks are sorted by NextProcessAt in ascending order.
//
// By default, it retrieves the first 30 tasks.
func (i *Inspector) ListScheduledTasks(queue string, opts ...ListOption) ([]*TaskInfo, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return nil, fmt.Errorf("asynq: %w", err)
	}
	opt := composeListOptions(opts...)
	pgn := rdb.Pagination{Size: opt.pageSize, Page: opt.pageNum - 1}
	infos, err := i.rdb.ListScheduled(queue, pgn)
	switch {
	case errors.IsQueueNotFound(err):
		return nil, fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case err != nil:
		return nil, fmt.Errorf("asynq: %w", err)
	}
	var tasks []*TaskInfo
	for _, i := range infos {
		tasks = append(tasks, newTaskInfo(
			i.Message,
			i.State,
			i.NextProcessAt,
			i.Result,
		))
	}
	return tasks, nil
}

// ListRetryTasks retrieves retry tasks from the specified queue.
// Tasks are sorted by NextProcessAt in ascending order.
//
// By default, it retrieves the first 30 tasks.
func (i *Inspector) ListRetryTasks(queue string, opts ...ListOption) ([]*TaskInfo, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return nil, fmt.Errorf("asynq: %w", err)
	}
	opt := composeListOptions(opts...)
	pgn := rdb.Pagination{Size: opt.pageSize, Page: opt.pageNum - 1}
	infos, err := i.rdb.ListRetry(queue, pgn)
	switch {
	case errors.IsQueueNotFound(err):
		return nil, fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case err != nil:
		return nil, fmt.Errorf("asynq: %w", err)
	}
	var tasks []*TaskInfo
	for _, i := range infos {
		tasks = append(tasks, newTaskInfo(
			i.Message,
			i.State,
			i.NextProcessAt,
			i.Result,
		))
	}
	return tasks, nil
}

// ListArchivedTasks retrieves archived tasks from the specified queue.
// Tasks are sorted by LastFailedAt in descending order.
//
// By default, it retrieves the first 30 tasks.
func (i *Inspector) ListArchivedTasks(queue string, opts ...ListOption) ([]*TaskInfo, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return nil, fmt.Errorf("asynq: %w", err)
	}
	opt := composeListOptions(opts...)
	pgn := rdb.Pagination{Size: opt.pageSize, Page: opt.pageNum - 1}
	infos, err := i.rdb.ListArchived(queue, pgn)
	switch {
	case errors.IsQueueNotFound(err):
		return nil, fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case err != nil:
		return nil, fmt.Errorf("asynq: %w", err)
	}
	var tasks []*TaskInfo
	for _, i := range infos {
		tasks = append(tasks, newTaskInfo(
			i.Message,
			i.State,
			i.NextProcessAt,
			i.Result,
		))
	}
	return tasks, nil
}

// ListCompletedTasks retrieves completed tasks from the specified queue.
// Tasks are sorted by expiration time (i.e. CompletedAt + Retention) in descending order.
//
// By default, it retrieves the first 30 tasks.
func (i *Inspector) ListCompletedTasks(queue string, opts ...ListOption) ([]*TaskInfo, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return nil, fmt.Errorf("asynq: %w", err)
	}
	opt := composeListOptions(opts...)
	pgn := rdb.Pagination{Size: opt.pageSize, Page: opt.pageNum - 1}
	infos, err := i.rdb.ListCompleted(queue, pgn)
	switch {
	case errors.IsQueueNotFound(err):
		return nil, fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case err != nil:
		return nil, fmt.Errorf("asynq: %w", err)
	}
	var tasks []*TaskInfo
	for _, i := range infos {
		tasks = append(tasks, newTaskInfo(
			i.Message,
			i.State,
			i.NextProcessAt,
			i.Result,
		))
	}
	return tasks, nil
}

// DeleteAllPendingTasks deletes all pending tasks from the specified queue,
// and reports the number tasks deleted.
func (i *Inspector) DeleteAllPendingTasks(queue string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.DeleteAllPendingTasks(queue)
	return int(n), err
}

// DeleteAllScheduledTasks deletes all scheduled tasks from the specified queue,
// and reports the number tasks deleted.
func (i *Inspector) DeleteAllScheduledTasks(queue string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.DeleteAllScheduledTasks(queue)
	return int(n), err
}

// DeleteAllRetryTasks deletes all retry tasks from the specified queue,
// and reports the number tasks deleted.
func (i *Inspector) DeleteAllRetryTasks(queue string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.DeleteAllRetryTasks(queue)
	return int(n), err
}

// DeleteAllArchivedTasks deletes all archived tasks from the specified queue,
// and reports the number tasks deleted.
func (i *Inspector) DeleteAllArchivedTasks(queue string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.DeleteAllArchivedTasks(queue)
	return int(n), err
}

// DeleteAllCompletedTasks deletes all completed tasks from the specified queue,
// and reports the number tasks deleted.
func (i *Inspector) DeleteAllCompletedTasks(queue string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.DeleteAllCompletedTasks(queue)
	return int(n), err
}

// DeleteAllAggregatingTasks deletes all tasks from the specified group,
// and reports the number of tasks deleted.
func (i *Inspector) DeleteAllAggregatingTasks(queue, group string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.DeleteAllAggregatingTasks(queue, group)
	return int(n), err
}

// UpdateTaskPayload updates a task with the given id from the given queue with given payload.
// The task needs to be in scheduled state,
// otherwise UpdateTaskPayload will return an error.
//
// If a queue with the given name doesn't exist, it returns an error wrapping ErrQueueNotFound.
// If a task with the given id doesn't exist in the queue, it returns an error wrapping ErrTaskNotFound.
// If the task is not in scheduled state, it returns a non-nil error.
func (i *Inspector) UpdateTaskPayload(queue, id string, payload []byte) error {
	if err := base.ValidateQueueName(queue); err != nil {
		return fmt.Errorf("asynq: %v", err)
	}
	err := i.rdb.UpdateTaskPayload(queue, id, payload)
	switch {
	case errors.IsQueueNotFound(err):
		return fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case errors.IsTaskNotFound(err):
		return fmt.Errorf("asynq: %w", ErrTaskNotFound)
	case err != nil:
		return fmt.Errorf("asynq: %v", err)
	}
	return nil

}

// DeleteTask deletes a task with the given id from the given queue.
// The task needs to be in pending, scheduled, retry, or archived state,
// otherwise DeleteTask will return an error.
//
// If a queue with the given name doesn't exist, it returns an error wrapping ErrQueueNotFound.
// If a task with the given id doesn't exist in the queue, it returns an error wrapping ErrTaskNotFound.
// If the task is in active state, it returns a non-nil error.
func (i *Inspector) DeleteTask(queue, id string) error {
	if err := base.ValidateQueueName(queue); err != nil {
		return fmt.Errorf("asynq: %w", err)
	}
	err := i.rdb.DeleteTask(queue, id)
	switch {
	case errors.IsQueueNotFound(err):
		return fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case errors.IsTaskNotFound(err):
		return fmt.Errorf("asynq: %w", ErrTaskNotFound)
	case err != nil:
		return fmt.Errorf("asynq: %w", err)
	}
	return nil

}

// RunAllScheduledTasks schedules all scheduled tasks from the given queue to run,
// and reports the number of tasks scheduled to run.
func (i *Inspector) RunAllScheduledTasks(queue string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.RunAllScheduledTasks(queue)
	return int(n), err
}

// RunAllRetryTasks schedules all retry tasks from the given queue to run,
// and reports the number of tasks scheduled to run.
func (i *Inspector) RunAllRetryTasks(queue string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.RunAllRetryTasks(queue)
	return int(n), err
}

// RunAllArchivedTasks schedules all archived tasks from the given queue to run,
// and reports the number of tasks scheduled to run.
func (i *Inspector) RunAllArchivedTasks(queue string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.RunAllArchivedTasks(queue)
	return int(n), err
}

// RunAllAggregatingTasks schedules all tasks from the given grou to run.
// and reports the number of tasks scheduled to run.
func (i *Inspector) RunAllAggregatingTasks(queue, group string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.RunAllAggregatingTasks(queue, group)
	return int(n), err
}

// RunTask updates the task to pending state given a queue name and task id.
// The task needs to be in scheduled, retry, or archived state, otherwise RunTask
// will return an error.
//
// If a queue with the given name doesn't exist, it returns an error wrapping ErrQueueNotFound.
// If a task with the given id doesn't exist in the queue, it returns an error wrapping ErrTaskNotFound.
// If the task is in pending or active state, it returns a non-nil error.
func (i *Inspector) RunTask(queue, id string) error {
	if err := base.ValidateQueueName(queue); err != nil {
		return fmt.Errorf("asynq: %w", err)
	}
	err := i.rdb.RunTask(queue, id)
	switch {
	case errors.IsQueueNotFound(err):
		return fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case errors.IsTaskNotFound(err):
		return fmt.Errorf("asynq: %w", ErrTaskNotFound)
	case err != nil:
		return fmt.Errorf("asynq: %w", err)
	}
	return nil
}

// ArchiveAllPendingTasks archives all pending tasks from the given queue,
// and reports the number of tasks archived.
func (i *Inspector) ArchiveAllPendingTasks(queue string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.ArchiveAllPendingTasks(queue)
	return int(n), err
}

// ArchiveAllScheduledTasks archives all scheduled tasks from the given queue,
// and reports the number of tasks archiveed.
func (i *Inspector) ArchiveAllScheduledTasks(queue string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.ArchiveAllScheduledTasks(queue)
	return int(n), err
}

// ArchiveAllRetryTasks archives all retry tasks from the given queue,
// and reports the number of tasks archiveed.
func (i *Inspector) ArchiveAllRetryTasks(queue string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.ArchiveAllRetryTasks(queue)
	return int(n), err
}

// ArchiveAllAggregatingTasks archives all tasks from the given group,
// and reports the number of tasks archived.
func (i *Inspector) ArchiveAllAggregatingTasks(queue, group string) (int, error) {
	if err := base.ValidateQueueName(queue); err != nil {
		return 0, err
	}
	n, err := i.rdb.ArchiveAllAggregatingTasks(queue, group)
	return int(n), err
}

// ArchiveTask archives a task with the given id in the given queue.
// The task needs to be in pending, scheduled, or retry state, otherwise ArchiveTask
// will return an error.
//
// If a queue with the given name doesn't exist, it returns an error wrapping ErrQueueNotFound.
// If a task with the given id doesn't exist in the queue, it returns an error wrapping ErrTaskNotFound.
// If the task is in already archived, it returns a non-nil error.
func (i *Inspector) ArchiveTask(queue, id string) error {
	if err := base.ValidateQueueName(queue); err != nil {
		return fmt.Errorf("asynq: err")
	}
	err := i.rdb.ArchiveTask(queue, id)
	switch {
	case errors.IsQueueNotFound(err):
		return fmt.Errorf("asynq: %w", ErrQueueNotFound)
	case errors.IsTaskNotFound(err):
		return fmt.Errorf("asynq: %w", ErrTaskNotFound)
	case err != nil:
		return fmt.Errorf("asynq: %w", err)
	}
	return nil
}

// CancelProcessing sends a signal to cancel processing of the task
// given a task id. CancelProcessing is best-effort, which means that it does not
// guarantee that the task with the given id will be canceled. The return
// value only indicates whether the cancelation signal has been sent.
func (i *Inspector) CancelProcessing(id string) error {
	return i.rdb.PublishCancelation(id)
}

// PauseQueue pauses task processing on the specified queue.
// If the queue is already paused, it will return a non-nil error.
func (i *Inspector) PauseQueue(queue string) error {
	if err := base.ValidateQueueName(queue); err != nil {
		return err
	}
	return i.rdb.Pause(queue)
}

// UnpauseQueue resumes task processing on the specified queue.
// If the queue is not paused, it will return a non-nil error.
func (i *Inspector) UnpauseQueue(queue string) error {
	if err := base.ValidateQueueName(queue); err != nil {
		return err
	}
	return i.rdb.Unpause(queue)
}

// Servers return a list of running servers' information.
func (i *Inspector) Servers() ([]*ServerInfo, error) {
	servers, err := i.rdb.ListServers()
	if err != nil {
		return nil, err
	}
	workers, err := i.rdb.ListWorkers()
	if err != nil {
		return nil, err
	}
	m := make(map[string]*ServerInfo) // ServerInfo keyed by serverID
	for _, s := range servers {
		m[s.ServerID] = &ServerInfo{
			ID:             s.ServerID,
			Host:           s.Host,
			PID:            s.PID,
			Concurrency:    s.Concurrency,
			Queues:         s.Queues,
			StrictPriority: s.StrictPriority,
			Started:        s.Started,
			Status:         s.Status,
			ActiveWorkers:  make([]*WorkerInfo, 0),
		}
	}
	for _, w := range workers {
		srvInfo, ok := m[w.ServerID]
		if !ok {
			continue
		}
		wrkInfo := &WorkerInfo{
			TaskID:      w.ID,
			TaskType:    w.Type,
			TaskPayload: w.Payload,
			Queue:       w.Queue,
			Started:     w.Started,
			Deadline:    w.Deadline,
		}
		srvInfo.ActiveWorkers = append(srvInfo.ActiveWorkers, wrkInfo)
	}
	var out []*ServerInfo
	for _, srvInfo := range m {
		out = append(out, srvInfo)
	}
	return out, nil
}

// ServerInfo describes a running Server instance.
type ServerInfo struct {
	// Unique Identifier for the server.
	ID string
	// Host machine on which the server is running.
	Host string
	// PID of the process in which the server is running.
	PID int

	// Server configuration details.
	// See Config doc for field descriptions.
	Concurrency    int
	Queues         map[string]int
	StrictPriority bool

	// Time the server started.
	Started time.Time
	// Status indicates the status of the server.
	// TODO: Update comment with more details.
	Status string
	// A List of active workers currently processing tasks.
	ActiveWorkers []*WorkerInfo
}

// WorkerInfo describes a running worker processing a task.
type WorkerInfo struct {
	// ID of the task the worker is processing.
	TaskID string
	// Type of the task the worker is processing.
	TaskType string
	// Payload of the task the worker is processing.
	TaskPayload []byte
	// Queue from which the worker got its task.
	Queue string
	// Time the worker started processing the task.
	Started time.Time
	// Time the worker needs to finish processing the task by.
	Deadline time.Time
}

// ClusterKeySlot returns an integer identifying the hash slot the given queue hashes to.
func (i *Inspector) ClusterKeySlot(queue string) (int64, error) {
	return i.rdb.ClusterKeySlot(queue)
}

// ClusterNode describes a node in redis cluster.
type ClusterNode struct {
	// Node ID in the cluster.
	ID string

	// Address of the node.
	Addr string
}

// ClusterNodes returns a list of nodes the given queue belongs to.
//
// Only relevant if task queues are stored in redis cluster.
func (i *Inspector) ClusterNodes(queue string) ([]*ClusterNode, error) {
	nodes, err := i.rdb.ClusterNodes(queue)
	if err != nil {
		return nil, err
	}
	var res []*ClusterNode
	for _, node := range nodes {
		res = append(res, &ClusterNode{ID: node.ID, Addr: node.Addr})
	}
	return res, nil
}

// SchedulerEntry holds information about a periodic task registered with a scheduler.
type SchedulerEntry struct {
	// Identifier of this entry.
	ID string

	// Spec describes the schedule of this entry.
	Spec string

	// Periodic Task registered for this entry.
	Task *Task

	// Opts is the options for the periodic task.
	Opts []Option

	// Next shows the next time the task will be enqueued.
	Next time.Time

	// Prev shows the last time the task was enqueued.
	// Zero time if task was never enqueued.
	Prev time.Time
}

// SchedulerEntries returns a list of all entries registered with
// currently running schedulers.
func (i *Inspector) SchedulerEntries() ([]*SchedulerEntry, error) {
	var entries []*SchedulerEntry
	res, err := i.rdb.ListSchedulerEntries()
	if err != nil {
		return nil, err
	}
	for _, e := range res {
		task := NewTask(e.Type, e.Payload)
		var opts []Option
		for _, s := range e.Opts {
			if o, err := parseOption(s); err == nil {
				// ignore bad data
				opts = append(opts, o)
			}
		}
		entries = append(entries, &SchedulerEntry{
			ID:   e.ID,
			Spec: e.Spec,
			Task: task,
			Opts: opts,
			Next: e.Next,
			Prev: e.Prev,
		})
	}
	return entries, nil
}

// parseOption interprets a string s as an Option and returns the Option if parsing is successful,
// otherwise returns non-nil error.
func parseOption(s string) (Option, error) {
	fn, arg := parseOptionFunc(s), parseOptionArg(s)
	switch fn {
	case "Queue":
		queue, err := strconv.Unquote(arg)
		if err != nil {
			return nil, err
		}
		return Queue(queue), nil
	case "MaxRetry":
		n, err := strconv.Atoi(arg)
		if err != nil {
			return nil, err
		}
		return MaxRetry(n), nil
	case "Timeout":
		d, err := time.ParseDuration(arg)
		if err != nil {
			return nil, err
		}
		return Timeout(d), nil
	case "Deadline":
		t, err := time.Parse(time.UnixDate, arg)
		if err != nil {
			return nil, err
		}
		return Deadline(t), nil
	case "Unique":
		d, err := time.ParseDuration(arg)
		if err != nil {
			return nil, err
		}
		return Unique(d), nil
	case "ProcessAt":
		t, err := time.Parse(time.UnixDate, arg)
		if err != nil {
			return nil, err
		}
		return ProcessAt(t), nil
	case "ProcessIn":
		d, err := time.ParseDuration(arg)
		if err != nil {
			return nil, err
		}
		return ProcessIn(d), nil
	case "Retention":
		d, err := time.ParseDuration(arg)
		if err != nil {
			return nil, err
		}
		return Retention(d), nil
	default:
		return nil, fmt.Errorf("cannot not parse option string %q", s)
	}
}

func parseOptionFunc(s string) string {
	i := strings.Index(s, "(")
	return s[:i]
}

func parseOptionArg(s string) string {
	i := strings.Index(s, "(")
	if i >= 0 {
		j := strings.Index(s, ")")
		if j > i {
			return s[i+1 : j]
		}
	}
	return ""
}

// SchedulerEnqueueEvent holds information about an enqueue event by a scheduler.
type SchedulerEnqueueEvent struct {
	// ID of the task that was enqueued.
	TaskID string

	// Time the task was enqueued.
	EnqueuedAt time.Time
}

// ListSchedulerEnqueueEvents retrieves a list of enqueue events from the specified scheduler entry.
//
// By default, it retrieves the first 30 tasks.
func (i *Inspector) ListSchedulerEnqueueEvents(entryID string, opts ...ListOption) ([]*SchedulerEnqueueEvent, error) {
	opt := composeListOptions(opts...)
	pgn := rdb.Pagination{Size: opt.pageSize, Page: opt.pageNum - 1}
	data, err := i.rdb.ListSchedulerEnqueueEvents(entryID, pgn)
	if err != nil {
		return nil, err
	}
	var events []*SchedulerEnqueueEvent
	for _, e := range data {
		events = append(events, &SchedulerEnqueueEvent{TaskID: e.TaskID, EnqueuedAt: e.EnqueuedAt})
	}
	return events, nil
}
