// Package skill orchestrator records skill artifacts and schedules the foundation assembly job.
//
// 设计要点：
//   - skill 完成后由各 worker handler 调 AfterSkillComplete
//   - AfterSkillComplete 找到该视频「新生成的」wiki 页（按 frontmatter.type 过滤），
//     回写 videos 表（CP-T006）
//   - outline/summary/graph are independently triggered by transcript activation
//   - assemble is scheduled only after the two foundation artifacts exist
package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

// Orchestrator skill 链编排器
type Orchestrator struct {
	DB   *gorm.DB
	Wiki *weknora.WikiClient
	KBID string
}

type WikiPageVersionSnapshot map[string]int

type WikiPageBaseline struct {
	Versions     WikiPageVersionSnapshot `json:"versions"`
	JobCreatedAt time.Time               `json:"job_created_at"`
}

var ErrSummaryUserEditProtected = errors.New("summary user edit protected")

// NewOrchestrator 构造
func NewOrchestrator(db *gorm.DB, wiki *weknora.WikiClient, kbID string) *Orchestrator {
	return &Orchestrator{DB: db, Wiki: wiki, KBID: kbID}
}

// EnqueueJob 入库一个 pending job（CP-T004 幂等键保证）
func (o *Orchestrator) EnqueueJob(ctx context.Context, videoID, jobType string) (string, error) {
	var jobID string
	err := o.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		jobID, err = o.enqueueJob(ctx, tx, videoID, jobType)
		return err
	})
	return jobID, err
}

func (o *Orchestrator) EnqueueContentPipeline(ctx context.Context, videoID string) error {
	return o.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		generation, inputPayload, err := o.transcriptSourceManifest(ctx, tx, videoID)
		if err != nil {
			return err
		}
		for _, jobType := range []string{JobGraph, JobOutline, JobSummary} {
			jobID, err := o.enqueueJob(ctx, tx, videoID, jobType)
			if err != nil {
				return fmt.Errorf("enqueue %s job: %w", jobType, err)
			}
			if err := o.ensureTranscriptSourceManifest(ctx, tx, jobID, generation, inputPayload); err != nil {
				return fmt.Errorf("persist %s source manifest: %w", jobType, err)
			}
		}
		return nil
	})
}

func (o *Orchestrator) transcriptSourceManifest(ctx context.Context, db *gorm.DB, videoID string) (string, string, error) {
	var video model.Video
	if err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "transcript_generation").First(&video, "id = ?", videoID).Error; err != nil {
		return "", "", fmt.Errorf("load transcript generation: %w", err)
	}
	if strings.TrimSpace(video.TranscriptGeneration) == "" {
		return "", "", fmt.Errorf("video %s has no active transcript generation", videoID)
	}
	var chunks []model.VideoTranscriptChunk
	if err := db.WithContext(ctx).Where("video_id = ? AND generation = ?", videoID, video.TranscriptGeneration).
		Order("chunk_index ASC").Find(&chunks).Error; err != nil {
		return "", "", fmt.Errorf("load transcript chunk manifest: %w", err)
	}
	if len(chunks) == 0 {
		return "", "", fmt.Errorf("video %s has no active transcript chunks", videoID)
	}
	if strings.TrimSpace(o.KBID) == "" {
		return "", "", fmt.Errorf("knowledge_base_routing:knowledge_kb_missing")
	}
	var source model.VideoTranscriptSource
	if err := db.WithContext(ctx).Where(
		"video_id = ? AND transcript_generation = ? AND knowledge_base_id = ?",
		videoID, video.TranscriptGeneration, o.KBID,
	).First(&source).Error; err != nil {
		return "", "", fmt.Errorf("load transcript source document: %w", err)
	}
	if source.Status != "created" || strings.TrimSpace(source.KnowledgeID) == "" {
		return "", "", fmt.Errorf("video %s transcript source document is not ready", videoID)
	}
	seen := make(map[string]struct{}, len(chunks))
	for index, chunk := range chunks {
		if chunk.ChunkIndex != index || chunk.Status != "completed" || strings.TrimSpace(chunk.KnowledgeID) == "" {
			return "", "", fmt.Errorf("video %s transcript chunk manifest is incomplete at index %d", videoID, index)
		}
		if _, exists := seen[chunk.KnowledgeID]; exists {
			return "", "", fmt.Errorf("video %s transcript chunk manifest contains duplicate knowledge id", videoID)
		}
		seen[chunk.KnowledgeID] = struct{}{}
	}
	inputPayload, err := json.Marshal(map[string]any{
		"transcript_generation":               video.TranscriptGeneration,
		"transcript_source_knowledge_id":      source.KnowledgeID,
		"transcript_source_knowledge_base_id": source.KnowledgeBaseID,
		"transcript_input_mode":               "full_document",
	})
	if err != nil {
		return "", "", fmt.Errorf("encode transcript source manifest: %w", err)
	}
	return video.TranscriptGeneration, string(inputPayload), nil
}

