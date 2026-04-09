package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// testAESKey is a 32-byte key for testing AES-GCM encryption.
const testAESKey = "01234567890123456789012345678901"

// ---------------------------------------------------------------------------
// VectorStore
// ---------------------------------------------------------------------------

func TestVectorStore_Validate(t *testing.T) {
	valid := VectorStore{
		Name:       "test-store",
		EngineType: PostgresRetrieverEngineType,
		TenantID:   1,
	}

	t.Run("valid input returns nil", func(t *testing.T) {
		assert.NoError(t, valid.Validate())
	})

	t.Run("empty name returns error", func(t *testing.T) {
		s := valid
		s.Name = ""
		err := s.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "name is required")
	})

	t.Run("unsupported engine type returns error", func(t *testing.T) {
		s := valid
		s.EngineType = "unknown"
		err := s.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported engine type")
	})

	t.Run("zero tenant_id returns error", func(t *testing.T) {
		s := valid
		s.TenantID = 0
		err := s.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tenant_id is required")
	})
}

func TestVectorStore_BeforeCreate(t *testing.T) {
	t.Run("generates UUID when ID is empty", func(t *testing.T) {
		v := &VectorStore{}
		err := v.BeforeCreate(&gorm.DB{})
		require.NoError(t, err)
		assert.NotEmpty(t, v.ID)
		assert.Len(t, v.ID, 36) // UUID format: 8-4-4-4-12
	})

	t.Run("preserves existing ID", func(t *testing.T) {
		v := &VectorStore{ID: "existing-id"}
		err := v.BeforeCreate(&gorm.DB{})
		require.NoError(t, err)
		assert.Equal(t, "existing-id", v.ID)
	})
}

func TestVectorStore_TableName(t *testing.T) {
	assert.Equal(t, "vector_stores", VectorStore{}.TableName())
}

func TestIsValidEngineType(t *testing.T) {
	validTypes := []RetrieverEngineType{
		PostgresRetrieverEngineType,
		ElasticsearchRetrieverEngineType,
		QdrantRetrieverEngineType,
		MilvusRetrieverEngineType,
		WeaviateRetrieverEngineType,
		SQLiteRetrieverEngineType,
	}
	for _, et := range validTypes {
		t.Run("valid: "+string(et), func(t *testing.T) {
			assert.True(t, IsValidEngineType(et))
		})
	}

	invalidTypes := []RetrieverEngineType{
		"unknown",
		"opensearch",
		"",
		InfinityRetrieverEngineType,
		ElasticFaissRetrieverEngineType,
	}
	for _, et := range invalidTypes {
		name := string(et)
		if name == "" {
			name = "(empty)"
		}
		t.Run("invalid: "+name, func(t *testing.T) {
			assert.False(t, IsValidEngineType(et))
		})
	}
}

// ---------------------------------------------------------------------------
// ConnectionConfig
// ---------------------------------------------------------------------------

