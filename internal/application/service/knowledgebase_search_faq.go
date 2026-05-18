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
// For non-FAQ knowledge bases, returns the input unchanged.
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
	chunks []*types.IndexWithScore,
	vectorResults []*types.IndexWithScore,
	groups []*storeGroup,
	params types.SearchParams,
	matchCount int,
) ([]*types.IndexWithScore, error) {
	if kb.Type != types.KnowledgeBaseTypeFAQ {
		return chunks, nil
	}

	// Check if we need iterative retrieval for FAQ with separate indexing.
	// Only use iterative retrieval if we don't have enough unique chunks
	// after first deduplication.
	needsIterativeRetrieval := len(chunks) < params.MatchCount && len(vectorResults) == matchCount
	if needsIterativeRetrieval {
		logger.Info(ctx, "Not enough unique chunks, using iterative retrieval for FAQ")
		return s.iterativeRetrieveWithDeduplication(
			ctx,
			groups,
			params.MatchCount,
			params.QueryText,
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
) ([]*types.IndexWithScore, error) {
	maxIterations := 5
	// Start with a larger TopK since we're called when first retrieval wasn't enough
	// The first retrieval already used matchCount*3, so start from there
	currentTopK := matchCount * 3
	uniqueChunks := make(map[string]*types.IndexWithScore)
	// Cache chunk data to avoid repeated DB queries across iterations
	chunkDataCache := make(map[string]*types.Chunk)
	// Track chunks that have been filtered out by negative questions
	filteredOutChunks := make(map[string]struct{})

	queryTextLower := strings.ToLower(strings.TrimSpace(queryText))
	tenantID := types.MustTenantIDFromContext(ctx)

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

		// Collect results
		iterationResults := []*types.IndexWithScore{}
		for _, retrieveResult := range retrieveResults {
			iterationResults = append(iterationResults, retrieveResult.Results...)
		}

		if len(iterationResults) == 0 {
			logger.Infof(ctx, "No results found at iteration %d", i+1)
			break
		}

		totalRetrieved := len(iterationResults)

		// Collect new chunk IDs that need to be fetched from DB
		newChunkIDs := make([]string, 0)
		for _, result := range iterationResults {
			if _, cached := chunkDataCache[result.ChunkID]; !cached {
				if _, filtered := filteredOutChunks[result.ChunkID]; !filtered {
					newChunkIDs = append(newChunkIDs, result.ChunkID)
				}
			}
		}

		// Batch fetch only new chunks
		if len(newChunkIDs) > 0 {
			newChunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, newChunkIDs)
			if err != nil {
				logger.Warnf(ctx, "Failed to fetch chunks at iteration %d: %v", i+1, err)
			} else {
				for _, chunk := range newChunks {
					chunkDataCache[chunk.ID] = chunk
				}
			}
		}

		// Deduplicate, merge, and filter in one pass
		for _, result := range iterationResults {
			// Skip if already filtered out
			if _, filtered := filteredOutChunks[result.ChunkID]; filtered {
				continue
			}

			// Check negative questions using cached data
			if chunkData, ok := chunkDataCache[result.ChunkID]; ok {
				if chunkData.ChunkType == types.ChunkTypeFAQ {
					if meta, err := chunkData.FAQMetadata(); err == nil && meta != nil {
						if s.matchesNegativeQuestions(queryTextLower, meta.NegativeQuestions) {
							filteredOutChunks[result.ChunkID] = struct{}{}
							delete(uniqueChunks, result.ChunkID)
							continue
						}
					}
				}
			}

			// Keep highest score for each chunk
			if existing, ok := uniqueChunks[result.ChunkID]; !ok || result.Score > existing.Score {
				uniqueChunks[result.ChunkID] = result
			}
		}

		logger.Infof(
			ctx,
			"After iteration %d: retrieved %d results, found %d valid unique chunks (target: %d)",
			i+1,
			totalRetrieved,
			len(uniqueChunks),
			matchCount,
		)

		// Early stop: Check if we have enough unique chunks after deduplication and filtering
		if len(uniqueChunks) >= matchCount {
			logger.Infof(ctx, "Found enough unique chunks after %d iterations", i+1)
			break
		}

		// Early stop: If we got fewer results than TopK, there are no more results to retrieve
		if totalRetrieved < currentTopK {
			logger.Infof(ctx, "No more results available (got %d < %d), stopping iteration", totalRetrieved, currentTopK)
			break
		}

		// Increase TopK for next iteration
		currentTopK *= 2
	}

	// Convert map to slice and sort by score
	result := make([]*types.IndexWithScore, 0, len(uniqueChunks))
	for _, chunk := range uniqueChunks {
		result = append(result, chunk)
	}

	slices.SortFunc(result, sortByScoreDesc)

	logger.Infof(ctx, "Iterative retrieval completed: %d unique chunks found after filtering", len(result))
	return result, nil
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

	// Batch fetch chunks to get negative questions
	allChunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
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
