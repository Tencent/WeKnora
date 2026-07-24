package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/contentcache"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/redis/go-redis/v9"
)

const postprocessLLMCacheTTL = 30 * 24 * time.Hour

type cachedPostprocessLLMText struct {
	Text     string `json:"text"`
	CachedAt int64  `json:"cached_at"`
}

func postprocessLLMCacheKey(
	layer string,
	chatModel chat.Chat,
	messages []chat.Message,
	opts *chat.ChatOptions,
	promptVersion string,
) string {
	payloadHash := stableJSONHash(struct {
		Messages []chat.Message    `json:"messages"`
		Options  *chat.ChatOptions `json:"options"`
	}{
		Messages: messages,
		Options:  opts,
	})
	return contentcache.PostprocessLLMKey(payloadHash, layer, chatModelCacheKey(chatModel), promptVersion)
}

func (s *knowledgeService) getCachedPostprocessLLMText(ctx context.Context, key string) (string, bool) {
	if s.redisClient == nil {
		return "", false
	}
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if err == redis.Nil || err != nil {
		return "", false
	}
	var cached cachedPostprocessLLMText
	if err := json.Unmarshal(data, &cached); err != nil {
		logger.Warnf(ctx, "postprocess LLM cache decode failed for %s: %v", key, err)
		return "", false
	}
	if cached.CachedAt <= 0 || strings.TrimSpace(cached.Text) == "" {
		return "", false
	}
	return cached.Text, true
}

func (s *knowledgeService) setCachedPostprocessLLMText(ctx context.Context, key, text string) {
	if s.redisClient == nil || strings.TrimSpace(text) == "" {
		return
	}
	data, err := json.Marshal(cachedPostprocessLLMText{
		Text:     text,
		CachedAt: time.Now().Unix(),
	})
	if err != nil {
		return
	}
	if err := s.redisClient.Set(ctx, key, data, postprocessLLMCacheTTL).Err(); err != nil {
		logger.Warnf(ctx, "postprocess LLM cache write failed for %s: %v", key, err)
	}
}