func TestConnectionConfig_ValueScan(t *testing.T) {
	t.Run("encrypts password and api_key on Value, decrypts on Scan", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", testAESKey)

		original := ConnectionConfig{
			Addr:     "http://es:9200",
			Username: "elastic",
			Password: "secret-pass",
			APIKey:   "sk-api-key",
		}

		// Value — encrypt
		raw, err := original.Value()
		require.NoError(t, err)

		// Verify the serialized JSON has encrypted fields
		var intermediate map[string]interface{}
		require.NoError(t, json.Unmarshal(raw.([]byte), &intermediate))
		assert.True(t, strings.HasPrefix(intermediate["password"].(string), "enc:v1:"))
		assert.True(t, strings.HasPrefix(intermediate["api_key"].(string), "enc:v1:"))
		// Non-sensitive fields remain plaintext
		assert.Equal(t, "http://es:9200", intermediate["addr"])
		assert.Equal(t, "elastic", intermediate["username"])

		// Scan — decrypt
		var scanned ConnectionConfig
		err = scanned.Scan(raw.([]byte))
		require.NoError(t, err)
		assert.Equal(t, "secret-pass", scanned.Password)
		assert.Equal(t, "sk-api-key", scanned.APIKey)
		assert.Equal(t, "http://es:9200", scanned.Addr)
		assert.Equal(t, "elastic", scanned.Username)
	})

	t.Run("skips encryption when fields are empty", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", testAESKey)

		original := ConnectionConfig{Addr: "http://es:9200"}
		raw, err := original.Value()
		require.NoError(t, err)

		var intermediate map[string]interface{}
		require.NoError(t, json.Unmarshal(raw.([]byte), &intermediate))
		_, hasPassword := intermediate["password"]
		_, hasAPIKey := intermediate["api_key"]
		assert.False(t, hasPassword)
		assert.False(t, hasAPIKey)
	})

	t.Run("skips encryption when AES key is not set", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", "")

		original := ConnectionConfig{
			Password: "secret-pass",
			APIKey:   "sk-api-key",
		}
		raw, err := original.Value()
		require.NoError(t, err)

		var intermediate map[string]interface{}
		require.NoError(t, json.Unmarshal(raw.([]byte), &intermediate))
		assert.Equal(t, "secret-pass", intermediate["password"])
		assert.Equal(t, "sk-api-key", intermediate["api_key"])
	})

	t.Run("does not double-encrypt already encrypted values", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", testAESKey)

		original := ConnectionConfig{Password: "secret-pass"}
		raw1, err := original.Value()
		require.NoError(t, err)

		// Scan to get the encrypted form, then re-serialize
		var scanned ConnectionConfig
		require.NoError(t, json.Unmarshal(raw1.([]byte), &scanned))
		// scanned.Password is now "enc:v1:..."
		raw2, err := scanned.Value()
		require.NoError(t, err)

		// Both serialized forms should produce the same decrypted result
		var result ConnectionConfig
		require.NoError(t, result.Scan(raw2.([]byte)))
		assert.Equal(t, "secret-pass", result.Password)
	})

	t.Run("Scan nil value returns no error", func(t *testing.T) {
		var c ConnectionConfig
		assert.NoError(t, c.Scan(nil))
	})

	t.Run("Scan non-byte value returns no error", func(t *testing.T) {
		var c ConnectionConfig
		assert.NoError(t, c.Scan(42))
	})

	t.Run("original struct is not mutated by Value", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", testAESKey)

		original := ConnectionConfig{Password: "secret-pass"}
		_, err := original.Value()
		require.NoError(t, err)
		assert.Equal(t, "secret-pass", original.Password, "value receiver should not mutate original")
	})
}

func TestConnectionConfig_GetEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		config   ConnectionConfig
		expected string
	}{
		{
			name:     "returns Addr when set",
			config:   ConnectionConfig{Addr: "http://es:9200"},
			expected: "http://es:9200",
		},
		{
			name:     "returns host:port when Host and Port set",
			config:   ConnectionConfig{Host: "qdrant-prod", Port: 6334},
			expected: "qdrant-prod:6334",
		},
		{
			name:     "defaults Port to 6334 when Host set and Port is 0",
			config:   ConnectionConfig{Host: "qdrant-prod"},
			expected: "qdrant-prod:6334",
		},
		{
			name:     "returns sentinel for default postgres connection",
			config:   ConnectionConfig{UseDefaultConnection: true},
			expected: "__default_postgres__",
		},
		{
			name:     "returns empty string when nothing is set",
			config:   ConnectionConfig{},
			expected: "",
		},
		{
			name:     "Addr takes precedence over Host",
			config:   ConnectionConfig{Addr: "http://es:9200", Host: "qdrant"},
			expected: "http://es:9200",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.GetEndpoint())
		})
	}
}

