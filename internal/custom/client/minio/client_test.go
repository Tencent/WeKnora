package minio

import (
	"errors"
	"testing"
)

func TestNormalizeMultipartPartsSortsByPartNumber(t *testing.T) {
	parts, err := normalizeMultipartParts([]CompletePart{
		{PartNumber: 3, ETag: `"etag-3"`},
		{PartNumber: 1, ETag: "etag-1"},
		{PartNumber: 2, ETag: " etag-2 "},
	})
	if err != nil {
		t.Fatalf("normalizeMultipartParts() error = %v", err)
	}

	want := []CompletePart{
		{PartNumber: 1, ETag: "etag-1"},
		{PartNumber: 2, ETag: "etag-2"},
		{PartNumber: 3, ETag: "etag-3"},
	}
	if len(parts) != len(want) {
		t.Fatalf("len(parts) = %d, want %d", len(parts), len(want))
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("parts[%d] = %#v, want %#v", i, parts[i], want[i])
		}
	}
}

func TestNormalizeMultipartPartsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		parts []CompletePart
	}{
		{
			name:  "empty parts",
			parts: nil,
		},
		{
			name: "non-positive part number",
			parts: []CompletePart{
				{PartNumber: 0, ETag: "etag-1"},
			},
		},
		{
			name: "duplicate part number",
			parts: []CompletePart{
				{PartNumber: 1, ETag: "etag-1"},
				{PartNumber: 1, ETag: "etag-1-retry"},
			},
		},
		{
			name: "empty etag",
			parts: []CompletePart{
				{PartNumber: 1, ETag: "   "},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeMultipartParts(tt.parts)
			if !errors.Is(err, ErrInvalidMultipartParts) {
				t.Fatalf("error = %v, want ErrInvalidMultipartParts", err)
			}
		})
	}
}
