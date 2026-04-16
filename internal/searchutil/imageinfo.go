package searchutil

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// CollectImageInfoByChunkIDs collects merged image_info JSON for each given
// chunk ID by querying child chunks (image_ocr / image_caption). It supports
// two-level resolution:
//   - If chunkIDs are text chunks, their direct children are image chunks → one query.
//   - If chunkIDs are parent_text chunks, their children are text chunks
//     whose children are image chunks → two queries.
//
// Returns a map of input chunkID → merged image_info JSON string.
func CollectImageInfoByChunkIDs(
	ctx context.Context,
	chunkRepo interfaces.ChunkRepository,
	tenantID uint64,
	chunkIDs []string,
) map[string]string {
	if len(chunkIDs) == 0 {
		return nil
	}

	children, err := chunkRepo.ListChunksByParentIDs(ctx, tenantID, chunkIDs)
	if err != nil || len(children) == 0 {
		return nil
	}

	type imageAgg struct {
		byURL map[string]types.ImageInfo
	}
	aggMap := make(map[string]*imageAgg)

	addInfo := func(targetID string, child *types.Chunk) {
		if child.ImageInfo == "" {
			return
		}
		var infos []types.ImageInfo
		if err := json.Unmarshal([]byte(child.ImageInfo), &infos); err != nil || len(infos) == 0 {
			return
		}
		agg, ok := aggMap[targetID]
		if !ok {
			agg = &imageAgg{byURL: make(map[string]types.ImageInfo)}
			aggMap[targetID] = agg
		}
		for _, info := range infos {
			key := info.URL
			if key == "" {
				key = info.OriginalURL
			}
			if key == "" {
				continue
			}
			existing, exists := agg.byURL[key]
			if !exists {
				agg.byURL[key] = info
			} else {
				if info.OCRText != "" {
					existing.OCRText = info.OCRText
				}
				if info.Caption != "" {
					existing.Caption = info.Caption
				}
				agg.byURL[key] = existing
			}
		}
	}

	var textChildIDs []string
	textToParent := make(map[string]string)

	for _, child := range children {
		switch child.ChunkType {
		case types.ChunkTypeImageOCR, types.ChunkTypeImageCaption:
			addInfo(child.ParentChunkID, child)
		case types.ChunkTypeText:
			textChildIDs = append(textChildIDs, child.ID)
			textToParent[child.ID] = child.ParentChunkID
		}
	}

	if len(textChildIDs) > 0 {
		grandChildren, err := chunkRepo.ListChunksByParentIDs(ctx, tenantID, textChildIDs)
		if err == nil {
			for _, gc := range grandChildren {
				if gc.ChunkType != types.ChunkTypeImageOCR && gc.ChunkType != types.ChunkTypeImageCaption {
					continue
				}
				if parentTextID, ok := textToParent[gc.ParentChunkID]; ok {
					addInfo(parentTextID, gc)
				}
			}
		}
	}

	out := make(map[string]string, len(aggMap))
	for id, agg := range aggMap {
		if len(agg.byURL) == 0 {
			continue
		}
		merged := make([]types.ImageInfo, 0, len(agg.byURL))
		for _, info := range agg.byURL {
			merged = append(merged, info)
		}
		data, err := json.Marshal(merged)
		if err != nil {
			continue
		}
		out[id] = string(data)
	}
	return out
}

// EnrichSearchResultsImageInfo fills in ImageInfo for SearchResults that have
// none by batch-querying child image chunks.
func EnrichSearchResultsImageInfo(
	ctx context.Context,
	chunkRepo interfaces.ChunkRepository,
	tenantID uint64,
	results []*types.SearchResult,
) {
	var chunkIDs []string
	seen := make(map[string]bool)
	for _, r := range results {
		if r.ImageInfo != "" {
			continue
		}
		if !seen[r.ID] {
			seen[r.ID] = true
			chunkIDs = append(chunkIDs, r.ID)
		}
	}
	if len(chunkIDs) == 0 {
		return
	}

	infoMap := CollectImageInfoByChunkIDs(ctx, chunkRepo, tenantID, chunkIDs)
	if len(infoMap) == 0 {
		return
	}

	for _, r := range results {
		if r.ImageInfo != "" {
			continue
		}
		if merged, ok := infoMap[r.ID]; ok {
			r.ImageInfo = merged
		}
	}
}
