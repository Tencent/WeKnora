package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestProcessingDashboardStateCoreFlow(t *testing.T) {
	now := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	row := types.ProcessingKnowledgeRow{ID: "kid", KnowledgeBaseID: "kb", KnowledgeBaseName: "KB", Title: "Doc", ParseStatus: types.ParseStatusProcessing}

	tests := []struct {
		name  string
		input processingKnowledgeStateInput
		want  map[types.ProcessingLogicalStage]types.ProcessingStageState
	}{
		{
			name: "initial pending reaches docreader only",
			input: processingKnowledgeStateInput{
				Knowledge:   row,
				Attempt:     1,
				QueueStatus: types.ProcessingQueueSnapshotOK,
				GeneratedAt: now,
			},
			want: map[types.ProcessingLogicalStage]types.ProcessingStageState{
				types.ProcessingStageDocReader: types.ProcessingStateQueued,
				types.ProcessingStageChunking:  types.ProcessingStateNotReached,
			},
		},
		{
			name: "docreader done makes chunking queued",
			input: processingKnowledgeStateInput{
				Knowledge:   row,
				Attempt:     1,
				Spans:       []types.KnowledgeProcessingSpan{pdSpan(1, types.StageDocReader, types.SpanStatusDone, now)},
				QueueStatus: types.ProcessingQueueSnapshotOK,
				GeneratedAt: now,
			},
			want: map[types.ProcessingLogicalStage]types.ProcessingStageState{
				types.ProcessingStageDocReader: types.ProcessingStateDone,
				types.ProcessingStageChunking:  types.ProcessingStateQueued,
			},
		},
		{
			name: "chunking done reaches embedding and multimodal independently",
			input: processingKnowledgeStateInput{
				Knowledge: row,
				Attempt:   1,
				Spans: []types.KnowledgeProcessingSpan{
					pdSpan(1, types.StageDocReader, types.SpanStatusDone, now),
					pdSpan(2, types.StageChunking, types.SpanStatusDone, now),
				},
				QueueStatus: types.ProcessingQueueSnapshotOK,
				GeneratedAt: now,
			},
			want: map[types.ProcessingLogicalStage]types.ProcessingStageState{
				types.ProcessingStageEmbedding:  types.ProcessingStateQueued,
				types.ProcessingStageMultimodal: types.ProcessingStateNotReached,
			},
		},
		{
			name: "postprocess waits for multimodal",
			input: processingKnowledgeStateInput{
				Knowledge: row,
				Attempt:   1,
				Spans: []types.KnowledgeProcessingSpan{
					pdSpan(1, types.StageChunking, types.SpanStatusDone, now),
					pdSpan(2, types.StageEmbedding, types.SpanStatusDone, now),
					pdSpan(3, types.StageMultimodal, types.SpanStatusRunning, now),
				},
				QueueStatus: types.ProcessingQueueSnapshotOK,
				GeneratedAt: now,
			},
			want: map[types.ProcessingLogicalStage]types.ProcessingStageState{
				types.ProcessingStagePostProcess: types.ProcessingStateNotReached,
			},
		},
		{
			name: "embedding skipped still allows postprocess when multimodal done",
			input: processingKnowledgeStateInput{
				Knowledge: row,
				Attempt:   1,
				Spans: []types.KnowledgeProcessingSpan{
					pdSpan(1, types.StageEmbedding, types.SpanStatusSkipped, now),
					pdSpan(2, types.StageMultimodal, types.SpanStatusDone, now),
				},
				QueueStatus: types.ProcessingQueueSnapshotOK,
				GeneratedAt: now,
			},
			want: map[types.ProcessingLogicalStage]types.ProcessingStageState{
				types.ProcessingStagePostProcess: types.ProcessingStateQueued,
			},
		},
		{
			name: "completed knowledge does not synthesize active stages",
			input: processingKnowledgeStateInput{
				Knowledge:   types.ProcessingKnowledgeRow{ID: "kid", ParseStatus: types.ParseStatusCompleted},
				Attempt:     1,
				QueueStatus: types.ProcessingQueueSnapshotOK,
				GeneratedAt: now,
			},
			want: map[types.ProcessingLogicalStage]types.ProcessingStageState{
				types.ProcessingStageDocReader: types.ProcessingStateNotReached,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := byStage(buildProcessingStages(tt.input))
			for stage, want := range tt.want {
				if got[stage].Item.State != want {
					t.Fatalf("%s state = %s, want %s", stage, got[stage].Item.State, want)
				}
			}
		})
	}
}

