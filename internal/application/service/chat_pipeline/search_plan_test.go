package chatpipeline

import "testing"

func TestShouldFallbackToWeb(t *testing.T) {
	tests := []struct {
		name  string
		count int
		topK  int
		want  bool
	}{
		{"empty recall", 0, 10, true},
		{"thin recall", 2, 10, true},
		{"enough recall", 3, 10, false},
		{"small top k", 1, 1, false},
		{"zero top k uses safe minimum", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldFallbackToWeb(tt.count, tt.topK); got != tt.want {
				t.Fatalf("shouldFallbackToWeb(%d, %d) = %v, want %v", tt.count, tt.topK, got, tt.want)
			}
		})
	}
}
