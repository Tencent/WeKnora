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
		"ELASTICSEARCH_ADDR":          "http://es:9200",
		"ELASTICSEARCH_USERNAME":      "elastic",
		"ELASTICSEARCH_PASSWORD":      "secret",
		"ELASTICSEARCH_INDEX":         "my_index",
		"QDRANT_HOST":                 "qdrant-host",
		"QDRANT_API_KEY":              "qd-key",
		"MILVUS_ADDRESS":              "milvus:19530",
		"TENCENT_VECTORDB_ADDR":       "http://tencent-vdb",
		"TENCENT_VECTORDB_USERNAME":   "root",
		"TENCENT_VECTORDB_API_KEY":    "vdb-key",
		"TENCENT_VECTORDB_DATABASE":   "weknora",
		"TENCENT_VECTORDB_COLLECTION": "weknora_embeddings",
		"WEAVIATE_HOST":               "weaviate:8080",
		"DORIS_ADDR":                  "doris-fe:9030",
		"DORIS_HTTP_PORT":             "8030",
		"DORIS_DATABASE":              "weknora",
		"DORIS_USERNAME":              "root",
		"DORIS_PASSWORD":              "doris-pass",
		"DORIS_TABLE_PREFIX":          "weknora_embeddings",
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
		assert.Equal(t, "PostgreSQL", stores[0].Name)
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
		stores := BuildEnvVectorStores("postgres,sqlite,elasticsearch_v8,elasticsearch_v7,qdrant,milvus,weaviate,doris,tencent_vectordb", lookup)
		require.Len(t, stores, 9)

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
		assert.Contains(t, ids, "__env_doris__")
		assert.Contains(t, ids, "__env_tencent_vectordb__")
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

	t.Run("tencent vectordb env store", func(t *testing.T) {
		stores := BuildEnvVectorStores("tencent_vectordb", lookup)
		require.Len(t, stores, 1)
		assert.Equal(t, "http://tencent-vdb", stores[0].ConnectionConfig.Addr)
		assert.Equal(t, "root", stores[0].ConnectionConfig.Username)
		assert.Equal(t, "vdb-key", stores[0].ConnectionConfig.APIKey)
		assert.Equal(t, "weknora", stores[0].ConnectionConfig.Database)
		assert.Equal(t, "weknora_embeddings", stores[0].IndexConfig.CollectionName)
	})

	t.Run("weaviate env store", func(t *testing.T) {
		stores := BuildEnvVectorStores("weaviate", lookup)
		require.Len(t, stores, 1)
		assert.Equal(t, "weaviate:8080", stores[0].ConnectionConfig.Host)
	})

	t.Run("doris env store", func(t *testing.T) {
		stores := BuildEnvVectorStores("doris", lookup)
		require.Len(t, stores, 1)
		assert.Equal(t, "__env_doris__", stores[0].ID)
		assert.Equal(t, DorisRetrieverEngineType, stores[0].EngineType)
		assert.Equal(t, "doris-fe:9030", stores[0].ConnectionConfig.Addr)
		assert.Equal(t, 8030, stores[0].ConnectionConfig.HTTPPort)
		assert.Equal(t, "weknora", stores[0].ConnectionConfig.Database)
		assert.Equal(t, "root", stores[0].ConnectionConfig.Username)
		assert.Equal(t, "doris-pass", stores[0].ConnectionConfig.Password)
		assert.Equal(t, "weknora_embeddings", stores[0].IndexConfig.CollectionPrefix)
	})

	t.Run("doris env store handles invalid http port gracefully", func(t *testing.T) {
		bad := mockEnvLookup(map[string]string{
			"DORIS_ADDR":      "doris-fe:9030",
			"DORIS_HTTP_PORT": "not-a-number",
			"DORIS_DATABASE":  "weknora",
		})
		stores := BuildEnvVectorStores("doris", bad)
		require.Len(t, stores, 1)
		assert.Equal(t, 0, stores[0].ConnectionConfig.HTTPPort) // falls back to 0 (factory will default to 8030)
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
		assert.Equal(t, RedactedSecretPlaceholder, resp.ConnectionConfig.Password)
		assert.Equal(t, RedactedSecretPlaceholder, resp.ConnectionConfig.APIKey)
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

	t.Run("returns supported external engine types (excludes postgres and sqlite)", func(t *testing.T) {
		assert.Len(t, types, 7)
	})

	t.Run("type names match engine constants", func(t *testing.T) {
		typeNames := make([]string, len(types))
		for i, typ := range types {
			typeNames[i] = typ.Type
		}
		assert.Contains(t, typeNames, "elasticsearch")
		assert.Contains(t, typeNames, "qdrant")
		assert.Contains(t, typeNames, "milvus")
		assert.Contains(t, typeNames, "tencent_vectordb")
		assert.Contains(t, typeNames, "weaviate")
		assert.Contains(t, typeNames, "doris")
		assert.Contains(t, typeNames, "opensearch")
		assert.NotContains(t, typeNames, "postgres")
		assert.NotContains(t, typeNames, "sqlite")
	})

	t.Run("doris has connection and index fields", func(t *testing.T) {
		var dorisType VectorStoreTypeInfo
		for _, typ := range types {
			if typ.Type == "doris" {
				dorisType = typ
				break
			}
		}
		require.NotEmpty(t, dorisType.ConnectionFields)
		require.NotEmpty(t, dorisType.IndexFields)

		// addr and database are required
		seen := map[string]VectorStoreFieldInfo{}
		for _, f := range dorisType.ConnectionFields {
			seen[f.Name] = f
		}
		assert.True(t, seen["addr"].Required)
		assert.True(t, seen["database"].Required)
		assert.True(t, seen["password"].Sensitive)
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

	t.Run("display names have no parenthetical suffix", func(t *testing.T) {
		for _, typ := range types {
			assert.NotContains(t, typ.DisplayName, "(", "display_name should not contain parenthetical suffix: %s", typ.DisplayName)
		}
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
		EngineType: ElasticsearchRetrieverEngineType,
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
		ElasticsearchRetrieverEngineType,
		QdrantRetrieverEngineType,
		MilvusRetrieverEngineType,
		WeaviateRetrieverEngineType,
		DorisRetrieverEngineType,
		TencentVectorDBRetrieverEngineType,
	}
	for _, et := range validTypes {
		t.Run("valid: "+string(et), func(t *testing.T) {
			assert.True(t, IsValidEngineType(et))
		})
	}

	// Postgres and SQLite are intentionally NOT registerable as DB stores —
	// they only make sense as env stores driven by RETRIEVE_DRIVER (see the
	// doc comment on validEngineTypes). UI/API surface stays consistent:
	// GetVectorStoreTypes does not list them, Validate rejects them, and
	// env stores reach the engine registry through BuildEnvVectorStores
	// instead of through CreateStore.
	// Note: opensearch is now a VALID DB-store engine (activated in this PR);
	// see TestIsValidEngineType_OpenSearch in vectorstore_opensearch_test.go.
	invalidTypes := []RetrieverEngineType{
		"unknown",
		"",
		PostgresRetrieverEngineType,
		SQLiteRetrieverEngineType,
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
		assert.Equal(t, RedactedSecretPlaceholder, masked.Password)
		assert.Equal(t, RedactedSecretPlaceholder, masked.APIKey)
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

	t.Run("preserves insecure_skip_verify as a visible operator-facing knob", func(t *testing.T) {
		// InsecureSkipVerify is deliberately NOT redacted: operators
		// must be able to see whether TLS verification is disabled.
		// This pin prevents a well-meaning future change from quietly
		// masking the flag and hiding a misconfiguration.
		c := ConnectionConfig{
			Addr:               "https://os:9200",
			Password:           "secret-pass",
			InsecureSkipVerify: true,
		}
		masked := c.MaskSensitiveFields()
		assert.Equal(t, RedactedSecretPlaceholder, masked.Password)
		assert.True(t, masked.InsecureSkipVerify,
			"InsecureSkipVerify must remain visible after masking")
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
		// Tencent VectorDB
		{
			name:       "tencent vectordb with custom collection name",
			config:     IndexConfig{CollectionName: "custom_collection"},
			engineType: TencentVectorDBRetrieverEngineType,
			expected:   "custom_collection",
		},
		{
			name:       "tencent vectordb default",
			config:     IndexConfig{},
			engineType: TencentVectorDBRetrieverEngineType,
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
			expected:   "Weknora_embeddings",
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

// ---------------------------------------------------------------------------
// IndexConfig — getter helpers
// ---------------------------------------------------------------------------

func TestIndexConfig_GetterHelpers(t *testing.T) {
	t.Run("nil receiver returns default", func(t *testing.T) {
		var ic *IndexConfig
		assert.Equal(t, 5, ic.GetNumberOfShards(5))
		assert.Equal(t, -1, ic.GetNumberOfReplicas(-1))
		assert.Equal(t, 0, ic.GetShardNumber(0))
		assert.Equal(t, 0, ic.GetReplicationFactor(0))
		assert.Equal(t, 1, ic.GetShardsNum(1))
		assert.Equal(t, 0, ic.GetReplicaNumber(0))
		assert.Equal(t, 0, ic.GetDesiredShardCount(0))
	})

	t.Run("zero value returns default", func(t *testing.T) {
		ic := &IndexConfig{}
		assert.Equal(t, 5, ic.GetNumberOfShards(5))
		assert.Equal(t, -1, ic.GetNumberOfReplicas(-1))
		assert.Equal(t, 0, ic.GetShardNumber(0))
		assert.Equal(t, 0, ic.GetReplicationFactor(0))
		assert.Equal(t, 1, ic.GetShardsNum(1))
		assert.Equal(t, 0, ic.GetReplicaNumber(0))
		assert.Equal(t, 0, ic.GetDesiredShardCount(0))
	})

	t.Run("positive value overrides default", func(t *testing.T) {
		ic := &IndexConfig{
			NumberOfShards:    3,
			NumberOfReplicas:  2,
			ShardNumber:       4,
			ReplicationFactor: 3,
			ShardsNum:         5,
			ReplicaNumber:     2,
			DesiredShardCount: 3,
		}
		assert.Equal(t, 3, ic.GetNumberOfShards(1))
		assert.Equal(t, 2, ic.GetNumberOfReplicas(-1))
		assert.Equal(t, 4, ic.GetShardNumber(0))
		assert.Equal(t, 3, ic.GetReplicationFactor(0))
		assert.Equal(t, 5, ic.GetShardsNum(1))
		assert.Equal(t, 2, ic.GetReplicaNumber(0))
		assert.Equal(t, 3, ic.GetDesiredShardCount(0))
	})

	t.Run("negative value returns default (treated as unset)", func(t *testing.T) {
		ic := &IndexConfig{
			NumberOfShards:    -1,
			NumberOfReplicas:  -1,
			ShardNumber:       -5,
			ReplicationFactor: -1,
			ShardsNum:         -1,
			ReplicaNumber:     -1,
			DesiredShardCount: -1,
		}
		assert.Equal(t, 1, ic.GetNumberOfShards(1))
		assert.Equal(t, -1, ic.GetNumberOfReplicas(-1))
		assert.Equal(t, 0, ic.GetShardNumber(0))
		assert.Equal(t, 0, ic.GetReplicationFactor(0))
		assert.Equal(t, 1, ic.GetShardsNum(1))
		assert.Equal(t, 0, ic.GetReplicaNumber(0))
		assert.Equal(t, 0, ic.GetDesiredShardCount(0))
	})
}

// ---------------------------------------------------------------------------
// IndexConfig — resolve helpers
// ---------------------------------------------------------------------------

func TestResolveIndexName(t *testing.T) {
	t.Run("nil IndexConfig falls back to env var", func(t *testing.T) {
		t.Setenv("ELASTICSEARCH_INDEX", "env_index")
		assert.Equal(t, "env_index", ResolveIndexName(nil, "ELASTICSEARCH_INDEX", "default"))
	})

	t.Run("nil IndexConfig falls back to default when env empty", func(t *testing.T) {
		t.Setenv("ELASTICSEARCH_INDEX", "")
		assert.Equal(t, "xwrag_default", ResolveIndexName(nil, "ELASTICSEARCH_INDEX", "xwrag_default"))
	})

	t.Run("IndexConfig value takes precedence over env var", func(t *testing.T) {
		t.Setenv("ELASTICSEARCH_INDEX", "env_index")
		ic := &IndexConfig{IndexName: "custom_index"}
		assert.Equal(t, "custom_index", ResolveIndexName(ic, "ELASTICSEARCH_INDEX", "default"))
	})

	t.Run("empty IndexConfig.IndexName falls back to env var", func(t *testing.T) {
		t.Setenv("ELASTICSEARCH_INDEX", "env_index")
		ic := &IndexConfig{}
		assert.Equal(t, "env_index", ResolveIndexName(ic, "ELASTICSEARCH_INDEX", "default"))
	})
}

func TestResolveCollectionName(t *testing.T) {
	t.Run("nil IndexConfig falls back to env var", func(t *testing.T) {
		t.Setenv("QDRANT_COLLECTION", "env_collection")
		assert.Equal(t, "env_collection", ResolveCollectionName(nil, "QDRANT_COLLECTION", "default"))
	})

	t.Run("CollectionPrefix takes precedence over CollectionName", func(t *testing.T) {
		ic := &IndexConfig{CollectionPrefix: "prefix_name", CollectionName: "full_name"}
		assert.Equal(t, "prefix_name", ResolveCollectionName(ic, "QDRANT_COLLECTION", "default"))
	})

	t.Run("CollectionName used when CollectionPrefix empty", func(t *testing.T) {
		ic := &IndexConfig{CollectionName: "full_name"}
		assert.Equal(t, "full_name", ResolveCollectionName(ic, "MILVUS_COLLECTION", "default"))
	})

	t.Run("empty IndexConfig falls back to default", func(t *testing.T) {
		t.Setenv("QDRANT_COLLECTION", "")
		ic := &IndexConfig{}
		assert.Equal(t, "weknora_embeddings", ResolveCollectionName(ic, "QDRANT_COLLECTION", "weknora_embeddings"))
	})
}

// ---------------------------------------------------------------------------
// OptionalUint32
// ---------------------------------------------------------------------------

func TestOptionalUint32(t *testing.T) {
	t.Run("zero returns nil", func(t *testing.T) {
		assert.Nil(t, OptionalUint32(0))
	})

	t.Run("negative returns nil", func(t *testing.T) {
		assert.Nil(t, OptionalUint32(-1))
		assert.Nil(t, OptionalUint32(-100))
	})

	t.Run("positive returns pointer to uint32", func(t *testing.T) {
		result := OptionalUint32(3)
		require.NotNil(t, result)
		assert.Equal(t, uint32(3), *result)
	})

	t.Run("large positive value", func(t *testing.T) {
		result := OptionalUint32(64)
		require.NotNil(t, result)
		assert.Equal(t, uint32(64), *result)
	})
}

// ---------------------------------------------------------------------------
// ValidateIndexConfig
// ---------------------------------------------------------------------------

func TestValidateIndexConfig(t *testing.T) {
	t.Run("empty config is valid", func(t *testing.T) {
		assert.NoError(t, ValidateIndexConfig(IndexConfig{}))
	})

	t.Run("valid config with all fields", func(t *testing.T) {
		ic := IndexConfig{
			IndexName:         "my_index",
			NumberOfShards:    3,
			NumberOfReplicas:  1,
			CollectionPrefix:  "my_collection",
			ShardNumber:       4,
			ReplicationFactor: 2,
			ShardsNum:         2,
			ReplicaNumber:     3,
			DesiredShardCount: 2,
		}
		assert.NoError(t, ValidateIndexConfig(ic))
	})

	// --- Name validation ---
	t.Run("index_name with special chars rejected", func(t *testing.T) {
		ic := IndexConfig{IndexName: "my index*"}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "index_name")
	})

	t.Run("index_name starting with number rejected", func(t *testing.T) {
		ic := IndexConfig{IndexName: "123abc"}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
	})

	t.Run("collection_prefix with slash rejected", func(t *testing.T) {
		ic := IndexConfig{CollectionPrefix: "path/to/collection"}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "collection_prefix")
	})

	t.Run("collection_name with dot rejected", func(t *testing.T) {
		ic := IndexConfig{CollectionName: "my.collection"}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "collection_name")
	})

	t.Run("valid names with underscore and hyphen", func(t *testing.T) {
		ic := IndexConfig{
			IndexName:        "my_index-v2",
			CollectionPrefix: "Weknora_embeddings",
			CollectionName:   "custom-collection-name",
		}
		assert.NoError(t, ValidateIndexConfig(ic))
	})

	// --- Numeric bounds ---
	t.Run("number_of_shards exceeds max", func(t *testing.T) {
		ic := IndexConfig{NumberOfShards: 100}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "number_of_shards")
	})

	t.Run("negative number_of_shards rejected", func(t *testing.T) {
		ic := IndexConfig{NumberOfShards: -1}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "number_of_shards")
	})

	t.Run("replication_factor exceeds max", func(t *testing.T) {
		ic := IndexConfig{ReplicationFactor: 50}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "replication_factor")
	})

	t.Run("shard_number at max boundary is valid", func(t *testing.T) {
		ic := IndexConfig{ShardNumber: 64}
		assert.NoError(t, ValidateIndexConfig(ic))
	})

	t.Run("replication_factor at max boundary is valid", func(t *testing.T) {
		ic := IndexConfig{ReplicationFactor: 10}
		assert.NoError(t, ValidateIndexConfig(ic))
	})

	t.Run("shards_num exceeds max", func(t *testing.T) {
		ic := IndexConfig{ShardsNum: 999}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "shards_num")
	})

	t.Run("replica_number exceeds max", func(t *testing.T) {
		ic := IndexConfig{ReplicaNumber: 50}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "replica_number")
	})

	t.Run("desired_shard_count exceeds max", func(t *testing.T) {
		ic := IndexConfig{DesiredShardCount: 100}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "desired_shard_count")
	})

	t.Run("buckets_num exceeds max", func(t *testing.T) {
		ic := IndexConfig{BucketsNum: 999}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "buckets_num")
	})

	t.Run("buckets_num at max boundary is valid", func(t *testing.T) {
		ic := IndexConfig{BucketsNum: 64}
		assert.NoError(t, ValidateIndexConfig(ic))
	})

	t.Run("replication_num exceeds max", func(t *testing.T) {
		ic := IndexConfig{ReplicationNum: 50}
		err := ValidateIndexConfig(ic)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "replication_num")
	})

	t.Run("doris GetIndexNameOrDefault falls back when prefix empty", func(t *testing.T) {
		ic := IndexConfig{}
		assert.Equal(t, "weknora_embeddings", ic.GetIndexNameOrDefault(DorisRetrieverEngineType))
	})

	t.Run("doris GetIndexNameOrDefault honors collection_prefix", func(t *testing.T) {
		ic := IndexConfig{CollectionPrefix: "custom_prefix"}
		assert.Equal(t, "custom_prefix", ic.GetIndexNameOrDefault(DorisRetrieverEngineType))
	})
}

