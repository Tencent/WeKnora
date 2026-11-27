package types

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// AnswerFeedbackLike is a positive rating of an assistant answer.
	AnswerFeedbackLike = "like"
	// AnswerFeedbackDislike is a negative rating of an assistant answer.
	AnswerFeedbackDislike = "dislike"
	// AnswerFeedbackNone clears a previous rating.
	AnswerFeedbackNone = "none"

	// ChunkWeightTriggerLike records a weight change caused by a like.
	ChunkWeightTriggerLike = "like"
	// ChunkWeightTriggerDislike records a weight change caused by a dislike.
	ChunkWeightTriggerDislike = "dislike"
	// ChunkWeightTriggerCancel records a weight change caused by clearing a rating.
	ChunkWeightTriggerCancel = "cancel"
	// ChunkWeightTriggerReset records an administrator reset.
	ChunkWeightTriggerReset = "admin_reset"

	// AnswerFeedbackReasonMaxRunes bounds a dislike reason.
	AnswerFeedbackReasonMaxRunes = 500
	chunkFeedbackPreviewRunes    = 160
)

// ChunkFeedbackConfig is the approval-rate policy for chunk recall weight.
// Approval rate is likes / (likes + dislikes).
//
//	rate >= BoostThreshold: recall weight becomes BoostWeight
//	DegradeThreshold <= rate < BoostThreshold: NeutralWeight
//	rate < DegradeThreshold: DegradeWeight
//	rate < NeedsOptimizationThreshold (and enough samples): NeedsOptimization
type ChunkFeedbackConfig struct {
	Enabled                    bool    `json:"enabled"`
	BoostThreshold             float64 `json:"boost_threshold"`
	DegradeThreshold           float64 `json:"degrade_threshold"`
	NeedsOptimizationThreshold float64 `json:"needs_optimization_threshold"`
	BoostWeight                float64 `json:"boost_weight"`
	NeutralWeight              float64 `json:"neutral_weight"`
	DegradeWeight              float64 `json:"degrade_weight"`
	MinSamples                 int     `json:"min_samples"`
}

// DefaultChunkFeedbackConfig matches the knowledge-QA feedback request:
// boost at or above 80%, leave the neutral band at 50%–80%, degrade below
// 50%, and flag a very low rate (below 30%) for manual cleanup.
func DefaultChunkFeedbackConfig() ChunkFeedbackConfig {
	return ChunkFeedbackConfig{
		Enabled:                    true,
		BoostThreshold:             0.8,
		DegradeThreshold:           0.5,
		NeedsOptimizationThreshold: 0.3,
		BoostWeight:                1.2,
		NeutralWeight:              1,
		DegradeWeight:              0.8,
		MinSamples:                 1,
	}
}

// EffectiveChunkFeedback returns the stored policy, or the defaults when the
// tenant has not saved one. A stored policy is returned as-is so an explicit
// Enabled=false is preserved.
func (c *RetrievalConfig) EffectiveChunkFeedback() ChunkFeedbackConfig {
	if c == nil || c.ChunkFeedback == nil {
		return DefaultChunkFeedbackConfig()
	}
	return *c.ChunkFeedback
}

// ChunkFeedbackRankingEnabled reports whether stored recall weights should
// change search order. A missing policy is enabled.
func (c *RetrievalConfig) ChunkFeedbackRankingEnabled() bool {
	if c == nil || c.ChunkFeedback == nil {
		return true
	}
	return c.ChunkFeedback.Enabled
}

// Validate rejects a policy that cannot be applied. Zero thresholds are
// rejected so a partial JSON body cannot silently collapse every band.
func (c ChunkFeedbackConfig) Validate() error {
	if c.BoostThreshold <= 0 || c.BoostThreshold > 1 {
		return fmt.Errorf("boost_threshold must be greater than 0 and at most 1")
	}
	if c.DegradeThreshold < 0 || c.DegradeThreshold > 1 {
		return fmt.Errorf("degrade_threshold must be between 0 and 1")
	}
	if c.NeedsOptimizationThreshold < 0 || c.NeedsOptimizationThreshold > 1 {
		return fmt.Errorf("needs_optimization_threshold must be between 0 and 1")
	}
	if c.NeedsOptimizationThreshold > c.DegradeThreshold {
		return fmt.Errorf("needs_optimization_threshold must be less than or equal to degrade_threshold")
	}
	if c.DegradeThreshold > c.BoostThreshold {
		return fmt.Errorf("degrade_threshold must be less than or equal to boost_threshold")
	}
	if c.BoostWeight <= 0 || c.NeutralWeight <= 0 || c.DegradeWeight <= 0 {
		return fmt.Errorf("recall weights must be greater than 0")
	}
	if c.BoostWeight > 5 || c.NeutralWeight > 5 || c.DegradeWeight > 5 {
		return fmt.Errorf("recall weights must be at most 5")
	}
	if c.MinSamples < 1 || c.MinSamples > 1000 {
		return fmt.Errorf("min_samples must be between 1 and 1000")
	}
	return nil
}

