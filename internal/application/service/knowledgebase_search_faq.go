package service

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"slices"
)

// applyFAQPostProcessing handles FAQ-specific post-processing: iterative retrieval
// when not enough unique chunks are found, or negative question filtering otherwise.
// Iterative retrieval follows the primary KB's type; negative questions are
// filtered whenever any KB in scope is an FAQ KB, so an FAQ searched alongside
// a document KB still honours them. Without an FAQ KB in scope the input is
// returned unchanged.
//
// The iterative retrieval path fans out across the supplied storeGroups so
// multi-store FAQ searches grow TopK uniformly across every bound vector
// store. A typed AppError raised inside that path (e.g.
// ErrVectorStoreUnavailable from a per-group timeout) is propagated to the
// caller so the user receives a faithful failure response rather than a
// silently truncated chunk list.
func (s *knowledgeBaseService) applyFAQPostProcessing(
	ctx context.Context,
	kb *types.KnowledgeBase,
	scopeKBs []*types.KnowledgeBase,
	chunks []*types.IndexWithScore,
	vectorLists [][]*types.IndexWithScore,
	groups []*storeGroup,
	params types.SearchParams,
	matchCount int,
) ([]*types.IndexWithScore, error) {
	isFAQ := func(k *types.KnowledgeBase) bool { return k != nil && k.Type == types.KnowledgeBaseTypeFAQ }
	if !isFAQ(kb) && !slices.ContainsFunc(scopeKBs, isFAQ) {
		return chunks, nil
	}

	// Check if we need iterative retrieval for FAQ with separate indexing.
	// Only use iterative retrieval if we don't have enough unique chunks
	// after first deduplication, some vector list came back full (so a
	// deeper search can find more), and the depth can still grow. A full
	// list is judged per list: the concatenation of several lists is larger
	// than matchCount whenever more than one vector list exists, which kept
	// this from ever triggering in multi-list searches.
	listFull := slices.ContainsFunc(vectorLists, func(l []*types.IndexWithScore) bool { return len(l) >= matchCount })
	needsIterativeRetrieval := isFAQ(kb) && len(chunks) < params.MatchCount && listFull &&
		matchCount < maxRetrievalPoolSize
	if needsIterativeRetrieval {
		logger.Info(ctx, "Not enough unique chunks, using iterative retrieval for FAQ")
		return s.iterativeRetrieveWithDeduplication(
			ctx,
			groups,
			params.MatchCount,
			params.QueryText,
			matchCount,
		)
	}

	// Filter by negative questions if not using iterative retrieval.
	result := s.filterByNegativeQuestions(ctx, chunks, params.QueryText)
	logger.Infof(ctx, "Result count after negative question filtering: %d", len(result))
	return result, nil
}

