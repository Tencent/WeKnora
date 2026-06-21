package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const supersededErrorCode = "TASK_SUPERSEDED"

var processingChildNameRE = regexp.MustCompile(`\[(\d+)\]$`)

type processingKnowledgeStateInput struct {
	Knowledge   types.ProcessingKnowledgeRow
	Attempt     int
	Spans       []types.KnowledgeProcessingSpan
	Fanout      map[types.ProcessingLogicalStage]*types.ProcessingFanoutStageAggregate
	Queue       map[types.ProcessingLogicalStage]*types.ProcessingQueueAggregate
	WikiPending *types.ProcessingWikiPendingRow
	QueueStatus string
	GeneratedAt time.Time
}

type processingStageComputation struct {
	Item               types.ProcessingStageItem
	RetryingObservable bool
	CompletionReliable bool
	sortTime           time.Time
}

func buildProcessingStages(input processingKnowledgeStateInput) []processingStageComputation {
	if input.GeneratedAt.IsZero() {
		input.GeneratedAt = time.Now()
	}
	if input.Attempt <= 0 {
		input.Attempt = 1
	}
	latest := latestSpansByName(input.Spans)
	out := make([]processingStageComputation, 0, len(types.ProcessingLogicalStages))
	for _, stage := range types.ProcessingLogicalStages {
		out = append(out, computeProcessingStage(input, latest, stage))
	}
	return out
}

func computeProcessingStage(
	input processingKnowledgeStateInput,
	latest map[string]types.KnowledgeProcessingSpan,
	stage types.ProcessingLogicalStage,
) processingStageComputation {
	meta := processingStageMeta(stage)
	item := types.ProcessingStageItem{
		KnowledgeID:       input.Knowledge.ID,
		KnowledgeBaseID:   input.Knowledge.KnowledgeBaseID,
		KnowledgeBaseName: input.Knowledge.KnowledgeBaseName,
		Title:             processingKnowledgeTitle(input.Knowledge),
		Attempt:           input.Attempt,
		Stage:             stage,
		State:             types.ProcessingStateNotReached,
		Phase:             meta.phase,
	}
	q := input.Queue[stage]
	queueStatus := input.QueueStatus
	queueObservable := queueStatus == types.ProcessingQueueSnapshotOK || queueStatus == types.ProcessingQueueSnapshotPartial
	queueComplete := queueStatus == types.ProcessingQueueSnapshotOK
	retryingObservable := queueObservable
	completionReliable := true

	var span *types.KnowledgeProcessingSpan
	if name := parentSpanName(stage); name != "" {
		if s, ok := latest[name]; ok {
			local := s
			span = &local
		}
	}

	switch stage {
	case types.ProcessingStageMultimodal:
		item, completionReliable = computeFanoutStage(input, latest, stage, "multimodal.image[", span, q, queueObservable, queueComplete, meta.unit)
	case types.ProcessingStageQuestion:
		item, completionReliable = computeFanoutStage(input, latest, stage, "postprocess.question.batch[", span, q, queueObservable, queueComplete, meta.unit)
	case types.ProcessingStageGraph:
		item, completionReliable = computeFanoutStage(input, latest, stage, "postprocess.graph.chunk[", span, q, queueObservable, queueComplete, meta.unit)
	case types.ProcessingStageWiki:
		item = computeWikiStage(input, latest, span, q, queueObservable)
	default:
		if span != nil && (genuineTerminalSpan(*span) || span.Status == types.SpanStatusRunning) {
			item.State = stateFromSpan(*span)
			applySpanTiming(&item, *span)
			item.Progress = progressForStage(stage, latest)
			item.ErrorCode = safeErrorCode(span.ErrorCode)
			item.ErrorMessage = safeErrorMessage(span.ErrorMessage)
			item.SkipReason = skipReason(*span)
		} else if q != nil {
			applyQueueTiming(&item, q)
			if q.ActiveCount > 0 {
				item.State = types.ProcessingStateRunning
			} else if q.RetryCount+q.ScheduledCount > 0 && retryingObservable {
				item.State = types.ProcessingStateRetrying
			} else if q.PendingCount+q.ScheduledCount > 0 {
				item.State = types.ProcessingStateQueued
			}
			if stage == types.ProcessingStageDocReader && item.State == types.ProcessingStateQueued && !input.Knowledge.CreatedAt.IsZero() {
				t := input.Knowledge.CreatedAt
				item.QueuedAt = firstTime(item.QueuedAt, &t)
			}
		}
		if item.State == types.ProcessingStateNotReached {
			item.State = dependencyQueuedState(input.Knowledge.ParseStatus, latest, stage)
		}
		if item.State == types.ProcessingStateQueued && item.QueuedAt == nil {
			item.QueuedAt = stableKnowledgeTime(input.Knowledge)
		}
	}

	if item.Progress == nil {
		item.Progress = progressForStage(stage, latest)
	}
	if item.State == types.ProcessingStateRetrying && !retryingObservable {
		item.State = types.ProcessingStateQueued
	}
	if !retryingObservable && item.State != types.ProcessingStateRunning {
		retryingObservable = false
	}
	if item.LastProgressAt == nil && span != nil {
		item.LastProgressAt = &span.UpdatedAt
	}
	item.ElapsedMs = elapsedMs(input.GeneratedAt, item.StartedAt, item.QueuedAt)
	item.DurationMs = durationMs(item.StartedAt, item.FinishedAt, item.ElapsedMs)
	return processingStageComputation{
		Item:               item,
		RetryingObservable: retryingObservable,
		CompletionReliable: completionReliable,
		sortTime:           stageSortTime(item),
	}
}

