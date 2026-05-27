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
// substituting defaults for unset fields. Validation of value ranges
// (e.g. hnsw_m / ef_construction caps) is a service-layer concern handled
// elsewhere; this function applies defaults only and never rejects.
//
// OpenSearch-specific overrides (knn_engine, hnsw_m, hnsw_ef_construction,
// hnsw_ef_search) are intentionally NOT read from IndexConfig here:
// IndexConfig is a schema shared across all drivers, and adding OpenSearch-
// specific fields would surface them in the shared VectorStoreFieldInfo
// form visible to every driver's create UI. Wiring those fields through to
// IndexConfig is a follow-up that lands alongside the activation switch.
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
	return cfg, nil
}
