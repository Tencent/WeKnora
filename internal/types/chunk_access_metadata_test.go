package types

import "testing"

func TestChunkAccessMetadataExtractsReservedObject(t *testing.T) {
	metadata := JSON(`{"access_metadata":{"department":"research","employee_nature":"formal"},` +
		`"generated_questions":["unrelated"]}`)
	chunk := &Chunk{Metadata: metadata}

	got, err := chunk.AccessMetadata()
	if err != nil {
		t.Fatalf("AccessMetadata() error = %v", err)
	}
	if got["department"] != "research" || got["employee_nature"] != "formal" {
		t.Fatalf("AccessMetadata() = %#v, want reserved object values", got)
	}
	if _, ok := got["generated_questions"]; ok {
		t.Fatalf("AccessMetadata() leaked unrelated chunk metadata: %#v", got)
	}
}

func TestChunkAccessMetadataReturnsEmptyMapWhenReservedObjectIsMissing(t *testing.T) {
	for _, chunk := range []*Chunk{
		{},
		{Metadata: JSON(`{"generated_questions":["unrelated"]}`)},
	} {
		got, err := chunk.AccessMetadata()
		if err != nil {
			t.Fatalf("AccessMetadata() error = %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("AccessMetadata() = %#v, want empty map", got)
		}
	}
}

func TestChunkAccessMetadataRejectsMalformedOrNonObjectReservedValue(t *testing.T) {
	for _, metadata := range []JSON{
		JSON(`{"access_metadata":"research"}`),
		JSON(`{"access_metadata":["research"]}`),
		JSON(`{"access_metadata":null}`),
		JSON(`{"access_metadata":`),
	} {
		chunk := &Chunk{Metadata: metadata}
		if _, err := chunk.AccessMetadata(); err == nil {
			t.Fatalf("AccessMetadata() accepted invalid metadata %s", metadata)
		}
	}
}

func TestChunkMetadataSettersPreserveReservedAccessMetadata(t *testing.T) {
	tests := []struct {
		name string
		set  func(*Chunk) error
	}{
		{
			name: "document metadata",
			set: func(chunk *Chunk) error {
				return chunk.SetDocumentMetadata(&DocumentChunkMetadata{
					GeneratedQuestions: []GeneratedQuestion{{ID: "q1", Question: "question"}},
				})
			},
		},
		{
			name: "FAQ metadata",
			set: func(chunk *Chunk) error {
				return chunk.SetFAQMetadata(&FAQChunkMetadata{StandardQuestion: "question"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := JSON(`{"access_metadata":{"department":"research"},` +
				`"generated_questions":["old"]}`)
			chunk := &Chunk{Metadata: metadata}
			if err := tt.set(chunk); err != nil {
				t.Fatalf("set metadata: %v", err)
			}
			got, err := chunk.AccessMetadata()
			if err != nil {
				t.Fatalf("AccessMetadata() error = %v", err)
			}
			if got["department"] != "research" {
				t.Fatalf("AccessMetadata() = %#v, want department=research", got)
			}
		})
	}
}

func TestChunkMetadataSettersNilPreserveOnlyReservedAccessMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata JSON
		set      func(*Chunk) error
	}{
		{
			name: "document metadata",
			metadata: JSON(`{"access_metadata":{"department":"research"},` +
				`"generated_questions":[{"id":"old","question":"old"}]}`),
			set: func(chunk *Chunk) error { return chunk.SetDocumentMetadata(nil) },
		},
		{
			name: "FAQ metadata",
			metadata: JSON(`{"access_metadata":{"department":"research"},` +
				`"standard_question":"old","version":2}`),
			set: func(chunk *Chunk) error { return chunk.SetFAQMetadata(nil) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk := &Chunk{
				Metadata:    tt.metadata,
				ContentHash: "old-hash",
			}
			if err := tt.set(chunk); err != nil {
				t.Fatalf("clear metadata: %v", err)
			}
			accessMetadata, err := chunk.AccessMetadata()
			if err != nil {
				t.Fatalf("AccessMetadata() error = %v", err)
			}
			if accessMetadata["department"] != "research" {
				t.Fatalf("AccessMetadata() = %#v, want reserved department", accessMetadata)
			}
			persisted, err := chunk.Metadata.Map()
			if err != nil {
				t.Fatalf("metadata map: %v", err)
			}
			if len(persisted) != 1 {
				t.Fatalf("cleared metadata = %#v, want only access_metadata", persisted)
			}
			if tt.name == "FAQ metadata" && chunk.ContentHash != "" {
				t.Fatalf("FAQ ContentHash = %q, want empty", chunk.ContentHash)
			}
		})
	}
}

func TestChunkMetadataSettersNilPreserveUnownedExtensionFields(t *testing.T) {
	tests := []struct {
		name     string
		metadata JSON
		set      func(*Chunk) error
		removed  []string
	}{
		{
			name: "document metadata",
			metadata: JSON(`{"access_metadata":{"department":"research"},"label":"visible",` +
				`"generated_questions":[{"id":"old","question":"old"}],"generated_questions_revision":2}`),
			set:     func(chunk *Chunk) error { return chunk.SetDocumentMetadata(nil) },
			removed: []string{"generated_questions", "generated_questions_revision"},
		},
		{
			name: "FAQ metadata",
			metadata: JSON(`{"access_metadata":{"department":"research"},"label":"visible",` +
				`"standard_question":"old","similar_questions":["old"],"negative_questions":["old"],` +
				`"answers":["old"],"answer_strategy":"all","version":2,"source":"test"}`),
			set: func(chunk *Chunk) error { return chunk.SetFAQMetadata(nil) },
			removed: []string{
				"standard_question",
				"similar_questions",
				"negative_questions",
				"answers",
				"answer_strategy",
				"version",
				"source",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk := &Chunk{Metadata: tt.metadata, ContentHash: "old-hash"}
			if err := tt.set(chunk); err != nil {
				t.Fatalf("clear metadata: %v", err)
			}
			persisted, err := chunk.Metadata.Map()
			if err != nil {
				t.Fatalf("metadata map: %v", err)
			}
			if persisted["label"] != "visible" {
				t.Fatalf("label = %#v, want visible", persisted["label"])
			}
			if _, ok := persisted["access_metadata"]; !ok {
				t.Fatalf("metadata = %#v, want access_metadata", persisted)
			}
			for _, field := range tt.removed {
				if _, ok := persisted[field]; ok {
					t.Fatalf("metadata = %#v, want %q removed", persisted, field)
				}
			}
			if tt.name == "FAQ metadata" && chunk.ContentHash != "" {
				t.Fatalf("FAQ ContentHash = %q, want empty", chunk.ContentHash)
			}
		})
	}
}

func TestChunkMetadataSettersNilClearMetadataWithoutReservedAccessObject(t *testing.T) {
	for _, tt := range []struct {
		metadata JSON
		set      func(*Chunk) error
	}{
		{
			metadata: JSON(`{"generated_questions":[{"id":"old","question":"old"}],` +
				`"generated_questions_revision":2}`),
			set: func(chunk *Chunk) error { return chunk.SetDocumentMetadata(nil) },
		},
		{
			metadata: JSON(`{"standard_question":"old","similar_questions":["old"],"version":2}`),
			set:      func(chunk *Chunk) error { return chunk.SetFAQMetadata(nil) },
		},
	} {
		chunk := &Chunk{Metadata: tt.metadata}
		if err := tt.set(chunk); err != nil {
			t.Fatalf("clear metadata: %v", err)
		}
		if len(chunk.Metadata) != 0 {
			t.Fatalf("metadata without reserved access object = %s, want empty", chunk.Metadata)
		}
	}
}