func computeFanoutStage(
	input processingKnowledgeStateInput,
	latest map[string]types.KnowledgeProcessingSpan,
	stage types.ProcessingLogicalStage,
	childPrefix string,
	parent *types.KnowledgeProcessingSpan,
	q *types.ProcessingQueueAggregate,
	queueObservable bool,
	queueComplete bool,
	unit string,
) (types.ProcessingStageItem, bool) {
	item := types.ProcessingStageItem{
		KnowledgeID:       input.Knowledge.ID,
		KnowledgeBaseID:   input.Knowledge.KnowledgeBaseID,
		KnowledgeBaseName: input.Knowledge.KnowledgeBaseName,
		Title:             processingKnowledgeTitle(input.Knowledge),
		Attempt:           input.Attempt,
		Stage:             stage,
		State:             types.ProcessingStateNotReached,
		Phase:             processingStageMeta(stage).phase,
	}
	if parent != nil {
		applySpanTiming(&item, *parent)
		item.ErrorCode = safeErrorCode(parent.ErrorCode)
		item.ErrorMessage = safeErrorMessage(parent.ErrorMessage)
	}
	if q != nil {
		applyQueueTiming(&item, q)
	}
	fanout := fanoutAggregateForInput(input, latest, stage, childPrefix)
	childSpans := map[string]types.KnowledgeProcessingSpan{}
	terminalDone := 0
	terminalTotal := 0
	completionReliable := true
	if fanout != nil {
		terminalDone = fanout.TerminalDoneCount
		terminalTotal = fanout.TerminalTotalCount
		completionReliable = fanout.CompletionReliable
		for key, span := range fanout.Details {
			childSpans[key] = span
		}
		if fanout.LatestUpdatedAt != nil {
			t := *fanout.LatestUpdatedAt
			item.LastProgressAt = &t
		}
	}
	if fanout == nil {
		for name, span := range latest {
			if strings.HasPrefix(name, childPrefix) {
				key := childKeyFromSpanName(childPrefix, name)
				if _, ok := childSpans[key]; !ok {
					childSpans[key] = span
				}
				if item.LastProgressAt == nil || span.UpdatedAt.After(*item.LastProgressAt) {
					t := span.UpdatedAt
					item.LastProgressAt = &t
				}
			}
		}
	}
	childKeys := map[string]struct{}{}
	for key := range childSpans {
		childKeys[key] = struct{}{}
	}
	if q != nil {
		for key := range q.Children {
			childKeys[key] = struct{}{}
			if strings.HasPrefix(key, "legacy") {
				completionReliable = false
			}
		}
	}
	counts := map[types.ProcessingStageState]int{}
	keys := make([]string, 0, len(childKeys))
	for key := range childKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var span *types.KnowledgeProcessingSpan
		if s, ok := childSpans[key]; ok {
			local := s
			span = &local
		}
		var childQ *types.ProcessingQueueChildAggregate
		if q != nil {
			childQ = q.Children[key]
		}
		state, reliable := resolveProcessingChild(span, childQ, queueObservable, queueComplete)
		if !reliable {
			completionReliable = false
		}
		counts[state]++
	}
	total := fanoutTotal(stage, parent, latest, terminalTotal+len(keys), q)
	completed := terminalDone +
		counts[types.ProcessingStateDone] +
		counts[types.ProcessingStateFailed] +
		counts[types.ProcessingStateSkipped] +
		counts[types.ProcessingStateCancelled]
	failed := counts[types.ProcessingStateFailed]
	item.FailedChildren = failed
	if total > 0 {
		item.Progress = &types.ProcessingStageProgress{
			Completed: completed,
			Total:     total,
			Failed:    failed,
			Unit:      unit,
			Reliable:  completionReliable,
		}
	}
	item.State = aggregateProcessingState(counts, total, parent)
	if total > 0 && completed >= total &&
		counts[types.ProcessingStateRunning] == 0 &&
		counts[types.ProcessingStateRetrying] == 0 &&
		counts[types.ProcessingStateQueued] == 0 {
		if failed > 0 {
			item.State = types.ProcessingStateDoneWithErrors
		} else {
			item.State = types.ProcessingStateDone
		}
	}
	if parent != nil && item.State == types.ProcessingStateNotReached {
		item.State = stateFromSpan(*parent)
	}
	if q != nil {
		if q.ActiveCount > 0 {
			item.State = types.ProcessingStateRunning
		} else if q.RetryCount+q.ScheduledCount > 0 && queueObservable && item.State != types.ProcessingStateRunning {
			item.State = types.ProcessingStateRetrying
		} else if q.PendingCount > 0 && item.State == types.ProcessingStateNotReached {
			item.State = types.ProcessingStateQueued
		}
	}
	return item, completionReliable
}