func TestProcessingDashboardFanoutAndQueueReconcile(t *testing.T) {
	now := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	row := types.ProcessingKnowledgeRow{ID: "kid", KnowledgeBaseID: "kb", Title: "Fanout", ParseStatus: types.ParseStatusFinalizing}

	t.Run("multimodal child states aggregate to one running knowledge", func(t *testing.T) {
		q := queueAgg("kid", 1, types.ProcessingStageMultimodal)
		q.Children["2"] = &types.ProcessingQueueChildAggregate{ChildKey: "2", ActiveCount: 1}
		q.Children["3"] = &types.ProcessingQueueChildAggregate{ChildKey: "3", PendingCount: 1}
		q.Children["4"] = &types.ProcessingQueueChildAggregate{ChildKey: "4", RetryCount: 1}
		q.ActiveCount = 1
		q.RetryCount = 1
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpanWithIO(1, types.StageMultimodal, types.SpanStatusDone, now, types.JSONMap{"image_count": 5}, nil),
				pdSpan(2, "multimodal.image[0]", types.SpanStatusDone, now),
				pdSpan(3, "multimodal.image[1]", types.SpanStatusDone, now),
				pdSpan(4, "multimodal.image[2]", types.SpanStatusRunning, now),
			},
			Queue:       map[types.ProcessingLogicalStage]*types.ProcessingQueueAggregate{types.ProcessingStageMultimodal: q},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageMultimodal]
		if got.Item.State != types.ProcessingStateRunning {
			t.Fatalf("state = %s, want running", got.Item.State)
		}
		if got.Item.Progress == nil || got.Item.Progress.Total != 5 || got.Item.Progress.Completed != 2 {
			t.Fatalf("progress = %#v, want 2/5", got.Item.Progress)
		}
	})

	t.Run("latest span by name wins", func(t *testing.T) {
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpan(1, "postprocess.question.batch[0]", types.SpanStatusFailed, now),
				pdSpan(2, "postprocess.question.batch[0]", types.SpanStatusDone, now),
			},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageQuestion]
		if got.Item.FailedChildren != 0 || got.Item.State != types.ProcessingStateDone {
			t.Fatalf("state=%s failed=%d, want done failed=0", got.Item.State, got.Item.FailedChildren)
		}
	})

	t.Run("superseded child with retry queue is retrying and not complete", func(t *testing.T) {
		q := queueAgg("kid", 1, types.ProcessingStageGraph)
		q.Children["7"] = &types.ProcessingQueueChildAggregate{ChildKey: "7", RetryCount: 1}
		q.RetryCount = 1
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpanErr(1, "postprocess.graph.chunk[7]", types.SpanStatusCancelled, supersededErrorCode, now),
			},
			Queue:       map[types.ProcessingLogicalStage]*types.ProcessingQueueAggregate{types.ProcessingStageGraph: q},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageGraph]
		if got.Item.State != types.ProcessingStateRetrying {
			t.Fatalf("state = %s, want retrying", got.Item.State)
		}
		if got.Item.Progress == nil || got.Item.Progress.Completed != 0 {
			t.Fatalf("progress = %#v, want completed 0", got.Item.Progress)
		}
	})

	t.Run("superseded child missing from complete queue stays running unreliable", func(t *testing.T) {
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpanErr(1, "postprocess.graph.chunk[7]", types.SpanStatusCancelled, supersededErrorCode, now),
			},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageGraph]
		if got.Item.FailedChildren != 0 || got.Item.State != types.ProcessingStateRunning || got.CompletionReliable {
			t.Fatalf("state=%s failed=%d reliable=%v, want running unreliable failed=0", got.Item.State, got.Item.FailedChildren, got.CompletionReliable)
		}
	})

	t.Run("running child missing from complete queue stays running unreliable", func(t *testing.T) {
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpan(1, "postprocess.graph.chunk[7]", types.SpanStatusRunning, now),
			},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageGraph]
		if got.Item.State != types.ProcessingStateRunning || got.CompletionReliable {
			t.Fatalf("state=%s reliable=%v, want running unreliable", got.Item.State, got.CompletionReliable)
		}
	})

	t.Run("real failed span remains failed", func(t *testing.T) {
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpan(1, "postprocess.graph.chunk[7]", types.SpanStatusFailed, now),
			},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageGraph]
		if got.Item.State != types.ProcessingStateDoneWithErrors || got.Item.FailedChildren != 1 {
			t.Fatalf("state=%s failed=%d, want done_with_errors failed=1", got.Item.State, got.Item.FailedChildren)
		}
	})

	t.Run("superseded child with degraded queue stays running and unreliable", func(t *testing.T) {
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpanErr(1, "postprocess.graph.chunk[7]", types.SpanStatusCancelled, supersededErrorCode, now),
			},
			QueueStatus: types.ProcessingQueueSnapshotDegraded,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageGraph]
		if got.Item.State != types.ProcessingStateRunning || got.CompletionReliable {
			t.Fatalf("state=%s reliable=%v, want running unreliable", got.Item.State, got.CompletionReliable)
		}
	})

	t.Run("queue retry wins over failed span without double count", func(t *testing.T) {
		q := queueAgg("kid", 1, types.ProcessingStageQuestion)
		q.Children["0"] = &types.ProcessingQueueChildAggregate{ChildKey: "0", RetryCount: 1}
		q.RetryCount = 1
		input := processingKnowledgeStateInput{
			Knowledge:   row,
			Attempt:     1,
			Spans:       []types.KnowledgeProcessingSpan{pdSpan(1, "postprocess.question.batch[0]", types.SpanStatusFailed, now)},
			Queue:       map[types.ProcessingLogicalStage]*types.ProcessingQueueAggregate{types.ProcessingStageQuestion: q},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageQuestion]
		if got.Item.State != types.ProcessingStateRetrying || got.Item.FailedChildren != 0 {
			t.Fatalf("state=%s failed=%d, want retrying failed=0", got.Item.State, got.Item.FailedChildren)
		}
	})

	t.Run("graph queue chunk index aligns with span child key", func(t *testing.T) {
		q := queueAgg("kid", 1, types.ProcessingStageGraph)
		q.Children["83"] = &types.ProcessingQueueChildAggregate{ChildKey: "83", ActiveCount: 1}
		q.ActiveCount = 1
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpanWithIO(1, types.StagePostProcess, types.SpanStatusDone, now, nil, types.JSONMap{"enqueued_graph_count": 1}),
				pdSpan(2, "postprocess.graph.chunk[83]", types.SpanStatusDone, now),
			},
			Queue:       map[types.ProcessingLogicalStage]*types.ProcessingQueueAggregate{types.ProcessingStageGraph: q},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageGraph]
		if got.Item.Progress == nil || got.Item.Progress.Total != 1 {
			t.Fatalf("progress = %#v, want total 1 without double count", got.Item.Progress)
		}
	})

	t.Run("fanout aggregate can mark all graph chunks done without materialized children", func(t *testing.T) {
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpanWithIO(1, types.StagePostProcess, types.SpanStatusDone, now, nil, types.JSONMap{"enqueued_graph_count": 120}),
			},
			Fanout: map[types.ProcessingLogicalStage]*types.ProcessingFanoutStageAggregate{
				types.ProcessingStageGraph: {
					KnowledgeID:        "kid",
					Attempt:            1,
					Stage:              types.ProcessingStageGraph,
					TerminalDoneCount:  120,
					TerminalTotalCount: 120,
					Details:            map[string]types.KnowledgeProcessingSpan{},
					CompletionReliable: true,
				},
			},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageGraph]
		if got.Item.State != types.ProcessingStateDone || got.Item.Progress == nil || got.Item.Progress.Completed != 120 || got.Item.Progress.Total != 120 {
			t.Fatalf("state=%s progress=%#v, want done 120/120", got.Item.State, got.Item.Progress)
		}
	})
}

