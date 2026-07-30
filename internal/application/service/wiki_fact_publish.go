package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// publishWikiFactPage is the only persistence path for LLM-generated fact
// pages. The visible wiki_pages projection and the immutable provenance ledger
// are committed together by WikiProvenancePublishService. Link and folder
// bookkeeping then runs idempotently against the committed projection.
func (s *wikiIngestService) publishWikiFactPage(
	ctx context.Context,
	page *types.WikiPage,
	output *wikiFactOutput,
) error {
	if s.provenance == nil {
		return errors.New("wiki provenance publisher is not configured")
	}
	if page == nil || output == nil {
		return errors.New("wiki fact page and output are required")
	}
	knowledgeIDs := wikiFactKnowledgeIDs(output)
	knowledges := make([]*types.Knowledge, 0, len(knowledgeIDs))
	if len(knowledgeIDs) > 0 {
		var err error
		knowledges, err = s.knowledgeSvc.GetKnowledgeBatch(ctx, page.TenantID, knowledgeIDs)
		if err != nil {
			return fmt.Errorf("load wiki fact source knowledge: %w", err)
		}
	}
	evidence := s.resolveWikiCitationEvidence(ctx, page.TenantID, page.KnowledgeBaseID, []string(page.ChunkRefs))
	parseAttempts := make(map[string]int, len(knowledgeIDs))
	for _, knowledgeID := range knowledgeIDs {
		parseAttempts[knowledgeID] = s.tracker().LatestAttempt(ctx, knowledgeID)
	}
	request, err := buildWikiFactPublishRequest(page, output, knowledges, evidence, parseAttempts)
	if err != nil {
		return err
	}
	result, err := s.provenance.Publish(ctx, request)
	if err != nil {
		return err
	}
	if result == nil || result.PageRevision == nil {
		return errors.New("wiki provenance publisher returned no page revision")
	}
	page.Version = result.PageRevision.RevisionNo
	page.Status = types.WikiPageStatusPublished

	// The transaction already wrote the user-visible fields. This second,
	// same-content update computes hierarchy caches and in/out links through
	// the existing Wiki service without creating another content revision.
	if _, err := s.wikiService.UpdatePage(ctx, page); err != nil {
		return fmt.Errorf("refresh published wiki page bookkeeping: %w", err)
	}
	return nil
}

