package embedding

import (
	"context"
	"errors"
	"testing"

	"github.com/panjf2000/ants/v2"
)

type failingBatchModel struct{ Embedder }

func (f failingBatchModel) BatchEmbed(context.Context, []string) ([][]float32, error) {
	return nil, errBatchTest
}

var errBatchTest = errors.New("provider failed")

func TestBatchPoolConcurrentFailure(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	pool, err := ants.NewPool(8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Release()
	texts := make([]string, 128)
	for i := range texts {
		texts[i] = "document chunk"
	}
	for attempt := 0; attempt < 20; attempt++ {
		result, err := NewBatchEmbedder(pool).BatchEmbedWithPool(context.Background(), failingBatchModel{}, texts)
		if !errors.Is(err, errBatchTest) || result != nil {
			t.Fatalf("result=%v error=%v", result, err)
		}
	}
}
