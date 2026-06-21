package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type ProcessingDashboardService struct {
	repo        repository.ProcessingDashboardRepository
	queueReader interfaces.ProcessingQueueSnapshotReader
	kbService   interfaces.KnowledgeBaseService
	kbShare     interfaces.KBShareService
	spanRepo    repository.KnowledgeSpanRepository
}

func NewProcessingDashboardService(
	repo repository.ProcessingDashboardRepository,
	queueReader interfaces.ProcessingQueueSnapshotReader,
	kbService interfaces.KnowledgeBaseService,
	kbShare interfaces.KBShareService,
	spanRepo repository.KnowledgeSpanRepository,
) interfaces.ProcessingDashboardService {
	return &ProcessingDashboardService{
		repo:        repo,
		queueReader: queueReader,
		kbService:   kbService,
		kbShare:     kbShare,
		spanRepo:    spanRepo,
	}
}

func (s *ProcessingDashboardService) GetDashboard(
	ctx context.Context,
	filter types.ProcessingDashboardFilter,
) (*types.ProcessingDashboardResponse, error) {
	state, err := s.buildState(ctx, filter)
	if err != nil {
		return nil, err
	}
	limit := filter.ActivePreviewLimit
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	stages := make([]types.ProcessingStageSummary, 0, len(types.ProcessingLogicalStages))
	for _, stage := range types.ProcessingLogicalStages {
		meta := processingStageMeta(stage)
		summary := types.ProcessingStageSummary{
			Key:                stage,
			Group:              meta.group,
			Order:              meta.order,
			Title:              meta.title,
			Description:        meta.description,
			RetryingObservable: state.retryingObservable && !state.truncatedStages[stage],
			CompletionReliable: true,
			CountsReliable:     !state.truncatedStages[stage],
			RunningItems:       []types.ProcessingStageItem{},
		}
		for _, item := range state.itemsByStage[stage] {
			switch item.Item.State {
			case types.ProcessingStateRunning:
				summary.RunningCount++
				if len(summary.RunningItems) < limit {
					summary.RunningItems = append(summary.RunningItems, item.Item)
				}
			case types.ProcessingStateQueued:
				summary.QueuedCount++
			case types.ProcessingStateRetrying:
				summary.RetryingCount++
			}
			if !item.CompletionReliable {
				summary.CompletionReliable = false
			}
		}
		stages = append(stages, summary)
	}
	return &types.ProcessingDashboardResponse{
		GeneratedAt: state.generatedAt,
		Source:      state.source,
		Filters: types.ProcessingDashboardFilters{
			KnowledgeBaseID: filter.KnowledgeBaseID,
			Keyword:         filter.Keyword,
		},
		Groups: []types.ProcessingStageGroup{
			{Key: "primary", Name: "主处理流程", Stages: []types.ProcessingLogicalStage{
				types.ProcessingStageDocReader,
				types.ProcessingStageChunking,
				types.ProcessingStageEmbedding,
				types.ProcessingStageMultimodal,
				types.ProcessingStagePostProcess,
			}},
			{Key: "enrichment", Name: "内容增强流程", Stages: []types.ProcessingLogicalStage{
				types.ProcessingStageSummaryGen,
				types.ProcessingStageQuestion,
				types.ProcessingStageGraph,
				types.ProcessingStageWiki,
			}},
		},
		Stages: stages,
	}, nil
}

