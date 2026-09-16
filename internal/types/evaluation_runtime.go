package types

import "time"

// EvaluationRuntimeMetrics is the schema-version-1 task observability snapshot.
type EvaluationRuntimeMetrics struct {
	SchemaVersion int                        `json:"schema_version"`
	StartedAt     time.Time                  `json:"started_at"`
	EndedAt       *time.Time                 `json:"ended_at,omitempty"`
	Durations     EvaluationRuntimeDurations `json:"durations"`
	Samples       EvaluationRuntimeSamples   `json:"samples"`
	Failure       EvaluationRuntimeFailure   `json:"failure"`
	Tokens        EvaluationRuntimeTokens    `json:"tokens"`
	Cost          *EvaluationRuntimeCost     `json:"cost,omitempty"`
}

// EvaluationRuntimeCost summarizes the model-call ledger for one evaluation task.
type EvaluationRuntimeCost struct {
	CallCount               int64            `json:"call_count"`
	AccountingCompleteCalls int64            `json:"accounting_complete_calls"`
	UnpricedCalls           int64            `json:"unpriced_calls"`
	UsageUnreportedCalls    int64            `json:"usage_unreported_calls"`
	StartedCalls            int64            `json:"started_calls"`
	Totals                  []ModelCostTotal `json:"totals"`
}

// EvaluationRuntimeDurations contains monotonic elapsed durations in milliseconds.
type EvaluationRuntimeDurations struct {
	DatasetLoadMs *int64 `json:"dataset_load_ms,omitempty"`
	IndexingMs    *int64 `json:"indexing_ms,omitempty"`
	ExecutionMs   *int64 `json:"execution_ms,omitempty"`
	PersistenceMs *int64 `json:"persistence_ms,omitempty"`
	CleanupMs     *int64 `json:"cleanup_ms,omitempty"`
	TotalMs       *int64 `json:"total_ms,omitempty"`
}

// EvaluationRuntimeSamples contains mutually interpretable sample counters.
type EvaluationRuntimeSamples struct {
	Total       int `json:"total"`
	Started     int `json:"started"`
	Success     int `json:"success"`
	Failed      int `json:"failed"`
	Canceled    int `json:"canceled"`
	Interrupted int `json:"interrupted"`
	NotStarted  int `json:"not_started"`
}

// EvaluationRuntimeFailure exposes an explicit failure-rate fraction.
type EvaluationRuntimeFailure struct {
	Numerator   int `json:"numerator"`
	Denominator int `json:"denominator"`
}

// EvaluationRuntimeTokens aggregates provider-reported generation usage.
type EvaluationRuntimeTokens struct {
	PromptTokens      int `json:"prompt_tokens"`
	CompletionTokens  int `json:"completion_tokens"`
	TotalTokens       int `json:"total_tokens"`
	ReportedSamples   int `json:"reported_samples"`
	UnreportedSamples int `json:"unreported_samples"`
}

// EvaluationPipelineTimings collects one sample's RAG stage durations.
// Fields use time.Duration internally and are serialized only through the
// per-question millisecond columns.
type EvaluationPipelineTimings struct {
	Retrieval      time.Duration `json:"-"`
	Rerank         time.Duration `json:"-"`
	Generation     time.Duration `json:"-"`
	retrievalSeen  bool
	rerankSeen     bool
	generationSeen bool
}

// AddStage records one completed pipeline event in its evaluation stage.
func (t *EvaluationPipelineTimings) AddStage(event EventType, duration time.Duration) {
	if t == nil {
		return
	}
	switch event {
	case CHUNK_SEARCH, CHUNK_SEARCH_PARALLEL, ENTITY_SEARCH, WEB_FETCH, CHUNK_MERGE, FILTER_TOP_K:
		t.Retrieval += duration
		t.retrievalSeen = true
	case CHUNK_RERANK:
		t.Rerank += duration
		t.rerankSeen = true
	case CHAT_COMPLETION, CHAT_COMPLETION_STREAM:
		t.Generation += duration
		t.generationSeen = true
	}
}

// Milliseconds returns non-null stage facts for the configured evaluator pipeline.
func (t *EvaluationPipelineTimings) Milliseconds() (retrieval, rerank, generation *int64) {
	if t == nil {
		return nil, nil, nil
	}
	if t.retrievalSeen {
		value := t.Retrieval.Milliseconds()
		retrieval = &value
	}
	if t.rerankSeen {
		value := t.Rerank.Milliseconds()
		rerank = &value
	}
	if t.generationSeen {
		value := t.Generation.Milliseconds()
		generation = &value
	}
	return retrieval, rerank, generation
}