func TestProcessingDashboardCanonicalQueueDoesNotOverrideTerminalSpan(t *testing.T) {
	now := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	row := types.ProcessingKnowledgeRow{ID: "kid", KnowledgeBaseID: "kb", Title: "Doc", ParseStatus: types.ParseStatusProcessing}

	t.Run("document active does not keep docreader running after done span", func(t *testing.T) {
		q := queueAgg("kid", 1, types.ProcessingStageDocReader)
		q.ActiveCount = 1
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpan(1, types.StageDocReader, types.SpanStatusDone, now),
				pdSpan(2, types.StageChunking, types.SpanStatusRunning, now),
			},
			Queue:       map[types.ProcessingLogicalStage]*types.ProcessingQueueAggregate{types.ProcessingStageDocReader: q},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))
		if got[types.ProcessingStageDocReader].Item.State != types.ProcessingStateDone {
			t.Fatalf("docreader = %s, want done", got[types.ProcessingStageDocReader].Item.State)
		}
		if got[types.ProcessingStageChunking].Item.State != types.ProcessingStateRunning {
			t.Fatalf("chunking = %s, want running", got[types.ProcessingStageChunking].Item.State)
		}
	})

	t.Run("document active without span still means docreader running", func(t *testing.T) {
		q := queueAgg("kid", 1, types.ProcessingStageDocReader)
		q.ActiveCount = 1
		got := byStage(buildProcessingStages(processingKnowledgeStateInput{
			Knowledge:   row,
			Attempt:     1,
			Queue:       map[types.ProcessingLogicalStage]*types.ProcessingQueueAggregate{types.ProcessingStageDocReader: q},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}))
		if got[types.ProcessingStageDocReader].Item.State != types.ProcessingStateRunning {
			t.Fatalf("docreader = %s, want running", got[types.ProcessingStageDocReader].Item.State)
		}
	})
}

