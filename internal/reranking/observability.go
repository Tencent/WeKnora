package reranking

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
)

// Observe selection in the shared implementation: agent tools and quick answers
// must expose the same decisions, including early exits and model-error fallback.
func recordSelection(ctx context.Context, input []*types.SearchResult, res *Result, opts Options,
	scoredAsImage map[int]bool,
) {
	out := summarizeSelection(input, res, opts, scoredAsImage)
	const name = "diagnostics.rerank.selection"
	logger.GetLogger(ctx).WithFields(logger.Fields(out)).Info(name)
	_, span := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: name,
		Metadata: map[string]interface{}{
			"observation_kind": "diagnostic", "sample_scope": "image_vectors",
		},
	})
	span.Finish(out, nil, nil)
}

func summarizeSelection(input []*types.SearchResult, res *Result, opts Options,
	scoredAsImage map[int]bool,
) map[string]interface{} {
	// Candidates retain input pointers. Final rows are copies, so use the
	// returned input indices to distinguish retained and rejected inputs.
	candidates := make(map[*types.SearchResult]int, len(res.Candidates))
	for i, r := range res.Candidates {
		candidates[r] = i
	}
	returned := make(map[int]*types.SearchResult, len(res.Indices))
	for i, index := range res.Indices {
		returned[index] = res.Results[i]
	}
	modelScores := make(map[int]float64, len(res.ModelScores))
	for _, score := range res.ModelScores {
		modelScores[score.Index] = score.RelevanceScore
	}
	passed := make(map[string]bool, len(res.Scored))
	for _, r := range res.Scored {
		passed[r.ID] = true
	}
	inputTypes, candidateTypes, outputTypes := map[string]int{}, map[string]int{}, map[string]int{}
	for _, r := range res.Candidates {
		candidateTypes[r.ChunkType]++
	}
	for _, r := range res.Results {
		if r != nil {
			outputTypes[r.ChunkType]++
		}
	}
	images := make([]map[string]interface{}, 0)
	decisions := map[string]int{}
	imageCount := 0
	for i, r := range input {
		if r == nil {
			continue
		}
		inputTypes[r.ChunkType]++
		if !isImageVectorHit(r) {
			continue
		}
		imageCount++
		pos, candidate := candidates[r]
		score, hasScore := modelScores[pos]
		decision := "candidate_cap_or_empty_passage"
		if kept, ok := returned[i]; ok {
			decision = "kept"
			if res.Diagnostics.Outcome == types.RerankOutcomeModelError {
				decision = "model_error_fallback"
			} else if kept.Metadata[types.MetadataKeptBy] == types.KeptByImageVector {
				decision = "image_vector_fallback"
			}
		} else if candidate {
			switch {
			case !hasScore:
				decision = "no_model_score"
			case passed[r.ID]:
				decision = "top_k_mmr_cut"
			default:
				decision = "rerank_rejected"
			}
		}
		decisions[decision]++
		if len(images) >= 10 {
			continue
		}
		row := map[string]interface{}{
			"input_rank": i + 1, "chunk_id": r.ID, "knowledge_id": r.KnowledgeID,
			"vector_score":    fmt.Sprintf("%.4f", r.VectorScore),
			"retrieval_score": fmt.Sprintf("%.4f", r.Score), "decision": decision,
		}
		if candidate && hasScore {
			row["model_score"] = fmt.Sprintf("%.4f", score)
			row["scored_as"] = "text"
			if scoredAsImage[pos] {
				row["scored_as"] = "image"
			}
		}
		images = append(images, row)
	}
	return map[string]interface{}{
		"input_count": len(input), "candidate_count": len(res.Candidates), "output_count": len(res.Results),
		"input_chunk_types": inputTypes, "candidate_chunk_types": candidateTypes, "output_chunk_types": outputTypes,
		"outcome": res.Diagnostics.Outcome, "images_scored": res.Diagnostics.ImagesScored,
		"images_kept_by_vector": res.Diagnostics.ImagesKept, "image_decision_counts": decisions,
		"image_samples": images, "image_samples_truncated": imageCount - len(images),
		// Keyword ordering and explicit scopes may use -Inf. Strings are JSON safe.
		"threshold": fmt.Sprint(opts.Threshold), "effective_threshold": fmt.Sprint(res.Diagnostics.EffectiveThreshold),
		"fallback_min_score": fmt.Sprint(opts.FallbackMinScore), "image_keep_score": fmt.Sprint(opts.ImageKeepScore),
		"max_candidates": opts.MaxCandidates, "top_k": opts.TopK,
		"top_model_score": fmt.Sprint(res.Diagnostics.TopScore),
	}
}
