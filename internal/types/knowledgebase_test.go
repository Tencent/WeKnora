package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseProviderScheme(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"local://tenant/file.pdf", "local"},
		{"minio://bucket/key", "minio"},
		{"cos://bucket/key", "cos"},
		{"tos://bucket/key", "tos"},
		{"s3://bucket/key", "s3"},
		{"s3://my-bucket/weknora/123/exports/abc.png", "s3"},
		{"https://example.com/img.png", ""},
		{"http://localhost:9000/bucket/key", ""},
		{"/data/files/images/abc.png", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseProviderScheme(tt.input)
			if got != tt.want {
				t.Errorf("ParseProviderScheme(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestInferStorageFromFilePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"local://tenant/file.pdf", "local"},
		{"minio://bucket/key", "minio"},
		{"cos://bucket/key", "cos"},
		{"tos://bucket/key", "tos"},
		{"s3://bucket/key", "s3"},
		{"https://my-bucket.cos.ap-guangzhou.myqcloud.com/key", "cos"},
		{"https://example.com/img.png", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := InferStorageFromFilePath(tt.input)
			if got != tt.want {
				t.Errorf("InferStorageFromFilePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// strPtr returns a pointer to the given string, used to express *string literals in tests.
func strPtr(s string) *string { return &s }

// TestKnowledgeBase_VectorStoreID_JSON covers (un)marshaling of the nullable
// VectorStoreID pointer, including the `omitempty` behavior and the three
// distinct JSON inputs (missing field, explicit null, explicit value).
//
// The `empty string pointer` case is fixed here as a behavior snapshot: PR1
// accepts it and stores &"" verbatim because validation has not been added
// yet. A follow-up PR that introduces validation will convert this case into
// a rejection and flip the expectation in this test accordingly.
func TestKnowledgeBase_VectorStoreID_JSON(t *testing.T) {
	t.Run("marshal nil omits field (omitempty)", func(t *testing.T) {
		kb := KnowledgeBase{ID: "kb-1", VectorStoreID: nil}
		b, err := json.Marshal(kb)
		if err != nil {
			t.Fatalf("json.Marshal returned error: %v", err)
		}
		if strings.Contains(string(b), `"vector_store_id"`) {
			t.Errorf("expected vector_store_id to be omitted when nil, got: %s", b)
		}
	})

	t.Run("marshal non-nil includes value", func(t *testing.T) {
		kb := KnowledgeBase{ID: "kb-1", VectorStoreID: strPtr("store-uuid")}
		b, err := json.Marshal(kb)
		if err != nil {
			t.Fatalf("json.Marshal returned error: %v", err)
		}
		if !strings.Contains(string(b), `"vector_store_id":"store-uuid"`) {
			t.Errorf("expected vector_store_id to be serialized, got: %s", b)
		}
	})

	unmarshalCases := []struct {
		name       string
		body       string
		wantNil    bool
		wantValue  string
	}{
		{name: "missing field", body: `{"id":"kb-1"}`, wantNil: true},
		{name: "explicit null", body: `{"id":"kb-1","vector_store_id":null}`, wantNil: true},
		{name: "explicit value", body: `{"id":"kb-1","vector_store_id":"store-uuid"}`, wantValue: "store-uuid"},
		{name: "empty string pointer", body: `{"id":"kb-1","vector_store_id":""}`, wantValue: ""},
	}
	for _, tt := range unmarshalCases {
		t.Run(tt.name, func(t *testing.T) {
			var kb KnowledgeBase
			if err := json.Unmarshal([]byte(tt.body), &kb); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v", err)
			}
			if tt.wantNil {
				if kb.VectorStoreID != nil {
					t.Errorf("expected VectorStoreID to be nil, got &%q", *kb.VectorStoreID)
				}
				return
			}
			if kb.VectorStoreID == nil {
				t.Fatalf("expected VectorStoreID to be &%q, got nil", tt.wantValue)
			}
			if *kb.VectorStoreID != tt.wantValue {
				t.Errorf("expected VectorStoreID = %q, got %q", tt.wantValue, *kb.VectorStoreID)
			}
		})
	}
}

// TestKnowledgeBase_UnmarshalJSON_WithVectorStoreID verifies that the custom
// UnmarshalJSON on KnowledgeBase (which shadows cos_config for legacy
// compatibility) still delegates the new vector_store_id field to the alias
// type path, without interfering with StorageProviderConfig inference.
func TestKnowledgeBase_UnmarshalJSON_WithVectorStoreID(t *testing.T) {
	// Legacy cos_config + new vector_store_id in the same payload: both must map correctly.
	body := `{
		"id": "kb-1",
		"cos_config": {"provider": "cos", "bucket_name": "legacy-bucket"},
		"vector_store_id": "store-uuid"
	}`

	var kb KnowledgeBase
	if err := json.Unmarshal([]byte(body), &kb); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if kb.VectorStoreID == nil || *kb.VectorStoreID != "store-uuid" {
		t.Errorf("expected VectorStoreID = &\"store-uuid\", got %v", kb.VectorStoreID)
	}
	if kb.StorageConfig.Provider != "cos" {
		t.Errorf("expected legacy StorageConfig.Provider = cos, got %q", kb.StorageConfig.Provider)
	}
	if kb.StorageProviderConfig == nil || kb.StorageProviderConfig.Provider != "cos" {
		t.Errorf("expected StorageProviderConfig.Provider auto-populated from cos_config, got %v", kb.StorageProviderConfig)
	}

	// Regression guard: the aux struct inside UnmarshalJSON must not shadow vector_store_id.
	// If a future change introduces such a shadow, the value above would fail to populate.
}