// iterativeRetrieveWithDeduplication performs iterative retrieval until enough unique chunks are found.
// This is used for FAQ knowledge bases with separate indexing mode.
// Negative question filtering is applied after each iteration with chunk data caching.
//
// Each round searches twice as deep as the one before, starting at twice
// initialDepth (the depth the first search already used), and fuses its
// lists exactly like the first search (fuseOrDeduplicate), so the scores it
// returns are on the same scale. A deeper round returns a superset of the
// previous one, so each round's fused list replaces the last.
//
// Each iteration only updates group.TopK; the underlying BaseParams stays
// immutable so the fan-out goroutines inside retrieveFromStores never
// observe a mid-mutation slice. Engines and grouping are computed once
// upstream and reused across iterations.
//
// Returns (results, error). A typed AppError raised inside
// retrieveFromStores (e.g. per-group timeout, or vector-store binding
// invalid) is propagated to the caller so the user sees an honest failure
// instead of a silently truncated result set. Non-AppError failures
// continue to break the loop with a warning and return whatever partial
// uniqueChunks have been accumulated — preserving the existing behavior
// for transient retrieve errors.
func (s *knowledgeBaseService) iterativeRetrieveWithDeduplication(ctx context.Context,
	groups []*storeGroup,
	matchCount int,
	queryText string,
	initialDepth int,
) ([]*types.IndexWithScore, error) {
	maxIterations := 5
	// matchCount is caller-supplied, so both the seed and the per-iteration
	// doubling are bounded by maxRetrievalPoolSize — otherwise a single request
	// could drive the vector-store query depth arbitrarily deep.
	currentTopK := min(max(matchCount*3, initialDepth*2), maxRetrievalPoolSize)
	var uniqueChunks []*types.IndexWithScore
	// Cache chunk data to avoid repeated DB queries across iterations
	chunkDataCache := make(map[string]*types.Chunk)
	// Track chunks that have been filtered out by negative questions
	filteredOutChunks := make(map[string]struct{})

	queryTextLower := strings.ToLower(strings.TrimSpace(queryText))
	tenantID := types.MustTenantIDFromContext(ctx)
	var retrievalCfg *types.RetrievalConfig
	if tenantInfo, _ := types.TenantInfoFromContext(ctx); tenantInfo != nil {
		retrievalCfg = tenantInfo.RetrievalConfig
	}

	for i := 0; i < maxIterations; i++ {
		// Bump only the per-group TopK. BaseParams is immutable and read
		// concurrently inside retrieveFromStores; paramsWithTopK rebuilds
		// a fresh slice per call so no goroutine ever sees a half-mutated
		// value.
		for _, grp := range groups {
			grp.TopK = currentTopK
		}

		retrieveResults, err := s.retrieveFromStores(
			ctx, groups, retriever.EngineAwareNormalizer{})
		if err != nil {
			// Typed AppErrors must surface to HybridSearch so the user
			// sees the failure rather than a silently truncated chunk
			// list. Non-AppError failures (e.g. transient infra hiccups)
			// preserve the existing "warn and break" behavior so the
			// caller still gets partial results from the iterations that
			// succeeded.
			if _, ok := apperrors.IsAppError(err); ok {
				logger.WarnWithFields(ctx, logger.Fields{
					"iteration": i + 1,
				}, "Iterative retrieval surfaced typed failure")
				return nil, err
			}
			logger.Warnf(ctx, "Iterative retrieval failed at iteration %d: %v", i+1, err)
			break
		}

		vectorLists, keywordLists := classifyRetrievalResults(ctx, retrieveResults)
		if len(vectorLists) == 0 && len(keywordLists) == 0 {
			logger.Infof(ctx, "No results found at iteration %d", i+1)
			break
		}
		// A list shorter than the requested depth is exhausted; when every
		// list is, searching deeper cannot find anything new.
		exhausted := !slices.ContainsFunc(append(vectorLists, keywordLists...),
			func(l []*types.IndexWithScore) bool { return len(l) >= currentTopK })
		fused := fuseOrDeduplicate(ctx, vectorLists, keywordLists, retrievalCfg)

		// Collect new chunk IDs that need to be fetched from DB
		newChunkIDs := make([]string, 0)
		for _, result := range fused {
			if _, cached := chunkDataCache[result.ChunkID]; !cached {
				if _, filtered := filteredOutChunks[result.ChunkID]; !filtered {
					newChunkIDs = append(newChunkIDs, result.ChunkID)
				}
			}
		}

		// Batch fetch only new chunks
		if len(newChunkIDs) > 0 {
			newChunks, err := s.listChunksByIDWithShared(ctx, tenantID, newChunkIDs)
			if err != nil {
				logger.Warnf(ctx, "Failed to fetch chunks at iteration %d: %v", i+1, err)
			} else {
				for _, chunk := range newChunks {
					chunkDataCache[chunk.ID] = chunk
				}
			}
		}

		// Filter negative questions using cached data
		uniqueChunks = uniqueChunks[:0]
		for _, result := range fused {
			if _, filtered := filteredOutChunks[result.ChunkID]; filtered {
				continue
			}
			if chunkData, ok := chunkDataCache[result.ChunkID]; ok && chunkData.ChunkType == types.ChunkTypeFAQ {
				if meta, err := chunkData.FAQMetadata(); err == nil && meta != nil &&
					s.matchesNegativeQuestions(queryTextLower, meta.NegativeQuestions) {
					filteredOutChunks[result.ChunkID] = struct{}{}
					continue
				}
			}
			uniqueChunks = append(uniqueChunks, result)
		}

		logger.Infof(
			ctx,
			"After iteration %d: depth %d, found %d valid unique chunks (target: %d)",
			i+1,
			currentTopK,
			len(uniqueChunks),
			matchCount,
		)

		// Early stop: Check if we have enough unique chunks after deduplication and filtering
		if len(uniqueChunks) >= matchCount {
			logger.Infof(ctx, "Found enough unique chunks after %d iterations", i+1)
			break
		}

		// Early stop: If every list came back short, there are no more results to retrieve
		if exhausted {
			logger.Infof(ctx, "No more results available at depth %d, stopping iteration", currentTopK)
			break
		}

		// Increase TopK for next iteration. Once the cap is reached, another
		// round would re-issue an identical query, so stop instead.
		if currentTopK >= maxRetrievalPoolSize {
			logger.Infof(ctx, "Retrieval depth cap %d reached, stopping iteration", maxRetrievalPoolSize)
			break
		}
		currentTopK = min(currentTopK*2, maxRetrievalPoolSize)
	}

	logger.Infof(ctx, "Iterative retrieval completed: %d unique chunks found after filtering", len(uniqueChunks))
	return uniqueChunks, nil
}

