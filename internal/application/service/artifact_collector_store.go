package service

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// messageRepoArtifactStore adapts interfaces.MessageRepository to the
// SessionArtifactStore contract expected by ArtifactCollector. It is a thin
// projection: KnownArtifacts is documented as best-effort, so we forward
// repo errors verbatim and let the collector decide how to degrade.
type messageRepoArtifactStore struct {
	repo interfaces.MessageRepository
}

// NewMessageRepoArtifactStore wraps a MessageRepository so ArtifactCollector
// can build its de-duplication set from every prior message of the session.
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

// RecordRestoredMtime stamps checkout mtimes onto this session's copied
// artifacts only. The parent session is a different session_id and is not
// loaded here.
func (s *messageRepoArtifactStore) RecordRestoredMtime(
	ctx context.Context, sessionID, sourcePath string, mod time.Time, hash string,
) error {
	if s == nil || s.repo == nil || sessionID == "" || sourcePath == "" {
		return nil
	}
	if rewriter, ok := s.repo.(restoredArtifactMtimeRewriter); ok {
		return rewriter.RecordRestoredArtifactMtime(ctx, sessionID, sourcePath, mod, hash)
	}
	return s.recordRestoredMtimeViaMessages(ctx, sessionID, sourcePath, mod, hash)
}

type restoredArtifactMtimeRewriter interface {
	RecordRestoredArtifactMtime(ctx context.Context, sessionID, sourcePath string, mod time.Time, hash string) error
}

func (s *messageRepoArtifactStore) recordRestoredMtimeViaMessages(
	ctx context.Context, sessionID, sourcePath string, mod time.Time, hash string,
) error {
	const pageSize = 200
	for page := 1; ; page++ {
		messages, err := s.repo.GetMessagesBySession(ctx, sessionID, page, pageSize)
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}
		for _, message := range messages {
			if message == nil {
				continue
			}
			updated, changed := message.Artifacts.WithRestoredMtime(sourcePath, mod, hash)
			if !changed {
				continue
			}
			message.Artifacts = updated
			if err := s.repo.UpdateMessage(ctx, message); err != nil {
				return err
			}
		}
		if len(messages) < pageSize {
			return nil
		}
	}
}
