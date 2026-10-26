package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// DocumentProgressService joins existing durable state for the system task console.
type DocumentProgressService struct {
	db   *gorm.DB
	task interfaces.TaskEnqueuer
}

// NewDocumentProgressService uses the existing queue and processing tables without a separate progress store.
func NewDocumentProgressService(db *gorm.DB, task interfaces.TaskEnqueuer) *DocumentProgressService {
	return &DocumentProgressService{db: db, task: task}
}

// DocumentProgressFilter selects documents independently of the live Redis queues.
type DocumentProgressFilter struct {
	KnowledgeBaseID string
	Search          string
	Status          string
	Page            int
	PageSize        int
}

// Valid bounds pagination and search work for this platform-wide view.
func (f DocumentProgressFilter) Valid() bool {
	return f.Page >= 1 && f.Page <= 10000 && f.PageSize >= 1 && f.PageSize <= 100 &&
		utf8.RuneCountInString(f.Search) <= 200 && len(f.KnowledgeBaseID) <= 64 &&
		(f.Status == "all" || f.Status == "unfinished" || f.Status == "failed" || f.Status == "completed")
}

// DocumentStageProgress contains metadata only; span prompts and outputs stay private.
type DocumentStageProgress struct {
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	ErrorCode  string     `json:"error_code,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// DocumentProgress separates document ingestion, Wiki work, and historical failures.
type DocumentProgress struct {
	ID                string                  `json:"id"`
	TenantID          uint64                  `json:"tenant_id"`
	KnowledgeBaseID   string                  `json:"knowledge_base_id"`
	KnowledgeBaseName string                  `json:"knowledge_base_name"`
	Title             string                  `json:"title"`
	ParseStatus       string                  `json:"parse_status"`
	IngestionStatus   string                  `json:"ingestion_status"`
	Attempt           int                     `json:"attempt"`
	WikiStatus        string                  `json:"wiki_status"`
	WikiPending       int64                   `json:"wiki_pending"`
	WikiRetries       int                     `json:"wiki_retries"`
	FailureCount      int64                   `json:"failure_count"`
	CanStart          bool                    `json:"can_start"`
	Stages            []DocumentStageProgress `json:"stages" gorm:"-"`
	UpdatedAt         time.Time               `json:"updated_at"`
	wikiEnabled       bool
}

// DocumentProgressResponse is one page of durable document metadata.
type DocumentProgressResponse struct {
	Documents []DocumentProgress `json:"documents"`
	Total     int64              `json:"total"`
	Page      int                `json:"page"`
	PageSize  int                `json:"page_size"`
}

// DocumentFailure is a bounded projection of an immutable dead-letter record.
type DocumentFailure struct {
	ID        int64     `json:"id"`
	TaskType  string    `json:"task_type"`
	LastError string    `json:"last_error"`
	FailCount int       `json:"fail_count"`
	FailedAt  time.Time `json:"failed_at"`
}

// DocumentProgressDetail keeps current stages separate from the original failure archive.
type DocumentProgressDetail struct {
	Document        DocumentProgress  `json:"document"`
	Failures        []DocumentFailure `json:"failures"`
	HasMoreFailures bool              `json:"has_more_failures"`
}

const documentAttemptSQL = `(SELECT COALESCE(MAX(a.attempt),0)
 FROM knowledge_processing_spans a WHERE a.knowledge_id=k.id)`

func (s *DocumentProgressService) jsonText(column, key string) string {
	if s.db.Name() == "sqlite" {
		return "json_extract(" + column + ",'$." + key + "')"
	}
	return column + "->>'" + key + "'"
}

func (s *DocumentProgressService) pendingSQL() string {
	attempt := s.jsonText("p.payload", "attempt")
	return `p.task_type='wiki:ingest' AND p.scope='knowledge_base' AND p.op='ingest'
		AND p.tenant_id=k.tenant_id AND p.scope_id=k.knowledge_base_id AND p.dedup_key=k.id
		AND ` + s.jsonText("p.payload", "knowledge_id") + `=k.id
		AND ` + s.jsonText("p.payload", "op") + `='ingest'
		AND (COALESCE(CAST(` + attempt + ` AS TEXT),'0') IN ('0',CAST(` + documentAttemptSQL + ` AS TEXT)))`
}

func documentFailureSQL() string {
	return `d.tenant_id=k.tenant_id AND
		((d.scope='knowledge_base' AND d.scope_id=k.knowledge_base_id AND d.related_id=k.id)
		OR (d.scope='knowledge' AND d.scope_id=k.id))`
}

func (s *DocumentProgressService) currentWikiFailureSQL() string {
	attempt := s.jsonText("d.payload", "attempt")
	return documentFailureSQL() + ` AND d.task_type='wiki:ingest' AND
  COALESCE(CAST(` + attempt + ` AS TEXT),'0') IN ('0',CAST(` + documentAttemptSQL + ` AS TEXT))`
}

func (s *DocumentProgressService) wikiEnabledSQL() string {
	return "COALESCE(CAST(" + s.jsonText("kb.indexing_strategy", "wiki_enabled") +
		" AS TEXT), 'false') IN ('true','1')"
}

func (s *DocumentProgressService) query(ctx context.Context, f DocumentProgressFilter) *gorm.DB {
	wikiEnabled := s.wikiEnabledSQL()
	pending := "EXISTS(SELECT 1 FROM task_pending_ops p WHERE " + s.pendingSQL() + ")"
	wikiSpan := `s.knowledge_id=k.id AND s.attempt=` + documentAttemptSQL + ` AND s.name='postprocess.wiki'
		AND s.id=(SELECT MAX(w.id) FROM knowledge_processing_spans w
		WHERE w.knowledge_id=k.id AND w.attempt=s.attempt AND w.name='postprocess.wiki')`
	done := `EXISTS(SELECT 1 FROM knowledge_processing_spans s WHERE ` + wikiSpan + ` AND s.status IN ('done','skipped')
		AND NOT EXISTS(SELECT 1 FROM task_dead_letters d WHERE ` + s.currentWikiFailureSQL() + `
		AND d.failed_at>s.finished_at))`
	failed := `EXISTS(SELECT 1 FROM knowledge_processing_spans s WHERE ` + wikiSpan + ` AND s.status='failed') OR
		EXISTS(SELECT 1 FROM task_dead_letters d WHERE ` + s.currentWikiFailureSQL() + `)`
	q := s.db.WithContext(ctx).Table("knowledges k").
		Joins("JOIN knowledge_bases kb ON kb.id=k.knowledge_base_id AND kb.tenant_id=k.tenant_id").
		Joins("JOIN tenants t ON t.id=k.tenant_id").
		Where("k.deleted_at IS NULL AND kb.deleted_at IS NULL AND t.deleted_at IS NULL").
		Where("k.type IN ?", []string{"file", "url", "manual"})
	if f.KnowledgeBaseID != "" {
		q = q.Where("kb.id=?", f.KnowledgeBaseID)
	}
	if search := strings.ToLower(strings.TrimSpace(f.Search)); search != "" {
		q = q.Where("LOWER(k.title) LIKE ? OR LOWER(k.file_name) LIKE ? OR LOWER(kb.name) LIKE ?",
			"%"+search+"%", "%"+search+"%", "%"+search+"%")
	}
	switch f.Status {
	case "unfinished":
		q = q.Where("k.parse_status IN ? OR ("+wikiEnabled+" AND ("+pending+" OR NOT "+done+"))",
			[]string{
				types.ParseStatusPending, types.ParseStatusProcessing,
				types.ParseStatusFinalizing, types.ParseStatusFailed,
			})
	case "failed":
		q = q.Where("k.parse_status=? OR ("+wikiEnabled+" AND NOT "+pending+" AND NOT "+done+" AND ("+failed+"))",
			types.ParseStatusFailed)
	case "completed":
		q = q.Where("k.parse_status=? AND (NOT "+wikiEnabled+" OR (NOT "+pending+" AND "+done+"))",
			types.ParseStatusCompleted)
	}
	return q
}

// List reads current attempts and their queue state from one consistent database snapshot.
func (s *DocumentProgressService) List(
	ctx context.Context, f DocumentProgressFilter,
) (*DocumentProgressResponse, error) {
	if !f.Valid() {
		return nil, errors.New("invalid document progress filter")
	}
	var result *DocumentProgressResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		reader := *s
		reader.db = tx
		var err error
		result, err = reader.list(ctx, f, "")
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}

func (s *DocumentProgressService) list(
	ctx context.Context, f DocumentProgressFilter, id string,
) (*DocumentProgressResponse, error) {
	result := &DocumentProgressResponse{Documents: []DocumentProgress{}, Page: f.Page, PageSize: f.PageSize}
	q := s.query(ctx, f)
	if id != "" {
		q = q.Where("k.id=?", id)
	}
	if err := q.Count(&result.Total).Error; err != nil {
		return nil, err
	}
	selectSQL := `k.id,k.tenant_id,k.knowledge_base_id,kb.name AS knowledge_base_name,
		COALESCE(NULLIF(k.title,''),k.file_name) AS title,k.parse_status,k.updated_at,
 ` + documentAttemptSQL + ` AS attempt,
		(SELECT COUNT(*) FROM task_dead_letters d WHERE ` + documentFailureSQL() + `) AS failure_count`
	if err := q.Select(selectSQL).Order("k.created_at DESC,k.id DESC").
		Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Scan(&result.Documents).Error; err != nil {
		return nil, err
	}
	if len(result.Documents) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(result.Documents))
	byID := make(map[string]*DocumentProgress, len(result.Documents))
	for i := range result.Documents {
		d := &result.Documents[i]
		ids = append(ids, d.ID)
		byID[d.ID] = d
		d.Stages = []DocumentStageProgress{}
		d.IngestionStatus = d.ParseStatus
		if d.ParseStatus == types.ParseStatusCompleted || d.ParseStatus == types.ParseStatusFinalizing {
			d.IngestionStatus = "ready"
		}
		d.WikiStatus = "not_started"
	}
	var kbs []types.KnowledgeBase
	if err := s.db.WithContext(ctx).Select("id,indexing_strategy").Where("id IN (?)",
		s.db.Table("knowledges").Select("knowledge_base_id").Where("id IN ?", ids)).Find(&kbs).Error; err != nil {
		return nil, err
	}
	enabled := map[string]bool{}
	for _, kb := range kbs {
		enabled[kb.ID] = kb.IsWikiEnabled()
	}
	for _, d := range byID {
		d.wikiEnabled = enabled[d.KnowledgeBaseID]
		if !d.wikiEnabled {
			d.WikiStatus = "disabled"
		}
	}
	type stageRow struct {
		KnowledgeID string
		DocumentStageProgress
	}
	var stages []stageRow
	latest := s.db.Model(&types.KnowledgeProcessingSpan{}).Select("MAX(id)").
		Where("knowledge_id IN ? AND (kind=? OR name=?)", ids, types.SpanKindStage, "postprocess.wiki").
		Group("knowledge_id,attempt,name")
	if err := s.db.WithContext(ctx).Table("knowledge_processing_spans s").
		Select("s.knowledge_id,s.name,s.status,s.error_code,s.finished_at").Where("s.id IN (?)", latest).
		Where(`s.attempt=(SELECT MAX(a.attempt) FROM knowledge_processing_spans a
 WHERE a.knowledge_id=s.knowledge_id)`).
		Order("s.id ASC").Scan(&stages).Error; err != nil {
		return nil, err
	}
	for _, st := range stages {
		d := byID[st.KnowledgeID]
		d.Stages = append(d.Stages, st.DocumentStageProgress)
		if st.Name == "postprocess.wiki" && d.wikiEnabled {
			switch st.Status {
			case types.SpanStatusDone:
				d.WikiStatus = "completed"
			case types.SpanStatusSkipped:
				d.WikiStatus = "skipped"
			case types.SpanStatusFailed:
				d.WikiStatus = "failed"
			case types.SpanStatusRunning:
				d.WikiStatus = "processing"
			}
		}
	}
	var failed []struct{ ID string }
	if err := s.db.WithContext(ctx).Table("knowledges k").Select("k.id").
		Joins("JOIN task_dead_letters d ON "+documentFailureSQL()).
		Where("k.id IN ? AND ("+s.currentWikiFailureSQL()+")", ids).
		Where(`NOT EXISTS(SELECT 1 FROM knowledge_processing_spans s WHERE s.knowledge_id=k.id
   AND s.attempt=` + documentAttemptSQL + ` AND s.name='postprocess.wiki'
   AND s.status IN ('done','skipped') AND s.finished_at>=d.failed_at)`).Group("k.id").Scan(&failed).Error; err != nil {
		return nil, err
	}
	for _, row := range failed {
		if d := byID[row.ID]; d.wikiEnabled {
			d.WikiStatus = "failed"
		}
	}
	var pending []struct {
		ID      string
		Pending int64
		Retries int
		Claimed int
	}
	stale := time.Now().Add(-wikiClaimStaleAfter)
	if err := s.db.WithContext(ctx).Table("knowledges k").
		Select(`k.id,COUNT(*) AS pending,MAX(p.fail_count) AS retries,
			MAX(CASE WHEN p.claimed_at>=? OR EXISTS(SELECT 1 FROM task_pending_ops held
			WHERE held.task_type=p.task_type AND held.scope=p.scope AND held.scope_id=p.scope_id
			AND held.dedup_key=p.dedup_key AND held.claimed_at>=?) THEN 1 ELSE 0 END) AS claimed`, stale, stale).
		Joins("JOIN task_pending_ops p ON "+s.pendingSQL()).
		Where("k.id IN ?", ids).Group("k.id").Scan(&pending).Error; err != nil {
		return nil, err
	}
	for _, row := range pending {
		d := byID[row.ID]
		d.WikiPending, d.WikiRetries = row.Pending, row.Retries
		if !d.wikiEnabled {
			continue
		}
		d.WikiStatus = "queued"
		if row.Retries > 0 {
			d.WikiStatus = "retrying"
		}
		if row.Claimed > 0 {
			d.WikiStatus = "processing"
		}
		d.CanStart = s.task != nil && row.Claimed == 0 &&
			(d.ParseStatus == types.ParseStatusCompleted || d.ParseStatus == types.ParseStatusFinalizing)
	}
	return result, nil
}

// Detail includes the latest 50 original failure records for one live document.
func (s *DocumentProgressService) Detail(ctx context.Context, id string) (*DocumentProgressDetail, error) {
	if id == "" || !validWikiStartKnowledgeID(id) {
		return nil, errors.New("invalid knowledge ID")
	}
	var result *DocumentProgressDetail
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		reader := *s
		reader.db = tx
		var err error
		result, err = reader.detail(ctx, id)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}

func (s *DocumentProgressService) detail(ctx context.Context, id string) (*DocumentProgressDetail, error) {
	page, err := s.list(ctx, DocumentProgressFilter{Status: "all", Page: 1, PageSize: 1}, id)
	if err != nil {
		return nil, err
	}
	if len(page.Documents) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	result := &DocumentProgressDetail{Document: page.Documents[0], Failures: []DocumentFailure{}}
	if err := s.db.WithContext(ctx).Table("task_dead_letters d").
		Select("d.id,d.task_type,SUBSTR(d.last_error,1,2048) AS last_error,d.fail_count,d.failed_at").
		Joins("JOIN knowledges k ON "+documentFailureSQL()).Where("k.id=?", id).
		Order("d.failed_at DESC,d.id DESC").Limit(51).Scan(&result.Failures).Error; err != nil {
		return nil, err
	}
	result.HasMoreFailures = len(result.Failures) > 50
	if result.HasMoreFailures {
		result.Failures = result.Failures[:50]
	}
	return result, nil
}
