package qdrant

import (
	"errors"
	"testing"

	"github.com/qdrant/go-client/qdrant"
)

func TestNeedsPayloadIndex(t *testing.T) {
	tests := []struct {
		name string
		info *qdrant.CollectionInfo
		want bool
	}{
		{name: "nil collection info", want: true},
		{name: "missing field", info: &qdrant.CollectionInfo{}, want: true},
		{
			name: "indexed field",
			info: &qdrant.CollectionInfo{
				PayloadSchema: map[string]*qdrant.PayloadSchemaInfo{
					fieldTagID: {DataType: qdrant.PayloadSchemaType_Keyword},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsPayloadIndex(tt.info, fieldTagID); got != tt.want {
				t.Fatalf("needsPayloadIndex() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainsQdrantAlreadyExistsMessage(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "qdrant conflict", err: errors.New("collection already exists"), want: true},
		{name: "snake case conflict", err: errors.New("resource_already_exists_exception"), want: true},
		{name: "unrelated error", err: errors.New("permission denied"), want: false},
		{name: "nil", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsQdrantAlreadyExistsMessage(tt.err); got != tt.want {
				t.Fatalf("containsQdrantAlreadyExistsMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}
