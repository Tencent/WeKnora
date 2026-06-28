package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// Span name markers written by KnowledgePostProcessService. Matched best-effort
// to detect already-completed enrichment so resume only re-drives the gaps; if
// a marker ever drifts, the worst case is re-running an already-done chunk,
// which is idempotent (neo4j apoc.merge) — never data loss.
const (
	graphChunkSpanPrefix = "postprocess.graph.chunk"
	wikiSpanName         = "postprocess.wiki"
)

// ResumeEnrichment re-drives ONLY the missing enrichment work for a
// failed/finalizing knowledge: graph chunks that were never extracted, plus
// wiki if it was never synthesized. It is strictly additive:
//
//   - It never calls cleanupKnowledgeResources, so chunks / embeddings / graph
//     nodes / wiki pages already produced are kept verbatim — the expensive
//     iGPU embedding work is never repeated.
//   - Already-completed graph chunks (and wiki) are detected from the latest
//     attempt's span tree and SKIPPED, so prior success is neither duplicated
//     nor destroyed.
//   - Graph re-extraction writes neo4j via idempotent apoc.merge, so even a
//     mis-detected "missing" chunk just re-merges identical data.
//
// includeGraph: nil/true ⇒ resume the missing graph chunks; false ⇒ skip graph
// entirely (e.g. only re-drive wiki). triggerWiki=false suppresses the per-doc
// wiki trigger so a batch caller can fire a single trigger via TriggerWikiBatch
// (the fix for the wiki concurrency storm).
func (s *knowledgeService) ResumeEnrichment(
	ctx context.Context,
	knowledgeID string,
	includeGraph *bool,
	triggerWiki bool,
) (*types.Knowledge, error) {
	tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
	existing, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "[ResumeEnrichment] load knowledge %s: %v", knowledgeID, err)
		return nil, err
	}

	// Only resume rows that finished the main pipeline but stalled on the
	// enrichment tail. processing/pending means the main pipeline is still
	// running (resuming would race it); completed needs nothing; deleting /
	// cancelled must not be revived.
	switch existing.ParseStatus {
	case types.ParseStatusFailed, types.ParseStatusFinalizing:
		// resumable
	default:
		return nil, werrors.NewBadRequestError(fmt.Sprintf(
			"resume-enrichment only supports failed/finalizing knowledge, current status=%s",
			existing.ParseStatus))
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, existing.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "[ResumeEnrichment] get kb for %s: %v", existing.ID, err)
		return nil, err
	}
	processOverrides, _ := existing.ProcessOverrides()
	eff := ResolveProcessConfig(kb, processOverrides)

	graphAllowed := eff.GraphEnabled
	if includeGraph != nil && !*includeGraph {
		graphAllowed = false
	}
	wikiAllowed := kb.IsWikiEnabled()

	// Inspect the latest attempt's span tree to learn what already succeeded,
	// so we resume only the gaps. Done BEFORE OpenAttempt so we read the prior
	// attempt's completion state, not the fresh (empty) one.
	doneGraphChunks := map[string]bool{}
	wikiAlreadyDone := false
	if latest := s.tracker().LatestAttempt(ctx, existing.ID); latest > 0 {
		for _, sp := range s.tracker().ListAttemptSpans(ctx, existing.ID, latest) {
			if sp.Status != types.SpanStatusDone {
				continue
			}
			if sp.Name == wikiSpanName {
				wikiAlreadyDone = true
				continue
			}
			if strings.HasPrefix(sp.Name, graphChunkSpanPrefix) && sp.Input != nil {
				if cid, ok := sp.Input["chunk_id"].(string); ok && cid != "" {
					doneGraphChunks[cid] = true
				}
			}
		}
	}

	// Collect text-like chunks still missing graph extraction (read-only;
	// chunks and their embeddings are never modified or deleted here).
	var missingGraphChunks []*types.Chunk
	if graphAllowed {
		chunks, lerr := s.chunkService.ListChunksByKnowledgeID(ctx, existing.ID)
		if lerr != nil {
			return nil, fmt.Errorf("list chunks for %s: %w", existing.ID, lerr)
		}
		for _, c := range chunks {
			if c.ChunkType != types.ChunkTypeText &&
				c.ChunkType != types.ChunkTypeImageOCR &&
				c.ChunkType != types.ChunkTypeImageCaption {
				continue
			}
			if doneGraphChunks[c.ID] {
				continue // already extracted — skip, never re-run
			}
			missingGraphChunks = append(missingGraphChunks, c)
		}
	}

	wikiNeeded := wikiAllowed && !wikiAlreadyDone
	graphCount := len(missingGraphChunks)
	wikiCount := 0
	if wikiNeeded {
		wikiCount = 1
	}
	expected := graphCount + wikiCount

	// Everything already done → flip to completed so the row leaves the
	// failed/finalizing limbo without re-running anything.
	if expected == 0 {
		if uerr := s.repo.UpdateKnowledgeColumns(ctx, existing.ID, map[string]interface{}{
			"parse_status":           types.ParseStatusCompleted,
			"pending_subtasks_count": 0,
			"error_message":          "",
			"processed_at":           time.Now(),
			"updated_at":             time.Now(),
		}); uerr != nil {
			return nil, uerr
		}
		logger.Infof(ctx, "[ResumeEnrichment] %s: graph+wiki already complete, marked completed", existing.ID)
		return existing, nil
	}

	// Fresh attempt: stale tasks still queued under the previous attempt are
	// skipped by attemptSuperseded in their handlers, so they cannot decrement
	// THIS attempt's counter; the UI also shows a clean new run.
	newAttempt := 0
	if root, n, e := s.tracker().OpenAttempt(ctx, existing.ID, ""); e == nil && root != nil {
		newAttempt = n
	} else if e != nil {
		logger.Warnf(ctx, "[ResumeEnrichment] OpenAttempt failed for %s: %v (worker will fall back)", existing.ID, e)
	}

	// Move the row to finalizing with the EXACT counter (= what we enqueue
	// below). Direct column write: SetFinalizing only transitions from
	// 'processing', but we are resuming a failed/finalizing row.
	if uerr := s.repo.UpdateKnowledgeColumns(ctx, existing.ID, map[string]interface{}{
		"parse_status":           types.ParseStatusFinalizing,
		"pending_subtasks_count": expected,
		"error_message":          "",
		"updated_at":             time.Now(),
	}); uerr != nil {
		return nil, fmt.Errorf("set finalizing for %s: %w", existing.ID, uerr)
	}

	// Re-enqueue graph extraction for the MISSING chunks only, under the new
	// attempt. neo4j writes are idempotent, so this never corrupts existing
	// graph data.
	enqueuedGraph := 0
	for i, c := range missingGraphChunks {
		ok, e := NewChunkExtractTask(ctx, s.task, tenantID, c.ID, kb.SummaryModelID, existing.ID, newAttempt, i)
		if e != nil {
			logger.Errorf(ctx, "[ResumeEnrichment] enqueue graph chunk %s: %v", c.ID, e)
		}
		if ok {
			enqueuedGraph++
		}
	}
	// Release counter slots for graph tasks that were planned but not actually
	// enqueued (e.g. NEO4J disabled → NewChunkExtractTask returns false), so
	// the row isn't stranded waiting on a task that will never run.
	if shortfall := graphCount - enqueuedGraph; shortfall > 0 {
		for k := 0; k < shortfall; k++ {
			if _, _, e := s.repo.FinalizeSubtask(ctx, existing.ID); e != nil {
				logger.Warnf(ctx, "[ResumeEnrichment] release graph slot for %s: %v", existing.ID, e)
			}
		}
	}

	// Wiki: persist the pending op (dedup by knowledge_id). In batch mode the
	// caller passes triggerWiki=false and fires ONE trigger at the end via
	// TriggerWikiBatch — the fix for the wiki concurrency storm where N per-doc
	// triggers all race the per-KB lock and dead-letter. The wiki handler's
	// scheduleFollowUp drains the whole pending queue from a single trigger.
	if wikiCount > 0 {
		s.enqueueWikiPendingOp(ctx, tenantID, existing.KnowledgeBaseID, existing.ID)
		if triggerWiki {
			if e := s.TriggerWikiBatch(ctx, existing.KnowledgeBaseID); e != nil {
				logger.Warnf(ctx, "[ResumeEnrichment] trigger wiki for kb %s: %v", existing.KnowledgeBaseID, e)
			}
		}
	}

	logger.Infof(ctx,
		"[ResumeEnrichment] %s attempt=%d graph_missing=%d graph_enqueued=%d graph_done_skipped=%d wiki_needed=%v pending_subtasks=%d",
		existing.ID, newAttempt, graphCount, enqueuedGraph, len(doneGraphChunks), wikiNeeded, expected)
	return existing, nil
}

