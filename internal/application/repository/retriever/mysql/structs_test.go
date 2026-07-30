package mysql

import (
	"math"
	"reflect"
	"testing"
)

func TestEmbeddingJSONRoundTrip(t *testing.T) {
	in := []float32{0.125, -2.5, 3}

	raw, err := embeddingToJSON(in)
	if err != nil {
		t.Fatalf("embeddingToJSON() error = %v", err)
	}
	got, err := parseEmbeddingJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parseEmbeddingJSON() error = %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("parseEmbeddingJSON() = %#v, want %#v", got, in)
	}
}

func TestEmbeddingToJSONRejectsNonFiniteValues(t *testing.T) {
	if _, err := embeddingToJSON([]float32{float32(math.NaN())}); err == nil {
		t.Fatal("embeddingToJSON() accepted NaN")
	}
}

func TestTableNameUsesDimension(t *testing.T) {
	repo := &mysqlRepository{tablePrefix: defaultTablePrefix}
	if got := repo.getTableName(768); got != "weknora_embeddings_768" {
		t.Fatalf("getTableName() = %q", got)
	}
}

func TestNormalizeTablePrefix(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "default", in: "", want: defaultTablePrefix},
		{name: "adds suffix", in: "custom", want: "custom_"},
		{name: "keeps suffix", in: "custom_", want: "custom_"},
		{name: "trims whitespace", in: "  custom  ", want: "custom_"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeTablePrefix(tt.in); got != tt.want {
				t.Fatalf("normalizeTablePrefix(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestQuoteIdentifierEscapesBackticks(t *testing.T) {
	if got := quoteIdentifier("bad`name"); got != "`bad``name`" {
		t.Fatalf("quoteIdentifier() = %q", got)
	}
}

func TestEscapeLikePatternEscapesPrefixWildcards(t *testing.T) {
	if got := escapeLikePattern(`weknora_embeddings_%\`); got != `weknora\_embeddings\_\%\\` {
		t.Fatalf("escapeLikePattern() = %q", got)
	}
}

func TestCosineSimilarity(t *testing.T) {
	query := []float32{1, 2, 3}
	queryNorm, err := validateQueryEmbedding(query)
	if err != nil {
		t.Fatalf("validateQueryEmbedding() error = %v", err)
	}
	score, err := cosineSimilarity(query, []float32{1, 2, 3}, queryNorm)
	if err != nil {
		t.Fatalf("cosineSimilarity() error = %v", err)
	}
	if math.Abs(score-1) > 1e-12 {
		t.Fatalf("cosineSimilarity() = %.16f, want 1", score)
	}

	score, err = cosineSimilarity(query, []float32{0, 0, 0}, queryNorm)
	if err != nil {
		t.Fatalf("zero cosineSimilarity() error = %v", err)
	}
	if score != 0 {
		t.Fatalf("zero cosineSimilarity() = %v, want 0", score)
	}

	if _, err := cosineSimilarity(query, []float32{1, 2}, queryNorm); err == nil {
		t.Fatal("cosineSimilarity() accepted a dimension mismatch")
	}
}