func (o *Orchestrator) ensureTranscriptSourceManifest(ctx context.Context, db *gorm.DB, jobID, generation, inputPayload string) error {
	var job model.VideoProcessingJob
	if err := db.WithContext(ctx).Select("id", "transcript_generation", "input_payload").First(&job, "id = ?", jobID).Error; err != nil {
		return err
	}
	if job.TranscriptGeneration != "" && job.TranscriptGeneration != generation {
		return fmt.Errorf("job %s transcript generation mismatch: %s != %s", jobID, job.TranscriptGeneration, generation)
	}
	updates := map[string]any{}
	if job.TranscriptGeneration == "" {
		updates["transcript_generation"] = generation
	}
	if strings.TrimSpace(job.InputPayload) == "" {
		updates["input_payload"] = inputPayload
	}
	if len(updates) == 0 {
		return nil
	}
	return db.WithContext(ctx).Model(&model.VideoProcessingJob{}).Where("id = ?", jobID).Updates(updates).Error
}

func (o *Orchestrator) IsSummaryUserEditProtected(ctx context.Context, videoID string) (bool, error) {
	var video model.Video
	if err := o.DB.WithContext(ctx).First(&video, "id = ?", videoID).Error; err != nil {
		return false, fmt.Errorf("load summary state: %w", err)
	}
	if video.SummaryUserEdited {
		return true, nil
	}
	if video.SummaryWikiPageID == "" {
		return false, nil
	}
	_, editSource, _, err := o.findWikiPageVersion(ctx, videoID, video.SummaryWikiPageID)
	if err != nil {
		return false, err
	}
	return editSource == "user" || editSource == "revert", nil
}

