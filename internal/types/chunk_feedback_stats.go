package types

type DislikeReasonStat struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

type ChunkFeedbackStats struct {
	ChunkID           string               `json:"chunk_id"`
	KnowledgeID       string               `json:"knowledge_id"`
	KnowledgeTitle    string               `json:"knowledge_title"`
	KnowledgeFilename string               `json:"knowledge_filename"`
	ChunkIndex        int                  `json:"chunk_index"`
	ContentPreview    string               `json:"content_preview"`
	LikeCount         int64                `json:"like_count"`
	DislikeCount      int64                `json:"dislike_count"`
	PositiveRate      float64              `json:"positive_rate"`
	RecallWeight      float64              `json:"recall_weight"`
	NeedsOptimization bool                 `json:"needs_optimization"`
	SessionCount      int64                `json:"session_count"`
	DislikeReasons    []*DislikeReasonStat `json:"dislike_reasons" gorm:"-"`
}
