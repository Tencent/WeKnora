package mysql

import (
	"reflect"
	"strings"
	"testing"
)

func TestEmbeddingJSONRoundTrip(t *testing.T) {
	in := []float32{0.125, -2.5, 3}

	raw := embeddingToJSON(in)
	got, err := parseEmbeddingJSON([]byte(raw))
	if err != nil {
		t.Fatalf("parseEmbeddingJSON() error = %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("parseEmbeddingJSON() = %#v, want %#v", got, in)
	}
}

func TestEmbeddingLiteral(t *testing.T) {
	got := embeddingLiteral([]float32{1, 0.5, -2})
	if got != "JSON_ARRAY(1,0.5,-2)" {
		t.Fatalf("embeddingLiteral() = %q", got)
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

func TestCosineSimilarityExprUsesPortableJSONFunctions(t *testing.T) {
	got := cosineSimilarityExpr("embedding", []float32{1, 2})
	for _, want := range []string{
		"JSON_EXTRACT(embedding, '$[0]')",
		"JSON_EXTRACT(embedding, '$[1]')",
		"SQRT",
		"CASE WHEN",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("cosineSimilarityExpr() missing %q in %s", want, got)
		}
	}
	for _, forbidden := range []string{"COSINE_DISTANCE", "JSON_ARRAY_PACK", "DOT_PRODUCT"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("cosineSimilarityExpr() should not depend on %s: %s", forbidden, got)
		}
	}
}
