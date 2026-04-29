package chatpipeline

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
)

// mergeOverlappingChunks merges chunks with overlapping or adjacent StartAt/EndAt
// ranges within a single knowledge source group. Chunks MUST be pre-sorted by
// StartAt ascending, EndAt ascending. The highest score among merged chunks is kept.
func (p *PluginMerge) mergeOverlappingChunks(
	ctx context.Context,
	knowledgeID string,
	chunks []*types.SearchResult,
) []*types.SearchResult {
	if len(chunks) == 0 {
		return nil
	}

	merged := []*types.SearchResult{chunks[0]}
	for i := 1; i < len(chunks); i++ {
		lastChunk := merged[len(merged)-1]

		// Non-overlapping: add as a new entry
		if chunks[i].StartAt > lastChunk.EndAt {
			merged = append(merged, chunks[i])
			continue
		}

		// Partial overlap: append the non-overlapping suffix.
		//
		// Offset math assumes len([]rune(Content)) == EndAt-StartAt, but a
		// few upstream paths break that invariant:
		//   - Parent-child chunker prepends table headers, so Content may be
		//     longer than EndAt-StartAt.
		//   - Legacy data / mixed chunk sources may carry EndAt-StartAt that
		//     exceed the actual rune length of Content.
		// We clamp offset into [0, len(contentRunes)] so the merge degrades
		// gracefully instead of panicking with a negative slice bound.
		if chunks[i].EndAt > lastChunk.EndAt {
			contentRunes := []rune(chunks[i].Content)
			suffixLen := chunks[i].EndAt - lastChunk.EndAt
			offset := len(contentRunes) - suffixLen
			if offset < 0 || offset > len(contentRunes) {
				pipelineWarn(ctx, "Merge", "overlap_offset_clamp", map[string]interface{}{
					"knowledge_id":    knowledgeID,
					"chunk_id":        chunks[i].ID,
					"content_runes":   len(contentRunes),
					"chunk_start":     chunks[i].StartAt,
					"chunk_end":       chunks[i].EndAt,
					"last_end":        lastChunk.EndAt,
					"computed_offset": offset,
				})
				if offset < 0 {
					offset = 0
				} else {
					offset = len(contentRunes)
				}
			}
			lastChunk.Content = lastChunk.Content + string(contentRunes[offset:])
			lastChunk.EndAt = chunks[i].EndAt
			lastChunk.SubChunkID = append(lastChunk.SubChunkID, chunks[i].ID)

			if err := mergeImageInfo(ctx, lastChunk, chunks[i]); err != nil {
				pipelineWarn(ctx, "Merge", "image_merge", map[string]interface{}{
					"knowledge_id": knowledgeID,
					"error":        err.Error(),
				})
			}
		} else {
			// Fully contained: track the subsumed chunk and merge its ImageInfo
			if !containsID(lastChunk.SubChunkID, chunks[i].ID) {
				lastChunk.SubChunkID = append(lastChunk.SubChunkID, chunks[i].ID)
			}
			if err := mergeImageInfo(ctx, lastChunk, chunks[i]); err != nil {
				pipelineWarn(ctx, "Merge", "image_merge_contained", map[string]interface{}{
					"knowledge_id": knowledgeID,
					"error":        err.Error(),
				})
			}
		}

		// Keep the higher score
		if chunks[i].Score > lastChunk.Score {
			lastChunk.Score = chunks[i].Score
		}
	}

	// Sort merged chunks by score (highest first)
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	return merged
}

// mergeImageInfo merges ImageInfo from source into target, deduplicating by URL.
func mergeImageInfo(ctx context.Context, target *types.SearchResult, source *types.SearchResult) error {
	if source.ImageInfo == "" {
		return nil
	}

	var sourceImageInfos []types.ImageInfo
	if err := json.Unmarshal([]byte(source.ImageInfo), &sourceImageInfos); err != nil {
		pipelineWarn(ctx, "Merge", "image_unmarshal_source", map[string]interface{}{
			"error": err.Error(),
		})
		return err
	}

	if len(sourceImageInfos) == 0 {
		return nil
	}

	var targetImageInfos []types.ImageInfo
	if target.ImageInfo != "" {
		if err := json.Unmarshal([]byte(target.ImageInfo), &targetImageInfos); err != nil {
			pipelineWarn(ctx, "Merge", "image_unmarshal_target", map[string]interface{}{
				"error": err.Error(),
			})
			target.ImageInfo = source.ImageInfo
			return nil
		}
	}

	targetImageInfos = append(targetImageInfos, sourceImageInfos...)

	uniqueMap := make(map[string]bool)
	uniqueImageInfos := make([]types.ImageInfo, 0, len(targetImageInfos))

	for _, imgInfo := range targetImageInfos {
		if imgInfo.URL != "" && !uniqueMap[imgInfo.URL] {
			uniqueMap[imgInfo.URL] = true
			uniqueImageInfos = append(uniqueImageInfos, imgInfo)
		}
	}

	mergedImageInfoJSON, err := json.Marshal(uniqueImageInfos)
	if err != nil {
		pipelineWarn(ctx, "Merge", "image_marshal", map[string]interface{}{
			"error": err.Error(),
		})
		return err
	}

	target.ImageInfo = string(mergedImageInfoJSON)
	pipelineInfo(ctx, "Merge", "image_merged", map[string]interface{}{
		"image_refs": len(uniqueImageInfos),
	})
	return nil
}