// ---------------------------------------------------------------------------
// IndexConfig — scalability fields round-trip
// ---------------------------------------------------------------------------

func TestIndexConfig_ScalabilityFieldsRoundTrip(t *testing.T) {
	t.Run("scalability fields serialize and deserialize", func(t *testing.T) {
		original := IndexConfig{
			IndexName:         "my_index",
			NumberOfShards:    3,
			NumberOfReplicas:  1,
			CollectionPrefix:  "my_prefix",
			ShardNumber:       4,
			ReplicationFactor: 2,
			ShardsNum:         5,
			ReplicaNumber:     3,
			DesiredShardCount: 2,
		}
		raw, err := original.Value()
		require.NoError(t, err)

		var scanned IndexConfig
		require.NoError(t, scanned.Scan(raw.([]byte)))
		assert.Equal(t, original, scanned)
	})

	t.Run("scalability fields omitted when zero", func(t *testing.T) {
		raw, err := IndexConfig{IndexName: "test"}.Value()
		require.NoError(t, err)

		var parsed map[string]interface{}
		require.NoError(t, json.Unmarshal(raw.([]byte), &parsed))
		assert.NotContains(t, parsed, "shard_number")
		assert.NotContains(t, parsed, "replication_factor")
		assert.NotContains(t, parsed, "shards_num")
		assert.NotContains(t, parsed, "replica_number")
		assert.NotContains(t, parsed, "desired_shard_count")
	})
}

