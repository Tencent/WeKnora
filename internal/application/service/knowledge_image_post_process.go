package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// workerReindexContext returns a context the chunk edit path can reindex with.
//
// The rules run inside an Asynq worker, whose context carries the tenant id but
// not the tenant itself. The retrieve-engine factory reads the whole tenant
// when it resolves a knowledge base that is not bound to a vector store, so an
// edit made without it saves the new content and then fails the reindex — the
// block ends up marked failed while the database already holds the change,
// which is the worst of both. Hydrating the context is what the summary refresh
// worker does for the same reason (restoreSummaryRefreshTenantInfo).
//
// A tenant that cannot be loaded is not fatal. The edit itself is still correct
// and the database stays consistent, so the run continues on the context it was
// given and lets the chunk service report the reindex failure it would have
// reported anyway. Skipping the rules over a lookup failure would be a worse
// trade: the document would keep its decorative images for no visible reason.
func (s *KnowledgePostProcessService) workerReindexContext(
	ctx context.Context,
	tenantID uint64,
) context.Context {
	if s.tenantRepo == nil {
		logger.Warnf(ctx,
			"[PostProcess] no tenant repository wired: image rules edit chunks without a reindexable context")
		return ctx
	}
	tenant, err := s.tenantRepo.GetTenantByID(ctx, tenantID)
	if err != nil {
		logger.Warnf(ctx,
			"[PostProcess] cannot load tenant %d for chunk reindex: %v", tenantID, err)
		return ctx
	}
	if tenant == nil {
		logger.Warnf(ctx, "[PostProcess] tenant %d not found for chunk reindex", tenantID)
		return ctx
	}
	return context.WithValue(ctx, types.TenantInfoContextKey, tenant)
}

