package model

import "time"

// DashboardQuestionStat 提问趋势聚合（按日，B17 定时任务产出）
type DashboardQuestionStat struct {
	ID               string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	StatDate         string    `gorm:"type:varchar(10);index" json:"stat_date"` // YYYY-MM-DD
	QuestionCount    int       `json:"question_count"`
	ActiveVideoCount int       `json:"active_video_count"`
	ClusterCount     int       `json:"cluster_count"`
	TopVideos        string    `gorm:"type:text" json:"top_videos"` // JSON：[{video_id,title,count}]
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// DashboardQuestionCluster 高频问题聚类（B17 定时任务产出）
type DashboardQuestionCluster struct {
	ID                     string     `gorm:"type:varchar(36);primaryKey" json:"id"`
	QuestionKey            string     `gorm:"type:text;index" json:"-"`
	RepresentativeQuestion string     `gorm:"type:text" json:"representative_question"`
	QuestionCount          int        `json:"question_count"`
	RelatedVideoCount      int        `json:"related_video_count"`
	LastAskedAt            *time.Time `json:"last_asked_at"`
	Videos                 string     `gorm:"type:text" json:"videos"` // JSON：[{video_id,title,video_type,first_seconds,first_timestamp}]
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// DashboardQuestionEvent 是真实用户提问的不可变记录。
// 聚合表可以重建，事件表保留原始业务事实和幂等键。
type DashboardQuestionEvent struct {
	ID            string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	EventID       string    `gorm:"type:varchar(128);uniqueIndex;not null" json:"event_id"`
	SessionID     string    `gorm:"type:varchar(64);index" json:"session_id"`
	VideoID       string    `gorm:"type:varchar(36);index" json:"video_id"`
	VideoTitle    string    `gorm:"type:varchar(255)" json:"video_title"`
	VideoCategory string    `gorm:"type:varchar(50)" json:"video_category"`
	VideoSeconds  int       `json:"video_seconds"`
	ClusterID     string    `gorm:"type:varchar(36);index" json:"cluster_id"`
	Question      string    `gorm:"type:text;not null" json:"question"`
	AskedAt       time.Time `gorm:"index;not null" json:"asked_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ChatSourceAudit records the sources actually present in the assistant's
// persisted answer references. It does not infer or manufacture citations.
type ChatSourceAudit struct {
	ID                 string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	EventID            string    `gorm:"type:varchar(128);uniqueIndex;not null" json:"event_id"`
	SessionID          string    `gorm:"type:varchar(64);index" json:"session_id"`
	Scope              string    `gorm:"type:varchar(16)" json:"scope"`
	VideoID            string    `gorm:"type:varchar(36);index" json:"video_id"`
	SourceMode         string    `gorm:"type:varchar(32)" json:"source_mode"`
	FallbackUsed       bool      `json:"fallback_used"`
	ReferencesFound    int       `json:"references_found"`
	WikiPageIDs        string    `gorm:"type:text" json:"wiki_page_ids"`
	KnowledgeObjectIDs string    `gorm:"type:text" json:"knowledge_object_ids"`
	TranscriptChunkIDs string    `gorm:"type:text" json:"transcript_chunk_ids"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type WikiRelationAudit struct {
	ID                   string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	VideoID              string    `gorm:"type:varchar(36);index" json:"video_id"`
	TranscriptGeneration string    `gorm:"type:varchar(64);index" json:"transcript_generation"`
	SourceWikiPageID     string    `gorm:"type:varchar(64);index" json:"source_wiki_page_id"`
	SourceObjectID       string    `gorm:"type:varchar(128);index" json:"source_object_id"`
	RelationID           string    `gorm:"type:varchar(128)" json:"relation_id"`
	RelationType         string    `gorm:"type:varchar(64)" json:"relation_type"`
	TargetObjectID       string    `gorm:"type:varchar(128)" json:"target_object_id"`
	TargetWikiPageID     string    `gorm:"type:varchar(64)" json:"target_wiki_page_id"`
	EvidenceIDs          string    `gorm:"type:text" json:"evidence_ids"`
	Confidence           float64   `json:"confidence"`
	Status               string    `gorm:"type:varchar(32);index" json:"status"`
	Reason               string    `gorm:"type:text" json:"reason"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type WikiIdentityAudit struct {
	ID                     string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	VideoID                string    `gorm:"type:varchar(36);index" json:"video_id"`
	TranscriptGeneration   string    `gorm:"type:varchar(64);index" json:"transcript_generation"`
	SourceWikiPageID       string    `gorm:"type:varchar(64);index" json:"source_wiki_page_id"`
	SourceObjectID         string    `gorm:"type:varchar(128);index" json:"source_object_id"`
	CandidateWikiPageID    string    `gorm:"type:varchar(64);index" json:"candidate_wiki_page_id"`
	CandidateObjectID      string    `gorm:"type:varchar(128);index" json:"candidate_object_id"`
	NormalizedName         string    `gorm:"type:varchar(255);index" json:"normalized_name"`
	SourceType             string    `gorm:"type:varchar(32)" json:"source_type"`
	CandidateType          string    `gorm:"type:varchar(32)" json:"candidate_type"`
	SourceEntitySubType    string    `gorm:"type:varchar(32)" json:"source_entity_sub_type"`
	CandidateEntitySubType string    `gorm:"type:varchar(32)" json:"candidate_entity_sub_type"`
	TypeMatch              bool      `json:"type_match"`
	TitleMatch             bool      `json:"title_match"`
	ContextMatch           bool      `json:"context_match"`
	EvidenceOverlap        bool      `json:"evidence_overlap"`
	Score                  float64   `json:"score"`
	Decision               string    `gorm:"type:varchar(32);index" json:"decision"`
	Reason                 string    `gorm:"type:text" json:"reason"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}