func buildWikiFactPublishRequest(
	page *types.WikiPage,
	output *wikiFactOutput,
	knowledges []*types.Knowledge,
	evidence map[string]wikiCitationEvidence,
	parseAttempts map[string]int,
) (*types.WikiProvenancePublishRequest, error) {
	if page == nil || output == nil {
		return nil, errors.New("wiki fact page and output are required")
	}
	if page.ID == "" || page.TenantID == 0 || page.KnowledgeBaseID == "" || page.Slug == "" {
		return nil, errors.New("wiki fact page scope is incomplete")
	}

	knowledgeByID := make(map[string]*types.Knowledge, len(knowledges))
	for _, knowledge := range knowledges {
		if knowledge == nil || knowledge.TenantID != page.TenantID || knowledge.KnowledgeBaseID != page.KnowledgeBaseID {
			continue
		}
		knowledgeByID[knowledge.ID] = knowledge
	}
	knowledgeIDs := wikiFactKnowledgeIDs(output)
	revisionAliases := make(map[string]string, len(knowledgeIDs))
	revisions := make([]types.KnowledgeRevision, 0, len(knowledgeIDs))
	for _, knowledgeID := range knowledgeIDs {
		knowledge := knowledgeByID[knowledgeID]
		if knowledge == nil {
			return nil, types.ErrWikiPublishScopeNotFound
		}
		alias := "knowledge-revision:" + knowledgeID
		revisionAliases[knowledgeID] = alias
		revisions = append(revisions, types.KnowledgeRevision{
			ID:              alias,
			TenantID:        page.TenantID,
			KnowledgeBaseID: page.KnowledgeBaseID,
			KnowledgeID:     knowledgeID,
			ParseAttempt:    max(parseAttempts[knowledgeID], 0),
			ContentHash:     wikiKnowledgeContentHash(knowledge),
		})
	}

	blocks := make([]types.WikiPageBlock, 0, len(output.Blocks))
	sources := make([]types.WikiBlockSource, 0)
	for index, factBlock := range output.Blocks {
		blockAlias := factBlock.LogicalBlockID
		if blockAlias == "" {
			blockAlias = deterministicWikiFactLogicalID(factBlock.Type, factBlock.Content)
		}
		blocks = append(blocks, types.WikiPageBlock{
			ID:               blockAlias,
			TenantID:         page.TenantID,
			KnowledgeBaseID:  page.KnowledgeBaseID,
			PageID:           page.ID,
			LogicalBlockID:   blockAlias,
			BlockType:        factBlock.Type,
			SortOrder:        index,
			Content:          factBlock.Content,
			AuthorType:       types.WikiBlockAuthorGenerated,
			ProvenanceStatus: types.WikiProvenanceVerified,
			Metadata:         types.JSON(`{}`),
		})
		for _, citation := range factBlock.Citations {
			trusted, ok := evidence[citation.ChunkID]
			if !ok || trusted.KnowledgeID != citation.KnowledgeID {
				return nil, fmt.Errorf("wiki fact block %s has stale citation %s", blockAlias, citation.ChunkID)
			}
			revisionAlias := revisionAliases[citation.KnowledgeID]
			if revisionAlias == "" {
				return nil, types.ErrWikiPublishScopeNotFound
			}
			chunkID := citation.ChunkID
			sources = append(sources, types.WikiBlockSource{
				BlockID:             blockAlias,
				TenantID:            page.TenantID,
				KnowledgeBaseID:     page.KnowledgeBaseID,
				PageID:              page.ID,
				KnowledgeID:         citation.KnowledgeID,
				KnowledgeRevisionID: revisionAlias,
				ChunkID:             &chunkID,
				SourceStart:         -1,
				SourceEnd:           -1,
				EvidenceHash:        sha256Hex(trusted.Content),
				SourceRole:          citation.Role,
				Confidence:          1,
				ValidationStatus:    types.WikiSourceValidationVerified,
				Metadata:            types.JSON(`{}`),
			})
		}
	}
	if len(blocks) == 0 {
		return nil, errors.New("wiki fact page has no blocks")
	}

	projection := *page
	projection.Status = types.WikiPageStatusPublished
	projection.Content = renderWikiFactOutput(output)
	projection.Summary = output.Summary
	projection.ChunkRefs = wikiFactChunkIDs(output)
	projection.SourceRefs = sourceRefsForWikiFacts(projection.SourceRefs, nil, output)

	idempotencyPayload := struct {
		PageID  string
		Title   string
		Type    string
		Output  *wikiFactOutput
		Folder  string
		Aliases types.StringArray
	}{
		PageID:  page.ID,
		Title:   page.Title,
		Type:    page.PageType,
		Output:  output,
		Folder:  page.FolderID,
		Aliases: page.Aliases,
	}
	encoded, _ := json.Marshal(idempotencyPayload)

	return &types.WikiProvenancePublishRequest{
		TenantID:           page.TenantID,
		KnowledgeBaseID:    page.KnowledgeBaseID,
		PageID:             page.ID,
		IdempotencyKey:     "wiki-fact:" + sha256Hex(string(encoded)),
		PageProjection:     projection,
		KnowledgeRevisions: revisions,
		PageRevision: types.WikiProvenancePageRevision{
			TenantID:         page.TenantID,
			KnowledgeBaseID:  page.KnowledgeBaseID,
			PageID:           page.ID,
			Title:            page.Title,
			Summary:          output.Summary,
			RenderedContent:  projection.Content,
			ProvenanceStatus: types.WikiProvenanceVerified,
		},
		Blocks:  blocks,
		Sources: sources,
	}, nil
}

func wikiKnowledgeContentHash(knowledge *types.Knowledge) string {
	if knowledge == nil {
		return ""
	}
	if value := strings.TrimSpace(knowledge.FileHash); value != "" {
		if len(value) <= 64 {
			return value
		}
		return sha256Hex(value)
	}
	payload := struct {
		ID        string
		Source    string
		FileSize  int64
		UpdatedAt string
		Metadata  types.JSON
	}{
		ID:        knowledge.ID,
		Source:    knowledge.Source,
		FileSize:  knowledge.FileSize,
		UpdatedAt: knowledge.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		Metadata:  knowledge.Metadata,
	}
	encoded, _ := json.Marshal(payload)
	return sha256Hex(string(encoded))
}