// TestVectorStore_PostgresSqliteNotRegisterable pins the write-path and
// read-path consistency for the two engines that are only meaningful as env
// stores. They must:
//
//  1. Be rejected by Validate() so POST /vector-stores returns a 4xx
//     instead of silently persisting a row that has no separation effect.
//  2. Be absent from GetVectorStoreTypes() so the UI dropdown doesn't
//     offer them as a choice.
//
// Both checks live here so a future change that re-introduces one path
// (e.g., adds Postgres back to validEngineTypes for some niche case)
// fails this test pair instead of silently re-opening the inconsistency
// that this fix closed.
func TestVectorStore_PostgresSqliteNotRegisterable(t *testing.T) {
	t.Run("Validate rejects postgres as DB store", func(t *testing.T) {
		v := &VectorStore{
			Name: "test", TenantID: 1,
			EngineType: PostgresRetrieverEngineType,
		}
		err := v.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported engine type")
	})

	t.Run("Validate rejects sqlite as DB store", func(t *testing.T) {
		v := &VectorStore{
			Name: "test", TenantID: 1,
			EngineType: SQLiteRetrieverEngineType,
		}
		err := v.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported engine type")
	})

	t.Run("GetVectorStoreTypes omits postgres and sqlite", func(t *testing.T) {
		listed := GetVectorStoreTypes()
		var got []string
		for _, info := range listed {
			got = append(got, info.Type)
		}
		assert.NotContains(t, got, string(PostgresRetrieverEngineType),
			"postgres must not appear in the UI dropdown — env-store only")
		assert.NotContains(t, got, string(SQLiteRetrieverEngineType),
			"sqlite must not appear in the UI dropdown — env-store only")
	})
}

