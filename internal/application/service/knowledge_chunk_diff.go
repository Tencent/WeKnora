package service

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

var rebuildManagedChunkTypes = []types.ChunkType{
	types.ChunkTypeText,
	types.ChunkTypeParentText,
	types.ChunkTypeImageOCR,
	types.ChunkTypeImageCaption,
}

var rebuildBaseChunkTypes = []types.ChunkType{
	types.ChunkTypeText,
	types.ChunkTypeParentText,
}

var rebuildImageChunkTypes = []types.ChunkType{
	types.ChunkTypeImageOCR,
	types.ChunkTypeImageCaption,
}

type chunkMetadataUpdate struct {
	Existing  *types.Chunk
	Candidate *types.Chunk
}

type chunkCandidateDiff struct {
	Unchanged    []*types.Chunk
	MetadataOnly []chunkMetadataUpdate
	ChangedNew   []*types.Chunk
	Stale        []*types.Chunk
	Results      []*types.KnowledgeRebuildChunkResult
}

type chunkSourceMetadata struct {
	TenantID        uint64           `json:"tenant_id"`
	KnowledgeID     string           `json:"knowledge_id"`
	KnowledgeBaseID string           `json:"knowledge_base_id"`
	TagID           string           `json:"tag_id"`
	Content         string           `json:"content"`
	ChunkIndex      int              `json:"chunk_index"`
	IsEnabled       bool             `json:"is_enabled"`
	Flags           types.ChunkFlags `json:"flags"`
	StartAt         int              `json:"start_at"`
	EndAt           int              `json:"end_at"`
	PreChunkID      string           `json:"pre_chunk_id"`
	NextChunkID     string           `json:"next_chunk_id"`
	ChunkType       types.ChunkType  `json:"chunk_type"`
	ParentChunkID   string           `json:"parent_chunk_id"`
	ImageInfo       string           `json:"image_info"`
}

func classifyChunkCandidates(oldChunks, candidates []*types.Chunk) chunkCandidateDiff {
	diff := chunkCandidateDiff{}
	oldByID := make(map[string]*types.Chunk, len(oldChunks))
	for _, chunk := range oldChunks {
		if chunk != nil && chunk.ID != "" {
			oldByID[chunk.ID] = chunk
		}
	}
	matched := make(map[string]struct{}, len(candidates))

	for _, candidate := range candidates {
		if candidate == nil || candidate.ID == "" {
			continue
		}
		candidateContentFingerprint := chunkContentFingerprint(candidate)
		candidate.ContentHash = candidateContentFingerprint
		candidateSourceMetadata := chunkSourceMetadataFor(candidate)
		existing, found := oldByID[candidate.ID]
		classification := types.RebuildChunkClassChangedNew
		switch {
		case !found:
			diff.ChangedNew = append(diff.ChangedNew, candidate)
		case chunkContentFingerprint(existing) != candidateContentFingerprint:
			matched[candidate.ID] = struct{}{}
			diff.ChangedNew = append(diff.ChangedNew, candidate)
		case chunkSourceMetadataFor(existing) != candidateSourceMetadata:
			matched[candidate.ID] = struct{}{}
			classification = types.RebuildChunkClassMetadataOnly
			diff.MetadataOnly = append(diff.MetadataOnly, chunkMetadataUpdate{Existing: existing, Candidate: candidate})
		default:
			matched[candidate.ID] = struct{}{}
			classification = types.RebuildChunkClassUnchanged
			diff.Unchanged = append(diff.Unchanged, existing)
		}
		diff.Results = append(diff.Results, &types.KnowledgeRebuildChunkResult{
			ChunkID:             candidate.ID,
			ChunkType:           candidate.ChunkType,
			Classification:      classification,
			ContentFingerprint:  candidateContentFingerprint,
			MetadataFingerprint: jsonStableHash(candidateSourceMetadata),
		})
	}

	for _, existing := range oldChunks {
		if existing == nil || existing.ID == "" {
			continue
		}
		if _, ok := matched[existing.ID]; ok {
			continue
		}
		diff.Stale = append(diff.Stale, existing)
		diff.Results = append(diff.Results, newRebuildChunkResult(existing, types.RebuildChunkClassStale))
	}

	sort.Slice(diff.Results, func(i, j int) bool {
		if diff.Results[i].Classification == diff.Results[j].Classification {
			return diff.Results[i].ChunkID < diff.Results[j].ChunkID
		}
		return diff.Results[i].Classification < diff.Results[j].Classification
	})
	return diff
}

