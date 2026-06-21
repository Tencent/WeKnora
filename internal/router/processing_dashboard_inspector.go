package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"golang.org/x/sync/singleflight"
)

const (
	processingQueueSnapshotTTL      = 5 * time.Second
	processingQueueSnapshotPageSize = 100
	processingQueueSnapshotMaxPages = 50
)

type asynqProcessingTaskLister interface {
	ListPendingTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	ListScheduledTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	ListRetryTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	ListActiveTasks(queue string, opts ...asynq.ListOption) ([]*asynq.TaskInfo, error)
}

type processingQueueSnapshotReader struct {
	inspector asynqProcessingTaskLister
	ttl       time.Duration
	maxPages  int

	group singleflight.Group
	cache atomic.Pointer[types.ProcessingQueueSnapshot]
}

func NewAsynqProcessingQueueSnapshotReader(inspector *asynq.Inspector) interfaces.ProcessingQueueSnapshotReader {
	if inspector == nil {
		return NewNoopProcessingQueueSnapshotReader()
	}
	return newProcessingQueueSnapshotReader(inspector, processingQueueSnapshotTTL, processingQueueSnapshotMaxPages)
}

func NewNoopProcessingQueueSnapshotReader() interfaces.ProcessingQueueSnapshotReader {
	return noopProcessingQueueSnapshotReader{}
}

func newProcessingQueueSnapshotReader(
	inspector asynqProcessingTaskLister,
	ttl time.Duration,
	maxPages int,
) interfaces.ProcessingQueueSnapshotReader {
	if maxPages <= 0 {
		maxPages = processingQueueSnapshotMaxPages
	}
	return &processingQueueSnapshotReader{inspector: inspector, ttl: ttl, maxPages: maxPages}
}

func (r *processingQueueSnapshotReader) Snapshot(ctx context.Context) (*types.ProcessingQueueSnapshot, error) {
	if r == nil || r.inspector == nil {
		return noopProcessingQueueSnapshotReader{}.Snapshot(ctx)
	}
	if cached := r.cache.Load(); cached != nil && time.Since(cached.GeneratedAt) < r.ttl {
		return cloneProcessingQueueSnapshot(cached), nil
	}
	v, err, _ := r.group.Do("processing-dashboard-queue-snapshot", func() (any, error) {
		if cached := r.cache.Load(); cached != nil && time.Since(cached.GeneratedAt) < r.ttl {
			return cloneProcessingQueueSnapshot(cached), nil
		}
		snap, buildErr := r.build(ctx)
		r.cache.Store(cloneProcessingQueueSnapshot(snap))
		return snap, buildErr
	})
	if err != nil {
		return nil, err
	}
	snap, _ := v.(*types.ProcessingQueueSnapshot)
	return cloneProcessingQueueSnapshot(snap), nil
}