// ---------------------------------------------------------------------------
// Phase 3 PR1 additions: ConnectionConfig.InsecureSkipVerify backward-compat
// and VectorStoreFieldInfo schema extensions (Immutable / Min / Max / Enum)
// ---------------------------------------------------------------------------

// TestConnectionConfig_InsecureSkipVerify_BackwardCompat ensures that
// ConnectionConfigs persisted before Phase 3 deserialize cleanly: the
// missing JSON field maps to false (Go zero-value). This is the
// foundational backward-compat guarantee for the schema extension.
func TestConnectionConfig_InsecureSkipVerify_BackwardCompat(t *testing.T) {
	t.Run("missing field defaults to false", func(t *testing.T) {
		legacy := `{"addr":"https://es:9200","username":"u","password":"p"}`
		var cfg ConnectionConfig
		require.NoError(t, json.Unmarshal([]byte(legacy), &cfg))
		assert.False(t, cfg.InsecureSkipVerify)
	})

	t.Run("explicit false deserializes correctly", func(t *testing.T) {
		raw := `{"addr":"https://es:9200","insecure_skip_verify":false}`
		var cfg ConnectionConfig
		require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
		assert.False(t, cfg.InsecureSkipVerify)
	})

	t.Run("explicit true deserializes correctly", func(t *testing.T) {
		raw := `{"addr":"https://os:9200","insecure_skip_verify":true}`
		var cfg ConnectionConfig
		require.NoError(t, json.Unmarshal([]byte(raw), &cfg))
		assert.True(t, cfg.InsecureSkipVerify)
	})
}

