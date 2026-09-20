package rerank

import (
	"context"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"golang.org/x/sync/errgroup"
)

// protocolReranker adapts a protocol client to the Reranker interface and
// owns the two things every vendor needs and none of them should implement
// itself: splitting a candidate set that exceeds the documented per-request
// ceilings, and putting the returned scores on one scale.
type protocolReranker struct {
	inner     api.Reranker
	settings  catalog.RerankSettings
	endpoint  string
	modelName string
	modelID   string
}

// defaultBatchConcurrency bounds in-flight batches when a vendor declares no
// preference, so a large embedding_top_k cannot fan out into an unbounded
// burst of requests.
const defaultBatchConcurrency = 4

func (r *protocolReranker) GetModelName() string { return r.modelName }
func (r *protocolReranker) GetModelID() string   { return r.modelID }

func (r *protocolReranker) Rerank(
	ctx context.Context, query string, documents []string,
) ([]RankResult, error) {
	if len(documents) == 0 {
		return nil, nil
	}
	logger.Debugf(ctx, "%s", buildRerankRequestDebug(r.modelName, r.endpoint, query, documents))

	batches, err := api.SplitBatches(documents, utf8.RuneCountInString(query), r.settings.BatchLimits())
	if err != nil {
		return nil, fmt.Errorf("%s rerank: %w", r.modelName, err)
	}

	// Scores are only comparable within one request: a vendor scores each
	// batch against the same query independently, so concatenating them is
	// the same thing the pre-catalog batching clients did.
	scored := make([][]api.RerankResult, len(batches))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(r.concurrency())
	for i, batch := range batches {
		group.Go(func() error {
			out, err := r.inner.Rerank(groupCtx, query, batch.Items)
			if err != nil {
				return err
			}
			for j := range out {
				out[j].Index += batch.Start
			}
			scored[i] = out
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	results := make([]RankResult, 0, len(documents))
	for _, batch := range scored {
		for _, item := range batch {
			if item.Index < 0 || item.Index >= len(documents) {
				return nil, fmt.Errorf(
					"%s rerank: index %d out of range for %d documents",
					r.modelName, item.Index, len(documents),
				)
			}
			text := item.Text
			if text == "" {
				text = documents[item.Index]
			}
			results = append(results, RankResult{
				Index:          item.Index,
				Document:       DocumentInfo{Text: text},
				RelevanceScore: normalizeScore(item.Score, r.settings.ScoreScale),
			})
		}
	}
	return results, nil
}

func (r *protocolReranker) concurrency() int {
	if r.settings.MaxConcurrency > 0 {
		return r.settings.MaxConcurrency
	}
	return defaultBatchConcurrency
}

// normalizeScore puts every vendor's score on the 0..1 scale the retrieval
// pipeline compares against RerankThreshold.
//
// Most protocols already return a relevance probability. NIM returns the raw
// logit of its relevance head instead — unbounded and routinely negative —
// so a threshold tuned for probabilities would reject almost everything on
// that vendor. The logistic function is the inverse of a log-odds, so this is
// a unit conversion rather than a heuristic: it maps the vendor's own ranking
// onto the scale the rest of the system already speaks, order preserved.
func normalizeScore(score float64, scale api.ScoreScale) float64 {
	if scale != api.ScoreLogit {
		return score
	}
	return 1 / (1 + math.Exp(-score))
}
