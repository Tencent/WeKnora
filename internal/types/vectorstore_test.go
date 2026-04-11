package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// PR2 additions: env store builder, response DTO, types metadata
// ---------------------------------------------------------------------------

// mockEnvLookup creates a simple env lookup function from a map.
func mockEnvLookup(env map[string]string) EnvLookupFunc {
	return func(key string) string {
		return env[key]
	}
}

func TestIsEnvStoreID(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		expected bool
	}{
		{"env postgres ID", "__env_postgres__", true},
		{"env elasticsearch ID", "__env_elasticsearch_v8__", true},
		{"env prefix only", "__env_", true},
		{"UUID ID", "550e8400-e29b-41d4-a716-446655440000", false},
		{"empty string", "", false},
		{"similar but not prefix", "_env_postgres__", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsEnvStoreID(tt.id))
		})
	}
}

func TestBuildEnvVectorStores(t *testing.T) {
	envMap := map[string]string{
		"ELASTICSEARCH_ADDR":     "http://es:9200",
		"ELASTICSEARCH_USERNAME": "elastic",
		"ELASTICSEARCH_PASSWORD": "secret",
		"ELASTICSEARCH_INDEX":    "my_index",
		"QDRANT_HOST":            "qdrant-host",
		"QDRANT_API_KEY":         "qd-key",
		"MILVUS_ADDRESS":         "milvus:19530",
		"WEAVIATE_HOST":          "weaviate:8080",
	}
	lookup := mockEnvLookup(envMap)

	t.Run("empty RETRIEVE_DRIVER returns nil", func(t *testing.T) {
		stores := BuildEnvVectorStores("", lookup)
		assert.Nil(t, stores)
	})

	t.Run("single driver postgres", func(t *testing.T) {
		stores := BuildEnvVectorStores("postgres", lookup)
		require.Len(t, stores, 1)
		assert.Equal(t, "__env_postgres__", stores[0].ID)
		assert.Equal(t, "postgres (env)", stores[0].Name)
		assert.Equal(t, PostgresRetrieverEngineType, stores[0].EngineType)
		assert.True(t, stores[0].ConnectionConfig.UseDefaultConnection)
	})

	t.Run("multiple drivers", func(t *testing.T) {
		stores := BuildEnvVectorStores("postgres,elasticsearch_v8", lookup)
		require.Len(t, stores, 2)
		assert.Equal(t, "__env_postgres__", stores[0].ID)
		assert.Equal(t, "__env_elasticsearch_v8__", stores[1].ID)
		assert.Equal(t, "http://es:9200", stores[1].ConnectionConfig.Addr)
		assert.Equal(t, "elastic", stores[1].ConnectionConfig.Username)
		assert.Equal(t, "secret", stores[1].ConnectionConfig.Password) // unmasked
		assert.Equal(t, "my_index", stores[1].IndexConfig.IndexName)
	})

	t.Run("env store retains raw password (not masked)", func(t *testing.T) {
		stores := BuildEnvVectorStores("elasticsearch_v8", lookup)
		require.Len(t, stores, 1)
		assert.Equal(t, "secret", stores[0].ConnectionConfig.Password)
	})

	t.Run("unknown driver is skipped", func(t *testing.T) {
		stores := BuildEnvVectorStores("postgres,unknown_db", lookup)
		require.Len(t, stores, 1)
		assert.Equal(t, "__env_postgres__", stores[0].ID)
	})

	t.Run("whitespace trimmed", func(t *testing.T) {
		stores := BuildEnvVectorStores(" postgres , elasticsearch_v8 ", lookup)
		require.Len(t, stores, 2)
	})

	t.Run("all supported drivers", func(t *testing.T) {
		stores := BuildEnvVectorStores("postgres,sqlite,elasticsearch_v8,elasticsearch_v7,qdrant,milvus,weaviate", lookup)
		require.Len(t, stores, 7)

		ids := make([]string, len(stores))
		for i, s := range stores {
			ids[i] = s.ID
		}
		assert.Contains(t, ids, "__env_postgres__")
		assert.Contains(t, ids, "__env_sqlite__")
		assert.Contains(t, ids, "__env_elasticsearch_v8__")
		assert.Contains(t, ids, "__env_elasticsearch_v7__")
		assert.Contains(t, ids, "__env_qdrant__")
		assert.Contains(t, ids, "__env_milvus__")
		assert.Contains(t, ids, "__env_weaviate__")
	})

	t.Run("qdrant env store", func(t *testing.T) {
		stores := BuildEnvVectorStores("qdrant", lookup)
		require.Len(t, stores, 1)
		assert.Equal(t, "qdrant-host", stores[0].ConnectionConfig.Host)
		assert.Equal(t, "qd-key", stores[0].ConnectionConfig.APIKey)
	})

	t.Run("milvus env store", func(t *testing.T) {
		stores := BuildEnvVectorStores("milvus", lookup)
		require.Len(t, stores, 1)
		assert.Equal(t, "milvus:19530", stores[0].ConnectionConfig.Addr)
	})

	t.Run("weaviate env store", func(t *testing.T) {
		stores := BuildEnvVectorStores("weaviate", lookup)
		require.Len(t, stores, 1)
		assert.Equal(t, "weaviate:8080", stores[0].ConnectionConfig.Host)
	})
}