// ChunkWeightDecision is the recall weight and flag for one chunk's counts.
type ChunkWeightDecision struct {
	ApprovalRate      *float64
	Weight            float64
	NeedsOptimization bool
	Changed           bool
}

// EffectiveRecallWeight maps an unset (zero or negative) stored weight to 1.
func EffectiveRecallWeight(weight float64) float64 {
	if weight <= 0 {
		return 1
	}
	return weight
}

// DecideChunkWeight maps approval rate onto the configured band.
// When the policy is disabled, the current weight is kept and only the
// approval rate and optimization flag are updated.
func DecideChunkWeight(cfg ChunkFeedbackConfig, likes, dislikes int, currentWeight float64) ChunkWeightDecision {
	if likes < 0 {
		likes = 0
	}
	if dislikes < 0 {
		dislikes = 0
	}
	total := likes + dislikes
	var rate *float64
	if total > 0 {
		value := float64(likes) / float64(total)
		rate = &value
	}
	base := EffectiveRecallWeight(currentWeight)
	neutral := cfg.NeutralWeight
	if neutral <= 0 {
		neutral = 1
	}
	weight := base
	if cfg.Enabled {
		switch {
		case total < cfg.MinSamples || rate == nil:
			weight = neutral
		case *rate >= cfg.BoostThreshold:
			weight = cfg.BoostWeight
		case *rate >= cfg.DegradeThreshold:
			weight = neutral
		default:
			weight = cfg.DegradeWeight
		}
		if weight <= 0 {
			weight = neutral
		}
	}
	needs := rate != nil && total >= cfg.MinSamples && *rate < cfg.NeedsOptimizationThreshold
	changed := weight-base > 1e-9 || base-weight > 1e-9
	return ChunkWeightDecision{
		ApprovalRate:      rate,
		Weight:            weight,
		NeedsOptimization: needs,
		Changed:           changed,
	}
}

// FeedbackCountDelta is the like/dislike counter change from prev to next.
// An empty rating and AnswerFeedbackNone are the same.
func FeedbackCountDelta(prev, next string) (dLike, dDislike int) {
	prev = NormalizeAnswerFeedbackRating(prev)
	next = NormalizeAnswerFeedbackRating(next)
	if prev == AnswerFeedbackLike {
		dLike--
	}
	if prev == AnswerFeedbackDislike {
		dDislike--
	}
	if next == AnswerFeedbackLike {
		dLike++
	}
	if next == AnswerFeedbackDislike {
		dDislike++
	}
	return dLike, dDislike
}

// NormalizeAnswerFeedbackRating accepts like, dislike, and none.
// Anything else, including empty, is none.
func NormalizeAnswerFeedbackRating(rating string) string {
	switch strings.ToLower(strings.TrimSpace(rating)) {
	case AnswerFeedbackLike:
		return AnswerFeedbackLike
	case AnswerFeedbackDislike:
		return AnswerFeedbackDislike
	default:
		return AnswerFeedbackNone
	}
}

// WeightTriggerForRating is the weight-log source for a submitted rating.
func WeightTriggerForRating(rating string) string {
	switch NormalizeAnswerFeedbackRating(rating) {
	case AnswerFeedbackLike:
		return ChunkWeightTriggerLike
	case AnswerFeedbackDislike:
		return ChunkWeightTriggerDislike
	default:
		return ChunkWeightTriggerCancel
	}
}

// AnswerChunkRef is one knowledge chunk cited by an assistant answer.
type AnswerChunkRef struct {
	ChunkID         string
	KnowledgeID     string
	KnowledgeBaseID string
}