func computeWikiStage(
	input processingKnowledgeStateInput,
	latest map[string]types.KnowledgeProcessingSpan,
	parent *types.KnowledgeProcessingSpan,
	q *types.ProcessingQueueAggregate,
	queueObservable bool,
) types.ProcessingStageItem {
	item := types.ProcessingStageItem{
		KnowledgeID:       input.Knowledge.ID,
		KnowledgeBaseID:   input.Knowledge.KnowledgeBaseID,
		KnowledgeBaseName: input.Knowledge.KnowledgeBaseName,
		Title:             processingKnowledgeTitle(input.Knowledge),
		Attempt:           input.Attempt,
		Stage:             types.ProcessingStageWiki,
		State:             types.ProcessingStateNotReached,
		Phase:             processingStageMeta(types.ProcessingStageWiki).phase,
	}
	if parent != nil {
		item.State = stateFromSpan(*parent)
		applySpanTiming(&item, *parent)
		item.ErrorCode = safeErrorCode(parent.ErrorCode)
		item.ErrorMessage = safeErrorMessage(parent.ErrorMessage)
	}
	if item.State == types.ProcessingStateNotReached && input.WikiPending != nil {
		item.State = types.ProcessingStateQueued
		t := input.WikiPending.QueuedAt
		item.QueuedAt = &t
	}
	if q != nil {
		applyQueueTiming(&item, q)
		if q.ActiveCount > 0 {
			item.State = types.ProcessingStateRunning
		} else if queueObservable && q.RetryCount+q.ScheduledCount > 0 && item.State != types.ProcessingStateRunning {
			item.State = types.ProcessingStateRetrying
		}
	}
	if parent != nil && genuineTerminalSpan(*parent) {
		total, okTotal := jsonMapInt(parent.Output, "pages_total")
		written, okWritten := jsonMapInt(parent.Output, "pages_written")
		dropped, okDropped := jsonMapInt(parent.Output, "pages_dropped")
		if okTotal && okWritten && total > 0 {
			if !okDropped {
				dropped = 0
			}
			item.Progress = &types.ProcessingStageProgress{Completed: written, Total: total, Failed: dropped, Unit: "page", Reliable: true}
			item.FailedChildren = dropped
		}
	}
	return item
}