func (s *ProcessingDashboardService) ListStageItems(
	ctx context.Context,
	filter types.ProcessingDashboardFilter,
	stage types.ProcessingLogicalStage,
	stateFilter types.ProcessingStageState,
	cursor string,
	pageSize int,
) (*types.ProcessingStageItemsResponse, error) {
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	state, err := s.buildState(ctx, filter)
	if err != nil {
		return nil, err
	}
	items := make([]processingStageComputation, 0)
	for _, item := range state.itemsByStage[stage] {
		if item.Item.State == stateFilter {
			items = append(items, item)
		}
	}
	sortStageComputations(items, stateFilter)
	start := 0
	if cursor != "" {
		c, err := decodeProcessingCursor(cursor)
		if err == nil {
			for i, item := range items {
				if compareProcessingCursor(item, c) > 0 {
					start = i
					break
				}
				start = i + 1
			}
		}
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	respItems := make([]types.ProcessingStageItem, 0, end-start)
	for _, item := range items[start:end] {
		respItems = append(respItems, item.Item)
	}
	next := ""
	if end < len(items) && len(respItems) > 0 {
		next = encodeProcessingCursor(items[end-1])
	}
	return &types.ProcessingStageItemsResponse{
		GeneratedAt: state.generatedAt,
		Source:      state.source,
		Stage:       stage,
		State:       stateFilter,
		Items:       respItems,
		NextCursor:  next,
		Total:       len(items),
	}, nil
}

func (s *ProcessingDashboardService) GetKnowledgeProcessingDetail(
	ctx context.Context,
	knowledgeID string,
	attempt int,
) (*types.ProcessingKnowledgeDetailResponse, error) {
	filter := types.ProcessingDashboardFilter{KnowledgeBaseID: ""}
	enriched, err := s.withAccessibleScopes(ctx, filter)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListKnowledgeRowsByIDs(ctx, enriched, []string{knowledgeID})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("knowledge not found or not accessible")
	}
	queueSnap, _ := s.safeQueueSnapshot(ctx)
	generatedAt := time.Now()
	if queueSnap != nil && !queueSnap.GeneratedAt.IsZero() {
		generatedAt = queueSnap.GeneratedAt
	}
	attempts, err := s.repo.ListLatestAttempts(ctx, []string{knowledgeID})
	if err != nil {
		return nil, err
	}
	currentAttempt := 1
	if len(attempts) > 0 && attempts[0].Attempt > 0 {
		currentAttempt = attempts[0].Attempt
	}
	if attempt > 0 {
		currentAttempt = attempt
	}
	spans, err := s.repo.ListCanonicalAndParentSpans(ctx, []string{knowledgeID})
	if err != nil {
		return nil, err
	}
	fanoutBuckets, fanoutDetails, err := s.repo.AggregateFanoutSpans(ctx, []string{knowledgeID})
	if err != nil {
		return nil, err
	}
	fanoutByKnowledge := buildFanoutAggregates(fanoutBuckets, fanoutDetails, map[string]int{knowledgeID: currentAttempt})
	currentSpans := filterSpansByAttempt(spans, knowledgeID, currentAttempt)
	queueByStage := queueForKnowledge(queueSnap, knowledgeID, currentAttempt)
	wikiRows, _ := s.repo.ListWikiPendingOps(ctx, enriched, []string{knowledgeID})
	var wiki *types.ProcessingWikiPendingRow
	if len(wikiRows) > 0 {
		wiki = &wikiRows[0]
	}
	computed := buildProcessingStages(processingKnowledgeStateInput{
		Knowledge:   rows[0],
		Attempt:     currentAttempt,
		Spans:       currentSpans,
		Fanout:      fanoutByKnowledge[knowledgeID],
		Queue:       queueByStage,
		WikiPending: wiki,
		QueueStatus: queueSnapshotStatus(queueSnap),
		GeneratedAt: generatedAt,
	})
	stageItems := make([]types.ProcessingStageItem, 0, len(computed))
	for _, item := range computed {
		stageItems = append(stageItems, item.Item)
	}
	rawRows := currentSpans
	if s.spanRepo != nil {
		if rows, err := s.spanRepo.ListByAttempt(ctx, knowledgeID, currentAttempt); err == nil {
			rawRows = rows
		}
	}
	k := &types.Knowledge{
		ID:                   rows[0].ID,
		TenantID:             rows[0].TenantID,
		KnowledgeBaseID:      rows[0].KnowledgeBaseID,
		KnowledgeBaseName:    rows[0].KnowledgeBaseName,
		Title:                rows[0].Title,
		FileName:             rows[0].FileName,
		ParseStatus:          rows[0].ParseStatus,
		PendingSubtasksCount: rows[0].PendingSubtasksCount,
		UpdatedAt:            rows[0].UpdatedAt,
		CreatedAt:            rows[0].CreatedAt,
		ErrorMessage:         rows[0].ErrorMessage,
	}
	return &types.ProcessingKnowledgeDetailResponse{
		GeneratedAt:          generatedAt,
		Source:               sourceFromQueueSnapshot(queueSnap),
		Knowledge:            k,
		CurrentAttempt:       currentAttempt,
		ParseStatus:          rows[0].ParseStatus,
		PendingSubtasksCount: rows[0].PendingSubtasksCount,
		Stages:               stageItems,
		RawTrace:             buildProcessingRawTrace(rawRows),
	}, nil
}

type processingDashboardState struct {
	generatedAt        time.Time
	source             types.ProcessingDashboardSource
	retryingObservable bool
	truncatedStages    map[types.ProcessingLogicalStage]bool
	itemsByStage       map[types.ProcessingLogicalStage][]processingStageComputation
}

