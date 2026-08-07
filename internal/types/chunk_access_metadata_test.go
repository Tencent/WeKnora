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
		name string
		set  func(*Chunk) error
	}{
		{name: "document metadata", set: func(chunk *Chunk) error { return chunk.SetDocumentMetadata(nil) }},
		{name: "FAQ metadata", set: func(chunk *Chunk) error { return chunk.SetFAQMetadata(nil) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk := &Chunk{
				Metadata:    JSON(`{"access_metadata":{"department":"research"},"generated_questions":["old"]}`),
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

func TestChunkMetadataSettersNilClearMetadataWithoutReservedAccessObject(t *testing.T) {
	for _, set := range []func(*Chunk) error{
		func(chunk *Chunk) error { return chunk.SetDocumentMetadata(nil) },
		func(chunk *Chunk) error { return chunk.SetFAQMetadata(nil) },
	} {
		chunk := &Chunk{Metadata: JSON(`{"generated_questions":["old"]}`)}
		if err := set(chunk); err != nil {
			t.Fatalf("clear metadata: %v", err)
		}
		if len(chunk.Metadata) != 0 {
			t.Fatalf("metadata without reserved access object = %s, want empty", chunk.Metadata)
		}
	}
}
