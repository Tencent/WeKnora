package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

var manualWikiOrderedListPattern = regexp.MustCompile(`^\d+[.)]\s+`)

type manualWikiMarkdownBlock struct {
	BlockType types.WikiBlockType
	Content   string
}

// publishManualWikiPage turns a user-authored page body into a mixed immutable
// block revision. Unchanged blocks inherit their previous author/source edges;
// changed or new blocks are manual and deliberately carry no source edges.
func (s *wikiPageService) publishManualWikiPage(
	ctx context.Context,
	previous *types.WikiPage,
	desired *types.WikiPage,
) (*types.WikiPage, error) {
	if desired == nil || s.provenancePublisher == nil || s.provenanceQuery == nil {
		return nil, errors.New("manual wiki provenance services are not configured")
	}

	var prior *types.WikiPageProvenanceResponse
	if previous != nil {
		loaded, err := s.provenanceQuery.GetPageProvenance(
			ctx, previous.TenantID, previous.KnowledgeBaseID, previous.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("load current block provenance: %w", err)
		}
		// Never copy sources from a stale revision onto text from a different
		// page version. Legacy/manual-fallback pages simply become all-manual
		// on their next user edit.
		if loaded.RevisionNo == previous.Version {
			prior = loaded
		}
	}

	request, err := buildManualWikiPublishRequest(previous, desired, prior)
	if err != nil {
		return nil, err
	}
	result, err := s.provenancePublisher.Publish(ctx, request)
	if err != nil {
		return nil, err
	}
	if result == nil || result.PageRevision == nil {
		return nil, errors.New("manual wiki provenance publisher returned no page revision")
	}

	published, err := s.repo.GetBySlug(ctx, desired.KnowledgeBaseID, desired.Slug)
	if err != nil {
		return nil, fmt.Errorf("reload manually published wiki page: %w", err)
	}
	return published, nil
}

