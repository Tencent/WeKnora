package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findVSType returns the VectorStoreTypeInfo with the given Type, or fails.
func findVSType(t *testing.T, typeName string) VectorStoreTypeInfo {
	t.Helper()
	for _, vt := range GetVectorStoreTypes() {
		if vt.Type == typeName {
			return vt
		}
	}
	t.Fatalf("GetVectorStoreTypes() has no entry for %q", typeName)
	return VectorStoreTypeInfo{}
}

// findField returns the field with the given Name from a slice, or fails.
func findField(t *testing.T, fields []VectorStoreFieldInfo, name string) VectorStoreFieldInfo {
	t.Helper()
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no field named %q", name)
	return VectorStoreFieldInfo{}
}

// TestIndexConfig_OpenSearchFieldsOmittedForOtherEngines verifies the new HNSW
// fields are omitempty so other engines' serialized IndexConfig is unchanged.
func TestIndexConfig_OpenSearchFieldsOmittedForOtherEngines(t *testing.T) {
	t.Run("omitted when unset", func(t *testing.T) {
		b, err := json.Marshal(IndexConfig{IndexName: "weknora", NumberOfShards: 4})
		require.NoError(t, err)
		s := string(b)
		assert.NotContains(t, s, "hnsw_m")
		assert.NotContains(t, s, "hnsw_ef_construction")
		assert.NotContains(t, s, "hnsw_ef_search")
		assert.NotContains(t, s, "knn_engine")
	})

	t.Run("present when set", func(t *testing.T) {
		b, err := json.Marshal(IndexConfig{
			HNSWM:              24,
			HNSWEFConstruction: 200,
			HNSWEFSearch:       128,
			KNNEngine:          "faiss",
		})
		require.NoError(t, err)
		s := string(b)
		assert.Contains(t, s, `"hnsw_m":24`)
		assert.Contains(t, s, `"hnsw_ef_construction":200`)
		assert.Contains(t, s, `"hnsw_ef_search":128`)
		assert.Contains(t, s, `"knn_engine":"faiss"`)
	})
}

// TestIsValidEngineType_OpenSearch verifies OpenSearch is now a valid DB-store engine.
func TestIsValidEngineType_OpenSearch(t *testing.T) {
	assert.True(t, IsValidEngineType(OpenSearchRetrieverEngineType))
}

// TestGetVectorStoreTypes_OpenSearchEntry verifies the OpenSearch metadata entry
// exposes the connection + HNSW index fields with the right bounds/enum/immutable.
func TestGetVectorStoreTypes_OpenSearchEntry(t *testing.T) {
	vt := findVSType(t, "opensearch")
	assert.Equal(t, "OpenSearch", vt.DisplayName)

	// Connection fields
	insecure := findField(t, vt.ConnectionFields, "insecure_skip_verify")
	assert.Equal(t, "boolean", insecure.Type)
	assert.Equal(t, false, insecure.Default) // never default-true
	pw := findField(t, vt.ConnectionFields, "password")
	assert.True(t, pw.Sensitive)

	// HNSW index fields: bounds match the flat-validator-aligned caps (14-D)
	m := findField(t, vt.IndexFields, "hnsw_m")
	require.NotNil(t, m.Min)
	require.NotNil(t, m.Max)
	assert.Equal(t, 2.0, *m.Min)
	assert.Equal(t, 100.0, *m.Max)
	assert.True(t, m.Immutable)

	shards := findField(t, vt.IndexFields, "number_of_shards")
	require.NotNil(t, shards.Max)
	assert.Equal(t, 64.0, *shards.Max) // flat ValidateIndexConfig maxShards

	replicas := findField(t, vt.IndexFields, "number_of_replicas")
	require.NotNil(t, replicas.Max)
	assert.Equal(t, 10.0, *replicas.Max) // flat maxReplicas

	eng := findField(t, vt.IndexFields, "knn_engine")
	assert.ElementsMatch(t, []string{"lucene", "faiss"}, eng.Enum)
	assert.True(t, eng.Immutable)

	efs := findField(t, vt.IndexFields, "hnsw_ef_search")
	assert.True(t, efs.Immutable) // no PutSettings path → immutable
}

// TestBuildEnvVectorStores_OpenSearch verifies the env-store builder case.
func TestBuildEnvVectorStores_OpenSearch(t *testing.T) {
	lookup := mockEnvLookup(map[string]string{
		"OPENSEARCH_ADDR":                 "https://os:9200",
		"OPENSEARCH_USERNAME":             "admin",
		"OPENSEARCH_PASSWORD":             "secret",
		"OPENSEARCH_INDEX":                "weknora",
		"OPENSEARCH_INSECURE_SKIP_VERIFY": "true",
	})
	stores := BuildEnvVectorStores("opensearch", lookup)
	require.Len(t, stores, 1)
	s := stores[0]
	assert.Equal(t, "__env_opensearch__", s.ID)
	assert.Equal(t, OpenSearchRetrieverEngineType, s.EngineType)
	assert.Equal(t, "https://os:9200", s.ConnectionConfig.Addr)
	assert.Equal(t, "admin", s.ConnectionConfig.Username)
	assert.Equal(t, "secret", s.ConnectionConfig.Password)
	assert.True(t, s.ConnectionConfig.InsecureSkipVerify)
	assert.Equal(t, "weknora", s.IndexConfig.IndexName)
}

// TestBuildEnvVectorStores_OpenSearch_InsecureDefaultsFalse verifies the TLS
// skip flag is false unless the env var is explicitly "true".
func TestBuildEnvVectorStores_OpenSearch_InsecureDefaultsFalse(t *testing.T) {
	lookup := mockEnvLookup(map[string]string{"OPENSEARCH_ADDR": "https://os:9200"})
	stores := BuildEnvVectorStores("opensearch", lookup)
	require.Len(t, stores, 1)
	assert.False(t, stores[0].ConnectionConfig.InsecureSkipVerify)
}

// TestRetrieverEngineMapping_OpenSearch verifies the RETRIEVE_DRIVER mapping.
func TestRetrieverEngineMapping_OpenSearch(t *testing.T) {
	m := GetRetrieverEngineMapping()
	params, ok := m["opensearch"]
	require.True(t, ok, `retrieverEngineMapping missing "opensearch"`)
	require.Len(t, params, 2)

	var hasKeywords, hasVector bool
	for _, p := range params {
		assert.Equal(t, OpenSearchRetrieverEngineType, p.RetrieverEngineType)
		switch p.RetrieverType {
		case KeywordsRetrieverType:
			hasKeywords = true
		case VectorRetrieverType:
			hasVector = true
		}
	}
	assert.True(t, hasKeywords && hasVector, "expected both Keywords and Vector retriever types")
}
