package im

import (
	"context"

	"github.com/Tencent/WeKnora/internal/im/langdetect"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// withIMReplyLanguage applies the last confidently detected language within
// this ChannelSession. The database column is shared by all service instances;
// soft-deleting the mapping on /clear naturally resets this state.
func (s *Service) withIMReplyLanguage(ctx context.Context, cs *ChannelSession, msg *IncomingMessage) context.Context {
	language := ""
	if msg.MessageType == "" || msg.MessageType == MessageTypeText {
		language = langdetect.Detect(msg.Content)
	}
	if language != "" {
		if err := s.db.Model(&ChannelSession{}).
			Where("id = ? AND deleted_at IS NULL", cs.ID).
			UpdateColumn("last_detected_language", language).Error; err != nil {
			logger.Warnf(ctx, "[IM] Failed to persist detected language for session %s: %v", cs.ID, err)
		}
	} else {
		var current ChannelSession
		if err := s.db.Select("last_detected_language").
			Where("id = ? AND deleted_at IS NULL", cs.ID).
			First(&current).Error; err == nil {
			language = current.LastDetectedLanguage
		} else {
			logger.Warnf(ctx, "[IM] Failed to read detected language for session %s: %v", cs.ID, err)
		}
	}
	if language == "" {
		language = types.DefaultLanguage()
	}
	return context.WithValue(ctx, types.LanguageContextKey, language)
}
