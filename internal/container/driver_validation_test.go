package container

import (
	"strings"
	"testing"
)

// ValidateDriverCombination guards against a misconfiguration where
// DB_DRIVER=mysql is paired with RETRIEVE_DRIVER=postgres (or sqlite, or
// empty, or an unknown value). Under MySQL mode the embeddings table is
// never created, so any retriever that expects a local embeddings table
// (postgres or sqlite) would crash at first embedding write. The guard
// fails fast at startup with an actionable message.
//
// It also enforces that MySQL mode has at least one valid external
// retriever - without one, the app would boot but every retrieval call
// would fail.
//
// These tests are the contract for that function.

func TestValidateDriverCombination(t *testing.T) {
	tests := []struct {
		name           string
		dbDriver       string
		retrieveDriver string
		wantErr        bool
		errContains    string
	}{
		{
			name:           "mysql with postgres retriever rejected",
			dbDriver:       "mysql",
			retrieveDriver: "postgres",
			wantErr:        true,
			errContains:    "postgres",
		},
		{
			name:           "mysql with sqlite retriever rejected",
			dbDriver:       "mysql",
			retrieveDriver: "sqlite",
			wantErr:        true,
			errContains:    "sqlite",
		},
		{
			name:           "mysql with mixed retriever containing postgres rejected",
			dbDriver:       "mysql",
			retrieveDriver: "postgres,qdrant",
			wantErr:        true,
			errContains:    "postgres",
		},
		{
			name:           "mysql with mixed retriever containing sqlite rejected",
			dbDriver:       "mysql",
			retrieveDriver: "qdrant,sqlite",
			wantErr:        true,
			errContains:    "sqlite",
		},
		{
			name:           "mysql with external retriever accepted",
			dbDriver:       "mysql",
			retrieveDriver: "qdrant",
			wantErr:        false,
		},
		{
			name:           "mysql with multiple external retrievers accepted",
			dbDriver:       "mysql",
			retrieveDriver: "qdrant,milvus,elasticsearch_v8",
			wantErr:        false,
		},
		{
			name:           "mysql with whitespace-padded external retriever accepted",
			dbDriver:       "mysql",
			retrieveDriver: "  qdrant  ,  milvus  ",
			wantErr:        false,
		},
		{
			name:           "mysql with empty retriever rejected (no retriever = no retrieval)",
			dbDriver:       "mysql",
			retrieveDriver: "",
			wantErr:        true,
			errContains:    "RETRIEVE_DRIVER",
		},
		{
			name:           "mysql with whitespace-only retriever rejected",
			dbDriver:       "mysql",
			retrieveDriver: "   ",
			wantErr:        true,
			errContains:    "RETRIEVE_DRIVER",
		},
		{
			name:           "mysql with unknown retriever rejected",
			dbDriver:       "mysql",
			retrieveDriver: "postgres,unknown-engine",
			wantErr:        true,
		},
		{
			name:           "mysql with only unknown retriever rejected",
			dbDriver:       "mysql",
			retrieveDriver: "vikingdb", // not in retrieverEngineMapping
			wantErr:        true,
		},
		{
			name:           "postgres with postgres retriever accepted (unchanged behaviour)",
			dbDriver:       "postgres",
			retrieveDriver: "postgres",
			wantErr:        false,
		},
		{
			name:           "postgres with external retriever accepted",
			dbDriver:       "postgres",
			retrieveDriver: "qdrant",
			wantErr:        false,
		},
		{
			name:           "postgres with empty retriever accepted (PG can self-host embeddings)",
			dbDriver:       "postgres",
			retrieveDriver: "",
			wantErr:        false,
		},
		{
			name:           "sqlite never validated (legacy behaviour)",
			dbDriver:       "sqlite",
			retrieveDriver: "postgres",
			wantErr:        false,
		},
		{
			name:           "sqlite with empty retriever accepted",
			dbDriver:       "sqlite",
			retrieveDriver: "",
			wantErr:        false,
		},
		{
			name:           "mysql with keyword-only engine (elasticsearch_v7) rejected — no vector capability",
			dbDriver:       "mysql",
			retrieveDriver: "elasticsearch_v7",
			wantErr:        true,
		},
		{
			name:           "mysql with mixed keyword+vector engines accepted",
			dbDriver:       "mysql",
			retrieveDriver: "elasticsearch_v7, qdrant",
			wantErr:        false,
		},
		{
			name:           "empty dbDriver accepted (other validation handles it)",
			dbDriver:       "",
			retrieveDriver: "postgres",
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDriverCombination(tt.dbDriver, tt.retrieveDriver)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error %q should contain %q", err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			}
		})
	}
}

