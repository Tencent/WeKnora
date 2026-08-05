package chatpipeline

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

// mergeSequentialChunks joins sequential current chunk bodies without using
// parser StartAt/EndAt coordinates. Chunks MUST be pre-sorted by ChunkIndex.
// Source coordinates remain useful for citations, but arbitrary user edits
// make them unsafe for deciding whether current content can be discarded.
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
		textContained := searchutil.ContainsChunkContent(lastChunk.Content, current.Content) ||
			searchutil.ContainsChunkContent(current.Content, lastChunk.Content)
		sequential := current.ChunkIndex == last.lastIndex+1
		if !textContained && !sequential {
			groups = append(groups, mergedGroup{result: current, lastIndex: current.ChunkIndex})
			continue
		}

		lastChunk.Content = searchutil.JoinChunkContent(lastChunk.Content, current.Content, "\n\n")
		if !containsID(lastChunk.SubChunkID, current.ID) {
			lastChunk.SubChunkID = append(lastChunk.SubChunkID, current.ID)
		}
		if err := mergeImageInfo(ctx, lastChunk, current); err != nil {
			pipelineWarn(ctx, "Merge", "image_merge", map[string]interface{}{
				"knowledge_id": knowledgeID,
				"error":        err.Error(),
			})
		}
		if current.ChunkIndex > last.lastIndex {
			last.lastIndex = current.ChunkIndex
		}

		// Keep the higher score
		if current.Score > lastChunk.Score {
			lastChunk.Score = current.Score
			lastChunk.RecallWeight = current.RecallWeight
			lastChunk.FeedbackWeightEnabled = current.FeedbackWeightEnabled
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