// TestConnectionConfig_InsecureSkipVerify_RoundTrip verifies that the
// new field serializes correctly when set, and is omitted (omitempty)
// when false — so existing entries do not gain a new wire-format key
// after the Phase 3 schema extension lands.
func TestConnectionConfig_InsecureSkipVerify_RoundTrip(t *testing.T) {
	t.Run("true serializes the field", func(t *testing.T) {
		cfg := ConnectionConfig{Addr: "https://os:9200", InsecureSkipVerify: true}
		out, err := json.Marshal(cfg)
		require.NoError(t, err)
		assert.Contains(t, string(out), `"insecure_skip_verify":true`)
	})

	t.Run("false omits the field via omitempty", func(t *testing.T) {
		cfg := ConnectionConfig{Addr: "https://os:9200", InsecureSkipVerify: false}
		out, err := json.Marshal(cfg)
		require.NoError(t, err)
		assert.NotContains(t, string(out), `"insecure_skip_verify"`)
	})
}

// TestConnectionConfig_AESGCMRoundTrip_PreservesInsecureSkipVerify
// ensures the AES-GCM Value/Scan path round-trips the new field
// without corruption. The field is stored as plaintext (no AES-GCM)
// but travels alongside encrypted Password / APIKey.
func TestConnectionConfig_AESGCMRoundTrip_PreservesInsecureSkipVerify(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", testAESKey)

	original := ConnectionConfig{
		Addr:               "https://os:9200",
		Username:           "admin",
		Password:           "secret-pass",
		APIKey:             "sk-api-key",
		InsecureSkipVerify: true,
	}

	// Value — encrypt
	raw, err := original.Value()
	require.NoError(t, err)

	// Verify the serialized JSON has the plaintext flag alongside the
	// encrypted secret fields.
	var intermediate map[string]interface{}
	require.NoError(t, json.Unmarshal(raw.([]byte), &intermediate))
	assert.True(t, strings.HasPrefix(intermediate["password"].(string), "enc:v1:"),
		"password should still be encrypted")
	assert.Equal(t, true, intermediate["insecure_skip_verify"],
		"insecure_skip_verify should be plaintext bool")

	// Scan — decrypt secrets and preserve the flag
	var scanned ConnectionConfig
	require.NoError(t, scanned.Scan(raw.([]byte)))
	assert.Equal(t, "secret-pass", scanned.Password)
	assert.Equal(t, "sk-api-key", scanned.APIKey)
	assert.True(t, scanned.InsecureSkipVerify,
		"InsecureSkipVerify should round-trip through Value/Scan")
}

