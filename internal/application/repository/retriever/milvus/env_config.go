package milvus

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	envMilvusShardsNum     = "MILVUS_SHARDS_NUM"
	envMilvusReplicaNumber = "MILVUS_REPLICA_NUMBER"

	// Bounds mirror the validation limits in types.IndexConfig
	// (maxShards / maxReplicas) so the env path and the DB-store path agree.
	maxShardsNum     = 64
	maxReplicaNumber = 10
)

// IndexConfigFromEnv builds a Milvus IndexConfig from MILVUS_SHARDS_NUM and
// MILVUS_REPLICA_NUMBER.
//
// It returns nil when neither variable carries a valid, in-range value, so the
// env path preserves its historical nil semantics — passing nil into
// NewMilvusRetrieveEngineRepository leaves shard/replica at the Milvus server
// defaults (both 1). Invalid or out-of-range values are logged and ignored
// rather than failing startup.
func IndexConfigFromEnv() *types.IndexConfig {
	cfg := &types.IndexConfig{}
	set := false

	if v := parseBoundedEnv(envMilvusShardsNum, maxShardsNum); v > 0 {
		cfg.ShardsNum = v
		set = true
	}
	if v := parseBoundedEnv(envMilvusReplicaNumber, maxReplicaNumber); v > 0 {
		cfg.ReplicaNumber = v
		set = true
	}

	if !set {
		return nil
	}
	return cfg
}

// parseBoundedEnv reads name, parses it as a positive integer no greater than
// max, and returns 0 (treated as "unset") for empty, non-numeric, or
// out-of-range values, logging a warning for the malformed cases.
func parseBoundedEnv(name string, max int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	log := logger.GetLogger(context.Background())
	v, err := strconv.Atoi(raw)
	if err != nil {
		log.Warnf("[Milvus] invalid %s=%q, ignoring (must be a positive integer)", name, raw)
		return 0
	}
	if v <= 0 || v > max {
		log.Warnf("[Milvus] %s=%d out of range (1-%d), ignoring", name, v, max)
		return 0
	}
	return v
}
