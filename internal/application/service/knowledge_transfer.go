package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// Transfer state lives with the document, so queue retries do not depend on
// an expiring progress cache. It is server metadata, never custom metadata.
type knowledgeTransferState struct {
	TaskID       string                     `json:"task_id"`
	Operation    access.KBTransferOperation `json:"operation"`
	SourceKB     string                     `json:"source_kb"`
	TargetKB     string                     `json:"target_kb"`
	SourceID     string                     `json:"source_id"`
	Mode         string                     `json:"mode,omitempty"`
	WikiChunkIDs []string                   `json:"wiki_chunk_ids,omitempty"`
	WikiSummary  string                     `json:"wiki_summary,omitempty"`
	Phase        string                     `json:"phase"`
}

func transferState(k *types.Knowledge) (*knowledgeTransferState, error) {
	fields := map[string]json.RawMessage{}
	if len(k.Metadata) != 0 {
		if err := json.Unmarshal(k.Metadata, &fields); err != nil {
			return nil, err
		}
	}
	raw := fields[types.KnowledgeTransferMetadataKey]
	if len(raw) == 0 {
		return nil, nil
	}
	var state knowledgeTransferState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func setTransferState(k *types.Knowledge, state knowledgeTransferState) error {
	fields := map[string]json.RawMessage{}
	if len(k.Metadata) != 0 {
		if err := json.Unmarshal(k.Metadata, &fields); err != nil {
			return err
		}
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	fields[types.KnowledgeTransferMetadataKey] = raw
	k.Metadata, err = json.Marshal(fields)
	return err
}

func matchesTransfer(
	ctx context.Context,
	state *knowledgeTransferState,
	source, target *types.KnowledgeBase,
	operation access.KBTransferOperation,
	sourceID, mode string,
) bool {
	return state != nil && state.TaskID != "" && state.TaskID == access.TransferTaskID(ctx) &&
		state.Operation == operation &&
		state.SourceKB == source.ID &&
		state.TargetKB == target.ID &&
		state.SourceID == sourceID &&
		state.Mode == mode
}

func validateTransferKnowledge(k *types.Knowledge, kb *types.KnowledgeBase) error {
	if k == nil || k.ID == "" || k.TenantID != kb.TenantID || k.KnowledgeBaseID != kb.ID {
		return fmt.Errorf("knowledge binding does not match the transfer scope")
	}
	return nil
}

// Read every chunk type and validate persisted document/KB/tag relationships
// before a clone or move can delete, index, create tags, or copy files.
func (s *knowledgeService) transferChunks(
	ctx context.Context,
	k *types.Knowledge,
	allowedKBs ...string,
) ([]*types.Chunk, error) {
	allowed := map[string]bool{}
	for _, id := range allowedKBs {
		allowed[id] = true
	}
	var all []*types.Chunk
	seen := map[string]bool{}
	tags := map[string]string{}
	chunks, err := s.chunkRepo.ListAllChunksByKnowledgeID(ctx, k.TenantID, k.ID)
	if err != nil {
		return nil, err
	}
	for _, c := range chunks {
		if c == nil || c.ID == "" || seen[c.ID] || c.TenantID != k.TenantID || c.KnowledgeID != k.ID ||
			!allowed[c.KnowledgeBaseID] {
			return nil, fmt.Errorf("chunk binding does not match the transfer scope")
		}
		seen[c.ID] = true
		if c.TagID != "" && tags[c.TagID] != c.KnowledgeBaseID {
			tag, err := s.tagRepo.GetByID(ctx, k.TenantID, c.TagID)
			if err != nil {
				return nil, err
			}
			if tag == nil || tag.ID != c.TagID || tag.TenantID != k.TenantID ||
				tag.KnowledgeBaseID != c.KnowledgeBaseID {
				return nil, fmt.Errorf("chunk tag belongs to another knowledge base")
			}
			tags[c.TagID] = c.KnowledgeBaseID
		}
		all = append(all, c)
	}

	for _, c := range all {
		if c.ParentChunkID != "" && !seen[c.ParentChunkID] {
			return nil, fmt.Errorf("chunk parent belongs to another document")
		}
	}
	return all, nil
}

func (s *knowledgeService) planKnowledgeMove(
	ctx context.Context,
	source, target *types.KnowledgeBase,
	ids []string,
	mode string,
) ([]*types.Knowledge, error) {
	if err := access.RequireKBTransfer(ctx, source, target, access.KBTransferMove); err != nil {
		return nil, err
	}
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if err := access.ValidateKBTransferCompatibility(source, target, access.KBTransferMove, mode, tenant); err != nil {
		return nil, err
	}
	ids, err := writeResourceIDs(ids)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("knowledge IDs cannot be empty")
	}
	rows, err := s.repo.GetKnowledgeBatch(ctx, source.TenantID, ids)
	if err != nil {
		return nil, err
	}
	byID := map[string]*types.Knowledge{}
	for _, row := range rows {
		if row != nil {
			byID[row.ID] = row
		}
	}
	engineChecked := false
	result := make([]*types.Knowledge, 0, len(ids))
	for _, id := range ids {
		row := byID[id]
		if row == nil || row.TenantID != source.TenantID {
			return nil, fmt.Errorf("knowledge %s not found in transfer tenant", id)
		}
		if err := validateMoveItem(ctx, row, source, target, mode); err != nil {
			return nil, err
		}
		allowed := []string{row.KnowledgeBaseID}
		state, _ := transferState(row)
		if matchesTransfer(ctx, state, source, target, access.KBTransferMove, row.ID, mode) && state.Phase == "moving" {
			allowed = []string{source.ID, target.ID}
		}
		_, err = s.transferChunks(ctx, row, allowed...)
		if err != nil {
			return nil, err
		}
		if mode == "reuse_vectors" && row.EmbeddingModelID != "" {
			if row.EmbeddingModelID != source.EmbeddingModelID {
				return nil, fmt.Errorf("knowledge %s uses a different embedding model", row.ID)
			}
			if !engineChecked {
				engine, err := retriever.CreateRetrieveEngineForKB(
					ctx,
					s.retrieveEngine,
					s.ownership,
					source.TenantID,
					source.VectorStoreID,
				)
				if err != nil {
					return nil, err
				}
				if err := engine.ValidateKnowledgeIndexMove(ctx); err != nil {
					return nil, err
				}
				engineChecked = true
			}
		}
		snapshot := *row
		result = append(result, &snapshot)
	}
	return result, nil
}

func validateMoveItem(ctx context.Context, k *types.Knowledge, source, target *types.KnowledgeBase, mode string) error {
	if k == nil || k.TenantID != source.TenantID {
		return access.ErrForbidden
	}
	state, err := transferState(k)
	if err != nil {
		return err
	}
	if matchesTransfer(ctx, state, source, target, access.KBTransferMove, k.ID, mode) {
		switch state.Phase {
		case "moving":
			if k.KnowledgeBaseID == source.ID &&
				(k.ParseStatus == types.ParseStatusProcessing || k.ParseStatus == types.ParseStatusFailed) {
				return nil
			}
		case "reparse_pending", "done":
			if k.KnowledgeBaseID == target.ID {
				return nil
			}
		}
		return fmt.Errorf("knowledge %s changed during move", k.ID)
	}
	if k.KnowledgeBaseID != source.ID || k.ParseStatus != types.ParseStatusCompleted {
		return fmt.Errorf("knowledge %s must be completed in the source knowledge base", k.ID)
	}
	if state != nil && state.Phase != "done" {
		return fmt.Errorf("knowledge %s has an unfinished transfer", k.ID)
	}
	if mode == "reparse" {
		if k.IsManual() {
			meta, err := k.ManualMetadata()
			if err != nil || meta == nil || meta.Content == "" {
				return fmt.Errorf("knowledge %s has no manual content to reparse", k.ID)
			}
		} else if k.FilePath == "" {
			return fmt.Errorf("knowledge %s has no stored file to reparse", k.ID)
		}
	}
	return nil
}

type knowledgeClonePlan struct {
	add    []*types.Knowledge
	remove []string
}

func (s *knowledgeService) planKnowledgeClone(
	ctx context.Context,
	source, target *types.KnowledgeBase,
) (*knowledgeClonePlan, error) {
	if err := access.RequireKBTransfer(ctx, source, target, access.KBTransferClone); err != nil {
		return nil, err
	}
	src, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, source.TenantID, source.ID)
	if err != nil {
		return nil, err
	}
	dst, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, target.TenantID, target.ID)
	if err != nil {
		return nil, err
	}
	for _, pair := range []struct {
		rows []*types.Knowledge
		kb   *types.KnowledgeBase
	}{{src, source}, {dst, target}} {
		for _, k := range pair.rows {
			if err := validateTransferKnowledge(k, pair.kb); err != nil {
				return nil, err
			}
			if _, err := s.transferChunks(ctx, k, pair.kb.ID); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(src, func(i, j int) bool { return src[i].ID < src[j].ID })
	sort.Slice(dst, func(i, j int) bool { return dst[i].ID < dst[j].ID })
	plan := &knowledgeClonePlan{}
	used := map[string]bool{}
	for _, k := range src {
		if k.ParseStatus != types.ParseStatusCompleted {
			return nil, fmt.Errorf("source knowledge %s is not completed", k.ID)
		}
		matched := false
		// Prefer exact source bindings created by this task; unlike file hashes,
		// these also identify manual/empty-hash documents across retries.
		for _, other := range dst {
			state, err := transferState(other)
			if err != nil {
				return nil, err
			}
			if !used[other.ID] && matchesTransfer(ctx, state, source, target, access.KBTransferClone, k.ID, "") {
				used[other.ID] = true
				if state.Phase == "done" && other.ParseStatus == types.ParseStatusCompleted {
					matched = true
				} else {
					plan.remove = append(plan.remove, other.ID)
				}
				break
			}
		}
		if !matched && k.FileHash != "" {
			for _, other := range dst {
				if !used[other.ID] && other.FileHash == k.FileHash && other.ParseStatus == types.ParseStatusCompleted {
					used[other.ID] = true
					matched = true
					break
				}
			}
		}
		if !matched {
			plan.add = append(plan.add, k)
		}
	}
	for _, k := range dst {
		if used[k.ID] {
			continue
		}
		if k.ParseStatus == types.ParseStatusProcessing || k.ParseStatus == types.ParseStatusPending ||
			k.ParseStatus == types.ParseStatusDeleting {
			return nil, fmt.Errorf("target knowledge %s is busy", k.ID)
		}
		plan.remove = append(plan.remove, k.ID)
	}
	return plan, nil
}

func (s *knowledgeService) executeKnowledgeClone(
	ctx context.Context,
	source, target *types.KnowledgeBase,
	progress func(int, int),
) error {
	plan, err := s.planKnowledgeClone(ctx, source, target)
	if err != nil {
		return err
	}
	total, done := len(plan.add)+len(plan.remove), 0
	if progress != nil {
		progress(done, total)
	}
	if len(plan.remove) > 0 {
		// The complete binding/preflight plan above precedes the first deletion.
		if err := deleteReferencedKnowledge(ctx, s, target.ID, plan.remove); err != nil {
			return err
		}
		done += len(plan.remove)
		if progress != nil {
			progress(done, total)
		}
	}
	// Serial execution keeps storage accounting, tag creation and progress
	// coherent; each completed document is independently resumable.
	for _, k := range plan.add {
		if err := s.cloneKnowledge(ctx, k, target); err != nil {
			return err
		}
		done++
		if progress != nil {
			progress(done, total)
		}
	}
	return nil
}

// acknowledgeMovedReparse completes transfer admission, not document parsing.
// The target processing pipeline owns ParseStatus after its task is enqueued.
func (s *knowledgeService) acknowledgeMovedReparse(
	ctx context.Context,
	knowledge *types.Knowledge,
	source, target *types.KnowledgeBase,
) error {
	// The processing worker can already have updated the row after enqueue.
	// Reload before checkpointing; never overwrite its parse status or metadata.
	current, err := s.repo.GetKnowledgeByID(ctx, knowledge.TenantID, knowledge.ID)
	if err != nil {
		return err
	}
	if err := validateTransferKnowledge(current, target); err != nil {
		return err
	}
	state, err := transferState(current)
	if err != nil {
		return err
	}
	if !matchesTransfer(ctx, state, source, target, access.KBTransferMove, knowledge.ID, "reparse") {
		return access.ErrForbidden
	}
	if state.Phase == "done" {
		return nil
	}
	before := *current
	after := *current
	state.Phase = "done"
	if err := setTransferState(&after, *state); err != nil {
		return err
	}
	return s.repo.UpdateKnowledgeForTransfer(ctx, &before, &after)
}

func (s *knowledgeService) preflightFAQClone(
	ctx context.Context,
	source, target *types.KnowledgeBase,
) (map[string]*types.Chunk, map[string]*types.Chunk, error) {
	if err := access.RequireKBTransfer(ctx, source, target, access.KBTransferClone); err != nil {
		return nil, nil, err
	}
	maps := []map[string]*types.Chunk{{}, {}}
	for i, kb := range []*types.KnowledgeBase{source, target} {
		rows, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, kb.TenantID, kb.ID)
		if err != nil {
			return nil, nil, err
		}
		if len(rows) > 1 {
			return nil, nil, fmt.Errorf("FAQ knowledge base has multiple document containers")
		}
		for _, row := range rows {
			if err := validateTransferKnowledge(row, kb); err != nil {
				return nil, nil, err
			}
			if row.Type != types.KnowledgeTypeFAQ {
				return nil, nil, fmt.Errorf("invalid FAQ container")
			}
			chunks, err := s.transferChunks(ctx, row, kb.ID)
			if err != nil {
				return nil, nil, err
			}
			for _, chunk := range chunks {
				if chunk.ChunkType != types.ChunkTypeFAQ {
					return nil, nil, fmt.Errorf("invalid FAQ chunk type")
				}
				maps[i][chunk.ID] = chunk
			}
		}
	}
	return maps[0], maps[1], nil
}