// filterByNegativeQuestions filters out chunks that match negative questions for FAQ knowledge bases.
func (s *knowledgeBaseService) filterByNegativeQuestions(ctx context.Context,
	chunks []*types.IndexWithScore,
	queryText string,
) []*types.IndexWithScore {
	if len(chunks) == 0 {
		return chunks
	}

	queryTextLower := strings.ToLower(strings.TrimSpace(queryText))
	if queryTextLower == "" {
		return chunks
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	// Collect chunk IDs
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ChunkID)
	}

	// Batch fetch chunks to get negative questions. Shared-KB chunks belong to
	// the sharing workspace; a tenant-scoped lookup missed them and the
	// "not found, keep it" branch below let them bypass the filter.
	allChunks, err := s.listChunksByIDWithShared(ctx, tenantID, chunkIDs)
	if err != nil {
		logger.Warnf(ctx, "Failed to fetch chunks for negative question filtering: %v", err)
		// If we can't fetch chunks, return original results
		return chunks
	}

	// Build chunk map for quick lookup
	chunkMap := make(map[string]*types.Chunk, len(allChunks))
	for _, chunk := range allChunks {
		chunkMap[chunk.ID] = chunk
	}

	// Filter out chunks that match negative questions
	filteredChunks := make([]*types.IndexWithScore, 0, len(chunks))
	for _, chunk := range chunks {
		chunkData, ok := chunkMap[chunk.ChunkID]
		if !ok {
			// If chunk not found, keep it (shouldn't happen, but be safe)
			filteredChunks = append(filteredChunks, chunk)
			continue
		}

		// Only filter FAQ type chunks
		if chunkData.ChunkType != types.ChunkTypeFAQ {
			filteredChunks = append(filteredChunks, chunk)
			continue
		}

		// Get FAQ metadata and check negative questions
		meta, err := chunkData.FAQMetadata()
		if err != nil || meta == nil {
			// If we can't parse metadata, keep the chunk
			filteredChunks = append(filteredChunks, chunk)
			continue
		}

		// Check if query matches any negative question
		if s.matchesNegativeQuestions(queryTextLower, meta.NegativeQuestions) {
			logger.Debugf(ctx, "Filtered FAQ chunk %s due to negative question match", chunk.ChunkID)
			continue
		}

		// Keep the chunk
		filteredChunks = append(filteredChunks, chunk)
	}

	return filteredChunks
}

// matchesNegativeQuestions checks if the query text matches any negative questions.
// Returns true if the query matches any negative question, false otherwise.
func (s *knowledgeBaseService) matchesNegativeQuestions(queryTextLower string, negativeQuestions []string) bool {
	if len(negativeQuestions) == 0 {
		return false
	}

	for _, negativeQ := range negativeQuestions {
		negativeQLower := strings.ToLower(strings.TrimSpace(negativeQ))
		if negativeQLower == "" {
			continue
		}
		// Check if query text is exactly the same as the negative question
		if queryTextLower == negativeQLower {
			return true
		}
	}
	return false
}