// applyImagePostProcessRules evaluates the knowledge base's image rules against
// every image in the document and runs the ones that are inline.
//
// It returns the chunk list the rest of post-processing should work from: the
// blocks a rule retired are dropped from it. That matters because graph
// selection and the subtask count are both derived from this list, and a block
// whose only content was a decorative image should not go on to spawn an
// extraction subtask or a summary of nothing. Blocks a rule merely edited stay,
// carrying their new content, so the models downstream read the cleaned version
// rather than the one that was read from the database.
//
// Only inline actions run here. A deferred action is matched and counted but not
// executed: it exists to avoid holding the parse up, which needs a queue and a
// worker to be worth anything, and until a file-level action gives that path
// something to carry it stays unbuilt rather than unverified.
func (s *KnowledgePostProcessService) applyImagePostProcessRules(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
	kb *types.KnowledgeBase,
	textChunks []*types.Chunk,
	parentSpan *Span,
	policies map[string]types.ImageClassPolicy,
) []*types.Chunk {
	// Whether the engine runs at all is the caller's decision — it knows the
	// per-upload override, which this function cannot see. The guard here only
	// covers a missing knowledge base.
	if kb == nil {
		return textChunks
	}
	// The class table's disabled rows are the built-in rule set: each one
	// becomes a drop-reference rule for that class. Rules the knowledge base
	// configured on top are appended after them, so both sources act — a
	// hand-written rule can add retirements, but it cannot un-retire a class
	// the table disabled. "Switched on, nothing disabled, no rules" runs an
	// empty set on purpose: that is the user saying nothing should be removed.
	if policies == nil {
		policies = types.MergeImageClassPolicies(kb.ImageProcessingConfig.ClassPolicies)
	}
	rules := imageDisableRules(policies)
	rules = append(rules, kb.ImageProcessingConfig.PostProcessImageRules...)

	// Every image in the document, grouped under the block that references it.
	// The image children are already in hand — the caller fetched them alongside
	// the text blocks — so pairing them up costs no second query.
	var candidates []*ImageCandidate
	for _, block := range textChunks {
		if block == nil || block.ChunkType != types.ChunkTypeText {
			continue
		}
		candidates = append(candidates, BuildImageCandidates(block, textChunks)...)
	}
	if len(candidates) == 0 {
		return textChunks
	}

	ctx = s.workerReindexContext(ctx, tenantID)

	span := s.tracker().BeginSubSpan(ctx, parentSpan, "postprocess.image_rules", "subspan",
		types.JSONMap{"rules": len(rules), "images": len(candidates)})

	registry := NewImageActionRegistry(newDropImageReferenceAction(s.chunkService))
	matches, notes := PlanImageActions(rules, registry, candidates)

	outcomes := map[string]int{}
	retiredBlocks := make(map[string]bool, len(matches))
	editedBlocks := make(map[string]bool, len(matches))

	for _, match := range matches {
		if match.Action.Phase() != ActionPhaseInline {
			outcomes[string(ActionPhaseDeferred)]++
			continue
		}
		result, err := match.Action.Apply(ctx, &ActionRequest{
			TenantID:    tenantID,
			KnowledgeID: knowledgeID,
			Rule:        match.Rule,
			Candidate:   match.Candidate,
		})
		if err != nil {
			// One image's failure must not cost the document the rest of its
			// rules, let alone the parse. It is logged and counted instead.
			logger.Warnf(ctx, "[PostProcess] image rule %q failed on %s: %v",
				ruleLabel(match.Rule), match.Candidate.URL, err)
			outcomes[ActionResultFailed]++
			continue
		}
		if result == nil {
			outcomes[ActionResultSkipped]++
			continue
		}
		outcomes[result.Outcome]++
		if result.Outcome != ActionResultApplied {
			continue
		}
		if block := match.Candidate.ParentChunk; block != nil {
			editedBlocks[block.ID] = true
			if !block.IsEnabled {
				retiredBlocks[block.ID] = true
			}
		}
	}

	output := types.JSONMap{
		"rules":          len(rules),
		"images":         len(candidates),
		"matched":        len(matches),
		"outcomes":       outcomes,
		"blocks_edited":  len(editedBlocks),
		"blocks_retired": len(retiredBlocks),
	}
	if len(notes) > 0 {
		output["rule_notes"] = notes
		logger.Warnf(ctx, "[PostProcess] %d image rule(s) were skipped: %v", len(notes), notes)
	}
	s.tracker().EndSpan(ctx, span, output)

	if len(editedBlocks) > 0 {
		logger.Infof(ctx, "[PostProcess] image rules matched %d of %d image(s), edited %d block(s), retired %d",
			len(matches), len(candidates), len(editedBlocks), len(retiredBlocks))
	}

	return filterRetiredBlocks(textChunks, retiredBlocks)
}

// imageDisableRules turns the class table's disabled rows into drop-reference
// rules, one per disabled class in the enum's stable order.
//
// The rules stay narrow on purpose. The class is the strongest evidence the
// pipeline has, and acting on it is reversible — the reference is removed and
// the image's own rows are switched off, nothing is deleted. A rule that
// matched more broadly would be a poor default to hand somebody who only
// flipped one switch.
func imageDisableRules(policies map[string]types.ImageClassPolicy) []types.ImageRule {
	disabled := types.DisabledImageClasses(policies)
	if len(disabled) == 0 {
		return nil
	}
	rules := make([]types.ImageRule, 0, len(disabled))
	for _, class := range disabled {
		rules = append(rules, types.ImageRule{
			ID:   fmt.Sprintf("disable-%s-images", class),
			Name: fmt.Sprintf("disable %s images", class),
			Match: types.ImageMatchSpec{
				Classes: []string{string(class)},
			},
			Action: DropImageReferenceActionName,
		})
	}
	return rules
}

// filterRetiredBlocks drops the blocks a rule switched off, along with the image
// children that hang off them. Only blocks retired by this pass are removed:
// anything that was already disabled before the rules ran stays in the list, so
// this pass changes nothing it did not itself decide.
func filterRetiredBlocks(chunks []*types.Chunk, retired map[string]bool) []*types.Chunk {
	if len(retired) == 0 {
		return chunks
	}
	kept := make([]*types.Chunk, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		if retired[chunk.ID] || retired[chunk.ParentChunkID] {
			continue
		}
		kept = append(kept, chunk)
	}
	return kept
}