func (r *processingQueueSnapshotReader) build(ctx context.Context) (*types.ProcessingQueueSnapshot, error) {
	now := time.Now()
	snap := &types.ProcessingQueueSnapshot{
		ExecutorMode: types.ProcessingExecutorModeAsynq,
		Status:       types.ProcessingQueueSnapshotOK,
		GeneratedAt:  now,
		Aggregates:   map[string]*types.ProcessingQueueAggregate{},
	}
	listers := []struct {
		state string
		list  func(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	}{
		{"pending", r.inspector.ListPendingTasks},
		{"scheduled", r.inspector.ListScheduledTasks},
		{"retry", r.inspector.ListRetryTasks},
		{"active", r.inspector.ListActiveTasks},
	}
	for _, queue := range queuesScanned {
		for _, l := range listers {
			truncated, err := r.scanQueueState(ctx, snap, queue, l.state, l.list)
			if err != nil {
				if errors.Is(err, asynq.ErrQueueNotFound) {
					continue
				}
				logger.Warnf(ctx, "[ProcessingDashboard] queue snapshot degraded queue=%s state=%s: %v", queue, l.state, err)
				return &types.ProcessingQueueSnapshot{
					ExecutorMode: types.ProcessingExecutorModeAsynq,
					Status:       types.ProcessingQueueSnapshotDegraded,
					GeneratedAt:  now,
					Aggregates:   map[string]*types.ProcessingQueueAggregate{},
					Message:      "queue data unavailable",
				}, nil
			}
			if truncated {
				snap.Status = types.ProcessingQueueSnapshotPartial
				snap.TruncatedQueues = append(snap.TruncatedQueues, fmt.Sprintf("%s:%s", queue, l.state))
			}
		}
	}
	return snap, nil
}

func (r *processingQueueSnapshotReader) scanQueueState(
	ctx context.Context,
	snap *types.ProcessingQueueSnapshot,
	queue string,
	state string,
	list func(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error),
) (bool, error) {
	for page := 1; page <= r.maxPages; page++ {
		tasks, err := list(queue, asynq.PageSize(processingQueueSnapshotPageSize), asynq.Page(page))
		if err != nil {
			return false, err
		}
		for _, task := range tasks {
			probe, ok := parseProcessingQueueTask(task)
			if !ok {
				continue
			}
			agg := ensureProcessingQueueAggregate(snap.Aggregates, probe.knowledgeID, probe.attempt, probe.stage)
			child := ensureProcessingQueueChild(agg, probe.childKey)
			applyProcessingQueueTaskState(agg, child, state, task)
		}
		if len(tasks) < processingQueueSnapshotPageSize {
			return false, nil
		}
	}
	logger.Warnf(ctx, "[ProcessingDashboard] queue snapshot truncated queue=%s state=%s max_pages=%d", queue, state, r.maxPages)
	return true, nil
}

type processingQueueTaskProbe struct {
	knowledgeID string
	attempt     int
	stage       types.ProcessingLogicalStage
	childKey    string
}

func parseProcessingQueueTask(task *asynq.TaskInfo) (processingQueueTaskProbe, bool) {
	if task == nil {
		return processingQueueTaskProbe{}, false
	}
	var payload struct {
		KnowledgeID string `json:"knowledge_id"`
		Attempt     int    `json:"attempt"`
		ImageIndex  int    `json:"image_index"`
		BatchIndex  int    `json:"batch_index"`
		ChunkIndex  *int   `json:"chunk_index"`
		ChunkID     string `json:"chunk_id"`
	}
	if err := json.Unmarshal(task.Payload, &payload); err != nil || payload.KnowledgeID == "" {
		return processingQueueTaskProbe{}, false
	}
	attempt := payload.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	probe := processingQueueTaskProbe{knowledgeID: payload.KnowledgeID, attempt: attempt}
	switch task.Type {
	case types.TypeDocumentProcess, types.TypeManualProcess:
		probe.stage = types.ProcessingStageDocReader
		probe.childKey = "document"
	case types.TypeImageMultimodal:
		probe.stage = types.ProcessingStageMultimodal
		probe.childKey = fmt.Sprintf("%d", payload.ImageIndex)
	case types.TypeKnowledgePostProcess:
		probe.stage = types.ProcessingStagePostProcess
		probe.childKey = "postprocess"
	case types.TypeSummaryGeneration:
		probe.stage = types.ProcessingStageSummaryGen
		probe.childKey = "summary"
	case types.TypeQuestionGeneration:
		probe.stage = types.ProcessingStageQuestion
		probe.childKey = fmt.Sprintf("%d", payload.BatchIndex)
	case types.TypeChunkExtract:
		probe.stage = types.ProcessingStageGraph
		if payload.ChunkIndex != nil {
			probe.childKey = fmt.Sprintf("%d", *payload.ChunkIndex)
		} else if payload.ChunkID != "" {
			probe.childKey = "legacy:" + payload.ChunkID
		} else {
			probe.childKey = "legacy"
		}
	default:
		return processingQueueTaskProbe{}, false
	}
	return probe, true
}

func ensureProcessingQueueAggregate(
	aggregates map[string]*types.ProcessingQueueAggregate,
	knowledgeID string,
	attempt int,
	stage types.ProcessingLogicalStage,
) *types.ProcessingQueueAggregate {
	key := fmt.Sprintf("%s:%d:%s", knowledgeID, attempt, stage)
	agg := aggregates[key]
	if agg == nil {
		agg = &types.ProcessingQueueAggregate{
			KnowledgeID: knowledgeID,
			Attempt:     attempt,
			Stage:       stage,
			Children:    map[string]*types.ProcessingQueueChildAggregate{},
		}
		aggregates[key] = agg
	}
	return agg
}

func ensureProcessingQueueChild(agg *types.ProcessingQueueAggregate, key string) *types.ProcessingQueueChildAggregate {
	if key == "" {
		key = "main"
	}
	child := agg.Children[key]
	if child == nil {
		child = &types.ProcessingQueueChildAggregate{ChildKey: key}
		agg.Children[key] = child
	}
	return child
}

func applyProcessingQueueTaskState(
	agg *types.ProcessingQueueAggregate,
	child *types.ProcessingQueueChildAggregate,
	state string,
	task *asynq.TaskInfo,
) {
	switch state {
	case "pending":
		agg.PendingCount++
		child.PendingCount++
	case "scheduled":
		agg.ScheduledCount++
		child.ScheduledCount++
		t := task.NextProcessAt
		setEarliest(&agg.NextRetryAt, t)
		setEarliest(&child.NextRetryAt, t)
	case "retry":
		agg.RetryCount++
		child.RetryCount++
		t := task.NextProcessAt
		if t.IsZero() {
			t = task.LastFailedAt
		}
		setEarliest(&agg.NextRetryAt, t)
		setEarliest(&child.NextRetryAt, t)
		if task.LastErr != "" {
			agg.LastError = task.LastErr
			child.LastError = task.LastErr
		}
		if !task.LastFailedAt.IsZero() {
			setLatest(&agg.LastErrorAt, task.LastFailedAt)
			setLatest(&child.LastErrorAt, task.LastFailedAt)
		}
	case "active":
		agg.ActiveCount++
		child.ActiveCount++
	}
}

func setEarliest(dst **time.Time, candidate time.Time) {
	if candidate.IsZero() {
		return
	}
	if *dst == nil || candidate.Before(**dst) {
		t := candidate
		*dst = &t
	}
}

func setLatest(dst **time.Time, candidate time.Time) {
	if candidate.IsZero() {
		return
	}
	if *dst == nil || candidate.After(**dst) {
		t := candidate
		*dst = &t
	}
}

type noopProcessingQueueSnapshotReader struct{}

func (noopProcessingQueueSnapshotReader) Snapshot(ctx context.Context) (*types.ProcessingQueueSnapshot, error) {
	return &types.ProcessingQueueSnapshot{
		ExecutorMode: types.ProcessingExecutorModeLite,
		Status:       types.ProcessingQueueSnapshotNotApplicable,
		GeneratedAt:  time.Now(),
		Aggregates:   map[string]*types.ProcessingQueueAggregate{},
	}, nil
}

func cloneProcessingQueueSnapshot(in *types.ProcessingQueueSnapshot) *types.ProcessingQueueSnapshot {
	if in == nil {
		return nil
	}
	out := *in
	out.TruncatedQueues = append([]string(nil), in.TruncatedQueues...)
	out.Aggregates = make(map[string]*types.ProcessingQueueAggregate, len(in.Aggregates))
	for k, agg := range in.Aggregates {
		if agg == nil {
			continue
		}
		aggCopy := *agg
		aggCopy.Children = make(map[string]*types.ProcessingQueueChildAggregate, len(agg.Children))
		for ck, child := range agg.Children {
			if child == nil {
				continue
			}
			childCopy := *child
			aggCopy.Children[ck] = &childCopy
		}
		out.Aggregates[k] = &aggCopy
	}
	return &out
}