func buildManualWikiPublishRequest(
	previous *types.WikiPage,
	desired *types.WikiPage,
	prior *types.WikiPageProvenanceResponse,
) (*types.WikiProvenancePublishRequest, error) {
	if desired == nil || desired.ID == "" || desired.TenantID == 0 ||
		desired.KnowledgeBaseID == "" || desired.Slug == "" {
		return nil, errors.New("manual wiki page scope is incomplete")
	}

	oldByRenderedContent := make(map[string][]types.WikiPageProvenanceBlock)
	if prior != nil {
		for _, block := range prior.Blocks {
			key := canonicalManualWikiBlock(renderStoredWikiBlock(block))
			oldByRenderedContent[key] = append(oldByRenderedContent[key], block)
		}
	}

	parsed := splitManualWikiMarkdown(desired.Content)
	if len(parsed) == 0 {
		// Keep an empty manually-created page attributable without inventing
		// visible text. The frontend renders only the manual marker.
		parsed = []manualWikiMarkdownBlock{{BlockType: types.WikiBlockDocument}}
	}

	blocks := make([]types.WikiPageBlock, 0, len(parsed))
	sources := make([]types.WikiBlockSource, 0)
	retainedKnowledge := make(map[string]string)
	retainedChunks := make(map[string]struct{})
	hasManualBlock := false
	manualLogicalIDs := make(map[string]int)

	for index, parsedBlock := range parsed {
		alias := fmt.Sprintf("manual-block-%d", index)
		key := canonicalManualWikiBlock(parsedBlock.Content)
		candidates := oldByRenderedContent[key]
		if len(candidates) > 0 {
			old := candidates[0]
			oldByRenderedContent[key] = candidates[1:]
			blocks = append(blocks, types.WikiPageBlock{
				ID:               alias,
				LogicalBlockID:   old.LogicalBlockID,
				BlockType:        old.BlockType,
				SortOrder:        index,
				Content:          old.Content,
				AuthorType:       old.AuthorType,
				ProvenanceStatus: old.ProvenanceStatus,
				Metadata:         types.JSON(`{}`),
			})
			if old.AuthorType == types.WikiBlockAuthorManual {
				hasManualBlock = true
			}
			for _, source := range old.Sources {
				chunkID := source.ChunkID
				sources = append(sources, types.WikiBlockSource{
					BlockID:             alias,
					KnowledgeID:         source.KnowledgeID,
					KnowledgeRevisionID: source.KnowledgeRevisionID,
					ChunkID:             chunkID,
					SourceStart:         source.SourceStart,
					SourceEnd:           source.SourceEnd,
					EvidenceHash:        source.EvidenceHash,
					SourceRole:          source.SourceRole,
					Confidence:          source.Confidence,
					ValidationStatus:    source.ValidationStatus,
					Metadata:            types.JSON(`{}`),
				})
				retainedKnowledge[source.KnowledgeID] = source.KnowledgeTitle
				if source.ChunkID != nil && *source.ChunkID != "" {
					retainedChunks[*source.ChunkID] = struct{}{}
				}
			}
			continue
		}

		hasManualBlock = true
		baseID := "manual-" + sha256Hex(string(parsedBlock.BlockType) + "\x00" + parsedBlock.Content)[:16]
		manualLogicalIDs[baseID]++
		logicalID := baseID
		if manualLogicalIDs[baseID] > 1 {
			logicalID = fmt.Sprintf("%s-%d", baseID, manualLogicalIDs[baseID])
		}
		blocks = append(blocks, types.WikiPageBlock{
			ID:               alias,
			LogicalBlockID:   logicalID,
			BlockType:        parsedBlock.BlockType,
			SortOrder:        index,
			Content:          parsedBlock.Content,
			AuthorType:       types.WikiBlockAuthorManual,
			ProvenanceStatus: types.WikiProvenancePartial,
			Metadata:         types.JSON(`{}`),
		})
	}

	projection := *desired
	projection.Version = 0
	if previous != nil {
		projection.Version = previous.Version
	}
	projection.LastEditSource = types.WikiEditSourceUser
	projection.SourceRefs = retainedManualSourceRefs(desired.SourceRefs, retainedKnowledge)
	projection.ChunkRefs = retainedManualChunkRefs(desired.ChunkRefs, retainedChunks)

	provenanceStatus := types.WikiProvenancePartial
	if !hasManualBlock && prior != nil && prior.ProvenanceStatus != "" {
		provenanceStatus = prior.ProvenanceStatus
	}
	idempotencyPayload := struct {
		PageID          string
		ExpectedVersion int
		Title           string
		Summary         string
		Content         string
		PageType        string
		Status          string
		Aliases         types.StringArray
	}{
		PageID: projection.ID, ExpectedVersion: projection.Version,
		Title: projection.Title, Summary: projection.Summary, Content: projection.Content,
		PageType: projection.PageType, Status: projection.Status, Aliases: projection.Aliases,
	}
	encoded, _ := json.Marshal(idempotencyPayload)

	return &types.WikiProvenancePublishRequest{
		TenantID:        projection.TenantID,
		KnowledgeBaseID: projection.KnowledgeBaseID,
		PageID:          projection.ID,
		IdempotencyKey:  "wiki-manual:" + sha256Hex(string(encoded)),
		PageProjection:  projection,
		PageRevision: types.WikiProvenancePageRevision{
			Title: projection.Title, Summary: projection.Summary,
			RenderedContent: projection.Content, ProvenanceStatus: provenanceStatus,
		},
		Blocks:  blocks,
		Sources: sources,
	}, nil
}

