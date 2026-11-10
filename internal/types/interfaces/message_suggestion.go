package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MessageSuggestionRepository persists generated suggestions and their
// product-analytics events. AcquireGeneration serializes duplicate requests
// from reconnecting clients with an expiring lease.
type MessageSuggestionRepository interface {
	GetByCacheKey(
		ctx context.Context,
		tenantID uint64,
		assistantMessageID string,
		placement string,
		configHash string,
		locale string,
	) (*types.MessageSuggestionSet, error)
	GetByID(ctx context.Context, tenantID uint64, sessionID string, id string) (*types.MessageSuggestionSet, error)
	AcquireGeneration(
		ctx context.Context,
		set *types.MessageSuggestionSet,
		regenerate bool,
	) (*types.MessageSuggestionSet, bool, error)
	Save(ctx context.Context, set *types.MessageSuggestionSet) error
	CreateEvent(ctx context.Context, event *types.MessageSuggestionEvent) error
	DeleteByMessageID(ctx context.Context, tenantID uint64, sessionID string, messageID string) error
	DeleteBySessionID(ctx context.Context, tenantID uint64, sessionID string) error
}

type MessageSuggestionService interface {
	EnsureFollowUps(
		ctx context.Context,
		sessionID string,
		assistantMessageID string,
		regenerate bool,
	) (*types.MessageSuggestionSet, error)
	GetFollowUps(
		ctx context.Context,
		sessionID string,
		assistantMessageID string,
	) (*types.MessageSuggestionSet, error)
	RecordEvent(
		ctx context.Context,
		sessionID string,
		setID string,
		questionID string,
		eventType string,
	) error
	ValidateAttribution(ctx context.Context, sessionID string, query string, attribution *types.SuggestionAttribution) error
}