func chunkContentFingerprint(chunk *types.Chunk) string {
	if chunk == nil {
		return ""
	}
	return stableHash(
		"chunk-content-v1",
		string(chunk.ChunkType),
		normalizedContentHash(chunk.Content),
	)
}

func chunkMetadataFingerprint(chunk *types.Chunk) string {
	if chunk == nil {
		return ""
	}
	return jsonStableHash(chunkSourceMetadataFor(chunk))
}

func chunkSourceMetadataFor(chunk *types.Chunk) chunkSourceMetadata {
	if chunk == nil {
		return chunkSourceMetadata{}
	}
	return chunkSourceMetadata{
		TenantID:        chunk.TenantID,
		KnowledgeID:     chunk.KnowledgeID,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
		TagID:           chunk.TagID,
		Content:         chunk.Content,
		ChunkIndex:      chunk.ChunkIndex,
		IsEnabled:       chunk.IsEnabled,
		Flags:           chunk.Flags,
		StartAt:         chunk.StartAt,
		EndAt:           chunk.EndAt,
		PreChunkID:      chunk.PreChunkID,
		NextChunkID:     chunk.NextChunkID,
		ChunkType:       chunk.ChunkType,
		ParentChunkID:   chunk.ParentChunkID,
		ImageInfo:       chunk.ImageInfo,
	}
}

func newRebuildChunkResult(chunk *types.Chunk, classification string) *types.KnowledgeRebuildChunkResult {
	return &types.KnowledgeRebuildChunkResult{
		ChunkID:             chunk.ID,
		ChunkType:           chunk.ChunkType,
		Classification:      classification,
		ContentFingerprint:  chunkContentFingerprint(chunk),
		MetadataFingerprint: chunkMetadataFingerprint(chunk),
	}
}

func mergeChunkSourceMetadata(existing, candidate *types.Chunk) *types.Chunk {
	if existing == nil || candidate == nil {
		return nil
	}
	merged := *existing
	merged.TenantID = candidate.TenantID
	merged.KnowledgeID = candidate.KnowledgeID
	merged.KnowledgeBaseID = candidate.KnowledgeBaseID
	merged.TagID = candidate.TagID
	merged.Content = candidate.Content
	merged.ChunkIndex = candidate.ChunkIndex
	merged.IsEnabled = candidate.IsEnabled
	merged.Flags = candidate.Flags
	merged.StartAt = candidate.StartAt
	merged.EndAt = candidate.EndAt
	merged.PreChunkID = candidate.PreChunkID
	merged.NextChunkID = candidate.NextChunkID
	merged.ChunkType = candidate.ChunkType
	merged.ParentChunkID = candidate.ParentChunkID
	merged.ImageInfo = candidate.ImageInfo
	merged.ContentHash = candidate.ContentHash
	merged.UpdatedAt = time.Now()
	return &merged
}

func filterChunksByType(chunks []*types.Chunk, allowed []types.ChunkType) []*types.Chunk {
	if len(chunks) == 0 || len(allowed) == 0 {
		return nil
	}
	allowedSet := make(map[types.ChunkType]struct{}, len(allowed))
	for _, chunkType := range allowed {
		allowedSet[chunkType] = struct{}{}
	}
	filtered := make([]*types.Chunk, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		if _, ok := allowedSet[chunk.ChunkType]; ok {
			filtered = append(filtered, chunk)
		}
	}
	return filtered
}

func chunkIDs(chunks []*types.Chunk) []string {
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil && chunk.ID != "" {
			ids = append(ids, chunk.ID)
		}
	}
	return ids
}

func imageChunksForImage(
	chunks []*types.Chunk,
	imageURL, parentChunkID string,
	imageIndex int,
) []*types.Chunk {
	if imageURL == "" {
		return nil
	}
	var exact []*types.Chunk
	var legacy []*types.Chunk
	for _, chunk := range chunks {
		if chunk == nil || (chunk.ChunkType != types.ChunkTypeImageOCR && chunk.ChunkType != types.ChunkTypeImageCaption) {
			continue
		}
		if parentChunkID != "" && chunk.ParentChunkID != parentChunkID {
			continue
		}
		var infos []types.ImageInfo
		if json.Unmarshal([]byte(chunk.ImageInfo), &infos) == nil {
			for _, info := range infos {
				if strings.TrimSpace(info.URL) == imageURL || strings.TrimSpace(info.OriginalURL) == imageURL {
					if info.ImageIndex != nil {
						if *info.ImageIndex == imageIndex {
							exact = append(exact, chunk)
						}
					} else {
						legacy = append(legacy, chunk)
					}
					break
				}
			}
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return legacy
}
