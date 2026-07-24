package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/contentcache"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/redis/go-redis/v9"
)

const wikiMapCacheTTL = 30 * 24 * time.Hour

type cachedWikiMapText struct {
	Text     string `json:"text"`
	CachedAt int64  `json:"cached_at"`
}

func (s *wikiIngestService) generateWikiMapWithTemplate(
	ctx context.Context,
	chatModel chat.Chat,
	promptTpl string,
	data map[string]string,
	layer string,
	promptVersion string,
) (string, bool, error) {
	if s.redisClient == nil {
		text, err := s.generateWithTemplate(ctx, chatModel, promptTpl, data)
		return text, false, err
	}

	key := wikiMapLLMCacheKey(chatModel, promptTpl, data, layer, promptVersion)
	if text, ok := s.getCachedWikiMapText(ctx, key); ok {
		logger.Infof(ctx, "wiki map cache hit layer=%s key=%s", layer, key)
		return text, true, nil
	}

	text, err := s.generateWithTemplate(ctx, chatModel, promptTpl, data)
	if err != nil {
		return "", false, err
	}
	if layer != "summary" && !json.Valid([]byte(cleanLLMJSON(text))) {
		return text, false, nil
	}
	s.setCachedWikiMapText(ctx, key, text)
	return text, false, nil
}

func wikiMapLLMCacheKey(
	chatModel chat.Chat,
	promptTpl string,
	data map[string]string,
	layer string,
	promptVersion string,
) string {
	payloadHash := stableJSONHash(struct {
		PromptTemplate string            `json:"prompt_template"`
		Data           map[string]string `json:"data"`
	}{
		PromptTemplate: promptTpl,
		Data:           data,
	})
	return contentcache.WikiMapKey(payloadHash, layer, chatModelCacheKey(chatModel), promptVersion)
}

func (s *wikiIngestService) getCachedWikiMapText(ctx context.Context, key string) (string, bool) {
	if s.redisClient == nil {
		return "", false
	}
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if err == redis.Nil || err != nil {
		return "", false
	}
	var cached cachedWikiMapText
	if err := json.Unmarshal(data, &cached); err != nil {
		logger.Warnf(ctx, "wiki map cache decode failed for %s: %v", key, err)
		return "", false
	}
	if cached.CachedAt <= 0 || cached.Text == "" {
		return "", false
	}
	return cached.Text, true
}

func (s *wikiIngestService) setCachedWikiMapText(ctx context.Context, key, text string) {
	if s.redisClient == nil {
		return
	}
	data, err := json.Marshal(cachedWikiMapText{
		Text:     text,
		CachedAt: time.Now().Unix(),
	})
	if err != nil {
		return
	}
	if err := s.redisClient.Set(ctx, key, data, wikiMapCacheTTL).Err(); err != nil {
		logger.Warnf(ctx, "wiki map cache write failed for %s: %v", key, err)
	}
}