func (s *ProcessingDashboardService) buildState(
	ctx context.Context,
	filter types.ProcessingDashboardFilter,
) (*processingDashboardState, error) {
	filter, err := s.withAccessibleScopes(ctx, filter)
	if err != nil {
		return nil, err
	}
	queueSnap, _ := s.safeQueueSnapshot(ctx)
	generatedAt := time.Now()
	if queueSnap != nil && !queueSnap.GeneratedAt.IsZero() {
		generatedAt = queueSnap.GeneratedAt
	}
	wikiRows, err := s.repo.ListWikiPendingOps(ctx, filter, nil)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListCandidateKnowledge(ctx, filter)
	if err != nil {
		return nil, err
	}
	knownIDs := map[string]struct{}{}
	for _, row := range rows {
		knownIDs[row.ID] = struct{}{}
	}
	extraIDs := make([]string, 0)
	for _, agg := range queueAggregates(queueSnap) {
		if _, ok := knownIDs[agg.KnowledgeID]; !ok {
			extraIDs = append(extraIDs, agg.KnowledgeID)
			knownIDs[agg.KnowledgeID] = struct{}{}
		}
	}
	for _, wiki := range wikiRows {
		if _, ok := knownIDs[wiki.KnowledgeID]; !ok {
			extraIDs = append(extraIDs, wiki.KnowledgeID)
			knownIDs[wiki.KnowledgeID] = struct{}{}
		}
	}
	if len(extraIDs) > 0 {
		more, err := s.repo.ListKnowledgeRowsByIDs(ctx, filter, extraIDs)
		if err != nil {
			return nil, err
		}
		rows = append(rows, more...)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	attemptRows, err := s.repo.ListLatestAttempts(ctx, ids)
	if err != nil {
		return nil, err
	}
	attemptByKnowledge := map[string]int{}
	for _, row := range attemptRows {
		attemptByKnowledge[row.KnowledgeID] = row.Attempt
	}
	for _, agg := range queueAggregates(queueSnap) {
		if agg.Attempt > attemptByKnowledge[agg.KnowledgeID] {
			attemptByKnowledge[agg.KnowledgeID] = agg.Attempt
		}
	}
	spans, err := s.repo.ListCanonicalAndParentSpans(ctx, ids)
	if err != nil {
		return nil, err
	}
	fanoutBuckets, fanoutDetails, err := s.repo.AggregateFanoutSpans(ctx, ids)
	if err != nil {
		return nil, err
	}
	spansByKnowledge := map[string][]types.KnowledgeProcessingSpan{}
	for _, span := range spans {
		if span.Attempt == attemptByKnowledge[span.KnowledgeID] {
			spansByKnowledge[span.KnowledgeID] = append(spansByKnowledge[span.KnowledgeID], span)
		}
	}
	fanoutByKnowledge := buildFanoutAggregates(fanoutBuckets, fanoutDetails, attemptByKnowledge)
	wikiByKnowledge := map[string]*types.ProcessingWikiPendingRow{}
	for i := range wikiRows {
		row := wikiRows[i]
		wikiByKnowledge[row.KnowledgeID] = &row
	}
	itemsByStage := map[types.ProcessingLogicalStage][]processingStageComputation{}
	queueStatus := queueSnapshotStatus(queueSnap)
	for _, row := range rows {
		attempt := attemptByKnowledge[row.ID]
		if attempt <= 0 && activeParseStatus(row.ParseStatus) {
			attempt = 1
		}
		if attempt <= 0 {
			continue
		}
		computed := buildProcessingStages(processingKnowledgeStateInput{
			Knowledge:   row,
			Attempt:     attempt,
			Spans:       spansByKnowledge[row.ID],
			Fanout:      fanoutByKnowledge[row.ID],
			Queue:       queueForKnowledge(queueSnap, row.ID, attempt),
			WikiPending: wikiByKnowledge[row.ID],
			QueueStatus: queueStatus,
			GeneratedAt: generatedAt,
		})
		for _, item := range computed {
			if item.Item.State == types.ProcessingStateNotReached ||
				item.Item.State == types.ProcessingStateDone ||
				item.Item.State == types.ProcessingStateDoneWithErrors ||
				item.Item.State == types.ProcessingStateSkipped ||
				item.Item.State == types.ProcessingStateCancelled {
				continue
			}
			itemsByStage[item.Item.Stage] = append(itemsByStage[item.Item.Stage], item)
		}
	}
	for stage := range itemsByStage {
		sortStageComputations(itemsByStage[stage], types.ProcessingStateRunning)
	}
	return &processingDashboardState{
		generatedAt:        generatedAt,
		source:             sourceFromQueueSnapshot(queueSnap),
		retryingObservable: queueStatus == types.ProcessingQueueSnapshotOK || queueStatus == types.ProcessingQueueSnapshotPartial,
		truncatedStages:    truncatedProcessingStages(queueSnap),
		itemsByStage:       itemsByStage,
	}, nil
}

func (s *ProcessingDashboardService) withAccessibleScopes(
	ctx context.Context,
	filter types.ProcessingDashboardFilter,
) (types.ProcessingDashboardFilter, error) {
	if filter.TenantID == 0 {
		if v, ok := ctx.Value(types.TenantIDContextKey).(uint64); ok {
			filter.TenantID = v
		}
	}
	if filter.UserID == "" {
		if v, ok := ctx.Value(types.UserIDContextKey).(string); ok {
			filter.UserID = v
		}
	}
	if len(filter.AccessibleScopes) > 0 {
		return filter, nil
	}
	scopes := make([]types.KnowledgeSearchScope, 0)
	if s.kbService != nil {
		kbs, err := s.kbService.ListKnowledgeBases(ctx)
		if err != nil {
			return filter, err
		}
		for _, kb := range kbs {
			if kb != nil && kb.Type == types.KnowledgeBaseTypeDocument {
				scopes = append(scopes, types.KnowledgeSearchScope{TenantID: kb.TenantID, KBID: kb.ID})
			}
		}
	}
	if s.kbShare != nil && filter.TenantID != 0 {
		shared, err := s.kbShare.ListSharedKnowledgeBases(ctx, filter.TenantID, types.TenantRoleFromContext(ctx))
		if err == nil {
			for _, info := range shared {
				if info != nil && info.KnowledgeBase != nil && info.KnowledgeBase.Type == types.KnowledgeBaseTypeDocument {
					scopes = append(scopes, types.KnowledgeSearchScope{TenantID: info.SourceTenantID, KBID: info.KnowledgeBase.ID})
				}
			}
		}
	}
	if filter.KnowledgeBaseID != "" {
		scopes = filterScopesByKB(scopes, filter.KnowledgeBaseID)
	}
	filter.AccessibleScopes = dedupeScopes(scopes)
	return filter, nil
}

func (s *ProcessingDashboardService) safeQueueSnapshot(ctx context.Context) (*types.ProcessingQueueSnapshot, error) {
	if s.queueReader == nil {
		return &types.ProcessingQueueSnapshot{
			ExecutorMode: types.ProcessingExecutorModeLite,
			Status:       types.ProcessingQueueSnapshotNotApplicable,
			GeneratedAt:  time.Now(),
			Aggregates:   map[string]*types.ProcessingQueueAggregate{},
		}, nil
	}
	snap, err := s.queueReader.Snapshot(ctx)
	if err != nil {
		return &types.ProcessingQueueSnapshot{
			ExecutorMode: types.ProcessingExecutorModeAsynq,
			Status:       types.ProcessingQueueSnapshotDegraded,
			GeneratedAt:  time.Now(),
			Aggregates:   map[string]*types.ProcessingQueueAggregate{},
			Message:      err.Error(),
		}, nil
	}
	return snap, nil
}

func sourceFromQueueSnapshot(snap *types.ProcessingQueueSnapshot) types.ProcessingDashboardSource {
	if snap == nil {
		return types.ProcessingDashboardSource{ExecutorMode: types.ProcessingExecutorModeLite, QueueSnapshot: types.ProcessingQueueSnapshotNotApplicable}
	}
	return types.ProcessingDashboardSource{
		ExecutorMode:    snap.ExecutorMode,
		QueueSnapshot:   snap.Status,
		TruncatedQueues: append([]string(nil), snap.TruncatedQueues...),
		Message:         snap.Message,
	}
}

func queueSnapshotStatus(snap *types.ProcessingQueueSnapshot) string {
	if snap == nil || snap.Status == "" {
		return types.ProcessingQueueSnapshotNotApplicable
	}
	return snap.Status
}

func queueAggregates(snap *types.ProcessingQueueSnapshot) []*types.ProcessingQueueAggregate {
	if snap == nil {
		return nil
	}
	out := make([]*types.ProcessingQueueAggregate, 0, len(snap.Aggregates))
	for _, agg := range snap.Aggregates {
		if agg != nil {
			out = append(out, agg)
		}
	}
	return out
}

func queueForKnowledge(snap *types.ProcessingQueueSnapshot, knowledgeID string, attempt int) map[types.ProcessingLogicalStage]*types.ProcessingQueueAggregate {
	out := map[types.ProcessingLogicalStage]*types.ProcessingQueueAggregate{}
	for _, agg := range queueAggregates(snap) {
		if agg.KnowledgeID == knowledgeID && agg.Attempt == attempt {
			out[agg.Stage] = agg
		}
	}
	return out
}

func buildFanoutAggregates(
	buckets []types.ProcessingFanoutSpanBucket,
	details []types.KnowledgeProcessingSpan,
	attemptByKnowledge map[string]int,
) map[string]map[types.ProcessingLogicalStage]*types.ProcessingFanoutStageAggregate {
	out := map[string]map[types.ProcessingLogicalStage]*types.ProcessingFanoutStageAggregate{}
	ensure := func(kid string, attempt int, stage types.ProcessingLogicalStage) *types.ProcessingFanoutStageAggregate {
		if attempt <= 0 {
			return nil
		}
		if out[kid] == nil {
			out[kid] = map[types.ProcessingLogicalStage]*types.ProcessingFanoutStageAggregate{}
		}
		agg := out[kid][stage]
		if agg == nil {
			agg = &types.ProcessingFanoutStageAggregate{
				KnowledgeID:        kid,
				Attempt:            attempt,
				Stage:              stage,
				Details:            map[string]types.KnowledgeProcessingSpan{},
				CompletionReliable: true,
			}
			out[kid][stage] = agg
		}
		return agg
	}
	for _, bucket := range buckets {
		if bucket.Attempt != attemptByKnowledge[bucket.KnowledgeID] {
			continue
		}
		agg := ensure(bucket.KnowledgeID, bucket.Attempt, bucket.Stage)
		if agg == nil {
			continue
		}
		agg.TerminalTotalCount += bucket.Count
		switch bucket.Status {
		case types.SpanStatusDone, types.SpanStatusSkipped:
			agg.TerminalDoneCount += bucket.Count
		case types.SpanStatusCancelled:
			if bucket.ErrorCode != supersededErrorCode {
				agg.TerminalDoneCount += bucket.Count
			} else {
				agg.TerminalTotalCount -= bucket.Count
			}
		case types.SpanStatusFailed, types.SpanStatusRunning, types.SpanStatusPending:
			agg.TerminalTotalCount -= bucket.Count
		default:
			agg.TerminalTotalCount -= bucket.Count
		}
		if !bucket.UpdatedAt.IsZero() && (agg.LatestUpdatedAt == nil || bucket.UpdatedAt.After(*agg.LatestUpdatedAt)) {
			t := bucket.UpdatedAt
			agg.LatestUpdatedAt = &t
		}
	}
	for _, span := range details {
		if span.Attempt != attemptByKnowledge[span.KnowledgeID] {
			continue
		}
		stage, prefix, ok := fanoutStageForSpanName(span.Name)
		if !ok {
			continue
		}
		agg := ensure(span.KnowledgeID, span.Attempt, stage)
		if agg == nil {
			continue
		}
		agg.Details[childKeyFromSpanName(prefix, span.Name)] = span
		if agg.LatestUpdatedAt == nil || span.UpdatedAt.After(*agg.LatestUpdatedAt) {
			t := span.UpdatedAt
			agg.LatestUpdatedAt = &t
		}
	}
	return out
}

func fanoutStageForSpanName(name string) (types.ProcessingLogicalStage, string, bool) {
	prefixes := []struct {
		stage  types.ProcessingLogicalStage
		prefix string
	}{
		{types.ProcessingStageMultimodal, "multimodal.image["},
		{types.ProcessingStageQuestion, "postprocess.question.batch["},
		{types.ProcessingStageGraph, "postprocess.graph.chunk["},
		{types.ProcessingStageWiki, "postprocess.wiki."},
	}
	for _, item := range prefixes {
		if strings.HasPrefix(name, item.prefix) {
			return item.stage, item.prefix, true
		}
	}
	return "", "", false
}

func truncatedProcessingStages(snap *types.ProcessingQueueSnapshot) map[types.ProcessingLogicalStage]bool {
	out := map[types.ProcessingLogicalStage]bool{}
	if snap == nil || snap.Status != types.ProcessingQueueSnapshotPartial {
		return out
	}
	for _, marker := range snap.TruncatedQueues {
		queue := marker
		if i := strings.Index(marker, ":"); i >= 0 {
			queue = marker[:i]
		}
		switch queue {
		case types.QueueMultimodal:
			out[types.ProcessingStageMultimodal] = true
		case types.QueueQuestion:
			out[types.ProcessingStageQuestion] = true
		case types.QueueGraph:
			out[types.ProcessingStageGraph] = true
		case types.QueueCritical, types.QueueDefault, types.QueueLow:
			for _, stage := range types.ProcessingLogicalStages {
				out[stage] = true
			}
		}
	}
	return out
}

func filterSpansByAttempt(spans []types.KnowledgeProcessingSpan, knowledgeID string, attempt int) []types.KnowledgeProcessingSpan {
	out := make([]types.KnowledgeProcessingSpan, 0)
	for _, span := range spans {
		if span.KnowledgeID == knowledgeID && span.Attempt == attempt {
			out = append(out, span)
		}
	}
	return out
}

func activeParseStatus(status string) bool {
	return status == types.ParseStatusPending || status == types.ParseStatusProcessing || status == types.ParseStatusFinalizing
}

func filterScopesByKB(scopes []types.KnowledgeSearchScope, kbID string) []types.KnowledgeSearchScope {
	out := make([]types.KnowledgeSearchScope, 0, len(scopes))
	for _, scope := range scopes {
		if scope.KBID == kbID {
			out = append(out, scope)
		}
	}
	return out
}

func dedupeScopes(scopes []types.KnowledgeSearchScope) []types.KnowledgeSearchScope {
	seen := map[string]struct{}{}
	out := make([]types.KnowledgeSearchScope, 0, len(scopes))
	for _, scope := range scopes {
		key := strconv.FormatUint(scope.TenantID, 10) + ":" + scope.KBID
		if _, ok := seen[key]; ok || scope.TenantID == 0 || scope.KBID == "" {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, scope)
	}
	return out
}

func sortStageComputations(items []processingStageComputation, state types.ProcessingStageState) {
	sort.SliceStable(items, func(i, j int) bool {
		ti := items[i].sortTime
		tj := items[j].sortTime
		if ti.IsZero() && !tj.IsZero() {
			return false
		}
		if !ti.IsZero() && tj.IsZero() {
			return true
		}
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if items[i].Item.KnowledgeID != items[j].Item.KnowledgeID {
			return items[i].Item.KnowledgeID < items[j].Item.KnowledgeID
		}
		return items[i].Item.Attempt < items[j].Item.Attempt
	})
}

type processingCursor struct {
	Timestamp int64  `json:"ts"`
	ID        string `json:"id"`
	Attempt   int    `json:"attempt"`
}

func encodeProcessingCursor(item processingStageComputation) string {
	c := processingCursor{Timestamp: item.sortTime.UnixNano(), ID: item.Item.KnowledgeID, Attempt: item.Item.Attempt}
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeProcessingCursor(raw string) (processingCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return processingCursor{}, err
	}
	var c processingCursor
	err = json.Unmarshal(b, &c)
	return c, err
}

func compareProcessingCursor(item processingStageComputation, c processingCursor) int {
	ts := item.sortTime.UnixNano()
	if ts != c.Timestamp {
		if ts < c.Timestamp {
			return -1
		}
		return 1
	}
	if item.Item.KnowledgeID != c.ID {
		if strings.Compare(item.Item.KnowledgeID, c.ID) < 0 {
			return -1
		}
		return 1
	}
	if item.Item.Attempt == c.Attempt {
		return 0
	}
	if item.Item.Attempt < c.Attempt {
		return -1
	}
	return 1
}

func buildProcessingRawTrace(rows []types.KnowledgeProcessingSpan) []*types.SpanTreeNode {
	nodes := make(map[string]*types.SpanTreeNode, len(rows))
	roots := make([]*types.SpanTreeNode, 0)
	for _, row := range rows {
		row.ErrorDetail = ""
		node := &types.SpanTreeNode{KnowledgeProcessingSpan: row}
		nodes[row.SpanID] = node
	}
	for _, row := range rows {
		node := nodes[row.SpanID]
		if row.ParentSpanID != "" {
			if parent := nodes[row.ParentSpanID]; parent != nil {
				parent.Children = append(parent.Children, node)
				continue
			}
		}
		roots = append(roots, node)
	}
	sort.SliceStable(roots, func(i, j int) bool {
		return roots[i].ID < roots[j].ID
	})
	return roots
}
