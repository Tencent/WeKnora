package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ListSourceChunksBySlug expands a wiki page's chunk_refs into the
// original document chunks those IDs point at. This is the query-time
// evidence surface for "knowledge point → cited source text": every
// stored ref is returned, without the associate-search per-leaf cap.
func (s *wikiPageService) ListSourceChunksBySlug(
	ctx context.Context,
	kbID string,
	slug string,
) (*types.WikiPageSourceChunksResult, error) {
	page, err := s.GetPageBySlug(ctx, kbID, slug)
	if err != nil {
		return nil, err
	}

	sources := parseWikiAssocSources(page.SourceRefs)
	titleByKnowledge := make(map[string]string, len(sources))
	for _, src := range sources {
		if src.KnowledgeID != "" && src.Title != "" {
			titleByKnowledge[src.KnowledgeID] = src.Title
		}
	}

	refs := uniqueChunkRefs(page.ChunkRefs)
	out := &types.WikiPageSourceChunksResult{
		KnowledgeBaseID: page.KnowledgeBaseID,
		Slug:            page.Slug,
		Title:           page.Title,
		PageType:        page.PageType,
		Sources:         sources,
		Chunks:          []types.WikiPageSourceChunk{},
		ChunkRefCount:   len(refs),
	}
	if page.KnowledgeBaseID == "" {
		out.KnowledgeBaseID = kbID
	}
	if len(refs) == 0 {
		out.Reason = types.WikiSourceChunksReasonNoRefs
		return out, nil
	}

	loaded, err := s.loadAssocChunks(ctx, []*types.WikiPage{page})
	if err != nil {
		return nil, fmt.Errorf("load source chunks: %w", err)
	}

	out.Chunks = make([]types.WikiPageSourceChunk, 0, len(refs))
	for _, id := range refs {
		item := types.WikiPageSourceChunk{ID: id}
		c, ok := loaded[id]
		if !ok || c == nil {
			item.Missing = true
			out.MissingCount++
			out.Chunks = append(out.Chunks, item)
			continue
		}
		item.KnowledgeID = c.KnowledgeID
		item.KnowledgeTitle = titleByKnowledge[c.KnowledgeID]
		item.ChunkIndex = c.ChunkIndex
		item.Content = c.Content
		out.Chunks = append(out.Chunks, item)
	}
	return out, nil
}

func uniqueChunkRefs(refs types.StringArray) []string {
	if len(refs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, id := range refs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
