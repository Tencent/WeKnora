package handler

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// maxBatchExplicitIDs is the per-request ID cap for select_all=false paths.
	maxBatchExplicitIDs = 200
	// maxSelectAllMatched is the safety cap for select_all=true after exclude.
	maxSelectAllMatched = 10000
)

// knowledgeBatchFilter mirrors ListKnowledge query filters for select_all batch ops.
// FolderPath is a pointer so an empty string (knowledge-base root) can be
// distinguished from "no folder filter".
type knowledgeBatchFilter struct {
	TagIDs          []string `json:"tag_ids,omitempty"`
	Keyword         string   `json:"keyword,omitempty"`
	FileType        string   `json:"file_type,omitempty"`
	ParseStatus     string   `json:"parse_status,omitempty"`
	Source          string   `json:"source,omitempty"`
	StartTime       string   `json:"start_time,omitempty"`
	EndTime         string   `json:"end_time,omitempty"`
	FolderPath      *string  `json:"folder_path,omitempty"`
	FolderRecursive bool     `json:"folder_recursive,omitempty"`
}

// knowledgeBatchSelection is the shared select_all / exclude_ids / filter block.
type knowledgeBatchSelection struct {
	SelectAll  bool                 `json:"select_all"`
	ExcludeIDs []string             `json:"exclude_ids,omitempty"`
	Filter     knowledgeBatchFilter `json:"filter"`
}

type resolvedBatchIDs struct {
	IDs           []string
	MatchedCount  int
	ExcludedCount int
}

func (f knowledgeBatchFilter) toListFilter() (types.KnowledgeListFilter, error) {
	out := types.KnowledgeListFilter{
		TagIDs:      dedupeKnowledgeIDs(f.TagIDs),
		Keyword:     f.Keyword,
		FileType:    f.FileType,
		ParseStatus: f.ParseStatus,
		Source:      f.Source,
	}
	if f.StartTime != "" {
		t, err := parseFilterTime(f.StartTime)
		if err != nil {
			return out, errors.NewBadRequestError("invalid start_time: " + err.Error())
		}
		out.UpdatedFrom = t
	}
	if f.EndTime != "" {
		t, err := parseFilterTime(f.EndTime)
		if err != nil {
			return out, errors.NewBadRequestError("invalid end_time: " + err.Error())
		}
		out.UpdatedTo = t
	}
	if f.FolderPath != nil {
		out.FolderPath = types.NormalizeKnowledgeFolderPath(*f.FolderPath)
		out.FolderScope = types.FolderScopeExact
		if f.FolderRecursive {
			out.FolderScope = types.FolderScopeSubtree
		}
	}
	return out, nil
}

// resolveBatchKnowledgeIDs resolves the target ID set for batch write APIs.
//
//   - select_all=false: explicitIDs are required, capped at maxBatchExplicitIDs,
//     and verified to exist in the given knowledge base.
//   - select_all=true: IDs are loaded from the DB via Filter, then exclude_ids
//     are removed; capped at maxSelectAllMatched. Existence is implied by the
//     filter query (no GetKnowledgeBatch round-trip).
func (h *KnowledgeHandler) resolveBatchKnowledgeIDs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	sel knowledgeBatchSelection,
	explicitIDs []string,
	emptyIDsMessage string,
) (*resolvedBatchIDs, error) {
	if emptyIDsMessage == "" {
		emptyIDsMessage = "ids cannot be empty"
	}

	if !sel.SelectAll {
		ids := dedupeKnowledgeIDs(explicitIDs)
		if len(ids) == 0 {
			return nil, errors.NewBadRequestError(emptyIDsMessage)
		}
		if len(ids) > maxBatchExplicitIDs {
			return nil, errors.NewBadRequestError(
				fmt.Sprintf("too many ids (max %d per batch)", maxBatchExplicitIDs))
		}
		if err := h.requireKnowledgeInKB(ctx, tenantID, kbID, ids); err != nil {
			return nil, err
		}
		return &resolvedBatchIDs{IDs: ids, MatchedCount: len(ids)}, nil
	}

	listFilter, err := sel.Filter.toListFilter()
	if err != nil {
		return nil, err
	}
	matched, err := h.kgService.ListKnowledgeIDsByFilter(ctx, kbID, listFilter)
	if err != nil {
		return nil, errors.NewInternalServerError(err.Error())
	}

	exclude := make(map[string]struct{}, len(sel.ExcludeIDs))
	for _, id := range dedupeKnowledgeIDs(sel.ExcludeIDs) {
		exclude[id] = struct{}{}
	}
	ids := make([]string, 0, len(matched))
	excludedCount := 0
	for _, id := range matched {
		if _, ok := exclude[id]; ok {
			excludedCount++
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, errors.NewBadRequestError("no knowledge entries matched the selection")
	}
	if len(ids) > maxSelectAllMatched {
		return nil, errors.NewBadRequestError(
			fmt.Sprintf("too many matched ids (max %d for select_all); narrow the filter or use clear-contents",
				maxSelectAllMatched))
	}
	return &resolvedBatchIDs{
		IDs:           ids,
		MatchedCount:  len(matched),
		ExcludedCount: excludedCount,
	}, nil
}

func chunkStringIDs(ids []string, size int) [][]string {
	if size <= 0 || len(ids) == 0 {
		return [][]string{ids}
	}
	chunks := make([][]string, 0, (len(ids)+size-1)/size)
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		chunks = append(chunks, ids[i:end])
	}
	return chunks
}
