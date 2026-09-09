package qdrant

import (
	"testing"
	"unicode/utf8"
)

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
