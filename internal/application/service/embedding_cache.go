package service

import (
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/redis/go-redis/v9"
)

func cachedEmbeddingModel(redisClient *redis.Client, embedder embedding.Embedder) embedding.Embedder {
	return embedding.NewCachedEmbedder(embedder, embedding.NewRedisEmbeddingCache(redisClient))
}