func (o *Orchestrator) enqueueJob(ctx context.Context, db *gorm.DB, videoID, jobType string) (string, error) {
	var video model.Video
	if err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "transcript_generation").First(&video, "id = ?", videoID).Error; err != nil {
		return "", fmt.Errorf("load video for %s: %w", jobType, err)
	}
	generation := strings.TrimSpace(video.TranscriptGeneration)
	idemKey := IdempotencyKey(videoID, jobType)
	if generation != "" {
		idemKey += ":" + generation
	}

	// Match the logical job identity as well as the idempotency key. This
	// repairs legacy rows created before transcript generations were included
	// in the key and prevents a repeated activation event from creating a
	// second effective final attempt with a different key.
	var existing model.VideoProcessingJob
	query := db.WithContext(ctx).
		Where("video_id = ? AND job_type = ?", videoID, jobType).
		Where("result_stage = ? OR result_stage = '' OR result_stage IS NULL", "final")
	if generation == "" {
		query = query.Where("COALESCE(transcript_generation, '') = ''")
	} else {
		// An active unbound attempt is a legacy representation of this same
		// generation. Only pending/running rows may be rebound; a terminal
		// unbound result must not be attached to a newer transcript.
		query = query.Where("transcript_generation = ? OR (COALESCE(transcript_generation, '') = '' AND status IN ?)", generation, []string{"pending", "running"})
	}
	if err := query.Order("CASE status WHEN 'running' THEN 0 WHEN 'pending' THEN 1 WHEN 'succeeded' THEN 2 WHEN 'failed' THEN 3 ELSE 4 END, updated_at DESC, created_at DESC").First(&existing).Error; err == nil {
		if existing.TranscriptGeneration == "" && generation != "" {
			if err := db.WithContext(ctx).Model(&existing).Updates(map[string]any{
				"transcript_generation": generation,
				"result_stage":          "final",
			}).Error; err != nil {
				return "", fmt.Errorf("bind %s job to transcript generation: %w", jobType, err)
			}
			existing.TranscriptGeneration = generation
			existing.ResultStage = "final"
		}
		if existing.Status == "succeeded" || existing.Status == "running" || existing.Status == "pending" {
			return existing.ID, nil
		}
		// 已有任务直接复用；失败/取消任务恢复原记录，避免唯一键冲突。
		if err := db.WithContext(ctx).Model(&existing).Updates(map[string]any{
			"status": "pending", "attempt_count": 0, "error_category": "", "error_code": "", "error_message": "",
			"started_at": nil, "completed_at": nil, "updated_at": time.Now().UTC(),
		}).Error; err != nil {
			return "", fmt.Errorf("reset %s job: %w", jobType, err)
		}
		return existing.ID, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("find existing %s job: %w", jobType, err)
	}
	job := model.VideoProcessingJob{
		ID:                   uuid.NewString(),
		VideoID:              videoID,
		JobType:              jobType,
		TranscriptGeneration: generation,
		Provider:             providerForJob(jobType),
		ResultStage:          "final",
		Status:               "pending",
		MaxAttempts:          3,
		IdempotencyKey:       idemKey,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
	result := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "idempotency_key"}},
		DoNothing: true,
	}).Create(&job)
	if result.Error != nil {
		return "", fmt.Errorf("enqueue %s job: %w", jobType, result.Error)
	}
	if result.RowsAffected == 0 {
		if err := db.WithContext(ctx).Where("idempotency_key = ?", idemKey).First(&existing).Error; err == nil {
			return existing.ID, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("load concurrent %s job: %w", jobType, err)
		}
		// A concurrent caller may have rebound a legacy unbound row without
		// using the new idempotency key. Re-run the logical lookup once before
		// reporting a conflict.
		if err := query.First(&existing).Error; err != nil {
			return "", fmt.Errorf("load concurrent %s job: %w", jobType, err)
		}
		return existing.ID, nil
	}
	return job.ID, nil
}

func providerForJob(jobType string) string {
	switch jobType {
	case JobOutline, JobSummary, JobSummaryEnhance:
		return "llm"
	default:
		return "weknora"
	}
}

// FindWikiPage 在该视频的 Wiki 页中找匹配 job 契约的产物页；
// 返回 page_id（空=未找到）+ 候选页总数 + 查询错误
func (o *Orchestrator) FindWikiPage(ctx context.Context, videoID, jobType string) (string, int, error) {
	return o.FindWikiPageAfter(ctx, videoID, jobType, WikiPageBaseline{})
}

func (o *Orchestrator) SnapshotWikiPageVersions(ctx context.Context, videoID string) (WikiPageVersionSnapshot, error) {
	pages, err := o.Wiki.ListByVideo(ctx, o.KBID, videoID, "")
	if err != nil {
		return nil, fmt.Errorf("list existing wiki pages: %w", err)
	}
	snapshot := make(WikiPageVersionSnapshot, len(pages))
	for _, page := range pages {
		snapshot[page.ID] = page.Version
	}
	return snapshot, nil
}

