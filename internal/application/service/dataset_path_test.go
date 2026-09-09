package service

import (
	"path/filepath"
	"testing"
)

func TestDatasetDirectory(t *testing.T) {
	tests := []struct {
		id      string
		want    string
		wantErr bool
	}{
		{id: "", want: filepath.FromSlash("dataset/samples")},
		{id: "default", want: filepath.FromSlash("dataset/samples")},
		{id: "topic3-10", want: filepath.Join("codex", "topic3", "datasets", "topic3-10")},
		{id: "../secret", wantErr: true},
		{id: "topic3/other", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got, err := datasetDirectory(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("datasetDirectory(%q) expected error, got %q", tt.id, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("datasetDirectory(%q): %v", tt.id, err)
			}
			if filepath.Clean(got) != filepath.Clean(tt.want) {
				t.Fatalf("datasetDirectory(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestTopic3DatasetLoadsTenStablePairs(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "codex", "topic3", "datasets", "topic3-10")
	dataset, err := loadDataset(dir)
	if err != nil {
		t.Fatalf("loadDataset(%q): %v", dir, err)
	}
	pairs := dataset.Iterate()
	if len(pairs) != 10 {
		t.Fatalf("loaded %d pairs, want 10", len(pairs))
	}
	seen := make(map[int]bool, len(pairs))
	for _, pair := range pairs {
		seen[pair.QID] = true
	}
	for id := 1; id <= 10; id++ {
		if !seen[id] {
			t.Fatalf("missing stable question id %d", id)
		}
	}
}