func TestFindEnvVectorStore(t *testing.T) {
	lookup := mockEnvLookup(map[string]string{})

	t.Run("found", func(t *testing.T) {
		store := FindEnvVectorStore("postgres", lookup, "__env_postgres__")
		require.NotNil(t, store)
		assert.Equal(t, "__env_postgres__", store.ID)
	})

	t.Run("not found", func(t *testing.T) {
		store := FindEnvVectorStore("postgres", lookup, "__env_unknown__")
		assert.Nil(t, store)
	})

	t.Run("empty driver returns nil", func(t *testing.T) {
		store := FindEnvVectorStore("", lookup, "__env_postgres__")
		assert.Nil(t, store)
	})
}

func TestNewVectorStoreResponse(t *testing.T) {
	store := &VectorStore{
		ID:         "test-id",
		Name:       "test-store",
		EngineType: ElasticsearchRetrieverEngineType,
		ConnectionConfig: ConnectionConfig{
			Addr:     "http://es:9200",
			Password: "secret",
			APIKey:   "my-api-key",
		},
	}

	t.Run("masks sensitive fields", func(t *testing.T) {
		resp := NewVectorStoreResponse(store, "user", false)
		assert.Equal(t, "***", resp.ConnectionConfig.Password)
		assert.Equal(t, "***", resp.ConnectionConfig.APIKey)
		assert.Equal(t, "http://es:9200", resp.ConnectionConfig.Addr) // non-sensitive preserved
	})

	t.Run("preserves source and readonly", func(t *testing.T) {
		resp := NewVectorStoreResponse(store, "env", true)
		assert.Equal(t, "env", resp.Source)
		assert.True(t, resp.ReadOnly)
	})

	t.Run("does not mutate original store", func(t *testing.T) {
		_ = NewVectorStoreResponse(store, "user", false)
		assert.Equal(t, "secret", store.ConnectionConfig.Password)
		assert.Equal(t, "my-api-key", store.ConnectionConfig.APIKey)
	})

	t.Run("empty sensitive fields not masked to ***", func(t *testing.T) {
		noSecret := &VectorStore{
			ID:               "test-id",
			ConnectionConfig: ConnectionConfig{Addr: "http://es:9200"},
		}
		resp := NewVectorStoreResponse(noSecret, "user", false)
		assert.Equal(t, "", resp.ConnectionConfig.Password)
		assert.Equal(t, "", resp.ConnectionConfig.APIKey)
	})
}

func TestGetVectorStoreTypes(t *testing.T) {
	types := GetVectorStoreTypes()

	t.Run("returns 6 engine types", func(t *testing.T) {
		assert.Len(t, types, 6)
	})

	t.Run("type names match engine constants", func(t *testing.T) {
		typeNames := make([]string, len(types))
		for i, typ := range types {
			typeNames[i] = typ.Type
		}
		assert.Contains(t, typeNames, "elasticsearch")
		assert.Contains(t, typeNames, "postgres")
		assert.Contains(t, typeNames, "qdrant")
		assert.Contains(t, typeNames, "milvus")
		assert.Contains(t, typeNames, "weaviate")
		assert.Contains(t, typeNames, "sqlite")
	})

	t.Run("elasticsearch has connection and index fields", func(t *testing.T) {
		var esType VectorStoreTypeInfo
		for _, typ := range types {
			if typ.Type == "elasticsearch" {
				esType = typ
				break
			}
		}
		assert.NotEmpty(t, esType.ConnectionFields)
		assert.NotEmpty(t, esType.IndexFields)

		// Check sensitive field marking
		var passwordField VectorStoreFieldInfo
		for _, f := range esType.ConnectionFields {
			if f.Name == "password" {
				passwordField = f
				break
			}
		}
		assert.True(t, passwordField.Sensitive)
	})

	t.Run("sqlite has no connection fields", func(t *testing.T) {
		var sqliteType VectorStoreTypeInfo
		for _, typ := range types {
			if typ.Type == "sqlite" {
				sqliteType = typ
				break
			}
		}
		assert.Empty(t, sqliteType.ConnectionFields)
	})
}

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
