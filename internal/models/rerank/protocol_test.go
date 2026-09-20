package rerank

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProtocol records every batch it was handed and scores documents by
// their position within the batch, so a wrong offset shows up immediately.
type fakeProtocol struct {
	mu      sync.Mutex
	batches [][]string
	err     error
}

func (f *fakeProtocol) Rerank(
	_ context.Context, _ string, documents []string,
) ([]api.RerankResult, error) {
	f.mu.Lock()
	f.batches = append(f.batches, append([]string(nil), documents...))
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]api.RerankResult, len(documents))
	for i, doc := range documents {
		out[i] = api.RerankResult{Index: i, Score: float64(i), Text: doc}
	}
	return out, nil
}

func newWrapped(inner api.Reranker, settings catalog.RerankSettings) *protocolReranker {
	return &protocolReranker{
		inner: inner, settings: settings,
		endpoint: "https://example.invalid", modelName: "m", modelID: "id",
	}
}

// The documented per-request ceilings live on the vendor, and one shared
// layer enforces them. Indices must come back pointing into the caller's
// original slice, not into the batch.
func TestBatchingSplitsAndRestoresGlobalIndices(t *testing.T) {
	fake := &fakeProtocol{}
	r := newWrapped(fake, catalog.RerankSettings{MaxDocuments: 2, MaxConcurrency: 1})

	docs := []string{"d0", "d1", "d2", "d3", "d4"}
	got, err := r.Rerank(context.Background(), "q", docs)
	require.NoError(t, err)

	require.Len(t, fake.batches, 3, "5 documents at 2 per request is 3 requests")
	indices := make([]int, 0, len(got))
	for _, item := range got {
		indices = append(indices, item.Index)
		assert.Equal(t, docs[item.Index], item.Document.Text, "index must address the caller's slice")
	}
	assert.ElementsMatch(t, []int{0, 1, 2, 3, 4}, indices)
}

func TestBatchingIsSkippedWhenTheVendorDeclaresNoLimit(t *testing.T) {
	fake := &fakeProtocol{}
	r := newWrapped(fake, catalog.RerankSettings{})

	_, err := r.Rerank(context.Background(), "q", []string{"a", "b", "c", "d"})
	require.NoError(t, err)
	assert.Len(t, fake.batches, 1)
}

// A document over the vendor's per-document ceiling cannot be scored. Saying
// so beats sending a prefix and returning a number for text nobody asked
// about.
func TestAnOversizedDocumentIsAnError(t *testing.T) {
	r := newWrapped(&fakeProtocol{}, catalog.RerankSettings{MaxDocumentChars: 10})

	_, err := r.Rerank(context.Background(), "q", []string{"ok", strings.Repeat("x", 99)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "index 1")
}

func TestBatchErrorsPropagate(t *testing.T) {
	r := newWrapped(&fakeProtocol{err: fmt.Errorf("upstream exploded")}, catalog.RerankSettings{})

	_, err := r.Rerank(context.Background(), "q", []string{"a"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upstream exploded")
}

func TestEmptyInputMakesNoRequest(t *testing.T) {
	fake := &fakeProtocol{}
	r := newWrapped(fake, catalog.RerankSettings{})

	got, err := r.Rerank(context.Background(), "q", nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, fake.batches)
}

// NIM returns the raw logit of its relevance head. The retrieval pipeline
// compares scores against RerankThreshold, which is tuned for probabilities,
// so the logit is converted rather than passed through — otherwise the
// default threshold of 0.2 keeps only documents whose logit is positive.
func TestLogitScoresBecomeProbabilities(t *testing.T) {
	for _, tc := range []struct {
		logit float64
		want  float64
	}{
		{logit: 0.226318359375, want: 0.5563},
		{logit: -1.171875, want: 0.2366},
		{logit: -6.3125, want: 0.0018},
		{logit: 0, want: 0.5},
	} {
		assert.InDelta(t, tc.want, normalizeScore(tc.logit, api.ScoreLogit), 1e-4,
			"logit %v", tc.logit)
	}
}

// The conversion is monotonic, so it re-scales without reordering.
func TestLogitConversionPreservesOrder(t *testing.T) {
	logits := []float64{-6.3125, -1.171875, 0.226318359375, 4}
	previous := -1.0
	for _, logit := range logits {
		got := normalizeScore(logit, api.ScoreLogit)
		assert.Greater(t, got, previous)
		previous = got
	}
}

func TestProbabilityScoresPassThroughUntouched(t *testing.T) {
	assert.Equal(t, 0.9819, normalizeScore(0.9819, api.ScoreProbability))
	assert.Equal(t, 0.9819, normalizeScore(0.9819, ""))
}