func resolveProcessingChild(
	span *types.KnowledgeProcessingSpan,
	q *types.ProcessingQueueChildAggregate,
	queueObservable bool,
	queueComplete bool,
) (types.ProcessingStageState, bool) {
	if queueObservable && q != nil {
		if q.ActiveCount > 0 {
			return types.ProcessingStateRunning, true
		}
		if q.RetryCount+q.ScheduledCount > 0 {
			return types.ProcessingStateRetrying, true
		}
		if q.PendingCount > 0 {
			return types.ProcessingStateQueued, true
		}
	}
	if span == nil {
		return types.ProcessingStateNotReached, true
	}
	if genuineTerminalSpan(*span) {
		return stateFromSpan(*span), true
	}
	if supersededInFlightSpan(*span) {
		return types.ProcessingStateRunning, false
	}
	if span.Status == types.SpanStatusRunning {
		return types.ProcessingStateRunning, false
	}
	return stateFromSpan(*span), true
}

func aggregateProcessingState(
	counts map[types.ProcessingStageState]int,
	total int,
	parent *types.KnowledgeProcessingSpan,
) types.ProcessingStageState {
	switch {
	case counts[types.ProcessingStateRunning] > 0:
		return types.ProcessingStateRunning
	case counts[types.ProcessingStateRetrying] > 0:
		return types.ProcessingStateRetrying
	case counts[types.ProcessingStateQueued] > 0:
		return types.ProcessingStateQueued
	case counts[types.ProcessingStateFailed] > 0 && total > 0 &&
		counts[types.ProcessingStateDone]+counts[types.ProcessingStateSkipped]+counts[types.ProcessingStateCancelled]+counts[types.ProcessingStateFailed] >= total:
		return types.ProcessingStateDoneWithErrors
	case total > 0 && counts[types.ProcessingStateDone]+counts[types.ProcessingStateSkipped]+counts[types.ProcessingStateCancelled] >= total:
		return types.ProcessingStateDone
	case counts[types.ProcessingStateFailed] > 0:
		return types.ProcessingStateFailed
	}
	if parent != nil {
		return stateFromSpan(*parent)
	}
	return types.ProcessingStateNotReached
}

func fanoutAggregateForInput(
	input processingKnowledgeStateInput,
	latest map[string]types.KnowledgeProcessingSpan,
	stage types.ProcessingLogicalStage,
	childPrefix string,
) *types.ProcessingFanoutStageAggregate {
	var agg *types.ProcessingFanoutStageAggregate
	if input.Fanout != nil {
		agg = input.Fanout[stage]
	}
	if agg != nil {
		if agg.Details == nil {
			agg.Details = map[string]types.KnowledgeProcessingSpan{}
		}
		if !agg.CompletionReliable && len(agg.Details) == 0 && agg.TerminalTotalCount == 0 {
			return agg
		}
		return agg
	}
	var local *types.ProcessingFanoutStageAggregate
	for name, span := range latest {
		if !strings.HasPrefix(name, childPrefix) {
			continue
		}
		if local == nil {
			local = &types.ProcessingFanoutStageAggregate{
				KnowledgeID:        input.Knowledge.ID,
				Attempt:            input.Attempt,
				Stage:              stage,
				Details:            map[string]types.KnowledgeProcessingSpan{},
				CompletionReliable: true,
			}
		}
		key := childKeyFromSpanName(childPrefix, name)
		if genuineTerminalSpan(span) && span.Status != types.SpanStatusFailed {
			local.TerminalDoneCount++
			local.TerminalTotalCount++
		} else {
			local.Details[key] = span
		}
		if local.LatestUpdatedAt == nil || span.UpdatedAt.After(*local.LatestUpdatedAt) {
			t := span.UpdatedAt
			local.LatestUpdatedAt = &t
		}
	}
	return local
}