func (s *knowledgeService) markMoveItemFailed(
	ctx context.Context,
	id string,
	source, target *types.KnowledgeBase,
	mode string,
	taskErr error,
) {
	if access.RequireKBTransfer(ctx, source, target, access.KBTransferMove) != nil {
		return
	}
	row, err := s.repo.GetKnowledgeByID(ctx, source.TenantID, id)
	if err != nil || row == nil || row.ID != id || row.TenantID != source.TenantID {
		return
	}
	state, err := transferState(row)
	if err != nil || !matchesTransfer(ctx, state, source, target, access.KBTransferMove, id, mode) {
		return
	}
	sourceFailed := row.KnowledgeBaseID == source.ID && state.Phase == "moving" &&
		row.ParseStatus == types.ParseStatusProcessing
	enqueueFailed := row.KnowledgeBaseID == target.ID && state.Phase == "reparse_pending" &&
		row.ParseStatus == types.ParseStatusPending
	if !sourceFailed && !enqueueFailed {
		return
	}
	before, after := *row, *row
	after.ParseStatus = types.ParseStatusFailed
	after.ErrorMessage = taskErr.Error()
	if err := s.repo.UpdateKnowledgeForTransfer(ctx, &before, &after); err != nil {
		logger.Errorf(ctx, "Failed to record move failure for knowledge %s, task %s: %v (move error: %v)",
			row.ID, state.TaskID, err, taskErr)
	}
}

