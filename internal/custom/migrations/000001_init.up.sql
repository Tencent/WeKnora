-- 自研业务库初始 schema（与 WeKnora 库隔离）
-- 单一数据源原则：视频内容存 WeKnora，这里只存 WeKnora 对象 ID 引用

CREATE TABLE IF NOT EXISTS videos (
    id VARCHAR(36) PRIMARY KEY,
    title VARCHAR(255) NOT NULL,
    video_type VARCHAR(50),
    duration_seconds INTEGER DEFAULT 0,
    file_url TEXT,
    thumbnail_url TEXT,
    subtitle_file_url TEXT,
    transcript_knowledge_id VARCHAR(64),
    outline_wiki_page_id VARCHAR(64),
    overview_wiki_page_id VARCHAR(64),
    summary_wiki_page_id VARCHAR(64),
    transcript_page_wiki_page_id VARCHAR(64),
    status VARCHAR(50),
    processing_error_summary TEXT,
    uploaded_at TIMESTAMP WITH TIME ZONE,
    ready_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_videos_video_type ON videos(video_type);
CREATE INDEX IF NOT EXISTS idx_videos_status ON videos(status);
CREATE INDEX IF NOT EXISTS idx_videos_deleted_at ON videos(deleted_at);

CREATE TABLE IF NOT EXISTS video_processing_jobs (
    id VARCHAR(36) PRIMARY KEY,
    video_id VARCHAR(36),
    job_type VARCHAR(50),
    provider VARCHAR(50),
    external_task_id VARCHAR(128),
    idempotency_key VARCHAR(128),
    status VARCHAR(50),
    progress INTEGER DEFAULT 0,
    attempt_count INTEGER DEFAULT 0,
    max_attempts INTEGER DEFAULT 0,
    input_payload TEXT,
    result_payload TEXT,
    error_code VARCHAR(100),
    error_message TEXT,
    callback_received_at TIMESTAMP WITH TIME ZONE,
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_idempotency_key ON video_processing_jobs(idempotency_key);
CREATE INDEX IF NOT EXISTS idx_jobs_video_job_status ON video_processing_jobs(video_id, job_type, status);
CREATE INDEX IF NOT EXISTS idx_jobs_external_task_id ON video_processing_jobs(external_task_id);

CREATE TABLE IF NOT EXISTS video_summary_frameworks (
    id VARCHAR(36) PRIMARY KEY,
    video_type VARCHAR(50),
    framework TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_frameworks_video_type ON video_summary_frameworks(video_type);

CREATE TABLE IF NOT EXISTS dashboard_question_stats (
    id VARCHAR(36) PRIMARY KEY,
    stat_date VARCHAR(10),
    question_count INTEGER DEFAULT 0,
    active_video_count INTEGER DEFAULT 0,
    cluster_count INTEGER DEFAULT 0,
    top_videos TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_question_stats_stat_date ON dashboard_question_stats(stat_date);

CREATE TABLE IF NOT EXISTS dashboard_question_clusters (
    id VARCHAR(36) PRIMARY KEY,
    representative_question TEXT,
    question_count INTEGER DEFAULT 0,
    related_video_count INTEGER DEFAULT 0,
    last_asked_at TIMESTAMP WITH TIME ZONE,
    videos TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
