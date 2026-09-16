package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	retrievalfusion "github.com/Tencent/WeKnora/internal/retrieval/fusion"
	"github.com/Tencent/WeKnora/internal/types"
)

// classifyRetrievalResults separates retrieval results by retriever type (vector vs keyword).
func classifyRetrievalResults(ctx context.Context, retrieveResults []*types.RetrieveResult) (
	vectorResults, keywordResults []*types.IndexWithScore,
) {
	for _, retrieveResult := range retrieveResults {
		logger.Infof(ctx, "Retrieval results, engine: %v, retriever: %v, count: %v",
			retrieveResult.RetrieverEngineType,
			retrieveResult.RetrieverType,
			len(retrieveResult.Results),
		)
		if retrieveResult.RetrieverType == types.VectorRetrieverType {
			vectorResults = append(vectorResults, retrieveResult.Results...)
		} else {
			keywordResults = append(keywordResults, retrieveResult.Results...)
		}
	}
	return
}

// fuseOrDeduplicate either fuses vector+keyword results via RRF or deduplicates vector-only results.
// retrievalCfg may be nil — defaults are then used for RRF parameters.
func fuseOrDeduplicate(ctx context.Context, vectorResults, keywordResults []*types.IndexWithScore, retrievalCfg *types.RetrievalConfig) []*types.IndexWithScore {
	return retrievalfusion.Results(ctx, vectorResults, keywordResults, retrievalCfg)
}

// sortByScoreDesc orders score-based results used outside rank fusion.
func sortByScoreDesc(a, b *types.IndexWithScore) int {
	if a.Score > b.Score {
		return -1
	}
	if a.Score < b.Score {
		return 1
	}
	return 0
}
