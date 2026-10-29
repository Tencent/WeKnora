package retriever

import (
	"math"
	"testing"
)

func TestProcessingCacheVectorPayloadValidation(t *testing.T) {
	for _, payload := range []string{"null", "[]", "[null]", "[1,2]", "[1e100]", "[NaN]", "{}", "[1] garbage"} {
		if _, err := decodeCachedVector([]byte(payload), 1); err == nil {
			t.Errorf("accepted %s", payload)
		}
	}
	for _, vector := range [][]float32{{float32(math.NaN())}, {float32(math.Inf(1))}, {float32(math.Inf(-1))}, nil, {1, 2}} {
		if err := validateVector(vector, 1); err == nil {
			t.Errorf("accepted %v", vector)
		}
	}
	if _, err := decodeCachedVector([]byte("[1.25]"), 1); err != nil {
		t.Fatal(err)
	}
	if err := validateVector([]float32{}, 0); err == nil {
		t.Fatal("accepted zero dimension")
	}
}
