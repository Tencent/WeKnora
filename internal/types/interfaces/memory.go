package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MemoryService defines the interface for the memory system
type MemoryService interface {
	// AddEpisode processes a conversation session and adds it as an episode to the memory graph
	AddEpisode(ctx context.Context, userID string, sessionID string, messages []types.Message) error

	// RetrieveMemory retrieves relevant memory context based on the current query and user
	RetrieveMemory(ctx context.Context, userID string, query string) (*types.MemoryContext, error)
}

// MemoryRepository defines the interface for storing and retrieving memory data
type MemoryRepository interface {
	// SaveEpisode saves an episode and its associated entities and relationships to the graph
	SaveEpisode(ctx context.Context, episode *types.Episode, entities []*types.Entity, relations []*types.Relationship) error

	// FindRelatedEpisodes finds episodes related to the given keywords for a specific user
	FindRelatedEpisodes(ctx context.Context, userID string, keywords []string, limit int) ([]*types.Episode, error)

	// IsAvailable checks if the memory repository is available
	IsAvailable(ctx context.Context) bool
}