func (o *Orchestrator) FindWikiPageAfter(
	ctx context.Context,
	videoID, jobType string,
	baseline WikiPageBaseline,
) (string, int, error) {
	contract, ok := Contract(jobType)
	if !ok {
		return "", 0, fmt.Errorf("unknown skill job type: %s", jobType)
	}
	// 传空查全部页面，再按 types.go 中的 job 契约过滤。
	pages, lerr := o.Wiki.ListByVideo(ctx, o.KBID, videoID, "")
	if lerr != nil {
		return "", 0, fmt.Errorf("list wiki pages: %w", lerr)
	}
	for i, p := range pages {
		ft, _ := p.ParsedFrontmatter()["type"].(string)
		slog.Info("wiki page candidate",
			"video_id", videoID, "index", i, "page_id", p.ID,
			"slug", p.Slug, "frontmatter_type", ft, "page_type", p.PageType)

		if !contract.MatchesPageType(p.PageType) {
			continue
		}

		if ft != contract.ArtifactType || !contract.MatchesSlug(p.Slug, videoID) {
			continue
		}
		current, err := o.isWikiPageCurrentGeneration(ctx, videoID, p)
		if err != nil {
			return "", len(pages), err
		}
		if !current {
			continue
		}
		readable, err := o.isWikiPageEligible(ctx, p, baseline)
		if err != nil {
			return "", len(pages), err
		}
		if readable {
			return p.ID, len(pages), nil
		}
	}

	return "", len(pages), nil
}

func (o *Orchestrator) isWikiPageCurrentGeneration(ctx context.Context, videoID string, candidate weknora.WikiPage) (bool, error) {
	if o.DB == nil {
		return true, nil
	}
	var video model.Video
	if err := o.DB.WithContext(ctx).Select("transcript_generation").First(&video, "id = ?", videoID).Error; err != nil {
		return false, fmt.Errorf("load transcript generation for wiki page: %w", err)
	}
	expected := strings.TrimSpace(video.TranscriptGeneration)
	if expected == "" {
		return false, nil
	}
	actual, _ := candidate.ParsedFrontmatter()["transcript_generation"].(string)
	return strings.TrimSpace(actual) == expected, nil
}

func (o *Orchestrator) validateWikiPageSource(ctx context.Context, videoID, pageID string) error {
	if o.DB == nil || o.Wiki == nil {
		return nil
	}
	pages, err := o.Wiki.ListByVideo(ctx, o.KBID, videoID, "")
	if err != nil {
		return fmt.Errorf("list wiki page source: %w", err)
	}
	for _, page := range pages {
		if page.ID != pageID {
			continue
		}
		frontmatter := page.ParsedFrontmatter()
		sourceVideoID, _ := frontmatter["source_video_id"].(string)
		if sourceVideoID != videoID {
			return fmt.Errorf("wiki page %s source_video_id mismatch", pageID)
		}
		current, err := o.isWikiPageCurrentGeneration(ctx, videoID, page)
		if err != nil {
			return err
		}
		if !current {
			return fmt.Errorf("wiki page %s transcript generation mismatch", pageID)
		}
		return nil
	}
	return fmt.Errorf("wiki page %s does not belong to video %s", pageID, videoID)
}