// TestVectorStoreFieldInfo_OmitemptyPreservesWire confirms that the
// four new optional fields (Immutable / Min / Max / Enum) added in
// Phase 3 PR 1 are omitted from JSON when unset, so existing
// VectorStoreFieldInfo entries (without these fields) serialize
// identically before and after the schema extension.
func TestVectorStoreFieldInfo_OmitemptyPreservesWire(t *testing.T) {
	legacy := VectorStoreFieldInfo{
		Name:        "addr",
		Type:        "string",
		Required:    true,
		Description: "URL",
		Default:     "http://localhost:9200",
	}
	out, err := json.Marshal(legacy)
	require.NoError(t, err)
	// Phase 3 new fields must all be omitted from the wire format.
	assert.NotContains(t, string(out), `"immutable"`)
	assert.NotContains(t, string(out), `"min"`)
	assert.NotContains(t, string(out), `"max"`)
	assert.NotContains(t, string(out), `"enum"`)
}

// TestVectorStoreFieldInfo_NewFieldsSerialize verifies the new fields
// appear in JSON when set — exercised by the OpenSearch IndexFields
// entry that lands in a later Phase 3 PR.
func TestVectorStoreFieldInfo_NewFieldsSerialize(t *testing.T) {
	t.Run("immutable", func(t *testing.T) {
		f := VectorStoreFieldInfo{Name: "knn_engine", Type: "string", Immutable: true}
		out, err := json.Marshal(f)
		require.NoError(t, err)
		assert.Contains(t, string(out), `"immutable":true`)
	})

	t.Run("min and max", func(t *testing.T) {
		minV, maxV := float64(4), float64(64)
		f := VectorStoreFieldInfo{Name: "hnsw_m", Type: "number", Min: &minV, Max: &maxV}
		out, err := json.Marshal(f)
		require.NoError(t, err)
		assert.Contains(t, string(out), `"min":4`)
		assert.Contains(t, string(out), `"max":64`)
	})

	t.Run("enum", func(t *testing.T) {
		f := VectorStoreFieldInfo{
			Name: "knn_engine", Type: "string",
			Enum: []string{"lucene", "faiss"},
		}
		out, err := json.Marshal(f)
		require.NoError(t, err)
		assert.Contains(t, string(out), `"enum":["lucene","faiss"]`)
	})

	t.Run("empty enum omits via omitempty", func(t *testing.T) {
		f := VectorStoreFieldInfo{Name: "addr", Type: "string", Enum: []string{}}
		out, err := json.Marshal(f)
		require.NoError(t, err)
		assert.NotContains(t, string(out), `"enum"`)
	})
}