func dependencyQueuedState(parseStatus string, spans map[string]types.KnowledgeProcessingSpan, stage types.ProcessingLogicalStage) types.ProcessingStageState {
	if parseStatus != types.ParseStatusPending && parseStatus != types.ParseStatusProcessing && parseStatus != types.ParseStatusFinalizing {
		return types.ProcessingStateNotReached
	}
	switch stage {
	case types.ProcessingStageDocReader:
		if _, hasLater := spans[types.StageChunking]; !hasLater {
			return types.ProcessingStateQueued
		}
	case types.ProcessingStageChunking:
		if terminalDependency(spans[types.StageDocReader]) && !terminalDependency(spans[types.StageChunking]) {
			return types.ProcessingStateQueued
		}
	case types.ProcessingStageEmbedding:
		if terminalDependency(spans[types.StageChunking]) && !terminalDependency(spans[types.StageEmbedding]) {
			return types.ProcessingStateQueued
		}
	case types.ProcessingStagePostProcess:
		if terminalDependency(spans[types.StageEmbedding]) && terminalDependency(spans[types.StageMultimodal]) && !terminalDependency(spans[types.StagePostProcess]) {
			return types.ProcessingStateQueued
		}
	}
	return types.ProcessingStateNotReached
}

func stateFromSpan(span types.KnowledgeProcessingSpan) types.ProcessingStageState {
	switch span.Status {
	case types.SpanStatusPending:
		return types.ProcessingStateQueued
	case types.SpanStatusRunning:
		return types.ProcessingStateRunning
	case types.SpanStatusDone:
		return types.ProcessingStateDone
	case types.SpanStatusFailed:
		return types.ProcessingStateFailed
	case types.SpanStatusSkipped:
		return types.ProcessingStateSkipped
	case types.SpanStatusCancelled:
		return types.ProcessingStateCancelled
	default:
		return types.ProcessingStateNotReached
	}
}

func genuineTerminalSpan(span types.KnowledgeProcessingSpan) bool {
	return span.Status == types.SpanStatusDone ||
		span.Status == types.SpanStatusFailed ||
		span.Status == types.SpanStatusSkipped ||
		(span.Status == types.SpanStatusCancelled && span.ErrorCode != supersededErrorCode)
}

func supersededInFlightSpan(span types.KnowledgeProcessingSpan) bool {
	return span.Status == types.SpanStatusCancelled && span.ErrorCode == supersededErrorCode
}

func terminalDependency(span types.KnowledgeProcessingSpan) bool {
	return span.Status == types.SpanStatusDone || span.Status == types.SpanStatusSkipped
}

func latestSpansByName(spans []types.KnowledgeProcessingSpan) map[string]types.KnowledgeProcessingSpan {
	out := make(map[string]types.KnowledgeProcessingSpan, len(spans))
	for _, span := range spans {
		cur, ok := out[span.Name]
		if !ok || span.ID > cur.ID {
			out[span.Name] = span
		}
	}
	return out
}

func parentSpanName(stage types.ProcessingLogicalStage) string {
	switch stage {
	case types.ProcessingStageDocReader:
		return types.StageDocReader
	case types.ProcessingStageChunking:
		return types.StageChunking
	case types.ProcessingStageEmbedding:
		return types.StageEmbedding
	case types.ProcessingStageMultimodal:
		return types.StageMultimodal
	case types.ProcessingStagePostProcess:
		return types.StagePostProcess
	case types.ProcessingStageSummaryGen:
		return "postprocess.summary"
	case types.ProcessingStageQuestion:
		return "postprocess.question"
	case types.ProcessingStageWiki:
		return "postprocess.wiki"
	default:
		return ""
	}
}

type processingStageMetaValue struct {
	group       string
	order       int
	title       string
	description string
	phase       string
	unit        string
}

