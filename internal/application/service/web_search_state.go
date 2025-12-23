package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
)

// webSearchStateService implements the WebSearchStateService interface
type webSearchStateService struct {
	redisClient          *redis.Client
	knowledgeService     interfaces.KnowledgeService
	knowledgeBaseService interfaces.KnowledgeBaseService
}

// NewWebSearchStateService creates a new web search state service instance
func NewWebSearchStateService(
	redisClient *redis.Client,
	knowledgeService interfaces.KnowledgeService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
) interfaces.WebSearchStateService {
	return &webSearchStateService{
		redisClient:          redisClient,
		knowledgeService:     knowledgeService,
		knowledgeBaseService: knowledgeBaseService,
	}
}

// GetWebSearchTempKBState retrieves the temporary KB state for web search from Redis
func (s *webSearchStateService) GetWebSearchTempKBState(
	ctx context.Context,
	sessionID string,
) (tempKBID string, seenURLs map[string]bool, knowledgeIDs []string) {
	stateKey := fmt.Sprintf("tempkb:%s", sessionID)
	if raw, getErr := s.redisClient.Get(ctx, stateKey).Bytes(); getErr == nil && len(raw) > 0 {
		var state struct {
			KBID         string          `json:"kbID"`
			KnowledgeIDs []string        `json:"knowledgeIDs"`
			SeenURLs     map[string]bool `json:"seenURLs"`
		}
		if err := json.Unmarshal(raw, &state); err == nil {
			tempKBID = state.KBID
			ids := state.KnowledgeIDs
			if state.SeenURLs != nil {
				seenURLs = state.SeenURLs
			} else {
				seenURLs = make(map[string]bool)
			}
			return tempKBID, seenURLs, ids
		}
	}
	return "", make(map[string]bool), []string{}
}

// SaveWebSearchTempKBState saves the temporary KB state for web search to Redis
func (s *webSearchStateService) SaveWebSearchTempKBState(
	ctx context.Context,
	sessionID string,
	tempKBID string,
	seenURLs map[string]bool,
	knowledgeIDs []string,
) {
	stateKey := fmt.Sprintf("tempkb:%s", sessionID)
	state := struct {
		KBID         string          `json:"kbID"`
		KnowledgeIDs []string        `json:"knowledgeIDs"`
		SeenURLs     map[string]bool `json:"seenURLs"`
	}{
		KBID:         tempKBID,
		KnowledgeIDs: knowledgeIDs,
		SeenURLs:     seenURLs,
	}
	if b, err := json.Marshal(state); err == nil {
		_ = s.redisClient.Set(ctx, stateKey, b, 0).Err()
	}
}

// DeleteWebSearchTempKBState deletes the temporary KB state for web search from Redis
// and cleans up associated knowledge base and knowledge items.
func (s *webSearchStateService) DeleteWebSearchTempKBState(ctx context.Context, sessionID string) error {
	if s.redisClient == nil {
		return nil
	}

	stateKey := fmt.Sprintf("tempkb:%s", sessionID)
	raw, getErr := s.redisClient.Get(ctx, stateKey).Bytes()
	if getErr != nil || len(raw) == 0 {
		// No state found, nothing to clean up
		return nil
	}

	var state struct {
		KBID         string          `json:"kbID"`
		KnowledgeIDs []string        `json:"knowledgeIDs"`
		SeenURLs     map[string]bool `json:"seenURLs"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		// Invalid state, just delete the key
		_ = s.redisClient.Del(ctx, stateKey).Err()
		return nil
	}

	// If KBID is empty, just delete the Redis key
	if strings.TrimSpace(state.KBID) == "" {
		_ = s.redisClient.Del(ctx, stateKey).Err()
		return nil
	}

	logger.Infof(ctx, "Cleaning temporary KB for session %s: %s", sessionID, state.KBID)

	// Delete all knowledge items
	for _, kid := range state.KnowledgeIDs {
		if delErr := s.knowledgeService.DeleteKnowledge(ctx, kid); delErr != nil {
			logger.Warnf(ctx, "Failed to delete temp knowledge %s: %v", kid, delErr)
		}
	}

	// Delete the knowledge base
	if delErr := s.knowledgeBaseService.DeleteKnowledgeBase(ctx, state.KBID); delErr != nil {
		logger.Warnf(ctx, "Failed to delete temp knowledge base %s: %v", state.KBID, delErr)
	}

	// Delete the Redis key
	if delErr := s.redisClient.Del(ctx, stateKey).Err(); delErr != nil {
		logger.Warnf(ctx, "Failed to delete Redis key %s: %v", stateKey, delErr)
		return fmt.Errorf("failed to delete Redis key: %w", delErr)
	}

	logger.Infof(ctx, "Successfully cleaned up temporary KB for session %s", sessionID)
	return nil
}
