package container

import (
	"slices"
	"strings"
	"testing"
)

func TestValidateDriverCombination(t *testing.T) {
	tests := []struct {
		name           string
		dbDriver       string
		retrieveDriver string
		wantErr        bool
		errContains    string
	}{
		{
			name:           "mysql rejects postgres local retriever",
			dbDriver:       "mysql",
			retrieveDriver: "postgres",
			wantErr:        true,
			errContains:    "postgres",
		},
		{
			name:           "mysql rejects sqlite local retriever",
			dbDriver:       "mysql",
			retrieveDriver: "sqlite",
			wantErr:        true,
			errContains:    "sqlite",
		},
		{
			name:           "mysql rejects empty retriever",
			dbDriver:       "mysql",
			retrieveDriver: "",
			wantErr:        true,
			errContains:    "RETRIEVE_DRIVER",
		},
		{
			name:           "mysql rejects unknown retriever",
			dbDriver:       "mysql",
			retrieveDriver: "unknown-engine",
			wantErr:        true,
			errContains:    "not a registered retriever engine",
		},
		{
			name:           "mysql rejects keyword-only retriever",
			dbDriver:       "mysql",
			retrieveDriver: "elasticsearch_v7",
			wantErr:        true,
			errContains:    "vector-capable",
		},
		{
			name:           "mysql accepts vector retriever",
			dbDriver:       "mysql",
			retrieveDriver: "qdrant",
		},
		{
			name:           "mysql accepts mixed keyword and vector retrievers",
			dbDriver:       "mysql",
			retrieveDriver: "elasticsearch_v7,qdrant",
		},
		{
			name:           "mysql normalizes whitespace empty entries and duplicates",
			dbDriver:       "mysql",
			retrieveDriver: "  qdrant, , qdrant  ",
		},
		{
			name:           "non-mysql driver passes through",
			dbDriver:       "postgres",
			retrieveDriver: "unknown-engine",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDriverCombination(tt.dbDriver, tt.retrieveDriver)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error %q should contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}

func TestParseRetrieveDrivers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "splits and trims",
			in:   "  qdrant  ,  milvus  ",
			want: []string{"qdrant", "milvus"},
		},
		{
			name: "drops empty entries",
			in:   ",qdrant,   ,milvus,",
			want: []string{"qdrant", "milvus"},
		},
		{
			name: "deduplicates preserving order",
			in:   "qdrant, milvus, qdrant, milvus",
			want: []string{"qdrant", "milvus"},
		},
		{
			name: "all empty returns nil",
			in:   "   ,   ",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRetrieveDrivers(tt.in)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("ParseRetrieveDrivers(%q) = %v; want %v", tt.in, got, tt.want)
			}
		})
	}
}
