package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	listDocumentsDefaultPageSize = 20
	listDocumentsMaxPageSize     = 100
)

var listDocumentsTool = BaseTool{
	name: ToolListDocuments,
	description: "List the documents inside one knowledge base, newest first, with pagination and an optional " +
		"title filter.\nUse it to see what a knowledge base contains or to find a document by name; each entry " +
		"carries the dN handle that read_document accepts.",
	schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "knowledge_base_id": {
      "type": "string",
      "description": "bN knowledge-base handle from the runtime context",
      "minLength": 1
    },
    "keyword": {
      "type": "string",
      "description": "Optional substring to match against document titles and file names"
    },
    "page": {
      "type": "integer",
      "description": "1-based page number (default 1)",
      "minimum": 1
    },
    "page_size": {
      "type": "integer",
      "description": "Documents per page (default 20, max 100)",
      "minimum": 1,
      "maximum": 100
    }
  },
  "required": ["knowledge_base_id"]
}`),
}

// ListDocumentsInput defines the input parameters for list_documents.
type ListDocumentsInput struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Keyword         string `json:"keyword,omitempty"`
	Page            int    `json:"page,omitempty"`
	PageSize        int    `json:"page_size,omitempty"`
}

// ListDocumentsTool pages through the documents of one knowledge base.
type ListDocumentsTool struct {
	BaseTool
	knowledgeService interfaces.KnowledgeService
	searchTargets    types.SearchTargets
}

// NewListDocumentsTool creates a new list_documents tool.
func NewListDocumentsTool(
	knowledgeService interfaces.KnowledgeService, searchTargets types.SearchTargets,
) *ListDocumentsTool {
	return &ListDocumentsTool{
		BaseTool:         listDocumentsTool,
		knowledgeService: knowledgeService,
		searchTargets:    searchTargets,
	}
}

// Execute lists documents, honouring any document/tag scope pinned on the KB.
func (t *ListDocumentsTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var input ListDocumentsInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Failed to parse args: %v", err)}, err
	}
	kbID := strings.TrimSpace(input.KnowledgeBaseID)
	if kbID == "" {
		return &types.ToolResult{Success: false, Error: "knowledge_base_id is required"},
			fmt.Errorf("missing knowledge_base_id")
	}
	if err := validateKnowledgeBaseIDsInSearchTargets(t.searchTargets, []string{kbID}); err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, err
	}
	if t.knowledgeService == nil {
		return &types.ToolResult{Success: false, Error: "knowledge service is unavailable"},
			fmt.Errorf("service unavailable")
	}

	page := input.Page
	if page < 1 {
		page = 1
	}
	pageSize := input.PageSize
	if pageSize < 1 {
		pageSize = listDocumentsDefaultPageSize
	}
	if pageSize > listDocumentsMaxPageSize {
		pageSize = listDocumentsMaxPageSize
	}
	keyword := strings.TrimSpace(input.Keyword)

	filtered, total, err := t.listScoped(ctx, kbID, keyword, page, pageSize)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("failed to list documents: %v", err)}, err
	}

	documents := make([]map[string]interface{}, 0, len(filtered))
	var b strings.Builder
	fmt.Fprintf(&b, "<documents knowledge_base_id=\"%s\" total=\"%d\" page=\"%d\" page_size=\"%d\"",
		xmlEscape(kbID), total, page, pageSize)
	if keyword != "" {
		fmt.Fprintf(&b, " keyword=\"%s\"", xmlEscape(keyword))
	}
	b.WriteString(">\n")
	for _, k := range filtered {
		fmt.Fprintf(&b, "<document knowledge_id=\"%s\" title=\"%s\"", xmlEscape(k.ID), xmlEscape(k.Title))
		if k.FileType != "" {
			fmt.Fprintf(&b, " file_type=\"%s\"", xmlEscape(k.FileType))
		}
		if k.ParseStatus != "" {
			fmt.Fprintf(&b, " parse_status=\"%s\"", xmlEscape(formatParseStatus(k.ParseStatus)))
		}
		if !k.UpdatedAt.IsZero() {
			fmt.Fprintf(&b, " updated_at=\"%s\"", k.UpdatedAt.Format("2006-01-02"))
		}
		if k.Description != "" {
			fmt.Fprintf(&b, ">%s</document>\n", xmlEscape(k.Description))
		} else {
			b.WriteString(" />\n")
		}
		doc := map[string]interface{}{
			"knowledge_id": k.ID,
			"title":        k.Title,
			"description":  k.Description,
			"type":         k.Type,
			"source":       k.Source,
			"file_name":    k.FileName,
			"file_type":    k.FileType,
			"file_size":    k.FileSize,
			"parse_status": k.ParseStatus,
			"is_faq":       false,
		}
		if !k.UpdatedAt.IsZero() {
			doc["updated_at"] = k.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
		}
		documents = append(documents, doc)
	}
	b.WriteString("</documents>")

	data := map[string]interface{}{
		"display_type":      "document_info",
		"knowledge_base_id": kbID,
		"documents":         documents,
		"total_docs":        total,
		"page":              page,
		"page_size":         pageSize,
	}
	if keyword != "" {
		data["keyword"] = keyword
	}
	if int64(page*pageSize) < total {
		data["next_page"] = page + 1
	}
	output := b.String()
	if len(filtered) == 0 {
		if keyword != "" {
			output = fmt.Sprintf("No documents matching %q in knowledge base %s.", keyword, kbID)
		} else if total == 0 {
			output = fmt.Sprintf("Knowledge base %s has no documents.", kbID)
		}
	}
	return &types.ToolResult{Success: true, Output: output, Data: data}, nil
}

// listDocumentsScopeCap bounds how many tagged documents are loaded when a
// turn pins both explicit documents and tags on the same knowledge base, the
// one case where the union has to be paged in memory.
const listDocumentsScopeCap = 1000

// listScoped returns one page of the documents the turn may read, with a
// total that counts only those documents. A pinned @tag scope is pushed into
// the database filter; pinned @file documents are loaded directly. Filtering
// after paging would report totals for the whole base and leave holes in the
// page.
func (t *ListDocumentsTool) listScoped(
	ctx context.Context, kbID, keyword string, page, pageSize int,
) ([]*types.Knowledge, int64, error) {
	tenantID := t.searchTargets.GetTenantIDForKB(kbID)
	// Documents live under the knowledge base owner, which may differ from the
	// caller's tenant for organization-shared bases.
	listCtx := ctx
	if tenantID != 0 {
		listCtx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	}
	listPage := func(filter types.KnowledgeListFilter, p, size int) ([]*types.Knowledge, int64, error) {
		filter.Keyword = keyword
		result, err := t.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(
			listCtx, kbID, &types.Pagination{Page: p, PageSize: size}, filter,
		)
		if err != nil || result == nil {
			return nil, 0, err
		}
		rows, _ := result.Data.([]*types.Knowledge)
		return rows, result.Total, nil
	}

	var explicitIDs, tagIDs []string
	for _, target := range t.searchTargets {
		if target == nil || target.KnowledgeBaseID != kbID {
			continue
		}
		if searchTargetIsWholeKB(target) {
			return listPage(types.KnowledgeListFilter{}, page, pageSize)
		}
		ids, tags := searchTargetScope(target)
		explicitIDs = append(explicitIDs, ids...)
		tagIDs = append(tagIDs, tags...)
	}
	explicitIDs = dedupNonEmptyStrings(explicitIDs)
	tagIDs = dedupNonEmptyStrings(tagIDs)

	if len(explicitIDs) == 0 {
		if len(tagIDs) == 0 {
			return nil, 0, nil
		}
		return listPage(types.KnowledgeListFilter{TagIDs: tagIDs}, page, pageSize)
	}

	// Explicit documents (plus any tagged ones) form a small union that is
	// paged in memory with the same newest-first order as the database.
	union := make(map[string]*types.Knowledge)
	explicit, err := t.knowledgeService.GetKnowledgeBatch(listCtx, tenantID, explicitIDs)
	if err != nil {
		return nil, 0, err
	}
	for _, k := range explicit {
		if k != nil && k.KnowledgeBaseID == kbID && knowledgeMatchesKeyword(k, keyword) {
			union[k.ID] = k
		}
	}
	if len(tagIDs) > 0 {
		tagged, _, err := listPage(types.KnowledgeListFilter{TagIDs: tagIDs}, 1, listDocumentsScopeCap)
		if err != nil {
			return nil, 0, err
		}
		for _, k := range tagged {
			if k != nil {
				union[k.ID] = k
			}
		}
	}
	all := make([]*types.Knowledge, 0, len(union))
	for _, k := range union {
		all = append(all, k)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID < all[j].ID
	})
	total := int64(len(all))
	start := (page - 1) * pageSize
	if start >= len(all) {
		return nil, total, nil
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], total, nil
}

// knowledgeMatchesKeyword mirrors the list filter's title / file name match.
func knowledgeMatchesKeyword(k *types.Knowledge, keyword string) bool {
	if keyword == "" {
		return true
	}
	needle := strings.ToLower(keyword)
	return strings.Contains(strings.ToLower(k.Title), needle) ||
		strings.Contains(strings.ToLower(k.FileName), needle)
}
