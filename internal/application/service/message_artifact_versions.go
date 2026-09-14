package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Existing conversations receive the same version clarification as new turns,
// including when the original producing message is outside the loaded page.
// All callers have already authorized access to the containing session.
func (s *messageService) clarifyReadArtifactVersions(ctx context.Context, sessionID string, messages []*types.Message) []*types.Message {
	needsHistory := false
	for _, message := range messages {
		if message == nil || len(message.Artifacts) == 0 {
			continue
		}
		owned := make(map[string]bool)
		for _, artifact := range message.Artifacts {
			owned[artifact.URL] = true
		}
		for _, ref := range types.ScanResourceReferences(message.Content) {
			if !owned[ref] {
				needsHistory = true
			}
		}
	}
	if !needsHistory {
		return messages
	}
	previous, err := s.messageRepo.GetSessionArtifacts(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "Read artifact versions failed: %v", err)
		return messages
	}
	result := append([]*types.Message(nil), messages...)
	for i, message := range messages {
		if message == nil {
			continue
		}
		refs := make(map[string]bool)
		for _, ref := range types.ScanResourceReferences(message.Content) {
			refs[ref] = true
		}
		var referenced types.MessageArtifacts
		for _, artifact := range previous {
			if refs[artifact.URL] {
				referenced = append(referenced, artifact)
			}
		}
		copy := *message
		copy.Content = types.ClarifyArtifactVersions(message.Content, message.Artifacts, referenced, types.LanguageFromContextOrDefault(ctx))
		result[i] = &copy
	}
	return result
}
