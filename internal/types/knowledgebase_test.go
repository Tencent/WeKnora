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
// The `empty string pointer` case is fixed here as a behavior snapshot:
// raw JSON encoding accepts it and stores &"" verbatim. Service-layer
// CreateKnowledgeBase normalizes &"" to nil before persistence
// (see (*KnowledgeBase).Normalize), so the stored DB row is NULL — but the
// JSON shape on the way in is whatever the client sent.
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
		name      string
		body      string
		wantNil   bool
		wantValue string
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

// TestKnowledgeBase_HasVectorStore covers the nil-safe binding accessor.
func TestKnowledgeBase_HasVectorStore(t *testing.T) {
	t.Run("nil receiver returns false", func(t *testing.T) {
		var kb *KnowledgeBase
		if kb.HasVectorStore() {
			t.Fatal("nil receiver should return false")
		}
	})
	t.Run("nil pointer returns false", func(t *testing.T) {
		kb := &KnowledgeBase{}
		if kb.HasVectorStore() {
			t.Fatal("nil VectorStoreID should return false")
		}
	})
	t.Run("empty string returns false", func(t *testing.T) {
		empty := ""
		kb := &KnowledgeBase{VectorStoreID: &empty}
		if kb.HasVectorStore() {
			t.Fatal("empty string VectorStoreID should return false")
		}
	})
	t.Run("non-empty returns true", func(t *testing.T) {
		s := "store-uuid"
		kb := &KnowledgeBase{VectorStoreID: &s}
		if !kb.HasVectorStore() {
			t.Fatal("non-empty VectorStoreID should return true")
		}
	})
}

// TestKnowledgeBase_Normalize covers the empty-string -> nil fold used to
// keep a single representation between the create path and the factory.
func TestKnowledgeBase_Normalize(t *testing.T) {
	t.Run("nil receiver is no-op", func(t *testing.T) {
		var kb *KnowledgeBase
		kb.Normalize() // must not panic
	})
	t.Run("empty string -> nil", func(t *testing.T) {
		empty := ""
		kb := &KnowledgeBase{VectorStoreID: &empty}
		kb.Normalize()
		if kb.VectorStoreID != nil {
			t.Fatalf("expected nil after normalize, got %v", kb.VectorStoreID)
		}
	})
	t.Run("non-empty unchanged", func(t *testing.T) {
		s := "store-uuid"
		kb := &KnowledgeBase{VectorStoreID: &s}
		kb.Normalize()
		if kb.VectorStoreID == nil || *kb.VectorStoreID != "store-uuid" {
			t.Fatalf("expected store-uuid, got %v", kb.VectorStoreID)
		}
	})
	t.Run("already nil unchanged", func(t *testing.T) {
		kb := &KnowledgeBase{}
		kb.Normalize()
		if kb.VectorStoreID != nil {
			t.Fatal("nil should stay nil")
		}
	})
	t.Run("idempotent", func(t *testing.T) {
		empty := ""
		kb := &KnowledgeBase{VectorStoreID: &empty}
		kb.Normalize()
		kb.Normalize() // second call no-op
		if kb.VectorStoreID != nil {
			t.Fatal("idempotent normalize broke")
		}
	})
}

// TestKnowledgeBase_SharesStoreWith covers the binding-equality helper used
// by CopyKnowledgeBase's same-store defense. The empty-string cases are a
// regression guard: rows persisted by callers that did not run Normalize
// first can carry `vector_store_id = ""` instead of NULL; without the
// normalization inside SharesStoreWith such rows would falsely fail
// same-store clone checks.
func TestKnowledgeBase_SharesStoreWith(t *testing.T) {
	mk := func(p *string) *KnowledgeBase { return &KnowledgeBase{VectorStoreID: p} }
	a, b := "store-A", "store-B"
	empty := ""
	tests := []struct {
		name string
		kb   *KnowledgeBase
		oth  *KnowledgeBase
		want bool
	}{
		{"both nil receivers", nil, nil, true},
		{"one nil receiver", nil, mk(nil), false},
		{"both nil store IDs", mk(nil), mk(nil), true},
		{"left nil store ID", mk(nil), mk(&a), false},
		{"right nil store ID", mk(&a), mk(nil), false},
		{"same store IDs", mk(&a), mk(&a), true},
		{"different store IDs", mk(&a), mk(&b), false},

		// empty-string == nil normalization (regression guard)
		{"left empty-string vs right nil", mk(&empty), mk(nil), true},
		{"left nil vs right empty-string", mk(nil), mk(&empty), true},
		{"both empty-string", mk(&empty), mk(&empty), true},
		{"left empty-string vs right UUID", mk(&empty), mk(&a), false},
		{"left UUID vs right empty-string", mk(&a), mk(&empty), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.kb.SharesStoreWith(tt.oth); got != tt.want {
				t.Fatalf("SharesStoreWith: got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEffectiveStorageProvider(t *testing.T) {
	tests := []struct {
		name          string
		kbProvider    string
		tenantDefault string
		want          string
	}{
		{"kb pins provider", "minio", "cos", "minio"},
		{"kb empty falls back to tenant default", "", "cos", "cos"},
		{"both empty", "", "", ""},
		{"tenant default cased", "", "  COS ", "cos"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := &KnowledgeBase{}
			if tt.kbProvider != "" {
				kb.StorageProviderConfig = &StorageProviderConfig{Provider: tt.kbProvider}
			}
			if got := kb.EffectiveStorageProvider(tt.tenantDefault); got != tt.want {
				t.Errorf("EffectiveStorageProvider(%q) with kb=%q = %q, want %q",
					tt.tenantDefault, tt.kbProvider, got, tt.want)
			}
		})
	}
}

// TestEffectiveStorageProvider_CrossBackendDetection documents the comparison the
// clone preflight performs: a mismatch is only flagged when both effective
// providers are non-empty and differ.
func TestEffectiveStorageProvider_CrossBackendDetection(t *testing.T) {
	tenantDefault := "minio"
	src := &KnowledgeBase{} // inherits tenant default -> minio
	dst := &KnowledgeBase{StorageProviderConfig: &StorageProviderConfig{Provider: "cos"}}

	sp := src.EffectiveStorageProvider(tenantDefault)
	dp := dst.EffectiveStorageProvider(tenantDefault)
	if sp == "" || dp == "" || sp == dp {
		t.Fatalf("expected cross-backend mismatch, got src=%q dst=%q", sp, dp)
	}

	// Same effective provider (dst empty inherits the same tenant default) must NOT be flagged.
	dstSame := &KnowledgeBase{}
	if dstSame.EffectiveStorageProvider(tenantDefault) != sp {
		t.Errorf("same tenant default should match: got %q vs %q",
			dstSame.EffectiveStorageProvider(tenantDefault), sp)
	}
}