func TestProcessingDashboardWikiAndRetryObservability(t *testing.T) {
	now := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	row := types.ProcessingKnowledgeRow{ID: "kid", KnowledgeBaseID: "kb", Title: "Wiki", ParseStatus: types.ParseStatusFinalizing}

	t.Run("wiki pending op queues one knowledge", func(t *testing.T) {
		input := processingKnowledgeStateInput{
			Knowledge:   row,
			Attempt:     1,
			WikiPending: &types.ProcessingWikiPendingRow{KnowledgeID: "kid", QueuedAt: now},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageWiki]
		if got.Item.State != types.ProcessingStateQueued {
			t.Fatalf("state = %s, want queued", got.Item.State)
		}
	})

	t.Run("wiki running span wins over residual pending op", func(t *testing.T) {
		input := processingKnowledgeStateInput{
			Knowledge:   row,
			Attempt:     1,
			Spans:       []types.KnowledgeProcessingSpan{pdSpan(1, "postprocess.wiki", types.SpanStatusRunning, now)},
			WikiPending: &types.ProcessingWikiPendingRow{KnowledgeID: "kid", QueuedAt: now},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageWiki]
		if got.Item.State != types.ProcessingStateRunning {
			t.Fatalf("state = %s, want running", got.Item.State)
		}
	})

	t.Run("wiki progress uses terminal parent output not helper child spans", func(t *testing.T) {
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpanWithIO(1, "postprocess.wiki", types.SpanStatusDone, now, nil, types.JSONMap{
					"pages_total":   13,
					"pages_written": 13,
					"pages_dropped": 0,
				}),
				pdSpan(2, "postprocess.wiki.extract", types.SpanStatusDone, now),
				pdSpan(3, "postprocess.wiki.summary", types.SpanStatusDone, now),
				pdSpan(4, "postprocess.wiki.classify", types.SpanStatusDone, now),
			},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageWiki]
		if got.Item.Progress == nil || got.Item.Progress.Completed != 13 || got.Item.Progress.Total != 13 {
			t.Fatalf("progress = %#v, want 13/13 from parent output", got.Item.Progress)
		}
	})

	t.Run("wiki progress reports dropped pages from terminal parent output", func(t *testing.T) {
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpanWithIO(1, "postprocess.wiki", types.SpanStatusDone, now, nil, types.JSONMap{
					"pages_total":   13,
					"pages_written": 11,
					"pages_dropped": 2,
				}),
			},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageWiki]
		if got.Item.Progress == nil || got.Item.Progress.Completed != 11 || got.Item.Progress.Total != 13 || got.Item.Progress.Failed != 2 {
			t.Fatalf("progress = %#v, want 11/13 failed=2", got.Item.Progress)
		}
	})

	t.Run("wiki running parent does not show page progress", func(t *testing.T) {
		input := processingKnowledgeStateInput{
			Knowledge: row,
			Attempt:   1,
			Spans: []types.KnowledgeProcessingSpan{
				pdSpanWithIO(1, "postprocess.wiki", types.SpanStatusRunning, now, nil, types.JSONMap{
					"pages_total":   13,
					"pages_written": 3,
				}),
			},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		}
		got := byStage(buildProcessingStages(input))[types.ProcessingStageWiki]
		if got.Item.Progress != nil {
			t.Fatalf("progress = %#v, want nil while wiki is running", got.Item.Progress)
		}
	})

	t.Run("degraded queue makes retrying unobservable", func(t *testing.T) {
		got := byStage(buildProcessingStages(processingKnowledgeStateInput{
			Knowledge:   row,
			Attempt:     1,
			QueueStatus: types.ProcessingQueueSnapshotDegraded,
			GeneratedAt: now,
		}))[types.ProcessingStageGraph]
		if got.RetryingObservable {
			t.Fatalf("retrying should be unobservable")
		}
	})
}

