package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// wikiSourceDocument is a source document named by a model-supplied
// source_refs entry, resolved once against server data. Every Wiki mutation
// tool derives both the stored "uuid|title" refs and any KB routing hints from
// this single resolution, so the model-supplied title suffix is never trusted
// and each document is looked up exactly once.
type wikiSourceDocument struct {
	ID              string
	Title           string
	KnowledgeBaseID string
}

// resolveWikiSourceDocuments parses model-supplied source_refs (bare IDs or
// legacy "uuid|title" entries), deduplicates them, and rebuilds each entry
// from the knowledge record. With enforceScope the document must also lie
// inside the Agent's SearchTargets. Without a knowledge service the IDs are
// kept as given (bare, trimmed); this is only reachable when scope
// enforcement is off, which production wiring never does.
func resolveWikiSourceDocuments(
	ctx context.Context,
	refs []string,
	knowledgeService interfaces.KnowledgeService,
	searchTargets types.SearchTargets,
	enforceScope bool,
) ([]wikiSourceDocument, error) {
	resolved := make([]wikiSourceDocument, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		knowledgeID, _ := types.ParseWikiSourceRef(ref)
		if knowledgeID == "" {
			continue
		}
		var knowledge *types.Knowledge
		switch {
		case enforceScope:
			var err error
			knowledge, err = authorizeKnowledgeInSearchTargets(ctx, searchTargets, knowledgeID, knowledgeService)
			if err != nil {
				return nil, err
			}
		case knowledgeService != nil:
			var err error
			knowledge, err = knowledgeService.GetKnowledgeByIDOnly(ctx, knowledgeID)
			if err != nil || knowledge == nil {
				if err == nil {
					err = fmt.Errorf("empty result")
				}
				return nil, fmt.Errorf("document %s not found: %w", knowledgeID, err)
			}
		default:
			knowledge = &types.Knowledge{ID: knowledgeID}
		}
		if _, exists := seen[knowledge.ID]; exists {
			continue
		}
		seen[knowledge.ID] = struct{}{}
		title := strings.TrimSpace(knowledge.Title)
		if title == "" {
			title = strings.TrimSpace(knowledge.FileName)
		}
		resolved = append(resolved, wikiSourceDocument{
			ID:              knowledge.ID,
			Title:           title,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
		})
	}
	return resolved, nil
}

// wikiSourceRefs renders resolved documents as stored source_refs entries.
func wikiSourceRefs(docs []wikiSourceDocument) []string {
	refs := make([]string, 0, len(docs))
	for _, doc := range docs {
		if ref := types.FormatWikiSourceRef(doc.ID, doc.Title); ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

// wikiSourceKnowledgeIDs returns the bare IDs of resolved documents.
func wikiSourceKnowledgeIDs(docs []wikiSourceDocument) []string {
	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		ids = append(ids, doc.ID)
	}
	return ids
}

// wikiKnowledgeBasesForSourceDocuments returns the distinct knowledge bases
// owning the resolved documents, as routing hints for a new page. A document
// outside the allowed Wiki KBs fails closed; a document whose KB is unknown
// (no knowledge service) contributes no hint.
func wikiKnowledgeBasesForSourceDocuments(docs []wikiSourceDocument, allowedKBIDs []string) ([]string, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	allowed := make(map[string]struct{}, len(allowedKBIDs))
	for _, kbID := range dedupNonEmptyStrings(allowedKBIDs) {
		allowed[kbID] = struct{}{}
	}
	var kbIDs []string
	for _, doc := range docs {
		if doc.KnowledgeBaseID == "" {
			continue
		}
		if _, ok := allowed[doc.KnowledgeBaseID]; !ok {
			return nil, fmt.Errorf(
				"source document %s belongs to knowledge base %s, which is outside the Wiki scope",
				doc.ID, doc.KnowledgeBaseID,
			)
		}
		kbIDs = append(kbIDs, doc.KnowledgeBaseID)
	}
	return dedupNonEmptyStrings(kbIDs), nil
}
