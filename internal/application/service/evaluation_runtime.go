package service

import (
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type evaluationRuntimeCollector struct {
	mu sync.Mutex

	startedAt   time.Time
	total       int
	started     int
	success     int
	failed      int
	canceled    int
	interrupted int

	datasetLoad     time.Duration
	indexing        time.Duration
	execution       time.Duration
	persistence     time.Duration
	cleanup         time.Duration
	datasetLoadSeen bool
	indexingSeen    bool
	executionSeen   bool
	persistenceSeen bool
	cleanupSeen     bool

	promptTokens      int
	completionTokens  int
	totalTokens       int
	reportedSamples   int
	unreportedSamples int
}

func newEvaluationRuntimeCollector(startedAt time.Time) *evaluationRuntimeCollector {
	return &evaluationRuntimeCollector{startedAt: startedAt.UTC()}
}

func (c *evaluationRuntimeCollector) setTotal(total int) {
	c.mu.Lock()
	c.total = total
	c.mu.Unlock()
}

func (c *evaluationRuntimeCollector) addDatasetLoad(d time.Duration) {
	c.addDuration(&c.datasetLoad, &c.datasetLoadSeen, d)
}

func (c *evaluationRuntimeCollector) addIndexing(d time.Duration) {
	c.addDuration(&c.indexing, &c.indexingSeen, d)
}

func (c *evaluationRuntimeCollector) addExecution(d time.Duration) {
	c.addDuration(&c.execution, &c.executionSeen, d)
}

func (c *evaluationRuntimeCollector) addPersistence(d time.Duration) {
	c.addDuration(&c.persistence, &c.persistenceSeen, d)
}

func (c *evaluationRuntimeCollector) addCleanup(d time.Duration) {
	c.addDuration(&c.cleanup, &c.cleanupSeen, d)
}

func (c *evaluationRuntimeCollector) addDuration(target *time.Duration, seen *bool, d time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	*target += d
	*seen = true
	c.mu.Unlock()
}

func (c *evaluationRuntimeCollector) sampleStarted() {
	c.mu.Lock()
	c.started++
	c.mu.Unlock()
}

func (c *evaluationRuntimeCollector) sampleSucceeded(prompt, completion, total int, reported bool) {
	c.mu.Lock()
	c.success++
	if reported {
		c.promptTokens += prompt
		c.completionTokens += completion
		c.totalTokens += total
		c.reportedSamples++
	} else {
		c.unreportedSamples++
	}
	c.mu.Unlock()
}

func (c *evaluationRuntimeCollector) sampleFailed(canceled bool) {
	c.mu.Lock()
	if canceled {
		c.canceled++
	} else {
		c.failed++
	}
	c.mu.Unlock()
}

func (c *evaluationRuntimeCollector) recordSampleTokens(prompt, completion, total int, reported bool) {
	c.mu.Lock()
	if reported {
		c.promptTokens += prompt
		c.completionTokens += completion
		c.totalTokens += total
		c.reportedSamples++
	} else {
		c.unreportedSamples++
	}
	c.mu.Unlock()
}

func (c *evaluationRuntimeCollector) snapshot(endedAt time.Time) *types.EvaluationRuntimeMetrics {
	return c.buildSnapshot(endedAt.UTC(), true)
}

func (c *evaluationRuntimeCollector) currentSnapshot(now time.Time) *types.EvaluationRuntimeMetrics {
	return c.buildSnapshot(now.UTC(), false)
}

func (c *evaluationRuntimeCollector) buildSnapshot(now time.Time, terminal bool) *types.EvaluationRuntimeMetrics {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	datasetLoadMs := evaluationDurationMilliseconds(c.datasetLoad, c.datasetLoadSeen)
	indexingMs := evaluationDurationMilliseconds(c.indexing, c.indexingSeen)
	executionMs := evaluationDurationMilliseconds(c.execution, c.executionSeen)
	persistenceMs := evaluationDurationMilliseconds(c.persistence, c.persistenceSeen)
	cleanupMs := evaluationDurationMilliseconds(c.cleanup, c.cleanupSeen)
	totalMs := now.Sub(c.startedAt).Milliseconds()
	if totalMs < 0 {
		totalMs = 0
	}
	var endedAt *time.Time
	if terminal {
		value := now
		endedAt = &value
	}
	completed := c.success + c.failed + c.canceled + c.interrupted
	notStarted := c.total - c.started
	if notStarted < 0 {
		notStarted = 0
	}
	return &types.EvaluationRuntimeMetrics{
		SchemaVersion: 1,
		StartedAt:     c.startedAt,
		EndedAt:       endedAt,
		Durations: types.EvaluationRuntimeDurations{
			DatasetLoadMs: datasetLoadMs,
			IndexingMs:    indexingMs,
			ExecutionMs:   executionMs,
			PersistenceMs: persistenceMs,
			CleanupMs:     cleanupMs,
			TotalMs:       &totalMs,
		},
		Samples: types.EvaluationRuntimeSamples{
			Total: c.total, Started: c.started, Success: c.success, Failed: c.failed,
			Canceled: c.canceled, Interrupted: c.interrupted, NotStarted: notStarted,
		},
		Failure: types.EvaluationRuntimeFailure{
			Numerator: c.failed + c.canceled + c.interrupted, Denominator: completed,
		},
		Tokens: types.EvaluationRuntimeTokens{
			PromptTokens: c.promptTokens, CompletionTokens: c.completionTokens, TotalTokens: c.totalTokens,
			ReportedSamples: c.reportedSamples, UnreportedSamples: c.unreportedSamples,
		},
	}
}

func evaluationDurationMilliseconds(duration time.Duration, seen bool) *int64 {
	if !seen {
		return nil
	}
	value := duration.Milliseconds()
	return &value
}

func evaluationQuestionUsage(response *types.ChatResponse) (prompt, completion, total int, reported bool) {
	if response == nil {
		return 0, 0, 0, false
	}
	usage := response.Usage
	reported = usage.UsageReported || usage.PromptTokens > 0 || usage.CompletionTokens > 0 || usage.TotalTokens > 0
	return usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens, reported
}

func applyEvaluationQuestionRuntime(
	input *types.EvaluationQuestionResultInput,
	timings *types.EvaluationPipelineTimings,
	total time.Duration,
	usageReported bool,
) {
	if input == nil {
		return
	}
	input.RetrievalMs, input.RerankMs, input.GenerationMs = timings.Milliseconds()
	totalMs := total.Milliseconds()
	input.TotalMs = &totalMs
	input.UsageReported = usageReported
	if !usageReported {
		input.PromptTokens = nil
		input.CompletionTokens = nil
		input.TotalTokens = nil
	}
}

func finalizeRecoveredEvaluationRuntime(
	raw types.JSON,
	startedAt time.Time,
	endedAt time.Time,
	canceled bool,
) (types.JSON, error) {
	runtimeMetrics, err := decodeEvaluationRuntimeMetrics(raw)
	if err != nil {
		return nil, err
	}
	if runtimeMetrics == nil {
		runtimeMetrics = &types.EvaluationRuntimeMetrics{
			SchemaVersion: 1,
			StartedAt:     startedAt.UTC(),
		}
	}
	endedAt = endedAt.UTC()
	runtimeMetrics.EndedAt = &endedAt
	totalMs := endedAt.Sub(runtimeMetrics.StartedAt).Milliseconds()
	if totalMs < 0 {
		totalMs = 0
	}
	runtimeMetrics.Durations.TotalMs = &totalMs
	resolved := runtimeMetrics.Samples.Success + runtimeMetrics.Samples.Failed +
		runtimeMetrics.Samples.Canceled + runtimeMetrics.Samples.Interrupted
	unresolved := runtimeMetrics.Samples.Started - resolved
	if unresolved < 0 {
		unresolved = 0
	}
	if canceled {
		runtimeMetrics.Samples.Canceled += unresolved
	} else {
		runtimeMetrics.Samples.Interrupted += unresolved
	}
	runtimeMetrics.Samples.NotStarted = runtimeMetrics.Samples.Total - runtimeMetrics.Samples.Started
	if runtimeMetrics.Samples.NotStarted < 0 {
		runtimeMetrics.Samples.NotStarted = 0
	}
	runtimeMetrics.Failure.Numerator = runtimeMetrics.Samples.Failed + runtimeMetrics.Samples.Canceled +
		runtimeMetrics.Samples.Interrupted
	runtimeMetrics.Failure.Denominator = runtimeMetrics.Samples.Success + runtimeMetrics.Failure.Numerator
	return encodeEvaluationRuntimeMetrics(runtimeMetrics)
}
