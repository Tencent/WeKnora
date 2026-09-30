package reranking

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func imageVectorRow(id string, score float64) *types.SearchResult {
	return &types.SearchResult{
		ID:        id,
		Content:   "![image](https://example.test/" + id + ".png)",
		ChunkType: string(types.ChunkTypeImageVector),
		Score:     score,
	}
}

func textRow(id, content string, score float64) *types.SearchResult {
	return &types.SearchResult{ID: id, Content: content, ChunkType: string(types.ChunkTypeText), Score: score}
}

// TestRerankKeepsImageVectorChunks is the regression guard for the whole
// image-retrieval feature reaching the answer: an image_vector chunk's content
// is a bare ![image](url), so passage cleaning empties it and the empty-passage
// filter would drop the best vector match for the query.
func TestRerankKeepsImageVectorChunks(t *testing.T) {
	t.Parallel()
	model := &stubReranker{scores: []float64{0.9}}
	in := []*types.SearchResult{
		textRow("c-text", "这是一段关于红色消防车的说明文字", 0.4),
		imageVectorRow("c-image", 0.72),
	}

	res := Rerank(context.Background(), model, "找出红色的汽车图片", in, Options{
		Threshold:        0.1,
		TopK:             5,
		FallbackMinScore: FallbackMinScore(false),
	})

	if len(model.documents) != 1 {
		t.Fatalf("rerank model saw %d passages, want 1 (the text chunk only): %q",
			len(model.documents), model.documents)
	}
	var found *types.SearchResult
	for _, r := range res.Results {
		if r.ID == "c-image" {
			found = r
		}
	}
	if found == nil {
		t.Fatalf("image vector chunk survived retrieval but was dropped by rerank; ids = %s", ids(res.Results))
	}
	if found.Metadata["rerank_exempt"] != "true" {
		t.Fatalf("image vector chunk not marked rerank_exempt; metadata = %v", found.Metadata)
	}
	// The retrieval score is the only relevance signal this chunk has; a
	// fabricated rerank score would be worse than none.
	if found.Score != 0.72 {
		t.Fatalf("image vector score = %v, want the original retrieval score 0.72", found.Score)
	}
}

// TestRerankSurvivesImageOnlyResults covers the degenerate case where every hit
// is an image: there is nothing for the model to score, so it must not be
// called and the images must still be returned.
func TestRerankSurvivesImageOnlyResults(t *testing.T) {
	t.Parallel()
	model := &stubReranker{scores: []float64{0.9}}
	in := []*types.SearchResult{imageVectorRow("c-a", 0.8), imageVectorRow("c-b", 0.5)}

	res := Rerank(context.Background(), model, "找出红色的汽车图片", in, Options{
		Threshold:        0.1,
		TopK:             5,
		FallbackMinScore: FallbackMinScore(false),
	})

	if model.calls != 0 {
		t.Fatalf("rerank model called with no rerankable passage: %q", model.documents)
	}
	if len(res.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(res.Results))
	}
	if res.Indices[0] != 0 || res.Indices[1] != 1 {
		t.Fatalf("indices = %v, want the input positions 0,1", res.Indices)
	}
}

// TestRerankModelErrorKeepsImageVectorChunks guards the fallback: a failed
// model call returns the input rows, and the exempted images are among them.
func TestRerankModelErrorKeepsImageVectorChunks(t *testing.T) {
	t.Parallel()
	model := &stubReranker{err: errors.New("upstream 500")}
	in := []*types.SearchResult{
		textRow("c-text", "一段可以被重排的文本内容", 0.4),
		imageVectorRow("c-image", 0.72),
	}

	res := Rerank(context.Background(), model, "找出红色的汽车图片", in, Options{
		Threshold:        0.1,
		TopK:             5,
		FallbackMinScore: FallbackMinScore(false),
	})

	if len(res.Results) != 2 {
		t.Fatalf("model-error fallback kept %d results, want 2 (text + image)", len(res.Results))
	}
	if res.Diagnostics.Outcome != types.RerankOutcomeModelError {
		t.Fatalf("outcome = %q, want %q", res.Diagnostics.Outcome, types.RerankOutcomeModelError)
	}
}