func processingStageMeta(stage types.ProcessingLogicalStage) processingStageMetaValue {
	switch stage {
	case types.ProcessingStageDocReader:
		return processingStageMetaValue{"primary", 1, "文档解析", "读取原始文档并提取正文", "解析中", ""}
	case types.ProcessingStageChunking:
		return processingStageMetaValue{"primary", 2, "文档分块", "切分文本并准备索引单元", "分块中", "chunk"}
	case types.ProcessingStageEmbedding:
		return processingStageMetaValue{"primary", 3, "索引构建", "写入向量和关键词检索索引", "索引构建中", "chunk"}
	case types.ProcessingStageMultimodal:
		return processingStageMetaValue{"primary", 4, "多模态识别", "识别图片中的文字与语义", "多模态识别", "image"}
	case types.ProcessingStagePostProcess:
		return processingStageMetaValue{"primary", 5, "后处理编排", "扇出摘要、问题、图谱和 Wiki 任务", "后处理编排", ""}
	case types.ProcessingStageSummaryGen:
		return processingStageMetaValue{"enrichment", 6, "摘要生成", "生成知识摘要", "摘要生成中", ""}
	case types.ProcessingStageQuestion:
		return processingStageMetaValue{"enrichment", 7, "问题生成", "生成可检索问题", "问题生成中", "batch"}
	case types.ProcessingStageGraph:
		return processingStageMetaValue{"enrichment", 8, "图谱抽取", "抽取实体关系并写入知识图谱", "实体关系抽取", "chunk"}
	case types.ProcessingStageWiki:
		return processingStageMetaValue{"enrichment", 9, "Wiki 合成", "合成 Wiki 页面和目录结构", "Wiki Reduce", "page"}
	default:
		return processingStageMetaValue{}
	}
}

func progressForStage(stage types.ProcessingLogicalStage, spans map[string]types.KnowledgeProcessingSpan) *types.ProcessingStageProgress {
	switch stage {
	case types.ProcessingStageDocReader:
		span, ok := spans[types.StageDocReader]
		if !ok {
			return nil
		}
		if n, ok := jsonMapInt(span.Output, "page_count"); ok && n > 0 {
			return &types.ProcessingStageProgress{Completed: n, Total: n, Unit: "page", Reliable: true}
		}
	case types.ProcessingStageChunking:
		span, ok := spans[types.StageChunking]
		if !ok {
			return nil
		}
		planned, okTotal := jsonMapInt(span.Output, "chunks_planned")
		written, okDone := jsonMapInt(span.Output, "chunks_written")
		if okTotal && okDone && planned > 0 {
			return &types.ProcessingStageProgress{Completed: written, Total: planned, Unit: "chunk", Reliable: true}
		}
	case types.ProcessingStageEmbedding:
		span, ok := spans[types.StageEmbedding]
		if !ok {
			return nil
		}
		total, okTotal := jsonMapInt(span.Input, "chunks_to_embed")
		if !okTotal {
			total, okTotal = jsonMapInt(span.Output, "chunks_to_embed")
		}
		done, okDone := jsonMapInt(span.Output, "vectors_written")
		if okTotal && okDone && total > 0 {
			return &types.ProcessingStageProgress{Completed: done, Total: total, Unit: "chunk", Reliable: true}
		}
	}
	return nil
}

func fanoutTotal(
	stage types.ProcessingLogicalStage,
	parent *types.KnowledgeProcessingSpan,
	latest map[string]types.KnowledgeProcessingSpan,
	childCount int,
	q *types.ProcessingQueueAggregate,
) int {
	switch stage {
	case types.ProcessingStageMultimodal:
		if parent != nil {
			if n, ok := jsonMapInt(parent.Input, "image_count"); ok && n > 0 {
				return n
			}
		}
	case types.ProcessingStageQuestion:
		if parent != nil {
			if n, ok := jsonMapInt(parent.Input, "batch_count"); ok && n > 0 {
				return n
			}
		}
		if post, ok := latest[types.StagePostProcess]; ok {
			if n, ok := jsonMapInt(post.Output, "batch_count"); ok && n > 0 {
				return n
			}
			if n, ok := jsonMapInt(post.Output, "enqueued_question_count"); ok && n > 0 {
				return n
			}
		}
	case types.ProcessingStageGraph:
		if post, ok := latest[types.StagePostProcess]; ok {
			if n, ok := jsonMapInt(post.Output, "enqueued_graph_count"); ok && n > 0 {
				return n
			}
		}
	}
	if childCount > 0 {
		return childCount
	}
	if q != nil && len(q.Children) > 0 {
		return len(q.Children)
	}
	return 0
}

