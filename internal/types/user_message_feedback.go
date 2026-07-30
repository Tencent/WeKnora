package types

import "time"

type UserMessageFeedbackVote string

const (
	UserMessageFeedbackVoteLike    UserMessageFeedbackVote = "like"
	UserMessageFeedbackVoteDislike UserMessageFeedbackVote = "dislike"
)

type UserMessageFeedback struct {
	ID            string                  `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID      uint64                  `json:"tenant_id" gorm:"index"`
	UserID        string                  `json:"user_id" gorm:"type:varchar(36);index"`
	SessionID     string                  `json:"session_id" gorm:"type:varchar(36);index"`
	MessageID     string                  `json:"message_id" gorm:"type:varchar(36);index"`
	Vote          UserMessageFeedbackVote `json:"vote" gorm:"type:varchar(10)"`
	DislikeReason string                  `json:"dislike_reason" gorm:"type:varchar(500);default:''"`
	CreatedAt     time.Time               `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt     time.Time               `json:"updated_at" gorm:"autoUpdateTime"`
}

func (UserMessageFeedback) TableName() string {
	return "user_message_feedbacks"
}
