package types

import (
	"strings"
	"time"
)

// Feedback values stored for one user on one assistant reply.
const (
	// FeedbackTypeLike is a positive vote.
	FeedbackTypeLike    = "like"
	FeedbackTypeDislike = "dislike"
	FeedbackTypeNone    = "none"

	FeedbackReasonInaccurate = "inaccurate"
	FeedbackReasonIrrelevant = "irrelevant"
	FeedbackReasonOutdated   = "outdated"
	FeedbackReasonIncomplete = "incomplete"
	FeedbackReasonOther      = "other"

	ChunkWeightLogSourceFeedback   = "user_feedback"
	ChunkWeightLogSourceAdminReset = "admin_reset"

	// FeedbackReasonTextMaxRunes bounds free-text dislike reasons.
	FeedbackReasonTextMaxRunes = 1000
)

// FeedbackReasonCodes are the dislike reasons the product offers.
func FeedbackReasonCodes() []string {
	return []string{
		FeedbackReasonInaccurate,
		FeedbackReasonIrrelevant,
		FeedbackReasonOutdated,
		FeedbackReasonIncomplete,
		FeedbackReasonOther,
	}
}

// ValidFeedbackReasonCode reports whether code is empty or a known reason.
func ValidFeedbackReasonCode(code string) bool {
	if code == "" {
		return true
	}
	for _, allowed := range FeedbackReasonCodes() {
		if code == allowed {
			return true
		}
	}
	return false
}

// MessageChunkRef is the persisted link from an assistant reply to one
// knowledge chunk that reply cited.
type MessageChunkRef struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	SessionTenantID uint64    `json:"session_tenant_id" gorm:"not null;uniqueIndex:uq_msg_chunk,priority:1;index"`
	ChunkTenantID   uint64    `json:"chunk_tenant_id" gorm:"not null;index"`
	SessionID       string    `json:"session_id" gorm:"type:varchar(36);not null;index"`
	MessageID       string    `json:"message_id" gorm:"type:varchar(36);not null;index;uniqueIndex:uq_msg_chunk,priority:2"` //nolint:lll
	ChunkID         string    `json:"chunk_id" gorm:"type:varchar(36);not null;index;uniqueIndex:uq_msg_chunk,priority:3"`   //nolint:lll
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"column:knowledge_base_id;type:varchar(36);not null;index"`
	KnowledgeID     string    `json:"knowledge_id" gorm:"column:knowledge_id;type:varchar(36);not null;index"`
	ChunkIndex      int       `json:"chunk_index" gorm:"column:chunk_index;not null;default:0"`
	ChunkType       string    `json:"chunk_type" gorm:"column:chunk_type;type:varchar(32);not null;default:''"`
	MatchType       MatchType `json:"match_type" gorm:"column:match_type;not null;default:0"`
	Score           float64   `json:"score" gorm:"column:score;not null;default:0"`
	CreatedAt       time.Time `json:"created_at"`
}

// MessageFeedback is one user's current vote on one assistant message.
type MessageFeedback struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	SessionTenantID uint64    `json:"session_tenant_id" gorm:"not null;uniqueIndex:uq_msg_feedback,priority:1"`
	UserID          string    `json:"user_id" gorm:"type:varchar(255);not null;uniqueIndex:uq_msg_feedback,priority:2;index"` //nolint:lll
	SessionID       string    `json:"session_id" gorm:"type:varchar(36);not null;index"`
	MessageID       string    `json:"message_id" gorm:"type:varchar(36);not null;uniqueIndex:uq_msg_feedback,priority:3;index"` //nolint:lll
	FeedbackType    string    `json:"feedback_type" gorm:"column:feedback_type;type:varchar(16);not null;default:'none'"`       //nolint:lll
	ReasonCode      string    `json:"reason_code,omitempty" gorm:"column:reason_code;type:varchar(64);not null;default:''"`     //nolint:lll
	ReasonText      string    `json:"reason_text,omitempty" gorm:"column:reason_text;type:text;not null;default:''"`
	FeedbackAt      time.Time `json:"feedback_at" gorm:"column:feedback_at;not null;index"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// MessageFeedbackView is the vote echoed on a loaded message.
type MessageFeedbackView struct {
	FeedbackType string `json:"feedback_type"`
	ReasonCode   string `json:"reason_code,omitempty"`
	ReasonText   string `json:"reason_text,omitempty"`
}

// View returns the client-facing vote.
func (f *MessageFeedback) View() *MessageFeedbackView {
	if f == nil {
		return &MessageFeedbackView{FeedbackType: FeedbackTypeNone}
	}
	return &MessageFeedbackView{
		FeedbackType: f.FeedbackType,
		ReasonCode:   f.ReasonCode,
		ReasonText:   f.ReasonText,
	}
}

