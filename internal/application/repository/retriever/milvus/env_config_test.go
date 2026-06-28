package milvus

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIndexConfigFromEnv covers the env-path construction of Milvus shard /
// replica settings (MILVUS_SHARDS_NUM / MILVUS_REPLICA_NUMBER). The env path
// historically passed nil into NewMilvusRetrieveEngineRepository, forcing
// Milvus server defaults (shard=1, replica=1). This function lets operators
// override them without going through the DB-store (vector_stores) path.
func TestIndexConfigFromEnv(t *testing.T) {
	t.Run("no env returns nil", func(t *testing.T) {
		t.Setenv("MILVUS_SHARDS_NUM", "")
		t.Setenv("MILVUS_REPLICA_NUMBER", "")
		assert.Nil(t, IndexConfigFromEnv())
	})

	t.Run("shards num set", func(t *testing.T) {
		t.Setenv("MILVUS_SHARDS_NUM", "4")
		t.Setenv("MILVUS_REPLICA_NUMBER", "")
		cfg := IndexConfigFromEnv()
		if assert.NotNil(t, cfg) {
			assert.Equal(t, 4, cfg.ShardsNum)
			assert.Equal(t, 0, cfg.ReplicaNumber)
		}
	})

	t.Run("replica number set", func(t *testing.T) {
		t.Setenv("MILVUS_SHARDS_NUM", "")
		t.Setenv("MILVUS_REPLICA_NUMBER", "3")
		cfg := IndexConfigFromEnv()
		if assert.NotNil(t, cfg) {
			assert.Equal(t, 0, cfg.ShardsNum)
			assert.Equal(t, 3, cfg.ReplicaNumber)
		}
	})

	t.Run("both set", func(t *testing.T) {
		t.Setenv("MILVUS_SHARDS_NUM", "2")
		t.Setenv("MILVUS_REPLICA_NUMBER", "2")
		cfg := IndexConfigFromEnv()
		if assert.NotNil(t, cfg) {
			assert.Equal(t, 2, cfg.ShardsNum)
			assert.Equal(t, 2, cfg.ReplicaNumber)
		}
	})

	t.Run("non-numeric is ignored", func(t *testing.T) {
		t.Setenv("MILVUS_SHARDS_NUM", "abc")
		t.Setenv("MILVUS_REPLICA_NUMBER", "")
		assert.Nil(t, IndexConfigFromEnv())
	})

	t.Run("zero is treated as unset", func(t *testing.T) {
		t.Setenv("MILVUS_SHARDS_NUM", "0")
		t.Setenv("MILVUS_REPLICA_NUMBER", "0")
		assert.Nil(t, IndexConfigFromEnv())
	})

	t.Run("shards above max is ignored", func(t *testing.T) {
		t.Setenv("MILVUS_SHARDS_NUM", "65") // maxShards = 64
		t.Setenv("MILVUS_REPLICA_NUMBER", "")
		assert.Nil(t, IndexConfigFromEnv())
	})

	t.Run("replica above max is ignored", func(t *testing.T) {
		t.Setenv("MILVUS_SHARDS_NUM", "")
		t.Setenv("MILVUS_REPLICA_NUMBER", "11") // maxReplicas = 10
		assert.Nil(t, IndexConfigFromEnv())
	})

	t.Run("valid shards survives invalid replica", func(t *testing.T) {
		t.Setenv("MILVUS_SHARDS_NUM", "4")
		t.Setenv("MILVUS_REPLICA_NUMBER", "abc")
		cfg := IndexConfigFromEnv()
		if assert.NotNil(t, cfg) {
			assert.Equal(t, 4, cfg.ShardsNum)
			assert.Equal(t, 0, cfg.ReplicaNumber)
		}
	})
}