func byStage(items []processingStageComputation) map[types.ProcessingLogicalStage]processingStageComputation {
	out := map[types.ProcessingLogicalStage]processingStageComputation{}
	for _, item := range items {
		out[item.Item.Stage] = item
	}
	return out
}

func pdSpan(id int64, name, status string, t time.Time) types.KnowledgeProcessingSpan {
	return pdSpanWithIO(id, name, status, t, nil, nil)
}

func pdSpanErr(id int64, name, status, code string, t time.Time) types.KnowledgeProcessingSpan {
	s := pdSpan(id, name, status, t)
	s.ErrorCode = code
	return s
}

func pdSpanWithIO(id int64, name, status string, t time.Time, input, output types.JSONMap) types.KnowledgeProcessingSpan {
	started := t.Add(-time.Minute)
	return types.KnowledgeProcessingSpan{
		ID:          id,
		KnowledgeID: "kid",
		Attempt:     1,
		SpanID:      name,
		Name:        name,
		Status:      status,
		Input:       input,
		Output:      output,
		StartedAt:   &started,
		UpdatedAt:   t,
	}
}

func queueAgg(kid string, attempt int, stage types.ProcessingLogicalStage) *types.ProcessingQueueAggregate {
	return &types.ProcessingQueueAggregate{
		KnowledgeID: kid,
		Attempt:     attempt,
		Stage:       stage,
		Children:    map[string]*types.ProcessingQueueChildAggregate{},
	}
}

func BenchmarkProcessingDashboardStateLargeFanout(b *testing.B) {
	now := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	inputs := make([]processingKnowledgeStateInput, 0, 100)
	for k := 0; k < 100; k++ {
		kid := fmt.Sprintf("kid-%03d", k)
		spans := []types.KnowledgeProcessingSpan{
			spanWithKid(int64(k*2000+1), kid, types.StagePostProcess, types.SpanStatusDone, now, nil, types.JSONMap{
				"enqueued_graph_count":    1000,
				"enqueued_question_count": 50,
			}),
			spanWithKid(int64(k*2000+2), kid, types.StageMultimodal, types.SpanStatusDone, now, types.JSONMap{"image_count": 100}, nil),
		}
		inputs = append(inputs, processingKnowledgeStateInput{
			Knowledge: types.ProcessingKnowledgeRow{
				ID:              kid,
				KnowledgeBaseID: "kb",
				Title:           kid,
				ParseStatus:     types.ParseStatusFinalizing,
			},
			Attempt: 1,
			Spans:   spans,
			Fanout: map[types.ProcessingLogicalStage]*types.ProcessingFanoutStageAggregate{
				types.ProcessingStageGraph: {
					KnowledgeID:        kid,
					Attempt:            1,
					Stage:              types.ProcessingStageGraph,
					TerminalDoneCount:  1000,
					TerminalTotalCount: 1000,
					Details:            map[string]types.KnowledgeProcessingSpan{},
					CompletionReliable: true,
				},
				types.ProcessingStageMultimodal: {
					KnowledgeID:        kid,
					Attempt:            1,
					Stage:              types.ProcessingStageMultimodal,
					TerminalDoneCount:  100,
					TerminalTotalCount: 100,
					Details:            map[string]types.KnowledgeProcessingSpan{},
					CompletionReliable: true,
				},
				types.ProcessingStageQuestion: {
					KnowledgeID:        kid,
					Attempt:            1,
					Stage:              types.ProcessingStageQuestion,
					TerminalDoneCount:  50,
					TerminalTotalCount: 50,
					Details:            map[string]types.KnowledgeProcessingSpan{},
					CompletionReliable: true,
				},
			},
			QueueStatus: types.ProcessingQueueSnapshotOK,
			GeneratedAt: now,
		})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		total := 0
		for _, input := range inputs {
			total += len(buildProcessingStages(input))
		}
		if total != 900 {
			b.Fatalf("unexpected stage count %d", total)
		}
	}
}

func spanWithKid(id int64, kid, name, status string, t time.Time, input, output types.JSONMap) types.KnowledgeProcessingSpan {
	started := t.Add(-time.Minute)
	return types.KnowledgeProcessingSpan{
		ID:          id,
		KnowledgeID: kid,
		Attempt:     1,
		SpanID:      fmt.Sprintf("%s-%d", name, id),
		Name:        name,
		Status:      status,
		Input:       input,
		Output:      output,
		StartedAt:   &started,
		UpdatedAt:   t,
	}
}
