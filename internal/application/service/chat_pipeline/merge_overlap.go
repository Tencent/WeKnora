package chatpipeline

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

// mergeSequentialChunks joins sequential current chunk bodies.
// Trusted pairs use position-aware merging;
// any pair involving edited, expanded, or stale content falls back to
// text matching so current content is never discarded on parser coordinates.
// Chunks MUST be pre-sorted by ChunkIndex.
func (p *PluginMerge) mergeSequentialChunks(
	ctx context.Context,
	knowledgeID string,
	chunks []*types.SearchResult,
) []*types.SearchResult {
	if len(chunks) == 0 {
		return nil
	}

	type mergedGroup struct {
		result    *types.SearchResult
		lastIndex int
	}
	groups := []mergedGroup{{result: chunks[0], lastIndex: chunks[0].ChunkIndex}}
	for i := 1; i < len(chunks); i++ {
		current := chunks[i]
		last := &groups[len(groups)-1]
		lastChunk := last.result

		switch classifyMerge(lastChunk, last.lastIndex, current) {
		case mergeSeparate:
			groups = append(groups, mergedGroup{result: current, lastIndex: current.ChunkIndex})
			continue
		case mergeExtend:
			lastChunk.Content = appendTrustedContent(lastChunk.Content, current.Content, lastChunk.EndAt-current.StartAt)
			lastChunk.EndAt = current.EndAt
			recordMergedChild(ctx, knowledgeID, lastChunk, current, "image_merge")
		case mergeSubsume:
			recordMergedChild(ctx, knowledgeID, lastChunk, current, "image_merge_contained")
		case mergeJoinDistinct:
			lastChunk.Content = searchutil.JoinChunkContent(lastChunk.Content, current.Content, "\n\n")
			recordMergedChild(ctx, knowledgeID, lastChunk, current, "image_merge_contained")
		case mergeJoinText:
			lastChunk.Content = searchutil.JoinChunkContent(lastChunk.Content, current.Content, "\n\n")
			recordMergedChild(ctx, knowledgeID, lastChunk, current, "image_merge")
		}

		// Extend the merged group's span and keep the higher score.
		if current.ChunkIndex > last.lastIndex {
			last.lastIndex = current.ChunkIndex
		}
		if current.Score > lastChunk.Score {
			lastChunk.Score = current.Score
		}
	}

	merged := make([]*types.SearchResult, 0, len(groups))
	for _, group := range groups {
		merged = append(merged, group.result)
	}

	// Sort merged chunks by score (highest first)
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	return merged
}

// appendTrustedContent joins a trusted pair using the overlap the coordinates
// already give us, and only falls back to text search when the overlap does not
// match character for character (HTML entities or synthetic table headers can
// keep the length invariant while changing the body).
//
// The fallback matters both ways: searching for the longest suffix match would
// otherwise mistake a repeated table row or log line for the overlap and drop
// real content, which is exactly what the position path is meant to avoid.
func appendTrustedContent(acc, next string, positionOverlap int) string {
	if merged, ok := searchutil.AppendWithExactOverlap(acc, next, positionOverlap); ok {
		return merged
	}
	return searchutil.AppendWithOverlap(acc, next, positionOverlap)
}

// chunkTrusted reports whether a result's StartAt/EndAt can be trusted for
// position-based merging: unedited, valid range, and splitter-consistent
// length (runeLen(Content) == EndAt-StartAt).
func chunkTrusted(chunk *types.SearchResult) bool {
	return chunk.ContentRevision == 0 &&
		chunk.EndAt > chunk.StartAt &&
		runeLen(chunk.Content) == chunk.EndAt-chunk.StartAt
}

// mergeSituation labels how the current result relates to the group's leading
// result in document order. The classifier decides it once; the merge loop
// switches on it without nesting.
type mergeSituation int

const (
	// mergeSeparate: current is not mergeable, it starts a new group
	// (position gap, or untrusted pair that is neither text-contained nor
	// index-sequential).
	mergeSeparate mergeSituation = iota
	// mergeExtend: trusted pair with partial overlap or adjacency; the
	// non-overlapping suffix is appended with text-verified trimming.
	mergeExtend
	// mergeSubsume: trusted pair, range-contained and text-verified; current
	// is recorded without touching the merged content.
	mergeSubsume
	// mergeJoinDistinct: trusted pair, range-contained but textually distinct;
	// current is kept via a text join, breaking the length invariant so later
	// pairs degrade to the text path.
	mergeJoinDistinct
	// mergeJoinText: untrusted pair that is text-contained or index-sequential;
	// joined via pure text matching.
	mergeJoinText
)

// classifyMerge labels the pair relationship so the merge loop can dispatch
// with a flat switch. It needs the group's running lastIndex for the untrusted
// sequentiality check.
func classifyMerge(lastChunk *types.SearchResult, lastIndex int, current *types.SearchResult) mergeSituation {
	if chunkTrusted(lastChunk) && chunkTrusted(current) && current.StartAt >= lastChunk.StartAt {
		switch {
		case current.StartAt > lastChunk.EndAt:
			return mergeSeparate
		case current.EndAt > lastChunk.EndAt:
			return mergeExtend
		case searchutil.ContainsChunkContent(lastChunk.Content, current.Content):
			return mergeSubsume
		default:
			return mergeJoinDistinct
		}
	}

	textContained := searchutil.ContainsChunkContent(lastChunk.Content, current.Content) ||
		searchutil.ContainsChunkContent(current.Content, lastChunk.Content)
	sequential := current.ChunkIndex == lastIndex+1
	if !textContained && !sequential {
		return mergeSeparate
	}
	return mergeJoinText
}

// recordMergedChild records a merged chunk on the group result: appends its ID
// to SubChunkID (deduplicated) and merges its ImageInfo, warning on failure.
// warnKey distinguishes merge contexts in pipeline diagnostics.
func recordMergedChild(ctx context.Context, knowledgeID string, target, source *types.SearchResult, warnKey string) {
	if !containsID(target.SubChunkID, source.ID) {
		target.SubChunkID = append(target.SubChunkID, source.ID)
	}
	if err := mergeImageInfo(ctx, target, source); err != nil {
		pipelineWarn(ctx, "Merge", warnKey, map[string]any{
			"knowledge_id": knowledgeID,
			"error":        err.Error(),
		})
	}
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