// The error message must name at least one valid external retrieval
// engine so the operator knows how to fix the config - not just "you
// can't do that". The names must match the actual registry in
// internal/types/tenant.go (retrieverEngineMapping) so the operator
// doesn't try an engine that the system doesn't recognise.
func TestValidateDriverCombination_ErrorIsActionable(t *testing.T) {
	err := ValidateDriverCombination("mysql", "postgres")
	if err == nil {
		t.Fatal("expected error for mysql+postgres")
	}
	if strings.Contains(err.Error(), "elasticsearch_v7") {
		t.Fatalf("error must not suggest a keyword-only engine: %v", err)
	}
	// Must mention at least one external engine the operator can switch to.
	for _, hint := range []string{"qdrant", "milvus", "elasticsearch_v8", "opensearch"} {
		if strings.Contains(err.Error(), hint) {
			return // good - at least one fix-it hint present
		}
	}
	t.Fatalf("error message must suggest a real external engine; got: %v", err)
}

// The error message must NOT suggest engines that are not in the
// retriever registry - otherwise the operator will set RETRIEVE_DRIVER
// to a value the system rejects downstream.
func TestValidateDriverCombination_ErrorDoesNotMentionBogusEngines(t *testing.T) {
	err := ValidateDriverCombination("mysql", "postgres")
	if err == nil {
		t.Fatal("expected error for mysql+postgres")
	}
	// vikingdb / tcvectordb were in the old error string but are not
	// keys in retrieverEngineMapping (the real key is tencent_vectordb).
	for _, bogus := range []string{"vikingdb", "tcvectordb"} {
		if strings.Contains(err.Error(), bogus) {
			t.Fatalf("error message must not mention bogus engine %q (not in registry); got: %v", bogus, err)
		}
	}
}

// ParseRetrieveDrivers is the shared normaliser for the RETRIEVE_DRIVER env
// value, used by both ValidateDriverCombination and the registry registration
// in container.initRetrieveEngineRegistry. It must split on commas, trim
// surrounding whitespace from each entry, drop empties, and de-dupe so a
// trailing comma or accidental duplicate (e.g. "qdrant, qdrant") does not
// produce a phantom empty driver or register the same engine twice.
func TestParseRetrieveDrivers(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "simple single driver",
			in:   "qdrant",
			want: []string{"qdrant"},
		},
		{
			name: "comma-separated list trims whitespace",
			in:   "  qdrant  ,  milvus  ",
			want: []string{"qdrant", "milvus"},
		},
		{
			name: "trailing comma drops empty entry",
			in:   "qdrant,",
			want: []string{"qdrant"},
		},
		{
			name: "leading comma drops empty entry",
			in:   ",qdrant",
			want: []string{"qdrant"},
		},
		{
			name: "duplicate drivers are de-duped preserving first-seen order",
			in:   "qdrant, milvus, qdrant, milvus",
			want: []string{"qdrant", "milvus"},
		},
		{
			name: "whitespace-only entries dropped",
			in:   "qdrant,   ,milvus",
			want: []string{"qdrant", "milvus"},
		},
		{
			name: "empty string yields nil (no drivers)",
			in:   "",
			want: nil,
		},
		{
			name: "all-whitespace string yields nil",
			in:   "   ,   ",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRetrieveDrivers(tt.in)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !equalStringSlices(got, tt.want) {
				t.Fatalf("ParseRetrieveDrivers(%q) = %v; want %v", tt.in, got, tt.want)
			}
		})
	}
}

// equalStringSlices is a small helper because reflect.DeepEqual on nil vs
// []string{} would make the "empty" cases brittle.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
