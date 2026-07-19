package database

import (
	"strings"
	"testing"
)

func TestValidateDriverCombination(t *testing.T) {
	tests := []struct {
		name        string
		dbDriver    string
		retrieve    string
		wantErrPart string
	}{
		{name: "mysql with qdrant", dbDriver: "mysql", retrieve: "qdrant"},
		{name: "mysql with normalized qdrant", dbDriver: " MYSQL ", retrieve: " QDRANT "},
		{name: "postgres remains supported", dbDriver: "postgres", retrieve: "postgres"},
		{
			name:        "mysql retriever is always rejected",
			dbDriver:    "postgres",
			retrieve:    "qdrant,mysql",
			wantErrPart: "RETRIEVE_DRIVER=mysql",
		},
		{
			name:        "mysql primary requires retriever",
			dbDriver:    "mysql",
			retrieve:    " ",
			wantErrPart: "RETRIEVE_DRIVER=qdrant is required",
		},
		{
			name:        "mysql primary rejects postgres retriever",
			dbDriver:    "mysql",
			retrieve:    "postgres",
			wantErrPart: "only supports RETRIEVE_DRIVER=qdrant",
		},
		{
			name:        "mysql primary rejects sqlite retriever",
			dbDriver:    "mysql",
			retrieve:    "sqlite",
			wantErrPart: "only supports RETRIEVE_DRIVER=qdrant",
		},
		{
			name:        "mysql primary rejects another external retriever",
			dbDriver:    "mysql",
			retrieve:    "milvus",
			wantErrPart: "only supports RETRIEVE_DRIVER=qdrant",
		},
		{
			name:        "mysql primary rejects multiple retrievers",
			dbDriver:    "mysql",
			retrieve:    "qdrant,milvus",
			wantErrPart: "only supports RETRIEVE_DRIVER=qdrant",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDriverCombination(tt.dbDriver, tt.retrieve)
			if tt.wantErrPart == "" {
				if err != nil {
					t.Fatalf("ValidateDriverCombination() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
				t.Fatalf("ValidateDriverCombination() error = %v, want substring %q", err, tt.wantErrPart)
			}
		})
	}
}