// AnswerChunkRefsFromMessage collects the knowledge chunks an answer cited.
// Parent, nearby, and relation context chunks are omitted: they are prompt
// context, not the hits the answer was grounded on. Web and chat-history
// hits are omitted because they are not knowledge-base chunks.
func AnswerChunkRefsFromMessage(msg *Message) []AnswerChunkRef {
	if msg == nil {
		return nil
	}
	byID := make(map[string]AnswerChunkRef)
	for _, ref := range msg.KnowledgeReferences {
		if ref == nil || strings.TrimSpace(ref.ID) == "" {
			continue
		}
		if !citedKnowledgeChunk(ref) {
			continue
		}
		id := strings.TrimSpace(ref.ID)
		byID[id] = AnswerChunkRef{
			ChunkID:         id,
			KnowledgeID:     ref.KnowledgeID,
			KnowledgeBaseID: ref.KnowledgeBaseID,
		}
	}
	for _, step := range msg.AgentSteps {
		for _, call := range step.ToolCalls {
			if call.Result == nil {
				continue
			}
			collectAnswerChunkRefs(call.Result.Data, byID)
		}
	}
	if len(byID) == 0 {
		return nil
	}
	ids := slices.Sorted(maps.Keys(byID))
	out := make([]AnswerChunkRef, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

func citedKnowledgeChunk(ref *SearchResult) bool {
	switch ref.MatchType {
	case MatchTypeNearByChunk, MatchTypeParentChunk, MatchTypeRelationChunk, MatchTypeHistory, MatchTypeWebSearch:
		return false
	}
	return ref.ChunkType != ChunkTypeWebSearch
}

func collectAnswerChunkRefs(value interface{}, byID map[string]AnswerChunkRef) {
	switch node := value.(type) {
	case map[string]interface{}:
		if id := chunkIDFromToolRow(node); id != "" {
			current := byID[id]
			ref := AnswerChunkRef{
				ChunkID:         id,
				KnowledgeID:     firstNonEmpty(stringField(node, "knowledge_id"), current.KnowledgeID),
				KnowledgeBaseID: firstNonEmpty(stringField(node, "knowledge_base_id"), current.KnowledgeBaseID),
			}
			byID[id] = ref
		}
		for _, child := range node {
			collectAnswerChunkRefs(child, byID)
		}
	case []interface{}:
		for _, child := range node {
			collectAnswerChunkRefs(child, byID)
		}
	}
}

func chunkIDFromToolRow(node map[string]interface{}) string {
	if id := stringField(node, "chunk_id"); id != "" {
		return id
	}
	return stringField(node, "faq_id")
}

func stringField(node map[string]interface{}, key string) string {
	value, ok := node[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ChunkContentPreview shortens chunk text for the admin list.
func ChunkContentPreview(content string) string {
	content = strings.TrimSpace(content)
	if utf8.RuneCountInString(content) <= chunkFeedbackPreviewRunes {
		return content
	}
	runes := []rune(content)
	return string(runes[:chunkFeedbackPreviewRunes]) + "…"
}

// AnswerChunkLink binds one assistant answer to a knowledge chunk it cited.
type AnswerChunkLink struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64    `json:"tenant_id" gorm:"index"`
	SessionID       string    `json:"session_id" gorm:"type:varchar(36);index"`
	MessageID       string    `json:"message_id" gorm:"type:varchar(36);uniqueIndex:idx_acl,priority:1"`
	ChunkID         string    `json:"chunk_id" gorm:"type:varchar(36);uniqueIndex:idx_acl,priority:2;index"`
	KnowledgeID     string    `json:"knowledge_id" gorm:"type:varchar(36)"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	CreatedAt       time.Time `json:"created_at"`
}

// TableName keeps the association table name stable.
func (AnswerChunkLink) TableName() string { return "answer_chunk_links" }

// AnswerFeedback is one user's rating of one assistant message.
type AnswerFeedback struct {
	ID        string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64    `json:"tenant_id" gorm:"index"`
	UserID    string    `json:"user_id" gorm:"type:varchar(64);uniqueIndex:idx_answer_feedback_user,priority:2"`
	SessionID string    `json:"session_id" gorm:"type:varchar(36);index"`
	MessageID string    `json:"message_id" gorm:"type:varchar(36);uniqueIndex:idx_answer_feedback_user,priority:1"`
	Rating    string    `json:"rating" gorm:"type:varchar(16)"`
	Reason    string    `json:"reason" gorm:"type:text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName keeps the rating table name stable.
func (AnswerFeedback) TableName() string { return "answer_feedbacks" }

// ChunkFeedbackAttribution is the rating currently counted on one chunk.
// Reset deletes these rows so later rating changes do not subtract counts
// that were already cleared.
type ChunkFeedbackAttribution struct {
	ID         string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID   uint64    `json:"tenant_id" gorm:"index"`
	FeedbackID string    `json:"feedback_id" gorm:"type:varchar(36);uniqueIndex:idx_chunk_feedback_attr,priority:1"`
	ChunkID    string    `json:"chunk_id" gorm:"type:varchar(36);uniqueIndex:idx_chunk_feedback_attr,priority:2;index"`
	Rating     string    `json:"rating" gorm:"type:varchar(16)"`
	Reason     string    `json:"reason" gorm:"type:text"`
	AppliedAt  time.Time `json:"applied_at"`
}

// TableName keeps the attribution table name stable.
func (ChunkFeedbackAttribution) TableName() string { return "chunk_feedback_attributions" }

// ChunkWeightLog is one recall-weight change for a chunk.
type ChunkWeightLog struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64    `json:"tenant_id" gorm:"index"`
	ChunkID         string    `json:"chunk_id" gorm:"type:varchar(36);index"`
	KnowledgeID     string    `json:"knowledge_id" gorm:"type:varchar(36)"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36)"`
	OldWeight       float64   `json:"old_weight"`
	NewWeight       float64   `json:"new_weight"`
	OldApprovalRate *float64  `json:"old_approval_rate"`
	NewApprovalRate *float64  `json:"new_approval_rate"`
	LikeCount       int       `json:"like_count"`
	DislikeCount    int       `json:"dislike_count"`
	TriggerSource   string    `json:"trigger_source" gorm:"type:varchar(32)"`
	MessageID       string    `json:"message_id" gorm:"type:varchar(36)"`
	UserID          string    `json:"user_id" gorm:"type:varchar(64)"`
	CreatedAt       time.Time `json:"created_at" gorm:"index"`
}

// TableName keeps the weight-log table name stable.
func (ChunkWeightLog) TableName() string { return "chunk_weight_logs" }

// MessageFeedbackState is the calling user's rating, returned with history
// and after a submit.
type MessageFeedbackState struct {
	Rating     string `json:"rating"`
	Reason     string `json:"reason,omitempty"`
	ChunkCount int    `json:"chunk_count,omitempty"`
}

// ChunkFeedbackListFilter selects chunk rows for the admin list.
type ChunkFeedbackListFilter struct {
	Page              int
	PageSize          int
	MinApproval       *float64
	MaxApproval       *float64
	NeedsOptimization *bool
	LowQuality        bool
	IncludeUnrated    bool
}

// ChunkFeedbackSummary is one row of the chunk feedback list.
type ChunkFeedbackSummary struct {
	ChunkID           string   `json:"chunk_id"`
	KnowledgeID       string   `json:"knowledge_id"`
	KnowledgeTitle    string   `json:"knowledge_title"`
	ContentPreview    string   `json:"content_preview"`
	LikeCount         int      `json:"like_count"`
	DislikeCount      int      `json:"dislike_count"`
	ApprovalRate      *float64 `json:"approval_rate"`
	RecallWeight      float64  `json:"recall_weight"`
	NeedsOptimization bool     `json:"needs_optimization"`
	SessionCount      int      `json:"session_count"`
}

// ChunkFeedbackPage is a page of chunk feedback rows.
type ChunkFeedbackPage struct {
	Items    []ChunkFeedbackSummary `json:"items"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
}

// ChunkFeedbackReason is one aggregated dislike reason.
type ChunkFeedbackReason struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// ChunkFeedbackSession is one answer that cited a chunk.
type ChunkFeedbackSession struct {
	SessionID string    `json:"session_id"`
	MessageID string    `json:"message_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ChunkWeightLogView is a weight-log row returned to admins.
type ChunkWeightLogView struct {
	ID              string    `json:"id"`
	OldWeight       float64   `json:"old_weight"`
	NewWeight       float64   `json:"new_weight"`
	OldApprovalRate *float64  `json:"old_approval_rate"`
	NewApprovalRate *float64  `json:"new_approval_rate"`
	LikeCount       int       `json:"like_count"`
	DislikeCount    int       `json:"dislike_count"`
	TriggerSource   string    `json:"trigger_source"`
	MessageID       string    `json:"message_id,omitempty"`
	UserID          string    `json:"user_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// ChunkFeedbackDetail is the admin view of one chunk.
type ChunkFeedbackDetail struct {
	ChunkFeedbackSummary
	DislikeReasons []ChunkFeedbackReason  `json:"dislike_reasons"`
	Sessions       []ChunkFeedbackSession `json:"sessions"`
	WeightLogs     []ChunkWeightLogView   `json:"weight_logs"`
}