func TestConnectionConfig_MaskSensitiveFields(t *testing.T) {
	t.Run("masks password and api_key", func(t *testing.T) {
		c := ConnectionConfig{
			Addr:     "http://es:9200",
			Username: "elastic",
			Password: "secret-pass",
			APIKey:   "sk-api-key",
		}
		masked := c.MaskSensitiveFields()
		assert.Equal(t, "***", masked.Password)
		assert.Equal(t, "***", masked.APIKey)
		assert.Equal(t, "http://es:9200", masked.Addr)
		assert.Equal(t, "elastic", masked.Username)
	})

	t.Run("does not mask empty fields", func(t *testing.T) {
		c := ConnectionConfig{Addr: "http://es:9200"}
		masked := c.MaskSensitiveFields()
		assert.Empty(t, masked.Password)
		assert.Empty(t, masked.APIKey)
	})

	t.Run("does not mutate original", func(t *testing.T) {
		c := ConnectionConfig{Password: "secret-pass", APIKey: "sk-api-key"}
		_ = c.MaskSensitiveFields()
		assert.Equal(t, "secret-pass", c.Password)
		assert.Equal(t, "sk-api-key", c.APIKey)
	})
}

// ---------------------------------------------------------------------------
// IndexConfig
// ---------------------------------------------------------------------------

func TestIndexConfig_ValueScan(t *testing.T) {
	t.Run("round-trip serialization", func(t *testing.T) {
		original := IndexConfig{
			IndexName:        "my_index",
			NumberOfShards:   3,
			NumberOfReplicas: 1,
		}
		raw, err := original.Value()
		require.NoError(t, err)

		var scanned IndexConfig
		require.NoError(t, scanned.Scan(raw.([]byte)))
		assert.Equal(t, original, scanned)
	})

	t.Run("empty config serializes to {}", func(t *testing.T) {
		raw, err := IndexConfig{}.Value()
		require.NoError(t, err)
		assert.JSONEq(t, `{}`, string(raw.([]byte)))
	})

	t.Run("Scan nil value returns no error", func(t *testing.T) {
		var c IndexConfig
		assert.NoError(t, c.Scan(nil))
	})

	t.Run("Scan non-byte value returns no error", func(t *testing.T) {
		var c IndexConfig
		assert.NoError(t, c.Scan(42))
	})
}

func TestIndexConfig_GetIndexNameOrDefault(t *testing.T) {
	tests := []struct {
		name       string
		config     IndexConfig
		engineType RetrieverEngineType
		expected   string
	}{
		// Elasticsearch
		{
			name:       "elasticsearch with custom index",
			config:     IndexConfig{IndexName: "custom_index"},
			engineType: ElasticsearchRetrieverEngineType,
			expected:   "custom_index",
		},
		{
			name:       "elasticsearch default",
			config:     IndexConfig{},
			engineType: ElasticsearchRetrieverEngineType,
			expected:   "xwrag_default",
		},
		// Qdrant
		{
			name:       "qdrant with custom collection prefix",
			config:     IndexConfig{CollectionPrefix: "custom_embeddings"},
			engineType: QdrantRetrieverEngineType,
			expected:   "custom_embeddings",
		},
		{
			name:       "qdrant default",
			config:     IndexConfig{},
			engineType: QdrantRetrieverEngineType,
			expected:   "weknora_embeddings",
		},
		// Milvus
		{
			name:       "milvus with custom collection name",
			config:     IndexConfig{CollectionName: "custom_collection"},
			engineType: MilvusRetrieverEngineType,
			expected:   "custom_collection",
		},
		{
			name:       "milvus default",
			config:     IndexConfig{},
			engineType: MilvusRetrieverEngineType,
			expected:   "weknora_embeddings",
		},
		// Weaviate
		{
			name:       "weaviate with custom prefix",
			config:     IndexConfig{CollectionPrefix: "Custom"},
			engineType: WeaviateRetrieverEngineType,
			expected:   "Custom",
		},
		{
			name:       "weaviate default",
			config:     IndexConfig{},
			engineType: WeaviateRetrieverEngineType,
			expected:   "WeKnora",
		},
		// Postgres (no index config)
		{
			name:       "postgres returns empty (no index config)",
			config:     IndexConfig{},
			engineType: PostgresRetrieverEngineType,
			expected:   "",
		},
		// SQLite (no index config)
		{
			name:       "sqlite returns empty (no index config)",
			config:     IndexConfig{},
			engineType: SQLiteRetrieverEngineType,
			expected:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.config.GetIndexNameOrDefault(tt.engineType))
		})
	}
}
