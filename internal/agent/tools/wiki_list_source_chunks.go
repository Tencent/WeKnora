package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiListSourceChunksTool struct {
	BaseTool
	wikiService      interfaces.WikiPageService
	knowledgeService interfaces.KnowledgeService
	scopes           []WikiScope
	routes           *WikiRouteResolver
}

func NewWikiListSourceChunksTool(
	wikiService interfaces.WikiPageService,
	knowledgeService interfaces.KnowledgeService,
	scopes []WikiScope,
	routes *WikiRouteResolver,
) types.Tool {
	if routes == nil {
		routes = NewWikiRouteResolver()
	}
	return &wikiListSourceChunksTool{
		BaseTool: NewBaseTool(
			ToolWikiListSourceChunks,
			`List every original document chunk cited by one or more wiki pages.
Use this after wiki_search or wiki_read_page when you need the real source text behind a knowledge point.
Returns the page identity, source documents, and all chunk_refs expanded to full chunk content — not a scan of the whole document.
Summary pages and pages with no citations return an empty chunk list with reason=no_chunk_refs.
Knowledge-base routing is automatic.`,
			json.RawMessage(`{
  "type": "object",
  "properties": {
    "slugs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "List of wiki page slugs (e.g. ['entity/acme-corp', 'concept/rag'])"
    }
  },
  "required": ["slugs"]
}`),
		),
		wikiService:      wikiService,
		knowledgeService: knowledgeService,
		scopes:           scopes,
		routes:           routes,
	}
}

