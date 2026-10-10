package service

import (
	"context"
	"fmt"
	"slices"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
)

const imageRecallSampleLimit = 10

// Every engine call (including refills) is observed before application filters
// mutate its rows. This adds no database/model requests. Missing rows cannot be
// attributed to the threshold vs the engine TopK from returned rows alone.
func retrieveObservedImagePool(ctx context.Context, g *storeGroup, params []types.RetrieveParams, phase string) (
	[]*types.RetrieveResult, error,
) {
	res, err := g.Engine.Retrieve(ctx, params)
	for _, rr := range res {
		if rr == nil {
			continue
		}
		out := summarizeImagePool(rr, g)
		requests := make([]map[string]interface{}, 0, len(params))
		for _, p := range params {
			if p.RetrieverType == rr.RetrieverType {
				requests = append(requests, map[string]interface{}{
					"top_k": p.TopK, "threshold": p.Threshold, "knowledge_type": p.KnowledgeType,
				})
			}
		}
		imageKBs := make([]string, 0, len(g.ImageKBIDs))
		for id := range g.ImageKBIDs {
			imageKBs = append(imageKBs, id)
		}
		slices.Sort(imageKBs)
		if len(requests) == 1 {
			out["pool_limit_reached"] = len(rr.Results) >= requests[0]["top_k"].(int)
		}
		out["phase"], out["requests"] = phase, requests
		out["engine"], out["retriever"] = rr.RetrieverEngineType, rr.RetrieverType
		out["kb_ids"], out["image_kb_ids"] = g.KBIDs, imageKBs
		out["result_complete"] = err == nil && rr.Error == nil
		out["unreturned_images"] = "unknown: engine threshold, top_k, or no eligible indexed images"
		recordImageRecallDecision(ctx, "engine_pool", nil, out)
	}
	return res, err
}

func summarizeImagePool(rr *types.RetrieveResult, g *storeGroup) map[string]interface{} {
	reasons := map[string]int{}
	images := make([]map[string]interface{}, 0)
	imageCount, keptImages := 0, 0
	var minScore, maxScore float64
	hasScore := false
	for i, hit := range rr.Results {
		reason := imageHitDropReason(hit, rr.RetrieverType, g)
		if reason == "" {
			reason = "kept"
		}
		reasons[reason]++
		if hit == nil {
			continue
		}
		if !hasScore || hit.Score < minScore {
			minScore = hit.Score
		}
		if !hasScore || hit.Score > maxScore {
			maxScore = hit.Score
		}
		hasScore = true
		if hit.SourceType != types.ImageSourceType {
			continue
		}
		imageCount++
		if reason == "kept" {
			keptImages++
		}
		if len(images) < imageRecallSampleLimit {
			images = append(images, map[string]interface{}{
				"pool_rank": i + 1, "chunk_id": hit.ChunkID, "knowledge_id": hit.KnowledgeID,
				"raw_score": fmt.Sprintf("%.4f", hit.Score), "filter_decision": reason,
			})
		}
	}
	out := map[string]interface{}{
		"returned_count": len(rr.Results), "returned_image_count": imageCount,
		"image_count_passing_filter": keptImages, "filter_decision_counts": reasons,
		"has_returned_scores": hasScore,
		"returned_min_score":  fmt.Sprint(minScore), "returned_max_score": fmt.Sprint(maxScore),
		"image_recall_enabled": g.imageRecall(),
		"image_samples":        images, "image_samples_truncated": imageCount - len(images),
	}
	if g.imageRecall() {
		out["text_threshold"] = g.VectorThreshold
		out["image_threshold"] = imageThreshold(g.VectorThreshold)
	}
	return out
}

// The fusion score cut is separate from the raw vector threshold. Retain image
// samples even when none appears in the generic top-hit preview.
func summarizeImageCandidateCut(hits []*types.IndexWithScore, limit int) map[string]interface{} {
	images := make([]map[string]interface{}, 0)
	before, after := 0, 0
	for i, hit := range hits {
		if hit == nil || hit.SourceType != types.ImageSourceType {
			continue
		}
		before++
		decision := "match_count_cut"
		if i < limit {
			after++
			decision = "kept"
		}
		if len(images) < imageRecallSampleLimit {
			images = append(images, map[string]interface{}{
				"rank": i + 1, "chunk_id": hit.ChunkID,
				"vector_score": fmt.Sprintf("%.4f", hit.VectorScore),
				"fusion_score": fmt.Sprintf("%.4f", hit.Score), "decision": decision,
			})
		}
	}
	return map[string]interface{}{
		"input_count": len(hits), "match_count": limit, "output_count": min(len(hits), limit),
		"input_image_count": before, "output_image_count": after, "dropped_image_count": before - after,
		"image_samples": images, "image_samples_truncated": before - len(images),
	}
}

func recordImageRecallDecision(ctx context.Context, stage string, input, output map[string]interface{}) {
	logger.GetLogger(ctx).WithFields(logger.Fields(output)).Info("image recall " + stage)
	_, span := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "retrieval.image_" + stage, Input: input,
	})
	span.Finish(output, nil, nil)
}