// enqueueWikiPendingOp writes a wiki ingest pending op to task_pending_ops
// WITHOUT enqueuing a trigger task. Batch resume uses this so it doesn't fire
// one trigger per document (which would storm the per-KB wiki lock); a single
// trigger is fired afterwards via TriggerWikiBatch.
func (s *knowledgeService) enqueueWikiPendingOp(ctx context.Context, tenantID uint64, kbID, knowledgeID string) {
	if s.taskPendingRepo == nil {
		return
	}
	lang, _ := types.LanguageFromContext(ctx)
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: knowledgeID, Language: lang}
	payloadBytes, err := json.Marshal(op)
	if err != nil {
		logger.Warnf(ctx, "[ResumeEnrichment] marshal wiki op %s: %v", knowledgeID, err)
		return
	}
	if err := s.taskPendingRepo.Enqueue(ctx, &types.TaskPendingOp{
		TenantID: tenantID,
		TaskType: wikiTaskType,
		Scope:    wikiTaskScope,
		ScopeID:  kbID,
		Op:       WikiOpIngest,
		DedupKey: knowledgeID,
		Payload:  payloadBytes,
	}); err != nil {
		logger.Warnf(ctx, "[ResumeEnrichment] enqueue wiki op %s: %v", knowledgeID, err)
	}
}

// TriggerWikiBatch enqueues a SINGLE wiki ingest trigger for the KB. The wiki
// handler self-chains via scheduleFollowUp, so this one trigger drains every
// queued pending op serially under the per-KB lock — no concurrency storm.
func (s *knowledgeService) TriggerWikiBatch(ctx context.Context, kbID string) error {
	tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
	lang, _ := types.LanguageFromContext(ctx)
	trigger := WikiIngestPayload{TenantID: tenantID, KnowledgeBaseID: kbID, Language: lang}
	langfuse.InjectTracing(ctx, &trigger)
	triggerBytes, err := json.Marshal(trigger)
	if err != nil {
		return err
	}
	t := asynq.NewTask(types.TypeWikiIngest, triggerBytes,
		asynq.Queue("low"),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(wikiIngestDelay),
	)
	if _, err := s.task.Enqueue(t); err != nil {
		return fmt.Errorf("enqueue wiki trigger: %w", err)
	}
	logger.Infof(ctx, "[ResumeEnrichment] enqueued single wiki trigger for kb %s", kbID)
	return nil
}