// validateGraphWikiPage enforces the P3 index contract at the write-back
// boundary. Worker-side discovery is intentionally not the only guard because
// other callers can invoke AfterSkillCompleteWithID directly.
func (o *Orchestrator) validateGraphWikiPage(ctx context.Context, videoID, pageID string) error {
	if o.DB == nil || o.Wiki == nil {
		return nil
	}
	page, err := o.Wiki.GetPageByID(ctx, o.KBID, pageID)
	if err != nil {
		return fmt.Errorf("read graph wiki page: %w", err)
	}
	if page == nil || strings.TrimSpace(page.Content) == "" {
		return fmt.Errorf("wiki page %s is not readable", pageID)
	}
	if page.PageType != "index" {
		return fmt.Errorf("graph wiki page %s must use page_type=index", pageID)
	}
	if page.Slug != "video/"+strings.TrimSpace(videoID) {
		return fmt.Errorf("graph wiki page %s slug does not match video %s", pageID, videoID)
	}
	frontmatter := page.ParsedFrontmatter()
	if strings.ToLower(strings.TrimSpace(graphFrontmatterString(frontmatter, "type"))) != "knowledge_base" {
		return fmt.Errorf("graph wiki page %s must declare type=knowledge_base", pageID)
	}
	if strings.TrimSpace(graphFrontmatterString(frontmatter, "source_video_id")) != strings.TrimSpace(videoID) {
		return fmt.Errorf("graph wiki page %s source_video_id mismatch", pageID)
	}
	var video model.Video
	if err := o.DB.WithContext(ctx).Select("transcript_generation").First(&video, "id = ?", videoID).Error; err != nil {
		return fmt.Errorf("load transcript generation for graph wiki page: %w", err)
	}
	expectedGeneration := strings.TrimSpace(video.TranscriptGeneration)
	if expectedGeneration == "" || strings.TrimSpace(graphFrontmatterString(frontmatter, "transcript_generation")) != expectedGeneration {
		return fmt.Errorf("graph wiki page %s transcript generation mismatch", pageID)
	}
	auditStatus := strings.ToLower(strings.TrimSpace(graphFrontmatterString(frontmatter, "audit_status")))
	if auditStatus != "aligned" && auditStatus != "passed" {
		return fmt.Errorf("graph wiki page %s audit_status must be aligned or passed", pageID)
	}
	return nil
}

func graphFrontmatterString(frontmatter map[string]any, key string) string {
	value, _ := frontmatter[key].(string)
	return value
}

func (o *Orchestrator) isWikiPageEligible(
	ctx context.Context,
	candidate weknora.WikiPage,
	baseline WikiPageBaseline,
) (bool, error) {
	if previousVersion, existed := baseline.Versions[candidate.ID]; existed && candidate.Version <= previousVersion {
		if baseline.JobCreatedAt.IsZero() || candidate.UpdatedAt.Before(baseline.JobCreatedAt) {
			return false, nil
		}
	}
	return o.isWikiPageReadable(ctx, candidate)
}

func (o *Orchestrator) isWikiPageReadable(ctx context.Context, candidate weknora.WikiPage) (bool, error) {
	page, err := o.Wiki.GetPage(ctx, o.KBID, candidate.Slug)
	if err != nil {
		return false, fmt.Errorf("read wiki page %s: %w", candidate.ID, err)
	}
	return page != nil && page.ID == candidate.ID && strings.TrimSpace(page.Content) != "", nil
}

// AfterSkillComplete skill 完成后：找新 wiki 页 → 回写 videos。
//
//   - expectedFrontmatterType: 例如 "knowledge_base" / "outline" 等
//
// 基础内容全部完成后，才会返回待组装的 job_id。
func (o *Orchestrator) AfterSkillComplete(ctx context.Context, videoID, jobType string) (wikiPageID string, nextJobID string, err error) {
	contract, ok := Contract(jobType)
	if !ok {
		return "", "", fmt.Errorf("unknown skill job type: %s", jobType)
	}

	// 本地 3 次重试（间隔 3s），双重防护 Wiki 写入延迟
	const (
		maxRetries = 3
		retryWait  = 3 * time.Second
	)
	var pageCount int
	for attempt := 1; attempt <= maxRetries; attempt++ {
		var perr error
		wikiPageID, pageCount, perr = o.FindWikiPage(ctx, videoID, jobType)
		if perr != nil {
			slog.Warn("AfterSkillComplete FindWikiPage error",
				"video_id", videoID, "job_type", jobType, "attempt", attempt, "error", perr)
		} else if wikiPageID != "" {
			break
		} else {
			slog.Warn("AfterSkillComplete wiki page not found yet",
				"video_id", videoID, "job_type", jobType, "attempt", attempt,
				"expected_type", contract.ArtifactType, "page_count", pageCount)
		}
		if attempt < maxRetries {
			time.Sleep(retryWait)
		}
	}
	if wikiPageID == "" {
		slog.Error("wiki page not found for job",
			"video_id", videoID, "job_type", jobType,
			"expected_type", contract.ArtifactType, "page_count", pageCount)
		err = fmt.Errorf("未找到 job=%s 的 wiki 页（type=%s，page_count=%d）", jobType, contract.ArtifactType, pageCount)
		return
	}
	return o.AfterSkillCompleteWithID(ctx, videoID, jobType, wikiPageID)
}

