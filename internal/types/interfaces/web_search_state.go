package interfaces

import (
	"context"
)

// WebSearchStateService defines the service interface for managing web search temporary KB state
type WebSearchStateService interface {
	// GetWebSearchTempKBState retrieves the temporary KB state for web search from Redis
	GetWebSearchTempKBState(
		ctx context.Context,
		sessionID string,
	) (tempKBID string, seenURLs map[string]bool, knowledgeIDs []string)

	// SaveWebSearchTempKBState saves the temporary KB state for web search to Redis
	SaveWebSearchTempKBState(
		ctx context.Context,
		sessionID string,
		tempKBID string,
		seenURLs map[string]bool,
		knowledgeIDs []string,
	)

	// DeleteWebSearchTempKBState deletes the temporary KB state for web search from Redis
	DeleteWebSearchTempKBState(ctx context.Context, sessionID string) error
}
