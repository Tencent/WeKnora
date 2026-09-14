package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// messageRepoArtifactStore adapts interfaces.MessageRepository to the
// historical-reference lookup used by ArtifactCollector.
type messageRepoArtifactStore struct {
	repo interfaces.MessageRepository
}

// NewMessageRepoArtifactStore wraps a MessageRepository so ArtifactCollector
// can resolve explicit references to artifacts from prior messages.
func NewMessageRepoArtifactStore(repo interfaces.MessageRepository) SessionArtifactStore {
	return &messageRepoArtifactStore{repo: repo}
}

// KnownArtifacts satisfies SessionArtifactStore.
func (s *messageRepoArtifactStore) KnownArtifacts(
	ctx context.Context, sessionID string,
) ([]types.MessageArtifact, error) {
	if s == nil || s.repo == nil || sessionID == "" {
		return nil, nil
	}
	arts, err := s.repo.GetSessionArtifacts(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return arts, nil
}
