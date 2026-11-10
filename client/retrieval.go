package client

// RerankOptions controls the rerank stage of the knowledge-search and
// hybrid-search endpoints.
//
// On hybrid-search, a nil *RerankOptions means no rerank. On knowledge-search,
// nil reranks with the tenant retrieval config; set Enabled to false to turn
// rerank off.
type RerankOptions struct {
	// Enabled turns rerank off when explicitly false. Nil means on.
	Enabled *bool `json:"enabled,omitempty"`
	// ModelID selects the rerank model. Empty uses the tenant retrieval
	// config, then the tenant's first rerank model.
	ModelID string `json:"model_id,omitempty"`
	// TopK caps the reranked results. Zero uses the endpoint's result count.
	TopK int `json:"top_k,omitempty"`
	// Threshold is the minimum rerank model score. Nil uses the tenant
	// retrieval config.
	Threshold *float64 `json:"threshold,omitempty"`
}

// RerankDiagnostics reports what the rerank stage did with a request.
type RerankDiagnostics struct {
	// Applied is true when rerank scores decided the returned order.
	Applied bool `json:"applied"`
	// Outcome is one of ok, threshold_degraded, fallback_top1,
	// all_below_threshold, model_error, model_unavailable, no_model,
	// disabled, no_candidates.
	Outcome string `json:"outcome"`
	// ModelSource is request, tenant or auto.
	ModelID            string  `json:"model_id,omitempty"`
	ModelSource        string  `json:"model_source,omitempty"`
	Threshold          float64 `json:"threshold"`
	EffectiveThreshold float64 `json:"effective_threshold"`
	TopScore           float64 `json:"top_score"`
	CandidateCount     int     `json:"candidate_count"`
	ResultCount        int     `json:"result_count"`
	Error              string  `json:"error,omitempty"`
}

// RetrievalMeta carries the diagnostics returned beside retrieval results.
type RetrievalMeta struct {
	Rerank *RerankDiagnostics `json:"rerank,omitempty"`
}
