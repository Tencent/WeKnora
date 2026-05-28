package opensearch

import "github.com/Tencent/WeKnora/internal/types"

// internalCfg is the driver-internal, immutable view of IndexConfig.
// Defaults are chosen so the env-path (no IndexConfig) and DB-store path
// (with IndexConfig) produce identical mappings.
type internalCfg struct {
	shards             int
	replicas           int
	knnEngine          string // "lucene" | "faiss"
	hnswM              int
	hnswEFConstruction int
	efSearch           int
}

// buildInternalCfg projects IndexConfig to the driver-internal view,
// substituting defaults for unset (zero / empty) fields. Validation of value
// ranges (e.g. hnsw_m / ef_construction caps) is a service-layer concern
// handled elsewhere (validateOpenSearchIndexConfig at CreateStore); this
// function applies defaults only and never rejects. The env-path bypasses
// service validation entirely, so the defaults below are its safety net.
//
// The OpenSearch-specific HNSW fields (knn_engine, hnsw_m,
// hnsw_ef_construction, hnsw_ef_search) are read here. They are omitempty on
// IndexConfig, so they do not affect other drivers' serialized config.
func buildInternalCfg(c *types.IndexConfig) (internalCfg, error) {
	cfg := internalCfg{
		shards:             4,        // matches the keyword-index default upstream
		replicas:           1,        // assumes >= 2-node cluster
		knnEngine:          "lucene", // OS default; Faiss preferred only at >= 10M docs
		hnswM:              16,       // OS official default
		hnswEFConstruction: 100,      // OS official default
		efSearch:           100,      // OS default
	}
	if c == nil {
		return cfg, nil
	}
	if c.NumberOfShards > 0 {
		cfg.shards = c.NumberOfShards
	}
	if c.NumberOfReplicas > 0 {
		cfg.replicas = c.NumberOfReplicas
	}
	if c.KNNEngine != "" {
		cfg.knnEngine = c.KNNEngine
	}
	if c.HNSWM > 0 {
		cfg.hnswM = c.HNSWM
	}
	if c.HNSWEFConstruction > 0 {
		cfg.hnswEFConstruction = c.HNSWEFConstruction
	}
	if c.HNSWEFSearch > 0 {
		cfg.efSearch = c.HNSWEFSearch
	}
	return cfg, nil
}
