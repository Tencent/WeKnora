package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// The mapped input and the folder plan belong to the pending operation. A
// failed page must not send its completed siblings through extraction again.
type wikiMappedDocument struct {
	InputKey     string            `json:"input_key"`
	Result       *docIngestResult  `json:"result"`
	Updates      []SlugUpdate      `json:"updates"`
	Folders      map[string]string `json:"folders,omitempty"`
	FoldersReady bool              `json:"folders_ready,omitempty"`
}

func wikiMappedInputKey(content, lang string, model chat.Chat, batch *WikiBatchContext) string {
	data, _ := json.Marshal([]string{content, lang, model.GetModelID(), model.GetModelName(),
		string(batch.ExtractionGranularity.Normalize()), batch.ExtractionInstructions, batch.ContentInstructions})
	return wikiExtractionDigest(data)
}

// Persist stages using the same CAS and mutex as extraction blocks. This also
// prevents a late worker from restoring progress after its row was replaced.
func (s *wikiIngestService) saveWikiMappedDocument(ctx context.Context, tenantID uint64, op *WikiPendingOp, mapped *wikiMappedDocument) error {
	if op.Extraction == nil {
		op.Extraction = &wikiExtractionProgress{}
	}
	progress := op.Extraction
	progress.mu.Lock()
	defer progress.mu.Unlock()
	before := progress.persisted
	if len(before) == 0 {
		before = op.dbPayload
	}
	next := &wikiExtractionProgress{Version: progress.Version, Fingerprint: progress.Fingerprint,
		Total: progress.Total, Segments: progress.Segments, PreviousSlugs: progress.PreviousSlugs, Mapped: mapped}
	copyOp := *op
	copyOp.Extraction = next
	after, err := json.Marshal(copyOp)
	if err != nil {
		return err
	}
	if op.dbID != 0 {
		store, ok := s.pendingRepo.(interfaces.TaskPendingOpsPayloadCheckpointer)
		if !ok {
			return errors.New("wiki progress: repository cannot persist mapped input")
		}
		saveCtx, cancel := wikiIngestCleanupContext(ctx)
		defer cancel()
		updated, err := store.CompareAndSwapPayload(saveCtx, tenantID, op.dbID, before, after)
		if err != nil {
			return fmt.Errorf("save wiki mapped input: %w", err)
		}
		if !updated {
			return errors.New("wiki progress: operation deleted or changed")
		}
	}
	progress.Mapped = mapped
	progress.persisted = after
	return nil
}

func (s *wikiIngestService) planResumableWikiFolders(ctx context.Context, model chat.Chat, kb *types.KnowledgeBase,
	updates map[string][]SlugUpdate, lang string, ops []WikiPendingOp) (map[string]string, error) {
	result := make(map[string]string)
	if len(updates) == 0 {
		return result, nil
	}
	remaining := make(map[string][]SlugUpdate)
	ready := make(map[string]bool)
	for _, op := range ops {
		if op.Extraction != nil && op.Extraction.Mapped != nil && op.Extraction.Mapped.FoldersReady {
			ready[op.KnowledgeID] = true
			for slug, id := range op.Extraction.Mapped.Folders {
				result[slug] = id
			}
		}
	}
	for slug, group := range updates {
		for _, u := range group {
			if !ready[u.KnowledgeID] {
				remaining[slug] = append(remaining[slug], u)
			}
		}
	}
	if len(remaining) > 0 {
		for slug, id := range s.resolvePlannedFolders(ctx, kb, s.planBatchTaxonomy(ctx, model, kb, remaining, lang)) {
			result[slug] = id
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i := range ops {
		op := &ops[i]
		if op.Extraction == nil || op.Extraction.Mapped == nil || ready[op.KnowledgeID] {
			continue
		}
		mapped := *op.Extraction.Mapped
		mapped.Folders = make(map[string]string)
		for _, u := range mapped.Updates {
			if id := result[u.Slug]; id != "" {
				mapped.Folders[u.Slug] = id
			}
		}
		mapped.FoldersReady = true
		if err := s.saveWikiMappedDocument(ctx, kb.TenantID, op, &mapped); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// A receipt is committed in the same page write as its body and source refs.
// Reading it under the slug lock skips exactly this operation, including after
// a crash between the page commit and acknowledgement of the document task.
const wikiAppliedOperationsKey = "ingest_operations"

func wikiPageOperations(page *types.WikiPage) (map[string]string, error) {
	metadata, err := page.PageMetadata.Map()
	if err != nil {
		return nil, err
	}
	ids := make(map[string]string)
	if raw, ok := metadata[wikiAppliedOperationsKey]; ok {
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &ids); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func pendingWikiPageUpdates(page *types.WikiPage, updates []SlugUpdate, batch *WikiBatchContext) ([]SlugUpdate, error) {
	if batch == nil || len(batch.OperationKeys) == 0 {
		return updates, nil
	}
	ids, err := wikiPageOperations(page)
	if err != nil {
		return nil, err
	}
	pending := make([]SlugUpdate, 0, len(updates))
	for _, u := range updates {
		key := batch.OperationKeys[u.KnowledgeID]
		if key == "" || ids[u.KnowledgeID] != key {
			pending = append(pending, u)
		}
	}
	return pending, nil
}

func recordWikiPageOperations(page *types.WikiPage, updates []SlugUpdate, batch *WikiBatchContext) error {
	if batch == nil || len(batch.OperationKeys) == 0 {
		return nil
	}
	ids, err := wikiPageOperations(page)
	if err != nil {
		return err
	}
	for _, u := range updates {
		if id := batch.OperationKeys[u.KnowledgeID]; id != "" {
			ids[u.KnowledgeID] = id
		}
	}
	metadata, err := page.PageMetadata.Map()
	if err != nil {
		return err
	}
	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	metadata[wikiAppliedOperationsKey] = ids
	page.PageMetadata, err = json.Marshal(metadata)
	return err
}
