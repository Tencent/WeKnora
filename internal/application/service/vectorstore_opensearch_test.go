package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestValidateConnectionConfig_OpenSearch_RequiresAddr(t *testing.T) {
	if err := validateConnectionConfig(types.OpenSearchRetrieverEngineType, types.ConnectionConfig{}); err == nil {
		t.Error("empty addr should be rejected for opensearch")
	}
	if err := validateConnectionConfig(types.OpenSearchRetrieverEngineType,
		types.ConnectionConfig{Addr: "https://os:9200"}); err != nil {
		t.Errorf("valid addr should pass: %v", err)
	}
}

func TestValidateOpenSearchIndexConfig_BoundaryMatrix(t *testing.T) {
	tests := []struct {
		name    string
		ic      types.IndexConfig
		wantErr bool
	}{
		{"all unset (defaults)", types.IndexConfig{}, false},
		{"valid mid-range", types.IndexConfig{HNSWM: 16, HNSWEFConstruction: 100, HNSWEFSearch: 100, KNNEngine: "lucene"}, false},
		{"valid faiss", types.IndexConfig{KNNEngine: "faiss"}, false},
		{"valid boundaries", types.IndexConfig{HNSWM: 2, HNSWEFConstruction: 2, HNSWEFSearch: 1}, false},
		{"valid upper boundaries", types.IndexConfig{HNSWM: 100, HNSWEFConstruction: 4096, HNSWEFSearch: 10000}, false},
		{"hnsw_m too low", types.IndexConfig{HNSWM: 1}, true},
		{"hnsw_m too high", types.IndexConfig{HNSWM: 101}, true},
		{"ef_construction too high", types.IndexConfig{HNSWEFConstruction: 4097}, true},
		{"ef_search too high", types.IndexConfig{HNSWEFSearch: 10001}, true},
		{"invalid engine", types.IndexConfig{KNNEngine: "nmslib"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOpenSearchIndexConfig(tt.ic)
			if tt.wantErr && err == nil {
				t.Errorf("want error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("want nil, got %v", err)
			}
		})
	}
}
