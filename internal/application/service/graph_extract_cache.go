package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/contentcache"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/redis/go-redis/v9"
)

const graphExtractCacheTTL = 30 * 24 * time.Hour

type cachedGraphExtract struct {
	Graph    *types.GraphData `json:"graph"`
	CachedAt int64            `json:"cached_at"`
}

func graphExtractCacheKey(content string, chatModel chat.Chat, template *types.PromptTemplateStructured) string {
	configHash := stableJSONHash(template)
	return contentcache.GraphExtractKey(
		contentcache.TextHash(content),
		configHash,
		chatModelCacheKey(chatModel),
		"graph-extract-v1",
	)
}

func (s *ChunkExtractService) getCachedGraphExtract(ctx context.Context, key string) (*types.GraphData, bool) {
	if s.redisClient == nil {
		return nil, false
	}
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if err == redis.Nil || err != nil {
		return nil, false
	}
	var cached cachedGraphExtract
	if err := json.Unmarshal(data, &cached); err != nil {
		logger.Warnf(ctx, "graph extract cache decode failed for %s: %v", key, err)
		return nil, false
	}
	if cached.Graph == nil {
		return nil, false
	}
	if cached.CachedAt <= 0 || (len(cached.Graph.Node) == 0 && len(cached.Graph.Relation) == 0) {
		return nil, false
	}
	return cloneGraphDataWithoutChunkOwnership(cached.Graph), true
}

func (s *ChunkExtractService) setCachedGraphExtract(ctx context.Context, key string, graph *types.GraphData) {
	if s.redisClient == nil || graph == nil || (len(graph.Node) == 0 && len(graph.Relation) == 0) {
		return
	}
	data, err := json.Marshal(cachedGraphExtract{
		Graph:    cloneGraphDataWithoutChunkOwnership(graph),
		CachedAt: time.Now().Unix(),
	})
	if err != nil {
		return
	}
	if err := s.redisClient.Set(ctx, key, data, graphExtractCacheTTL).Err(); err != nil {
		logger.Warnf(ctx, "graph extract cache write failed for %s: %v", key, err)
	}
}

func cloneGraphDataWithoutChunkOwnership(graph *types.GraphData) *types.GraphData {
	if graph == nil {
		return nil
	}
	out := &types.GraphData{
		Text:     graph.Text,
		Node:     make([]*types.GraphNode, 0, len(graph.Node)),
		Relation: make([]*types.GraphRelation, 0, len(graph.Relation)),
	}
	for _, node := range graph.Node {
		if node == nil {
			continue
		}
		out.Node = append(out.Node, &types.GraphNode{
			Name:       node.Name,
			Attributes: append([]string(nil), node.Attributes...),
		})
	}
	for _, rel := range graph.Relation {
		if rel == nil {
			continue
		}
		out.Relation = append(out.Relation, &types.GraphRelation{
			Node1: rel.Node1,
			Node2: rel.Node2,
			Type:  rel.Type,
		})
	}
	return out
}
