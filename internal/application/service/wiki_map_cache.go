package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type wikiCachedTextPayload struct {
	Text string `json:"text"`
}

func wikiMapContentKey(kind string, data map[string]string) string {
	raw, _ := json.Marshal(struct {
		Schema string            `json:"schema"`
		Kind   string            `json:"kind"`
		Data   map[string]string `json:"data"`
	}{
		Schema: types.WikiMapCacheSchemaVersion,
		Kind:   kind,
		Data:   data,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func wikiMapConfigHash(promptTpl string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		types.WikiMapCacheSchemaVersion,
		promptTpl,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func wikiMapCacheKey(kind, contentKey, modelSignature, configHash string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		types.WikiMapCacheSchemaVersion,
		kind,
		contentKey,
		modelSignature,
		configHash,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func wikiChatModelSignature(chatModel chat.Chat) (modelID string, signature string) {
	modelID = chatModel.GetModelID()
	signature = strings.Join([]string{modelID, chatModel.GetModelName()}, "\x00")
	return modelID, signature
}

func (s *wikiIngestService) generateWikiMapWithCache(
	ctx context.Context,
	tenantID uint64,
	chatModel chat.Chat,
	kind string,
	promptTpl string,
	data map[string]string,
	validate func(string) error,
) (string, bool, error) {
	return s.generateWikiMapWithCacheKeyData(ctx, tenantID, chatModel, kind, promptTpl, data, data, validate)
}

func (s *wikiIngestService) generateWikiMapWithCacheKeyData(
	ctx context.Context,
	tenantID uint64,
	chatModel chat.Chat,
	kind string,
	promptTpl string,
	data map[string]string,
	keyData map[string]string,
	validate func(string) error,
) (string, bool, error) {
	if s.wikiMapCacheRepo == nil {
		text, _, err := s.generateValidatedWikiMapText(ctx, chatModel, kind, promptTpl, data, validate)
		return text, false, err
	}

	modelID, modelSignature := wikiChatModelSignature(chatModel)
	contentKey := wikiMapContentKey(kind, keyData)
	configHash := wikiMapConfigHash(promptTpl)
	cacheKey := wikiMapCacheKey(kind, contentKey, modelSignature, configHash)

	cache, cacheErr := s.wikiMapCacheRepo.GetByKey(ctx, tenantID, cacheKey)
	if cacheErr != nil {
		logger.Warnf(ctx, "wiki map cache lookup failed kind=%s key=%s: %v", kind, cacheKey, cacheErr)
	} else if cache != nil {
		var payload wikiCachedTextPayload
		if err := json.Unmarshal(cache.Payload, &payload); err != nil {
			logger.Warnf(ctx, "wiki map cache decode failed kind=%s key=%s: %v", kind, cacheKey, err)
		} else {
			logger.Infof(ctx, "wiki map cache hit kind=%s key=%s", kind, cacheKey)
			return payload.Text, true, nil
		}
	}

	text, valid, err := s.generateValidatedWikiMapText(ctx, chatModel, kind, promptTpl, data, validate)
	if err != nil {
		return "", false, err
	}
	if !valid {
		logger.Warnf(ctx, "wiki map cache skip invalid output kind=%s key=%s", kind, cacheKey)
		return text, false, nil
	}
	rawPayload, err := json.Marshal(wikiCachedTextPayload{Text: text})
	if err != nil {
		logger.Warnf(ctx, "wiki map cache marshal failed kind=%s key=%s: %v", kind, cacheKey, err)
		return text, false, nil
	}
	if err := s.wikiMapCacheRepo.Upsert(ctx, &types.WikiMapCache{
		TenantID:   tenantID,
		CacheKey:   cacheKey,
		Kind:       kind,
		ContentKey: contentKey,
		ModelID:    modelID,
		ConfigHash: configHash,
		SchemaVer:  types.WikiMapCacheSchemaVersion,
		Payload:    types.JSON(rawPayload),
	}); err != nil {
		logger.Warnf(ctx, "wiki map cache upsert failed kind=%s key=%s: %v", kind, cacheKey, err)
	}
	return text, false, nil
}

func (s *wikiIngestService) generateValidatedWikiMapText(
	ctx context.Context,
	chatModel chat.Chat,
	kind string,
	promptTpl string,
	data map[string]string,
	validate func(string) error,
) (string, bool, error) {
	text, err := s.generateWithTemplate(ctx, chatModel, promptTpl, data)
	if err != nil {
		return "", false, err
	}

	normalized, validErr := normalizeWikiMapText(text, validate)
	if validErr == nil {
		return normalized, true, nil
	}

	logger.Warnf(ctx, "wiki map output invalid kind=%s, retry once: %v", kind, validErr)
	retryText, retryErr := s.generateWithTemplate(ctx, chatModel, promptTpl, data)
	if retryErr != nil {
		logger.Warnf(ctx, "wiki map output retry failed kind=%s: %v", kind, retryErr)
		return normalized, false, nil
	}
	normalizedRetry, retryValidErr := normalizeWikiMapText(retryText, validate)
	if retryValidErr != nil {
		logger.Warnf(ctx, "wiki map output still invalid kind=%s: %v", kind, retryValidErr)
		return normalizedRetry, false, nil
	}
	return normalizedRetry, true, nil
}

func normalizeWikiMapText(text string, validate func(string) error) (string, error) {
	if validate == nil {
		return text, nil
	}
	normalized := cleanLLMJSON(text)
	if err := validate(normalized); err != nil {
		return normalized, err
	}
	return normalized, nil
}

func validateWikiCombinedExtractionJSON(text string) error {
	var parsed combinedExtraction
	return json.Unmarshal([]byte(cleanLLMJSON(text)), &parsed)
}

func validateWikiCitationJSON(text string) error {
	var parsed citationBatchResult
	return json.Unmarshal([]byte(cleanLLMJSON(text)), &parsed)
}
