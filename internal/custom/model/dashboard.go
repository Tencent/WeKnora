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
	RepresentativeQuestion string     `gorm:"type:text" json:"representative_question"`
	QuestionCount          int        `json:"question_count"`
	RelatedVideoCount      int        `json:"related_video_count"`
	LastAskedAt            *time.Time `json:"last_asked_at"`
	Videos                 string     `gorm:"type:text" json:"videos"` // JSON：[{video_id,title,video_type,first_seconds,first_timestamp}]
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}