// AfterSkillCompleteWithID 跳过 FindWikiPage 直接用给定 pageID 执行回写 + 触发下一环节。
// 用于 Worker 已确认产物页，或外部已知 page_id 的场景。
func (o *Orchestrator) AfterSkillCompleteWithID(ctx context.Context, videoID, jobType, wikiPageID string) (string, string, error) {
	return o.afterSkillCompleteWithID(ctx, videoID, jobType, wikiPageID, false)
}

func (o *Orchestrator) AfterExplicitSummaryRegeneration(ctx context.Context, videoID, jobType, wikiPageID string) (string, string, error) {
	if jobType != JobSummary && jobType != JobSummaryEnhance {
		return "", "", fmt.Errorf("explicit summary regeneration does not support job_type %s", jobType)
	}
	return o.afterSkillCompleteWithID(ctx, videoID, jobType, wikiPageID, true)
}

func (o *Orchestrator) afterSkillCompleteWithID(ctx context.Context, videoID, jobType, wikiPageID string, allowSummaryOverwrite bool) (string, string, error) {
	if wikiPageID == "" {
		return "", "", fmt.Errorf("AfterSkillCompleteWithID: page_id is empty (job=%s, video=%s)", jobType, videoID)
	}
	slog.Info("after skill complete (with page ID)",
		"video_id", videoID, "job_type", jobType, "page_id", wikiPageID)

	contract, ok := Contract(jobType)
	if !ok || contract.VideoField == "" {
		return "", "", fmt.Errorf("job_type %s 无映射字段", jobType)
	}
	if jobType == JobGraph {
		if err := o.validateGraphWikiPage(ctx, videoID, wikiPageID); err != nil {
			return "", "", err
		}
	} else if err := o.validateWikiPageSource(ctx, videoID, wikiPageID); err != nil {
		return "", "", err
	}
	candidateVersion, _, auditStatus, err := o.findWikiPageVersion(ctx, videoID, wikiPageID)
	if err != nil {
		return "", "", err
	}
	if (jobType == JobSummary || jobType == JobSummaryEnhance) && !allowSummaryOverwrite {
		var current model.Video
		if err := o.DB.WithContext(ctx).First(&current, "id = ?", videoID).Error; err != nil {
			return "", "", fmt.Errorf("load summary state: %w", err)
		}
		currentSummaryVersion, currentSummaryEditSource, _, err := o.findWikiPageVersion(ctx, videoID, current.SummaryWikiPageID)
		if err != nil {
			return "", "", err
		}
		legacyEditDetected := currentSummaryEditSource == "" && current.SummaryWikiPageVersion > 0 && currentSummaryVersion > current.SummaryWikiPageVersion
		if current.SummaryUserEdited || currentSummaryEditSource == "user" || currentSummaryEditSource == "revert" || legacyEditDetected {
			if err := o.DB.WithContext(ctx).Model(&model.Video{}).Where("id = ?", videoID).Updates(map[string]any{
				"summary_user_edited": true, "summary_source": "user_edited",
				"processing_error_summary": "智能总结存在用户编辑，已跳过自动覆盖",
			}).Error; err != nil {
				return "", "", fmt.Errorf("persist summary edit protection: %w", err)
			}
			return wikiPageID, "", ErrSummaryUserEditProtected
		}
	}

	var nextJobID string
	err = o.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var video model.Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&video, "id = ?", videoID).Error; err != nil {
			return fmt.Errorf("load video before artifact update: %w", err)
		}
		result := tx.Model(&model.Video{}).
			Where("id = ?", videoID).
			Update(contract.VideoField, wikiPageID)
		if result.Error != nil {
			return fmt.Errorf("update %s: %w", contract.VideoField, result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("video not found: %s", videoID)
		}

		if jobType == JobSummary || jobType == JobSummaryEnhance {
			updates := map[string]any{"summary_source": "initial", "summary_knowledge_enhanced": false}
			if allowSummaryOverwrite {
				updates["summary_user_edited"] = false
			}
			if jobType == JobSummaryEnhance {
				updates["summary_source"] = "enhanced"
				updates["summary_knowledge_enhanced"] = true
			}
			if candidateVersion > 0 {
				updates["summary_wiki_page_version"] = candidateVersion
			}
			if err := tx.Model(&model.Video{}).Where("id = ?", videoID).Updates(updates).Error; err != nil {
				return fmt.Errorf("persist summary metadata: %w", err)
			}
		}
		if jobType == JobGraph {
			mappedAuditStatus := mapAuditStatus(auditStatus)
			if err := tx.Model(&model.Video{}).Where("id = ?", videoID).Update("knowledge_audit_status", mappedAuditStatus).Error; err != nil {
				return fmt.Errorf("persist knowledge audit status: %w", err)
			}
		}
		if jobType == JobOutline || jobType == JobSummary {
			var updated model.Video
			if err := tx.First(&updated, "id = ?", videoID).Error; err != nil {
				return fmt.Errorf("reload foundation artifacts: %w", err)
			}
			if updated.OutlineWikiPageID != "" && updated.SummaryWikiPageID != "" {
				var err error
				nextJobID, err = o.enqueueJob(ctx, tx, videoID, JobAssemble)
				if err != nil {
					return err
				}
				generation, inputPayload, err := o.transcriptSourceManifest(ctx, tx, videoID)
				if err != nil {
					return err
				}
				return o.ensureTranscriptSourceManifest(ctx, tx, nextJobID, generation, inputPayload)
			}
		}
		if jobType == JobGraph || jobType == JobSummary {
			var updated model.Video
			if err := tx.First(&updated, "id = ?", videoID).Error; err != nil {
				return fmt.Errorf("reload enhancement prerequisites: %w", err)
			}
			if updated.KnowledgeAuditStatus == "passed" && updated.SummaryWikiPageID != "" {
				nextJobID, err = o.enqueueJob(ctx, tx, videoID, JobSummaryEnhance)
				if err != nil {
					return err
				}
				generation, inputPayload, err := o.transcriptSourceManifest(ctx, tx, videoID)
				if err != nil {
					return err
				}
				return o.ensureTranscriptSourceManifest(ctx, tx, nextJobID, generation, inputPayload)
			}
		}
		if jobType == JobAssemble {
			if err := tx.First(&video, "id = ?", videoID).Error; err != nil {
				return fmt.Errorf("reload assembled video: %w", err)
			}
			if video.OutlineWikiPageID == "" || video.SummaryWikiPageID == "" || video.TranscriptPageWikiPageID == "" {
				return fmt.Errorf("incomplete content artifacts after assemble")
			}
			if err := tx.Model(&video).Updates(map[string]any{
				"status": model.VideoStatusCompleted, "processing_error_summary": "",
			}).Error; err != nil {
				return fmt.Errorf("mark video completed: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return wikiPageID, nextJobID, nil
}

func (o *Orchestrator) findWikiPageVersion(ctx context.Context, videoID, pageID string) (int, string, string, error) {
	if pageID == "" || o.Wiki == nil {
		return 0, "", "", nil
	}
	pages, err := o.Wiki.ListByVideo(ctx, o.KBID, videoID, "")
	if err != nil {
		return 0, "", "", fmt.Errorf("list wiki pages for version tracking: %w", err)
	}
	for _, page := range pages {
		if page.ID == pageID {
			auditStatus, _ := page.ParsedFrontmatter()["audit_status"].(string)
			return page.Version, page.LastEditSource, auditStatus, nil
		}
	}
	return 0, "", "", nil
}

func mapAuditStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "aligned", "passed":
		return "passed"
	case "conditional":
		return "conditional"
	case "failed":
		return "failed"
	default:
		return "conditional"
	}
}
