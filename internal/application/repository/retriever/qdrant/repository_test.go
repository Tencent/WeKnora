package qdrant

import (
	"strconv"
	"testing"
	"unicode/utf8"
)

func TestMatchesCollectionName(t *testing.T) {
	tests := []struct {
		name       string
		base       string
		collection string
		want       bool
	}{
		{"minimum dimension", "embeddings", "embeddings_1", true},
		{"typical dimension", "weknora_embeddings", "weknora_embeddings_768", true},
		{"custom base", "team_42_vectors", "team_42_vectors_1536", true},
		{"base ending in underscore", "vectors_", "vectors__768", true},
		{"longer base", "vectors", "vectors_extra_768", false},
		{"backup collection", "weknora_embeddings", "weknora_embeddings_backup_768", false},
		{"backup suffix", "vectors", "vectors_768_backup", false},
		{"missing separator", "vectors", "vectors768", false},
		{"missing dimension", "vectors", "vectors_", false},
		{"base only", "vectors", "vectors", false},
		{"short name", "vectors", "vec", false},
		{"empty name", "vectors", "", false},
		{"different base", "vectors", "other_768", false},
		{"case mismatch", "vectors", "Vectors_768", false},
		{"zero dimension", "vectors", "vectors_0", false},
		{"negative dimension", "vectors", "vectors_-768", false},
		{"explicit plus", "vectors", "vectors_+768", false},
		{"leading zero", "vectors", "vectors_0768", false},
		{"fraction", "vectors", "vectors_7.68", false},
		{"non decimal", "vectors", "vectors_0x300", false},
		{"leading space", "vectors", "vectors_ 768", false},
		{"trailing space", "vectors", "vectors_768 ", false},
		{"unicode digits", "vectors", "vectors_７６８", false},
		{"overflow", "vectors", "vectors_999999999999999999999999999999999", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &qdrantRepository{collectionBaseName: tt.base}
			if got := repo.matchesCollectionName(tt.collection); got != tt.want {
				t.Errorf("matchesCollectionName(%q) = %v, want %v", tt.collection, got, tt.want)
			}
		})
	}
}

func TestMatchesGeneratedCollectionNames(t *testing.T) {
	repo := &qdrantRepository{collectionBaseName: "custom_vectors"}
	for _, dimension := range []int{1, 384, 768, 1536, int(^uint(0) >> 1)} {
		t.Run(strconv.Itoa(dimension), func(t *testing.T) {
			name := repo.getCollectionName(dimension)
			if !repo.matchesCollectionName(name) {
				t.Errorf("generated collection name %q was rejected", name)
			}
		})
	}
}

func TestNewQdrantValueMapSanitizesInvalidUTF8AndNUL(t *testing.T) {
	malformed := "prefix" + string([]byte{0xff}) + "\x00suffix"
	payload := newQdrantValueMap(map[string]any{
		fieldContent:    malformed,
		fieldSourceType: int64(1),
		fieldIsEnabled:  true,
	})

	got := payload[fieldContent].GetStringValue()
	if got != "prefixsuffix" {
		t.Fatalf("unexpected sanitized content: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("sanitized content is not valid UTF-8: % x", []byte(got))
	}
	if gotSourceType := payload[fieldSourceType].GetIntegerValue(); gotSourceType != 1 {
		t.Fatalf("source type changed: got %d, want 1", gotSourceType)
	}
	if gotEnabled := payload[fieldIsEnabled].GetBoolValue(); !gotEnabled {
		t.Fatal("is_enabled changed during payload sanitization")
	}
}

func TestNewQdrantValueMapPreservesValidUTF8(t *testing.T) {
	valid := "valid UTF-8 中文内容"
	payload := newQdrantValueMap(map[string]any{
		fieldContent: valid,
	})

	got := payload[fieldContent].GetStringValue()
	if got != valid {
		t.Fatalf("valid content changed: got %q, want %q", got, valid)
	}
}

func TestCreatePayloadSanitizesAllStringFields(t *testing.T) {
	malformed := "a" + string([]byte{0xff}) + "\x00b"
	embedding := &QdrantVectorEmbedding{
		Content:         malformed,
		SourceID:        malformed,
		SourceType:      2,
		ChunkID:         malformed,
		KnowledgeID:     malformed,
		KnowledgeBaseID: malformed,
		TagID:           malformed,
		IsEnabled:       true,
	}

	payload := createPayload(embedding)
	stringFields := []string{
		fieldContent,
		fieldSourceID,
		fieldChunkID,
		fieldKnowledgeID,
		fieldKnowledgeBaseID,
		fieldTagID,
	}
	for _, field := range stringFields {
		got := payload[field].GetStringValue()
		if got != "ab" {
			t.Errorf("%s was not sanitized correctly: got %q", field, got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s remains invalid UTF-8: % x", field, []byte(got))
		}
	}
	if got := payload[fieldSourceType].GetIntegerValue(); got != 2 {
		t.Errorf("source type changed: got %d, want 2", got)
	}
	if got := payload[fieldIsEnabled].GetBoolValue(); !got {
		t.Error("is_enabled changed during payload creation")
	}
}
