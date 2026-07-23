package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func artifactModelID(model chat.Chat) string {
	if model == nil {
		return "unknown"
	}
	if id := strings.TrimSpace(model.GetModelID()); id != "" {
		return id
	}
	if name := strings.TrimSpace(model.GetModelName()); name != "" {
		return name
	}
	return "unknown"
}

func tenantIDFromContext(ctx context.Context) uint64 {
	if ctx == nil {
		return 0
	}
	switch tenantID := ctx.Value(types.TenantIDContextKey).(type) {
	case uint64:
		return tenantID
	case int:
		if tenantID > 0 {
			return uint64(tenantID)
		}
	}
	return 0
}

func artifactCacheKey(kind, subject string, parts ...string) string {
	identity := strings.Join(parts, "\x00")
	return fmt.Sprintf("%s:%s:%s", kind, shortArtifactSubject(subject), types.ContentHash(identity, ""))
}

func shortArtifactSubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "unknown"
	}
	if len(subject) <= 64 {
		return subject
	}
	return types.ContentHash(subject, "")[:32]
}

func getContentCacheJSON(
	ctx context.Context,
	repo interfaces.ContentCacheRepository,
	tenantID uint64,
	cacheKind string,
	cacheKey string,
	out any,
) bool {
	if repo == nil {
		return false
	}
	entry, err := repo.GetByKey(ctx, tenantID, cacheKind, cacheKey)
	if err != nil {
		logger.Warnf(ctx, "%s cache get failed: %v", cacheKind, err)
		return false
	}
	if entry == nil {
		return false
	}
	if err := json.Unmarshal(entry.Payload, out); err != nil {
		logger.Warnf(ctx, "%s cache decode failed: %v", cacheKind, err)
		return false
	}
	return true
}

func setContentCacheJSON(
	ctx context.Context,
	repo interfaces.ContentCacheRepository,
	tenantID uint64,
	cacheKind string,
	cacheKey string,
	payload any,
) {
	if repo == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		logger.Warnf(ctx, "%s cache encode failed: %v", cacheKind, err)
		return
	}
	if err := repo.Upsert(ctx, &types.ContentCacheEntry{
		TenantID:  tenantID,
		CacheKind: cacheKind,
		CacheKey:  cacheKey,
		Payload:   types.JSON(data),
	}); err != nil {
		logger.Warnf(ctx, "%s cache upsert failed: %v", cacheKind, err)
	}
}

func graphChunkCacheKey(
	chunk *types.Chunk,
	chatModel chat.Chat,
	template *types.PromptTemplateStructured,
	extractCfg types.ExtractConfig,
) string {
	subject := ""
	contentHash := ""
	if chunk != nil {
		subject = chunk.ID
		contentHash = chunk.ContentHash
		if contentHash == "" {
			contentHash = types.ContentHash(chunk.Content, chunk.ContextHeader)
		}
	}
	templateJSON, _ := json.Marshal(template)
	configJSON, _ := json.Marshal(extractCfg)
	return artifactCacheKey(types.ContentCacheKindGraphChunk, subject,
		contentHash,
		artifactModelID(chatModel),
		string(templateJSON),
		string(configJSON),
	)
}

func summaryCacheKey(
	knowledgeID string,
	content string,
	summaryModel chat.Chat,
	summaryPrompt string,
	langName string,
	maxInputChars int,
	maxTokens int,
) string {
	return artifactCacheKey(types.ContentCacheKindSummary, knowledgeID,
		types.ContentHash(content, ""),
		artifactModelID(summaryModel),
		types.ContentHash(summaryPrompt, ""),
		strings.TrimSpace(langName),
		fmt.Sprintf("%d", maxInputChars),
		fmt.Sprintf("%d", maxTokens),
	)
}

func questionCacheKey(
	content string,
	prevContent string,
	nextContent string,
	docName string,
	questionModel chat.Chat,
	renderedPrompt string,
	customInstructions string,
	langName string,
	questionCount int,
) string {
	contentHash := types.ContentHash(content, "")
	return artifactCacheKey(types.ContentCacheKindQuestion, contentHash,
		contentHash,
		types.ContentHash(prevContent, ""),
		types.ContentHash(nextContent, ""),
		types.ContentHash(docName, ""),
		artifactModelID(questionModel),
		types.ContentHash(renderedPrompt, ""),
		types.ContentHash(customInstructions, ""),
		strings.TrimSpace(langName),
		fmt.Sprintf("%d", questionCount),
	)
}

func cloneGraphDataForChunk(graph *types.GraphData, chunkID string) *types.GraphData {
	if graph == nil {
		return nil
	}
	cloned := &types.GraphData{Text: graph.Text}
	if len(graph.Node) > 0 {
		cloned.Node = make([]*types.GraphNode, 0, len(graph.Node))
		for _, node := range graph.Node {
			if node == nil {
				cloned.Node = append(cloned.Node, nil)
				continue
			}
			cloned.Node = append(cloned.Node, &types.GraphNode{
				Name:       node.Name,
				Chunks:     []string{chunkID},
				Attributes: append([]string(nil), node.Attributes...),
			})
		}
	}
	if len(graph.Relation) > 0 {
		cloned.Relation = make([]*types.GraphRelation, 0, len(graph.Relation))
		for _, rel := range graph.Relation {
			if rel == nil {
				cloned.Relation = append(cloned.Relation, nil)
				continue
			}
			copied := *rel
			cloned.Relation = append(cloned.Relation, &copied)
		}
	}
	return cloned
}