func splitManualWikiMarkdown(content string) []manualWikiMarkdownBlock {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	blocks := make([]manualWikiMarkdownBlock, 0)
	current := make([]string, 0)
	inFence := false
	fenceMarker := ""
	flush := func() {
		value := strings.TrimSpace(strings.Join(current, "\n"))
		current = current[:0]
		if value == "" {
			return
		}
		blocks = append(blocks, manualWikiMarkdownBlock{
			BlockType: classifyManualWikiBlock(value),
			Content:   value,
		})
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inFence {
			current = append(current, line)
			if strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
				fenceMarker = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if len(current) > 0 {
				flush()
			}
			inFence = true
			fenceMarker = trimmed[:3]
			current = append(current, line)
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	return blocks
}

func classifyManualWikiBlock(content string) types.WikiBlockType {
	trimmed := strings.TrimSpace(content)
	switch {
	case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
		return types.WikiBlockCode
	case strings.HasPrefix(trimmed, "#"):
		return types.WikiBlockHeading
	case strings.HasPrefix(trimmed, ">"):
		return types.WikiBlockQuote
	case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") ||
		strings.HasPrefix(trimmed, "+ ") || manualWikiOrderedListPattern.MatchString(trimmed):
		return types.WikiBlockListItem
	case strings.HasPrefix(trimmed, "|"):
		return types.WikiBlockTableRow
	default:
		return types.WikiBlockParagraph
	}
}

func renderStoredWikiBlock(block types.WikiPageProvenanceBlock) string {
	content := strings.TrimSpace(block.Content)
	// Manual blocks already store the exact Markdown entered by the user.
	// Generated blocks store semantic content and receive Markdown prefixes
	// only when the page projection is rendered.
	if block.AuthorType == types.WikiBlockAuthorManual {
		return content
	}
	switch block.BlockType {
	case types.WikiBlockHeading:
		if !strings.HasPrefix(content, "#") {
			content = "## " + content
		}
	case types.WikiBlockListItem:
		if !strings.HasPrefix(content, "- ") && !strings.HasPrefix(content, "* ") {
			content = "- " + content
		}
	case types.WikiBlockQuote:
		if !strings.HasPrefix(content, ">") {
			content = "> " + content
		}
	}
	return content
}

func canonicalManualWikiBlock(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.TrimSpace(content)
}

func retainedManualSourceRefs(
	current types.StringArray,
	retained map[string]string,
) types.StringArray {
	result := make(types.StringArray, 0, len(retained))
	seen := make(map[string]struct{})
	for _, ref := range current {
		knowledgeID := strings.SplitN(ref, "|", 2)[0]
		if _, ok := retained[knowledgeID]; !ok {
			continue
		}
		if _, duplicate := seen[knowledgeID]; duplicate {
			continue
		}
		seen[knowledgeID] = struct{}{}
		result = append(result, ref)
	}
	missingKnowledgeIDs := make([]string, 0, len(retained))
	for knowledgeID := range retained {
		if _, duplicate := seen[knowledgeID]; duplicate {
			continue
		}
		missingKnowledgeIDs = append(missingKnowledgeIDs, knowledgeID)
	}
	sort.Strings(missingKnowledgeIDs)
	for _, knowledgeID := range missingKnowledgeIDs {
		title := retained[knowledgeID]
		ref := knowledgeID
		if title != "" {
			ref += "|" + title
		}
		result = append(result, ref)
	}
	return result
}

func retainedManualChunkRefs(
	current types.StringArray,
	retained map[string]struct{},
) types.StringArray {
	result := make(types.StringArray, 0, len(retained))
	seen := make(map[string]struct{})
	for _, chunkID := range current {
		if _, ok := retained[chunkID]; !ok {
			continue
		}
		if _, duplicate := seen[chunkID]; duplicate {
			continue
		}
		seen[chunkID] = struct{}{}
		result = append(result, chunkID)
	}
	missingChunkIDs := make([]string, 0, len(retained))
	for chunkID := range retained {
		if _, duplicate := seen[chunkID]; !duplicate {
			missingChunkIDs = append(missingChunkIDs, chunkID)
		}
	}
	sort.Strings(missingChunkIDs)
	result = append(result, missingChunkIDs...)
	return result
}