func jsonMapInt(m types.JSONMap, key string) (int, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case json.Number:
		i, err := strconv.Atoi(string(n))
		return i, err == nil
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	default:
		return 0, false
	}
}

func childKeyFromSpanName(prefix, name string) string {
	if strings.HasPrefix(name, prefix) {
		if m := processingChildNameRE.FindStringSubmatch(name); len(m) == 2 {
			return m[1]
		}
	}
	return name
}

func processingQueueKey(knowledgeID string, attempt int, stage types.ProcessingLogicalStage) string {
	return fmt.Sprintf("%s:%d:%s", knowledgeID, attempt, stage)
}

func applySpanTiming(item *types.ProcessingStageItem, span types.KnowledgeProcessingSpan) {
	item.StartedAt = firstTime(item.StartedAt, span.StartedAt)
	item.FinishedAt = firstTime(item.FinishedAt, span.FinishedAt)
	if item.LastProgressAt == nil || span.UpdatedAt.After(*item.LastProgressAt) {
		t := span.UpdatedAt
		item.LastProgressAt = &t
	}
}

func applyQueueTiming(item *types.ProcessingStageItem, q *types.ProcessingQueueAggregate) {
	item.QueuedAt = firstTime(item.QueuedAt, q.EarliestEnqueuedAt)
	item.StartedAt = firstTime(item.StartedAt, q.EarliestActiveAt)
	item.NextRetryAt = firstTime(item.NextRetryAt, q.NextRetryAt)
	item.ErrorMessage = safeErrorMessage(q.LastError)
	if q.LastErrorAt != nil && (item.LastProgressAt == nil || q.LastErrorAt.After(*item.LastProgressAt)) {
		t := *q.LastErrorAt
		item.LastProgressAt = &t
	}
}

func firstTime(current, candidate *time.Time) *time.Time {
	if candidate == nil {
		return current
	}
	if current == nil || candidate.Before(*current) {
		t := *candidate
		return &t
	}
	return current
}

func stableKnowledgeTime(row types.ProcessingKnowledgeRow) *time.Time {
	if !row.CreatedAt.IsZero() {
		t := row.CreatedAt
		return &t
	}
	if !row.UpdatedAt.IsZero() {
		t := row.UpdatedAt
		return &t
	}
	return nil
}

func valueTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func elapsedMs(now time.Time, startedAt, queuedAt *time.Time) int64 {
	base := startedAt
	if base == nil {
		base = queuedAt
	}
	if base == nil {
		return 0
	}
	if now.Before(*base) {
		return 0
	}
	return now.Sub(*base).Milliseconds()
}

func durationMs(startedAt, finishedAt *time.Time, fallback int64) int64 {
	if startedAt != nil && finishedAt != nil && finishedAt.After(*startedAt) {
		return finishedAt.Sub(*startedAt).Milliseconds()
	}
	return fallback
}

func stageSortTime(item types.ProcessingStageItem) time.Time {
	if item.State == types.ProcessingStateQueued {
		return valueTime(item.QueuedAt)
	}
	if item.State == types.ProcessingStateRetrying {
		if item.NextRetryAt != nil {
			return *item.NextRetryAt
		}
		return valueTime(item.LastProgressAt)
	}
	if item.StartedAt != nil {
		return *item.StartedAt
	}
	return valueTime(item.LastProgressAt)
}

func processingKnowledgeTitle(row types.ProcessingKnowledgeRow) string {
	if strings.TrimSpace(row.Title) != "" {
		return row.Title
	}
	if strings.TrimSpace(row.FileName) != "" {
		return row.FileName
	}
	return row.ID
}

func safeErrorCode(code string) string {
	code = strings.TrimSpace(code)
	if code == supersededErrorCode {
		return ""
	}
	return code
}

func safeErrorMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	if len(msg) > 500 {
		return msg[:500]
	}
	return msg
}

func skipReason(span types.KnowledgeProcessingSpan) string {
	if span.Status != types.SpanStatusSkipped {
		return ""
	}
	return safeErrorMessage(span.ErrorMessage)
}