// A stale parser must not start after a move has claimed a completed document,
// including the interval before its knowledge_base_id changes. Reparse tasks
// emitted by the move are admitted only once the destination checkpoint exists.
func validateProcessingKnowledge(knowledge *types.Knowledge, tenant uint64, kbID, knowledgeID string) error {
	if knowledge == nil || tenant == 0 || kbID == "" || knowledge.ID != knowledgeID || knowledge.TenantID != tenant ||
		knowledge.KnowledgeBaseID != kbID {
		return fmt.Errorf("processing task binding changed: %w", asynq.SkipRetry)
	}
	state, err := transferState(knowledge)
	if err != nil {
		return fmt.Errorf("invalid processing metadata: %v: %w", err, asynq.SkipRetry)
	}
	if state != nil && state.Operation == access.KBTransferMove && state.Phase == "moving" {
		return fmt.Errorf("knowledge is being moved: %w", asynq.SkipRetry)
	}
	return nil
}

// Cleanup consumes the durable source references only after target ownership
// is committed. A done transfer is retried here when wiki reconciliation fails.
func (s *knowledgeService) cleanupMovedSourceWiki(ctx context.Context, knowledge *types.Knowledge,
	source, target *types.KnowledgeBase,
) error {
	if !source.IsWikiEnabled() {
		return nil
	}
	if knowledge.KnowledgeBaseID != target.ID {
		return access.ErrForbidden
	}
	state, err := transferState(knowledge)
	if err != nil {
		return err
	}
	if state == nil || state.SourceKB != source.ID || state.TargetKB != target.ID {
		return access.ErrForbidden
	}
	old := *knowledge
	old.KnowledgeBaseID = source.ID
	old.Description = state.WikiSummary
	refs := make(map[string]bool, len(state.WikiChunkIDs))
	for _, id := range state.WikiChunkIDs {
		refs[id] = true
	}
	return s.cleanupWikiReferences(ctx, &old, refs)
}
