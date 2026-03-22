package chatpipeline

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// populateFAQAnswers populates FAQ answers for the search results
func (p *PluginMerge) populateFAQAnswers(
	ctx context.Context,
	chatManage *types.ChatManage,
	results []*types.SearchResult,
) []*types.SearchResult {
	if len(results) == 0 || p.chunkRepo == nil {
		return results
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	if tenantID == 0 && chatManage != nil {
		tenantID = chatManage.TenantID
	}
	if tenantID == 0 {
		pipelineWarn(ctx, "Merge", "faq_enrich_skip", map[string]interface{}{
			"reason": "missing_tenant",
		})
		return results
	}

	chunkResultMap := make(map[string][]*types.SearchResult)
	chunkIDSet := make(map[string]struct{})
	for _, r := range results {
		if r == nil || r.ID == "" {
			continue
		}
		if r.ChunkType != string(types.ChunkTypeFAQ) {
			continue
		}
		chunkResultMap[r.ID] = append(chunkResultMap[r.ID], r)
		if _, exists := chunkIDSet[r.ID]; !exists {
			chunkIDSet[r.ID] = struct{}{}
		}
	}

	if len(chunkIDSet) == 0 {
		return results
	}

	chunkIDs := make([]string, 0, len(chunkIDSet))
	for id := range chunkIDSet {
		chunkIDs = append(chunkIDs, id)
	}

	chunks, err := p.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
	if err != nil {
		pipelineWarn(ctx, "Merge", "faq_chunk_fetch_failed", map[string]interface{}{
			"error": err.Error(),
		})
		return results
	}

	updated := 0
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		meta, err := chunk.FAQMetadata()
		if err != nil || meta == nil {
			if err != nil {
				pipelineWarn(ctx, "Merge", "faq_metadata_parse_failed", map[string]interface{}{
					"chunk_id": chunk.ID,
					"error":    err.Error(),
				})
			}
			continue
		}
		content := buildFAQAnswerContent(meta)
		if content == "" {
			continue
		}
		for _, r := range chunkResultMap[chunk.ID] {
			if r == nil {
				continue
			}
			r.Content = content
			updated++
		}
	}

	if updated > 0 {
		pipelineInfo(ctx, "Merge", "faq_content_enriched", map[string]interface{}{
			"chunk_cnt": updated,
		})
	}
	return results
}

// buildFAQAnswerContent builds the content of a FAQ answer
func buildFAQAnswerContent(meta *types.FAQChunkMetadata) string {
	if meta == nil {
		return ""
	}

	question := strings.TrimSpace(meta.StandardQuestion)
	answers := make([]string, 0, len(meta.Answers))
	for _, ans := range meta.Answers {
		if trimmed := strings.TrimSpace(ans); trimmed != "" {
			answers = append(answers, trimmed)
		}
	}

	if question == "" && len(answers) == 0 {
		return ""
	}

	var builder strings.Builder
	if question != "" {
		builder.WriteString("Q: ")
		builder.WriteString(question)
		builder.WriteString("\n")
	}

	if len(answers) > 0 {
		builder.WriteString("Answer:\n")
		for _, ans := range answers {
			builder.WriteString("- ")
			builder.WriteString(ans)
			builder.WriteString("\n")
		}
	}

	return strings.TrimSpace(builder.String())
}