// ChunkWeightLog records why a chunk's recall weight changed.
type ChunkWeightLog struct {
	ID               string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	ChunkTenantID    uint64    `json:"chunk_tenant_id" gorm:"not null;index:idx_weight_log_chunk,priority:1"`
	ChunkID          string    `json:"chunk_id" gorm:"type:varchar(36);not null;index:idx_weight_log_chunk,priority:2"`
	OldWeight        float64   `json:"old_weight" gorm:"not null"`
	NewWeight        float64   `json:"new_weight" gorm:"not null"`
	Source           string    `json:"source" gorm:"type:varchar(32);not null"`
	SourceMessageID  string    `json:"source_message_id,omitempty" gorm:"type:varchar(36);not null;default:'';index"`
	SourceFeedbackID string    `json:"source_feedback_id,omitempty" gorm:"type:varchar(36);not null;default:'';index"`
	Reason           string    `json:"reason,omitempty" gorm:"column:reason;type:text;not null;default:''"`
	CreatedAt        time.Time `json:"created_at" gorm:"index:idx_chunk_weight_logs_chunk_created,priority:3"`
}

// MessageFeedbackRequest is the body of a like, dislike, or cancel.
type MessageFeedbackRequest struct {
	FeedbackType string `json:"feedback_type"`
	ReasonCode   string `json:"reason_code,omitempty"`
	ReasonText   string `json:"reason_text,omitempty"`
}

// NormalizeFeedbackRequest trims a vote and rejects unknown types or reasons.
func NormalizeFeedbackRequest(req *MessageFeedbackRequest) error {
	if req == nil {
		return errInvalidFeedback
	}
	req.FeedbackType = strings.TrimSpace(req.FeedbackType)
	req.ReasonCode = strings.TrimSpace(req.ReasonCode)
	req.ReasonText = strings.TrimSpace(req.ReasonText)
	switch req.FeedbackType {
	case FeedbackTypeLike, FeedbackTypeNone:
		req.ReasonCode = ""
		req.ReasonText = ""
		return nil
	case FeedbackTypeDislike:
		if !ValidFeedbackReasonCode(req.ReasonCode) {
			return errInvalidFeedback
		}
		if len([]rune(req.ReasonText)) > FeedbackReasonTextMaxRunes {
			req.ReasonText = string([]rune(req.ReasonText)[:FeedbackReasonTextMaxRunes])
		}
		return nil
	default:
		return errInvalidFeedback
	}
}

// FeedbackPolicy is the configurable mapping from approval rate to recall weight.
type FeedbackPolicy struct {
	MinFeedbackCount               int
	BoostPositiveRateThreshold     float64
	NeutralPositiveRateThreshold   float64
	NeedsOptimizationRateThreshold float64
	BoostRecallWeight              float64
	NeutralRecallWeight            float64
	PenaltyRecallWeight            float64
}

// DefaultFeedbackPolicy is the product default: boost at 80%, hold from 50%,
// degrade below 50%, and flag needs-optimization below 30% after 3 votes.
func DefaultFeedbackPolicy() FeedbackPolicy {
	return FeedbackPolicy{
		MinFeedbackCount:               3,
		BoostPositiveRateThreshold:     0.8,
		NeutralPositiveRateThreshold:   0.5,
		NeedsOptimizationRateThreshold: 0.3,
		BoostRecallWeight:              1.2,
		NeutralRecallWeight:            1,
		PenaltyRecallWeight:            0.8,
	}
}

// Normalize repairs inverted bands and non-positive weights.
func (p FeedbackPolicy) Normalize() FeedbackPolicy {
	if p.MinFeedbackCount < 1 {
		p.MinFeedbackCount = 1
	}
	if p.BoostPositiveRateThreshold <= 0 {
		p.BoostPositiveRateThreshold = 0.8
	}
	if p.NeutralPositiveRateThreshold <= 0 {
		p.NeutralPositiveRateThreshold = 0.5
	}
	if p.NeedsOptimizationRateThreshold <= 0 {
		p.NeedsOptimizationRateThreshold = 0.3
	}
	if p.BoostPositiveRateThreshold < p.NeutralPositiveRateThreshold {
		boost := p.NeutralPositiveRateThreshold
		p.NeutralPositiveRateThreshold = p.BoostPositiveRateThreshold
		p.BoostPositiveRateThreshold = boost
	}
	if p.NeedsOptimizationRateThreshold > p.NeutralPositiveRateThreshold {
		p.NeedsOptimizationRateThreshold = p.NeutralPositiveRateThreshold
	}
	if p.BoostRecallWeight <= 0 {
		p.BoostRecallWeight = 1.2
	}
	if p.NeutralRecallWeight <= 0 {
		p.NeutralRecallWeight = 1
	}
	if p.PenaltyRecallWeight <= 0 {
		p.PenaltyRecallWeight = 0.8
	}
	return p
}

