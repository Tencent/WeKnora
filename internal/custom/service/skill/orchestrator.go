// Package skill orchestrator：skill 完成后回写 wiki_page_id + 触发下一环节（CP-T005 + CP-T006）。
//
// 设计要点：
//   - skill 完成后由各 worker handler 调 AfterSkillComplete
//   - AfterSkillComplete 找到该视频「新生成的」wiki 页（按 frontmatter.type 过滤），
//     回写 videos 表（CP-T006）
//   - 然后按 ChainOrder 触发下一个 job（CP-T005 串行）
//   - 最后一个 job（assemble）完成后不触发新 job
package skill

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

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

// NewOrchestrator 构造
func NewOrchestrator(db *gorm.DB, wiki *weknora.WikiClient, kbID string) *Orchestrator {
	return &Orchestrator{DB: db, Wiki: wiki, KBID: kbID}
}

// EnqueueJob 入库一个 pending job（CP-T004 幂等键保证）
func (o *Orchestrator) EnqueueJob(ctx context.Context, videoID, jobType string) (string, error) {
	return o.enqueueJob(ctx, o.DB, videoID, jobType)
}

func (o *Orchestrator) enqueueJob(ctx context.Context, db *gorm.DB, videoID, jobType string) (string, error) {
	idemKey := IdempotencyKey(videoID, jobType)
	var video model.Video
	if err := db.WithContext(ctx).Select("transcript_generation").First(&video, "id = ?", videoID).Error; err == nil && video.TranscriptGeneration != "" {
		idemKey += ":" + video.TranscriptGeneration
	}
	var existing model.VideoProcessingJob
	if err := db.WithContext(ctx).Where("idempotency_key = ?", idemKey).First(&existing).Error; err == nil {
		// 已有任务直接复用；失败任务恢复原记录，避免唯一键冲突。
		if existing.Status == "succeeded" || existing.Status == "running" || existing.Status == "pending" {
			return existing.ID, nil
		}
		if err := db.WithContext(ctx).Model(&existing).Updates(map[string]any{
			"status": "pending", "attempt_count": 0, "error_code": "", "error_message": "",
			"started_at": nil, "completed_at": nil, "updated_at": time.Now().UTC(),
		}).Error; err != nil {
			return "", fmt.Errorf("reset %s job: %w", jobType, err)
		}
		return existing.ID, nil
	}
	job := model.VideoProcessingJob{
		ID:             uuid.NewString(),
		VideoID:        videoID,
		JobType:        jobType,
		Provider:       "weknora",
		Status:         "pending",
		MaxAttempts:    3,
		IdempotencyKey: idemKey,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := db.WithContext(ctx).Create(&job).Error; err != nil {
		return "", fmt.Errorf("enqueue %s job: %w", jobType, err)
	}
	return job.ID, nil
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

		if ft != contract.ArtifactType && !contract.MatchesSlug(p.Slug, videoID) {
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

// AfterSkillComplete skill 完成后：找新 wiki 页 → 回写 videos → 触发下一环节
//
//   - expectedFrontmatterType: 例如 "knowledge_base" / "outline" / "overview" 等
//
// 返回回写是否成功 + 下一个 job_id（如果有）
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
	if wikiPageID == "" {
		return "", "", fmt.Errorf("AfterSkillCompleteWithID: page_id is empty (job=%s, video=%s)", jobType, videoID)
	}
	slog.Info("after skill complete (with page ID)",
		"video_id", videoID, "job_type", jobType, "page_id", wikiPageID)

	contract, ok := Contract(jobType)
	if !ok || contract.VideoField == "" {
		return "", "", fmt.Errorf("job_type %s 无映射字段", jobType)
	}

	var nextJobID string
	err := o.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Video{}).
			Where("id = ?", videoID).
			Update(contract.VideoField, wikiPageID)
		if result.Error != nil {
			return fmt.Errorf("update %s: %w", contract.VideoField, result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("video not found: %s", videoID)
		}

		if next := NextJob(jobType); next != "" {
			var err error
			nextJobID, err = o.enqueueJob(ctx, tx, videoID, next)
			return err
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return wikiPageID, nextJobID, nil
}
