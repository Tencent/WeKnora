package types

import "errors"

// RerankOptions is the per-request rerank control shared by the retrieval
// APIs (knowledge-search and hybrid-search).
//
// On hybrid-search, an absent object means "no rerank" (the endpoint is a raw
// recall primitive). On knowledge-search, an absent object means "rerank with
// the tenant configuration", which is what the endpoint has always done.
type RerankOptions struct {
	// Enabled turns the rerank stage off when explicitly false. Nil means on.
	Enabled *bool `json:"enabled,omitempty"`
	// ModelID selects the rerank model. Empty falls back to the tenant
	// RetrievalConfig, then to the first rerank model of the tenant.
	ModelID string `json:"model_id,omitempty"`
	// TopK caps the reranked results. Zero uses the endpoint's result count.
	TopK int `json:"top_k,omitempty"`
	// Threshold is the minimum rerank model score. Nil uses the tenant
	// RetrievalConfig. A pointer because 0 and negative scores are valid.
	Threshold *float64 `json:"threshold,omitempty"`
}

// IsEnabled reports whether the options ask for a rerank stage. A nil
// receiver is not enabled; callers decide what an absent object means.
func (o *RerankOptions) IsEnabled() bool {
	return o != nil && (o.Enabled == nil || *o.Enabled)
}

// Validate rejects option values no request can mean. A nil receiver is valid.
func (o *RerankOptions) Validate() error {
	if o != nil && o.TopK < 0 {
		return errors.New("rerank.top_k must not be negative")
	}
	return nil
}

// RerankOutcome says what the rerank stage did with a request.
type RerankOutcome string

const (
	// RerankOutcomeOK means results passed the threshold and were reranked.
	RerankOutcomeOK RerankOutcome = "ok"
	// RerankOutcomeThresholdDegraded means nothing passed the requested
	// threshold, so a lower one was applied.
	RerankOutcomeThresholdDegraded RerankOutcome = "threshold_degraded"
	// RerankOutcomeFallbackTop1 means nothing passed any threshold and only the
	// best candidate was kept.
	RerankOutcomeFallbackTop1 RerankOutcome = "fallback_top1"
	// RerankOutcomeAllBelowThreshold means the model rejected every candidate
	// and the result is empty.
	RerankOutcomeAllBelowThreshold RerankOutcome = "all_below_threshold"
	// RerankOutcomeModelError means the rerank call failed and the retrieval
	// order was returned instead.
	RerankOutcomeModelError RerankOutcome = "model_error"
	// RerankOutcomeModelUnavailable means the configured model could not be
	// loaded and the retrieval order was returned instead.
	RerankOutcomeModelUnavailable RerankOutcome = "model_unavailable"
	// RerankOutcomeNoModel means rerank was wanted but the tenant has no rerank
	// model, so the retrieval order was returned.
	RerankOutcomeNoModel RerankOutcome = "no_model"
	// RerankOutcomeDisabled means the request turned rerank off.
	RerankOutcomeDisabled RerankOutcome = "disabled"
	// RerankOutcomeNoCandidates means retrieval returned nothing to rerank.
	RerankOutcomeNoCandidates RerankOutcome = "no_candidates"
)

// Rerank model ID sources reported in RerankDiagnostics.ModelSource.
const (
	RerankModelSourceRequest = "request"
	RerankModelSourceTenant  = "tenant"
	RerankModelSourceAuto    = "auto"
)

// RerankDiagnostics reports what the rerank stage did, so an empty or
// unexpectedly ordered result can be explained to API callers.
type RerankDiagnostics struct {
	// Applied is true when rerank scores decided the returned order.
	Applied bool          `json:"applied"`
	Outcome RerankOutcome `json:"outcome"`
	// ModelID and ModelSource (request / tenant / auto) identify the model.
	ModelID     string `json:"model_id,omitempty"`
	ModelSource string `json:"model_source,omitempty"`
	// Threshold is the requested minimum score; EffectiveThreshold is the one
	// applied after degradation.
	Threshold          float64 `json:"threshold"`
	EffectiveThreshold float64 `json:"effective_threshold"`
	// TopScore is the best rerank model score among the candidates.
	TopScore       float64 `json:"top_score"`
	CandidateCount int     `json:"candidate_count"`
	ResultCount    int     `json:"result_count"`
	// Error carries the reason for model_error / model_unavailable.
	Error string `json:"error,omitempty"`
}

// RetrievalMeta is the diagnostic envelope returned beside retrieval API
// results.
type RetrievalMeta struct {
	Rerank *RerankDiagnostics `json:"rerank,omitempty"`
}

// RetrievalResult is a retrieval API result with its diagnostics.
type RetrievalResult struct {
	Results []*SearchResult
	Meta    RetrievalMeta
}

// KnowledgeSearchOptions are the caller overrides accepted by the
// knowledge-search API. Zero values keep the tenant RetrievalConfig.
type KnowledgeSearchOptions struct {
	// VectorThreshold and KeywordThreshold override the recall thresholds.
	VectorThreshold  *float64
	KeywordThreshold *float64
	// MatchCount is the number of results to return.
	MatchCount           int
	DisableKeywordsMatch bool
	DisableVectorMatch   bool
	// Rerank overrides the rerank stage. Nil reranks with the tenant config.
	Rerank *RerankOptions
}