// ChunkQuality is the denormalized result of applying a policy to a tally.
type ChunkQuality struct {
	PositiveRate      *float64
	RecallWeight      float64
	NeedsOptimization bool
}

// Evaluate maps a like/dislike tally to an approval rate, recall weight, and
// the needs-optimization flag. Below MinFeedbackCount the rate is still
// reported but the weight stays neutral.
func (p FeedbackPolicy) Evaluate(likes, dislikes int64) ChunkQuality {
	p = p.Normalize()
	quality := ChunkQuality{RecallWeight: p.NeutralRecallWeight}
	total := likes + dislikes
	if total <= 0 {
		return quality
	}
	rate := float64(likes) / float64(total)
	quality.PositiveRate = &rate
	if total < int64(p.MinFeedbackCount) {
		return quality
	}
	switch {
	case rate >= p.BoostPositiveRateThreshold:
		quality.RecallWeight = p.BoostRecallWeight
	case rate >= p.NeutralPositiveRateThreshold:
		quality.RecallWeight = p.NeutralRecallWeight
	default:
		quality.RecallWeight = p.PenaltyRecallWeight
	}
	quality.NeedsOptimization = rate < p.NeedsOptimizationRateThreshold
	return quality
}

// EffectiveRecallWeight treats a missing or zero stored weight as neutral.
func EffectiveRecallWeight(weight float64) float64 {
	if weight <= 0 {
		return 1
	}
	return weight
}

// FeedbackReasonStat is one aggregated dislike reason for a chunk.
type FeedbackReasonStat struct {
	ReasonCode string `json:"reason_code"`
	Count      int64  `json:"count"`
}

// FeedbackSessionRef is one QA session that cited a chunk.
type FeedbackSessionRef struct {
	SessionID   string    `json:"session_id"`
	LastCitedAt time.Time `json:"last_cited_at"`
}

// ChunkFeedbackStats is the admin view of one chunk's votes.
type ChunkFeedbackStats struct {
	ChunkID                string               `json:"chunk_id"`
	LikeCount              int64                `json:"like_count"`
	DislikeCount           int64                `json:"dislike_count"`
	PositiveRate           *float64             `json:"positive_rate"`
	RecallWeight           float64              `json:"recall_weight"`
	NeedsOptimization      bool                 `json:"needs_optimization"`
	AssociatedSessionCount int64                `json:"associated_session_count"`
	Sessions               []FeedbackSessionRef `json:"sessions"`
	ReasonStats            []FeedbackReasonStat `json:"reason_stats"`
	RecentReasons          []string             `json:"recent_reasons"`
	FeedbackResetAt        *time.Time           `json:"feedback_reset_at,omitempty"`
}

// ChunkFeedbackFilter selects chunks by approval rate or the optimization flag.
// MaxPositiveRate keeps chunks whose rate is strictly below the value.
type ChunkFeedbackFilter struct {
	MaxPositiveRate   *float64
	NeedsOptimization *bool
}

// Active reports whether the filter constrains the list.
func (f ChunkFeedbackFilter) Active() bool {
	return f.MaxPositiveRate != nil || f.NeedsOptimization != nil
}

// errInvalidFeedback is returned by NormalizeFeedbackRequest. Callers map it
// to a 400. It lives here so the types package stays free of the errors
// package used by handlers.
type feedbackError string

func (e feedbackError) Error() string { return string(e) }

const errInvalidFeedback feedbackError = "invalid feedback"

// ErrInvalidFeedback is the sentinel for a rejected vote payload.
var ErrInvalidFeedback error = errInvalidFeedback

// IsInvalidFeedback reports whether err came from NormalizeFeedbackRequest.
func IsInvalidFeedback(err error) bool {
	return err == ErrInvalidFeedback
}

// AttributableKnowledgeRef reports whether a search hit should be stored as
// an answer-to-chunk link. Web and history hits are not knowledge chunks.
func AttributableKnowledgeRef(ref *SearchResult) bool {
	if ref == nil || strings.TrimSpace(ref.ID) == "" || strings.TrimSpace(ref.KnowledgeID) == "" {
		return false
	}
	if ref.MatchType == MatchTypeHistory || ref.MatchType == MatchTypeWebSearch {
		return false
	}
	if ref.KnowledgeSource == "web_search" {
		return false
	}
	return true
}