// TestVectorStoreFieldInfo_RoundTrip ensures deserialization back into
// the struct works for all new fields, including the *float64 pointer
// distinction (nil vs explicit 0).
func TestVectorStoreFieldInfo_RoundTrip(t *testing.T) {
	t.Run("min=0 deserializes as &0, not nil", func(t *testing.T) {
		raw := `{"name":"replicas","type":"number","min":0,"max":5}`
		var f VectorStoreFieldInfo
		require.NoError(t, json.Unmarshal([]byte(raw), &f))
		require.NotNil(t, f.Min)
		assert.Equal(t, float64(0), *f.Min)
		require.NotNil(t, f.Max)
		assert.Equal(t, float64(5), *f.Max)
	})

	t.Run("missing min/max deserialize as nil", func(t *testing.T) {
		raw := `{"name":"addr","type":"string"}`
		var f VectorStoreFieldInfo
		require.NoError(t, json.Unmarshal([]byte(raw), &f))
		assert.Nil(t, f.Min)
		assert.Nil(t, f.Max)
	})

	t.Run("min and max are independent pointers", func(t *testing.T) {
		// Setting only Min must leave Max nil (and vice versa). A
		// refactor that accidentally aliases the two via a shared
		// local would still pass the previous two subtests because
		// they always set both ends.
		raw := `{"name":"upper_only","type":"number","max":10}`
		var f VectorStoreFieldInfo
		require.NoError(t, json.Unmarshal([]byte(raw), &f))
		assert.Nil(t, f.Min)
		require.NotNil(t, f.Max)
		assert.Equal(t, float64(10), *f.Max)
	})

	t.Run("nil enum omits via omitempty", func(t *testing.T) {
		// `nil` and `[]string{}` both round-trip through omitempty
		// identically — pin both shapes so a future Go encoder
		// behavior change cannot regress the wire format.
		f := VectorStoreFieldInfo{Name: "addr", Type: "string"} // Enum nil
		out, err := json.Marshal(f)
		require.NoError(t, err)
		assert.NotContains(t, string(out), `"enum"`)
	})
}