func (t *wikiListSourceChunksTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Slug  any `json:"slug"`
		Slugs any `json:"slugs"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Invalid parameters: " + err.Error()}, nil
	}

	slugsToFetch := dedupNonEmptyStrings(append(
		parseStringOrArray(params.Slugs),
		parseStringOrArray(params.Slug)...,
	))
	if len(slugsToFetch) == 0 {
		return &types.ToolResult{Success: false, Error: "Missing 'slugs' parameter"}, nil
	}

	var fetchTags knowledgeTagsFetcher
	if t.knowledgeService != nil {
		fetchTags = t.knowledgeService.GetKnowledgeTags
	}

	var outputs []string
	var errs []string
	var allChunks []map[string]interface{}
	foundKBs := make(map[string][]string)

	for _, slug := range slugsToFetch {
		cachedScopes := t.routes.scopesForSlug(slug, t.scopes)
		effectiveScopes := append(
			append([]WikiScope(nil), cachedScopes...),
			scopesOutsideKBs(t.scopes, cachedScopes)...,
		)

		var hits []struct {
			result *types.WikiPageSourceChunksResult
			kbID   string
		}
		filteredOut := []string{}
		lookupFailed := false

		for _, sc := range effectiveScopes {
			kbID := sc.KnowledgeBaseID
			if kbID == "" {
				continue
			}
			result, err := t.wikiService.ListSourceChunksBySlug(ctx, kbID, slug)
			if err != nil {
				if !errors.Is(err, repository.ErrWikiPageNotFound) {
					lookupFailed = true
					errs = append(errs, fmt.Sprintf("Failed to list source chunks for '%s' in KB %s: %v", slug, kbID, err))
				}
				continue
			}
			if result == nil {
				continue
			}
			page := result.AsPage()
			passesScope, scopeErr := pagePassesWikiScope(ctx, page, sc, fetchTags)
			if scopeErr != nil {
				errs = append(errs, fmt.Sprintf("Failed to validate wiki scope for '%s' in KB %s: %v", slug, kbID, scopeErr))
				continue
			}
			if !passesScope {
				filteredOut = append(filteredOut, kbID)
				continue
			}
			hits = append(hits, struct {
				result *types.WikiPageSourceChunksResult
				kbID   string
			}{result, kbID})
			t.routes.remember(slug, kbID)
			foundKBs[slug] = append(foundKBs[slug], kbID)
		}

		if len(hits) == 0 {
			if len(filteredOut) > 0 {
				errs = append(errs, fmt.Sprintf(
					"Wiki page '%s' exists in %v but none of its source documents are within the scope pinned by the user",
					slug, filteredOut,
				))
			} else if !lookupFailed {
				errs = append(errs, fmt.Sprintf("Wiki page '%s' not found", slug))
			}
			continue
		}

		for _, h := range hits {
			outputs = append(outputs, renderWikiSourceChunks(h.kbID, h.result))
			allChunks = append(allChunks, sourceChunksAsDrawerItems(h.result)...)
		}
	}

	if len(outputs) == 0 {
		return &types.ToolResult{Success: false, Error: strings.Join(errs, "; ")}, nil
	}

	output := strings.Join(outputs, "\n\n")
	if len(errs) > 0 {
		output += fmt.Sprintf("\n\n<errors>\n%s\n</errors>", strings.Join(errs, "\n"))
	}

	data := map[string]interface{}{
		"display_type": "knowledge_chunks_list",
		"found_kbs":    foundKBs,
		"chunks":       allChunks,
	}
	if len(allChunks) > 0 {
		data["knowledge_id"] = allChunks[0]["knowledge_id"]
		data["knowledge_title"] = allChunks[0]["knowledge_title"]
		data["knowledge_base_id"] = allChunks[0]["knowledge_base_id"]
	}

	return &types.ToolResult{Success: true, Output: output, Data: data}, nil
}

func renderWikiSourceChunks(kbID string, result *types.WikiPageSourceChunksResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<wiki_source_chunks>\n")
	fmt.Fprintf(&sb, "<metadata>\n<knowledge_base_id>%s</knowledge_base_id>\n<link>[[%s|%s]]</link>\n<type>%s</type>\n<chunk_ref_count>%d</chunk_ref_count>\n",
		kbID, result.Slug, result.Title, result.PageType, result.ChunkRefCount)
	if result.Reason != "" {
		fmt.Fprintf(&sb, "<reason>%s</reason>\n", result.Reason)
	}
	if result.MissingCount > 0 {
		fmt.Fprintf(&sb, "<missing_count>%d</missing_count>\n", result.MissingCount)
	}
	sb.WriteString("</metadata>\n<sources>\n")
	if len(result.Sources) == 0 {
		sb.WriteString("(none)\n")
	} else {
		for _, src := range result.Sources {
			if src.Title != "" {
				fmt.Fprintf(&sb, `<source knowledge_id="%s">%s</source>`+"\n", src.KnowledgeID, src.Title)
			} else {
				fmt.Fprintf(&sb, `<source knowledge_id="%s"/>`+"\n", src.KnowledgeID)
			}
		}
	}
	sb.WriteString("</sources>\n")
	if result.Reason == types.WikiSourceChunksReasonNoRefs {
		sb.WriteString("<chunks count=\"0\" />\n")
		sb.WriteString("<message>This page has no chunk-level citations. Summary pages and citation misses do not bind original chunks.</message>\n")
		sb.WriteString("</wiki_source_chunks>")
		return sb.String()
	}

	fmt.Fprintf(&sb, "<chunks count=\"%d\">\n", len(result.Chunks))
	for _, c := range result.Chunks {
		if c.Missing {
			fmt.Fprintf(&sb, "<chunk id=%q missing=\"true\" />\n", c.ID)
			continue
		}
		fmt.Fprintf(&sb, "<chunk id=%q knowledge_id=%q chunk_index=\"%d\">\n%s\n</chunk>\n",
			c.ID, c.KnowledgeID, c.ChunkIndex, c.Content)
	}
	sb.WriteString("</chunks>\n</wiki_source_chunks>")
	return sb.String()
}

func sourceChunksAsDrawerItems(result *types.WikiPageSourceChunksResult) []map[string]interface{} {
	if result == nil {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(result.Chunks))
	for _, c := range result.Chunks {
		if c.Missing || strings.TrimSpace(c.Content) == "" {
			continue
		}
		out = append(out, map[string]interface{}{
			"chunk_id":          c.ID,
			"chunk_index":       c.ChunkIndex,
			"content":           c.Content,
			"knowledge_id":      c.KnowledgeID,
			"knowledge_title":   c.KnowledgeTitle,
			"knowledge_base_id": result.KnowledgeBaseID,
			"knowledge_base":    result.KnowledgeBaseID,
		})
	}
	return out
}
