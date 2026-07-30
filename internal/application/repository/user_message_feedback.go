package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type userMessageFeedbackRepository struct {
	db *gorm.DB
}

func NewUserMessageFeedbackRepository(db *gorm.DB) interfaces.UserMessageFeedbackRepository {
	return &userMessageFeedbackRepository{db: db}
}

func (r *userMessageFeedbackRepository) GetByUserMessage(ctx context.Context, tenantID uint64, userID, messageID string) (*types.UserMessageFeedback, error) {
	var rec types.UserMessageFeedback
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND user_id = ? AND message_id = ?", tenantID, userID, messageID).
		First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (r *userMessageFeedbackRepository) Upsert(ctx context.Context, feedback *types.UserMessageFeedback) error {
	if feedback == nil {
		return nil
	}
	return r.db.WithContext(ctx).Save(feedback).Error
}

func (r *userMessageFeedbackRepository) DeleteByUserMessage(ctx context.Context, tenantID uint64, userID, messageID string) error {
	if messageID == "" {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND user_id = ? AND message_id = ?", tenantID, userID, messageID).
		Delete(&types.UserMessageFeedback{}).Error
}
