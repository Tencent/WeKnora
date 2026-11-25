package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// ErrInvalidWikiSourceRebuild identifies a rebuild that does not match the stored sources.
var ErrInvalidWikiSourceRebuild = errors.New("invalid Wiki source rebuild")

func (s *wikiPageService) RebuildSourceSections(
	ctx context.Context, kbID, slug string, req types.WikiSourceRebuildRequest,
) (*types.WikiSourceRebuildPreview, error) {
	page, err := s.repo.GetBySlug(ctx, kbID, slug)
	if err != nil {
		return nil, err
	}
	if page.PageType != types.WikiPageTypeEntity && page.PageType != types.WikiPageTypeConcept {
		return nil, fmt.Errorf("%w: only entity and concept pages can be rebuilt", ErrInvalidWikiSourceRebuild)
	}
	if req.ManualSummary == nil {
		return nil, fmt.Errorf("%w: manual_summary is required", ErrInvalidWikiSourceRebuild)
	}
	if req.Apply && req.OriginalContent == nil {
		return nil, fmt.Errorf("%w: original_content from the preview is required", ErrInvalidWikiSourceRebuild)
	}
	if req.Version != page.Version || (req.OriginalContent != nil && *req.OriginalContent != page.Content) {
		return nil, repository.ErrWikiPageConflict
	}

	parts := make([]wikiSourcePart, 0, len(req.Sections)+1)
	if req.ManualContent != "" {
		if hasWikiSourceMarkers(req.ManualContent) {
			return nil, fmt.Errorf("%w: manual_content contains source markers", ErrInvalidWikiSourceRebuild)
		}
		manual := strings.TrimRight(stripWikiInlineChunkCitations(req.ManualContent), "\n")
		parts = append(parts, wikiSourcePart{text: manual + "\n\n"})
	}
	for _, section := range req.Sections {
		text, err := wikiSourcePartText(section.Content)
		if err != nil || len(section.Sources) == 0 {
			return nil, fmt.Errorf("%w: each section needs content and source IDs", ErrInvalidWikiSourceRebuild)
		}
		parts = append(parts, wikiSourcePart{text: text, sources: section.Sources})
	}

	knownSources := make(map[string]bool)
	for _, id := range page.SourceKnowledgeIDs() {
		knownSources[id] = true
	}
	sources, proposedChunks := wikiSourcePartRefs(parts)
	if len(knownSources) == 0 || !maps.Equal(sources, knownSources) {
		return nil, fmt.Errorf("%w: sections must cover all current documents", ErrInvalidWikiSourceRebuild)
	}
	knownChunks := make(map[string]bool)
	for _, id := range page.ChunkRefs {
		knownChunks[id] = true
	}
	if len(knownChunks) != len(proposedChunks) ||
		slices.ContainsFunc(proposedChunks, func(id string) bool { return !knownChunks[id] }) {
		return nil, fmt.Errorf("%w: sections must cover all current chunk refs", ErrInvalidWikiSourceRebuild)
	}
	if err := s.validateWikiSourceChunkOwners(ctx, page, parts, proposedChunks); err != nil {
		return nil, err
	}

	proposed := renderWikiSourceParts(parts)
	preview := &types.WikiSourceRebuildPreview{
		Version: page.Version, OriginalContent: page.Content, Content: proposed,
		OriginalSummary: page.Summary, Summary: stripWikiInlineChunkCitations(strings.TrimSpace(*req.ManualSummary)),
	}
	if req.Apply {
		ctx = types.WithWikiPageWriteSnapshot(ctx, page)
		ctx = context.WithValue(ctx, wikiSourceRebuildKey{}, true)
		ctx = types.WithWikiEditSource(ctx, types.WikiEditSourceUser)
		if err := setWikiSourceSectionMode(page, true); err != nil {
			return nil, err
		}
		page.Content = proposed
		page.Summary = preview.Summary
		updated, err := s.UpdatePage(ctx, page)
		if err != nil {
			return nil, err
		}
		preview.Version, preview.Content, preview.Applied = updated.Version, updated.Content, true
	}
	return preview, nil
}

func (s *wikiPageService) validateWikiSourceChunkOwners(
	ctx context.Context, page *types.WikiPage, parts []wikiSourcePart, ids []string,
) error {
	owners := make(map[string]string, len(ids))
	for start := 0; start < len(ids); start += 500 {
		chunks, err := s.chunkRepo.ListChunksByID(ctx, page.TenantID, ids[start:min(start+500, len(ids))])
		if err != nil {
			return err
		}
		for _, chunk := range chunks {
			if chunk != nil && chunk.KnowledgeBaseID == page.KnowledgeBaseID {
				owners[chunk.ID] = chunk.KnowledgeID
			}
		}
	}
	for _, part := range parts {
		for source, chunkIDs := range part.sources {
			for _, id := range chunkIDs {
				if owners[id] != source {
					return fmt.Errorf("%w: chunk is missing or has another owner", ErrInvalidWikiSourceRebuild)
				}
			}
		}
	}
	return nil
}