// TestOpenSearchRetrieverEngineType_StringValue pins the wire string
// to the official product name. The constant exists in PR 1 so the
// EngineAwareNormalizer case and AuditAction constants can reference
// it; the driver itself lands in a later PR.
func TestOpenSearchRetrieverEngineType_StringValue(t *testing.T) {
	assert.Equal(t,
		RetrieverEngineType("opensearch"),
		OpenSearchRetrieverEngineType,
	)
}

// TestOpenSearchRetrieverEngineType_DistinctFromExisting ensures the
// new wire value does not collide with any of the 10 existing engine
// types. A collision would silently route requests to the wrong
// engine after the activation switch lands.
func TestOpenSearchRetrieverEngineType_DistinctFromExisting(t *testing.T) {
	existing := []RetrieverEngineType{
		PostgresRetrieverEngineType,
		ElasticsearchRetrieverEngineType,
		InfinityRetrieverEngineType,
		ElasticFaissRetrieverEngineType,
		QdrantRetrieverEngineType,
		MilvusRetrieverEngineType,
		WeaviateRetrieverEngineType,
		DorisRetrieverEngineType,
		SQLiteRetrieverEngineType,
		TencentVectorDBRetrieverEngineType,
	}
	for _, e := range existing {
		assert.NotEqual(t, e, OpenSearchRetrieverEngineType,
			"OpenSearch wire value must not collide with %s", e)
	}
}
