package chatpipeline

import (
	"context"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

// Ranking works on expanded contexts. The model must instead see every source
// body under its own ID; a parent's neighbouring paragraph is not child evidence.
func (p *PluginMerge) attachCitationSources(ctx context.Context, results []*types.SearchResult) {
	if p.chunkRepo == nil {
		return
	}
	ids := map[string]bool{}
	for _, r := range results {
		if len(r.SubChunkID) > 0 {
			r.ContentRewritten = true
		}
		if r.ContentRewritten {
			ids[r.ID] = true
			for _, id := range r.SubChunkID {
				ids[id] = true
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	chunks, err := p.chunkRepo.ListChunksByIDOnly(ctx, keys)
	if err != nil {
		return
	}
	byID := map[string]*types.Chunk{}
	parents := map[string]bool{}
	for _, c := range chunks {
		byID[c.ID] = c
		if c.ParentChunkID != "" {
			parents[c.ParentChunkID] = true
		}
	}
	keys = nil
	for id := range parents {
		keys = append(keys, id)
	}
	// Image -> text -> parent_text. Fetch the intermediate text parent before
	// collecting siblings. All IDs originate in authorized retrieval results.
	if len(keys) > 0 {
		ancestors, e := p.chunkRepo.ListChunksByIDOnly(ctx, keys)
		if e == nil {
			for _, c := range ancestors {
				byID[c.ID] = c
				if c.ParentChunkID != "" {
					parents[c.ParentChunkID] = true
				}
			}
		}
	}
	keys = nil
	for id := range parents {
		keys = append(keys, id)
	}
	if len(keys) > 0 {
		siblings, e := p.chunkRepo.ListChunksByParentIDsOnly(ctx, keys)
		if e == nil {
			for _, c := range siblings {
				byID[c.ID] = c
			}
		}
	}
	// Fetch actual image children too: enrichment may have placed their OCR in
	// a text result, but that OCR needs the image child's own citation identity.
	textIDs := []string{}
	for _, c := range byID {
		if c.ChunkType == types.ChunkTypeText {
			textIDs = append(textIDs, c.ID)
		}
	}
	if len(textIDs) > 0 {
		images, e := p.chunkRepo.ListChunksByParentIDsOnly(ctx, textIDs)
		if e == nil {
			for _, c := range images {
				if c.ChunkType == types.ChunkTypeImageOCR || c.ChunkType == types.ChunkTypeImageCaption {
					byID[c.ID] = c
				}
			}
		}
	}
	for _, r := range results {
		if !r.ContentRewritten {
			continue
		}
		allowedParents := map[string]bool{}
		for _, id := range append([]string{r.ID}, r.SubChunkID...) {
			if c := byID[id]; c != nil {
				allowedParents[c.ParentChunkID] = true
				if parent := byID[c.ParentChunkID]; parent != nil && parent.ChunkType == types.ChunkTypeText {
					allowedParents[parent.ParentChunkID] = true
				}
			}
		}
		candidates := []*types.Chunk{}
		for _, c := range byID {
			if !c.IsEnabled ||
				c.KnowledgeID != r.KnowledgeID ||
				c.KnowledgeBaseID != r.KnowledgeBaseID ||
				c.Content == "" ||
				c.ChunkType == types.ChunkTypeParentText {
				continue
			}
			related := c.ID == r.ID || containsID(r.SubChunkID, c.ID) || allowedParents[c.ParentChunkID]
			if parent := byID[c.ParentChunkID]; parent != nil && (c.ChunkType == types.ChunkTypeImageOCR ||
				c.ChunkType == types.ChunkTypeImageCaption) {
				related = related || allowedParents[parent.ParentChunkID]
			}
			if !related {
				continue
			}
			body := searchutil.PruneMarkdownImagesByImageInfo(c.Content, r.ImageInfo)
			imageHit := false
			if c.ChunkType == types.ChunkTypeImageOCR || c.ChunkType == types.ChunkTypeImageCaption {
				imageHit = searchutil.FilterImageInfoByContentURLs(r.Content, c.ImageInfo) != ""
			}
			if c.ID == r.ID ||
				containsID(r.SubChunkID,
					c.ID) ||
				imageHit ||
				(strings.TrimSpace(body) != "" && strings.Contains(r.Content,
					body)) {
				candidates = append(candidates, c)
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].ChunkIndex != candidates[j].ChunkIndex {
				return candidates[i].ChunkIndex < candidates[j].ChunkIndex
			}
			return candidates[i].ID < candidates[j].ID
		})
		for _, c := range candidates {
			source := *r
			source.ID = c.ID
			source.Content = c.Content
			source.ChunkIndex = c.ChunkIndex
			source.ChunkType = string(c.ChunkType)
			source.ParentChunkID = c.ParentChunkID
			source.SourceLocators = c.SourceLocators
			source.ImageInfo = c.ImageInfo
			source.StartAt = c.StartAt
			source.EndAt = c.EndAt
			source.ContentRevision = c.ContentRevision
			source.ContextHeader = c.ContextHeader
			source.ChunkMetadata = c.Metadata
			source.MatchedContent = ""
			source.ContentRewritten = false
			source.SubChunkID = nil
			source.CitationSources = nil
			r.CitationSources = append(r.CitationSources, &source)
		}
	}
}

func expandCitationSources(results []*types.SearchResult) []*types.SearchResult {
	out := make([]*types.SearchResult, 0, len(results))
	seen := map[string]bool{}
	for _, r := range results {
		if r == nil {
			continue
		}
		sources := r.CitationSources
		if len(sources) == 0 && r.ContentRewritten {
			// A failed source lookup must not relabel expanded context as evidence.
			continue
		}
		if len(sources) == 0 {
			sources = []*types.SearchResult{r}
		}
		for _, s := range sources {
			if !seen[s.ID] {
				seen[s.ID] = true
				out = append(out, s)
			}
		}
	}
	return out
}
