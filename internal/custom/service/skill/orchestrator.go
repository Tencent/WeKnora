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

// NewOrchestrator 构造
func NewOrchestrator(db *gorm.DB, wiki *weknora.WikiClient, kbID string) *Orchestrator {
	return &Orchestrator{DB: db, Wiki: wiki, KBID: kbID}
}

// NextJob 在JobType 链中返回下一个 job_type；空表示已是末位
func NextJob(currentJobType string) string {
	for i, t := range ChainOrder {
		if t == currentJobType {
			if i+1 < len(ChainOrder) {
				return ChainOrder[i+1]
			}
			return ""
		}
	}
	return ""
}

// EnqueueJob 入库一个 pending job（CP-T004 幂等键保证）
func (o *Orchestrator) EnqueueJob(ctx context.Context, videoID, jobType string) (string, error) {
	idemKey := IdempotencyKey(videoID, jobType)
	var video model.Video
	if err := o.DB.Select("transcript_generation").First(&video, "id = ?", videoID).Error; err == nil && video.TranscriptGeneration != "" {
		idemKey += ":" + video.TranscriptGeneration
	}
	var existing model.VideoProcessingJob
	if err := o.DB.Where("idempotency_key = ?", idemKey).First(&existing).Error; err == nil {
		// 已有任务直接复用；失败任务恢复原记录，避免唯一键冲突。
		if existing.Status == "succeeded" || existing.Status == "running" || existing.Status == "pending" {
			return existing.ID, nil
		}
		if err := o.DB.Model(&existing).Updates(map[string]any{
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
	if err := o.DB.Create(&job).Error; err != nil {
		return "", fmt.Errorf("enqueue %s job: %w", jobType, err)
	}
	return job.ID, nil
}

// FindWikiPage 在该视频的 Wiki 页中找匹配 expectedType 的产物页；
// 返回 page_id（空=未找到）+ 候选页总数 + 查询错误
//
// 匹配优先级（按 SKILL.md 契约与实际 LLM 输出偏差逐层兜底）：
//   1) frontmatter.type == expectedType  （契约首选）
//   2) slug 前缀 == expectedType（如 typed_summary → typed-summary/）
//   3) 别名兜底：
//        - knowledge_base (graph) → page_type=index 且 slug=video/{video_id}
//          （SKILL.md 规定 graph 索引页 page_type=index frontmatter.type=knowledge_base，
//          但 LLM 常漏写 frontmatter.type，直接查 slug/video 兜底最稳）
//        - typed_summary / summary → page_type=synthesis
//          （WeKnora 保留字 "summary"，wiki_write_page 已把 page_type 改写为 synthesis，
//          但 frontmatter.type 仍应等于 typed_summary/summary；二者同时命中）
func (o *Orchestrator) FindWikiPage(ctx context.Context, videoID, expectedType string) (string, int, error) {
	// 不写死 page_type=index：不同 skill 产物页 page_type 不同
	// （index/knowledge_base/synthesis/...），传空查全部后在自研侧过滤
	pages, lerr := o.Wiki.ListByVideo(ctx, o.KBID, videoID, "")
	if lerr != nil {
		return "", 0, fmt.Errorf("list wiki pages: %w", lerr)
	}
	slugPrefix := strings.ReplaceAll(expectedType, "_", "-")
	// transcript_page 的实际 slug 前缀是 transcript/（非 transcript-page/），
	// 去掉 -page 后缀作为备选前缀
	slugAltPrefix := strings.TrimSuffix(slugPrefix, "-page")

	// 别名匹配表（expectedType → 可接受的 page_type 集合）
	// 用于 LLM 漏写 frontmatter.type 时的兜底
	aliasPageTypes := map[string]map[string]struct{}{}
	switch expectedType {
	case "knowledge_base":
		// graph 索引页：SKILL.md 规定 page_type=index frontmatter.type=knowledge_base
		aliasPageTypes[expectedType] = map[string]struct{}{
			"index":     {},
			"synthesis": {}, // 兼容保留字兜底（万一 summary/knowledge_base 也兜底成 synthesis 写入）
		}
	case "summary", "typed_summary":
		// typed_summary / summary：WeKnora 保留字 summary 会被兜底成 synthesis
		aliasPageTypes[expectedType] = map[string]struct{}{
			"synthesis": {},
		}
	case "transcript_page":
		aliasPageTypes[expectedType] = map[string]struct{}{
			"synthesis": {}, // 组装页如被保留字拦截也兜底
		}
	}
	aliasPT, hasAlias := aliasPageTypes[expectedType]

	var firstIndexPageID string // knowledge_base 兜底：找不到 video/{id} 时用"该视频关联的任一 index 页"

	for i, p := range pages {
		ft, _ := p.ParsedFrontmatter()["type"].(string)
		slog.Info("wiki page candidate",
			"video_id", videoID, "index", i, "page_id", p.ID,
			"slug", p.Slug, "frontmatter_type", ft, "page_type", p.PageType)

		// 记录遇到的第一个 page_type=index 页，供 knowledge_base 场景兜底
		if expectedType == "knowledge_base" && firstIndexPageID == "" && (p.PageType == "index" || p.PageType == "synthesis") {
			firstIndexPageID = p.ID
		}

		// 1) 契约命中：frontmatter.type 精确匹配
		if ft == expectedType {
			return p.ID, len(pages), nil
		}

		// 2) slug 前缀命中
		if strings.HasPrefix(p.Slug, slugPrefix+"/") ||
			(slugAltPrefix != slugPrefix && strings.HasPrefix(p.Slug, slugAltPrefix+"/")) {
			return p.ID, len(pages), nil
		}

		// 3) 别名 page_type 兜底
		if hasAlias {
			if _, ok := aliasPT[p.PageType]; ok {
				// knowledge_base 特殊：优先匹配"该视频的"索引页（slug 必须是 video/{video_id}）
				// 防止一个视频有多个 index 类型的页（如分类页）时误命中
				// 找不到 video/{id} 精确 slug 时，退化为该视频关联的任意 index/synthesis 页
				if expectedType == "knowledge_base" {
					videoSlug := "video/" + videoID
					if p.Slug == videoSlug || strings.HasPrefix(p.Slug, videoSlug) {
						return p.ID, len(pages), nil
					}
					continue // 暂不返回，等 firstIndexPageID 记录完循环后统一兜底
				}
				return p.ID, len(pages), nil
			}
		}
	}

	// 4) knowledge_base 终极兜底：该视频关联的任一 index/synthesis 页（LLM 没按约定写 slug=video/{id} 时）
	if expectedType == "knowledge_base" && firstIndexPageID != "" {
		slog.Warn("FindWikiPage knowledge_base fall back to first index/synthesis page of video",
			"video_id", videoID, "page_id", firstIndexPageID)
		return firstIndexPageID, len(pages), nil
	}

	// 5) knowledge_base 双保险兜底：直接复用 index job 已写入的 transcript_knowledge_id
	//    （index job 会把视频分块索引页 page_id 写回 videos.transcript_knowledge_id，
	//     该页 page_type=index、slug=video/{id}，与 SKILL.md graph 索引页契约一致。
	//     当 WeKnora skill 端漏写/重写失败、ListByVideo 过滤缺漏时，这里是最后一道锚点）
	if expectedType == "knowledge_base" {
		var id string
		if err := o.DB.Raw(
			`SELECT transcript_knowledge_id FROM videos WHERE id = ? AND deleted_at IS NULL`,
			videoID).Scan(&id).Error; err == nil && id != "" {
			slog.Warn("FindWikiPage knowledge_base anchored by videos.transcript_knowledge_id",
				"video_id", videoID, "page_id", id)
			return id, len(pages), nil
		}
	}

	return "", len(pages), nil
}

// AfterSkillComplete skill 完成后：找新 wiki 页 → 回写 videos → 触发下一环节
//
//   - expectedFrontmatterType: 例如 "knowledge_base" / "outline" / "overview" 等
//
// 返回回写是否成功 + 下一个 job_id（如果有）
func (o *Orchestrator) AfterSkillComplete(ctx context.Context, videoID, jobType string) (wikiPageID string, nextJobID string, err error) {
	expectedType := JobSkillType[jobType]

	// 本地 3 次重试（间隔 3s），双重防护 Wiki 写入延迟
	const (
		maxRetries = 3
		retryWait  = 3 * time.Second
	)
	var pageCount int
	for attempt := 1; attempt <= maxRetries; attempt++ {
		var perr error
		wikiPageID, pageCount, perr = o.FindWikiPage(ctx, videoID, expectedType)
		if perr != nil {
			slog.Warn("AfterSkillComplete FindWikiPage error",
				"video_id", videoID, "job_type", jobType, "attempt", attempt, "error", perr)
		} else if wikiPageID != "" {
			break
		} else {
			slog.Warn("AfterSkillComplete wiki page not found yet",
				"video_id", videoID, "job_type", jobType, "attempt", attempt,
				"expected_type", expectedType, "page_count", pageCount)
		}
		if attempt < maxRetries {
			time.Sleep(retryWait)
		}
	}
	if wikiPageID == "" {
		slog.Error("wiki page not found for job",
			"video_id", videoID, "job_type", jobType,
			"expected_type", expectedType, "page_count", pageCount)
		err = fmt.Errorf("未找到 job=%s 的 wiki 页（type=%s，page_count=%d）", jobType, expectedType, pageCount)
		return
	}
	return o.AfterSkillCompleteWithID(ctx, videoID, jobType, wikiPageID)
}

// AfterSkillCompleteWithID 跳过 FindWikiPage 直接用给定 pageID 执行回写 + 触发下一环节
// （graph 兜底锚定 / 外部已知 page_id 场景使用）
func (o *Orchestrator) AfterSkillCompleteWithID(ctx context.Context, videoID, jobType, wikiPageID string) (string, string, error) {
	if wikiPageID == "" {
		return "", "", fmt.Errorf("AfterSkillCompleteWithID: page_id is empty (job=%s, video=%s)", jobType, videoID)
	}
	slog.Info("after skill complete (with page ID)",
		"video_id", videoID, "job_type", jobType, "page_id", wikiPageID)

	// 回写 videos 表（CP-T006）
	field := VideoField[jobType]
	if field == "" {
		return "", "", fmt.Errorf("job_type %s 无映射字段", jobType)
	}
	if uerr := o.DB.Model(&model.Video{}).
		Where("id = ?", videoID).
		Update(field, wikiPageID).Error; uerr != nil {
		return "", "", fmt.Errorf("update %s: %w", field, uerr)
	}

	// 触发下一环节（CP-T005）
	var nextJobID string
	var err error
	if next := NextJob(jobType); next != "" {
		nextJobID, err = o.EnqueueJob(ctx, videoID, next)
	}
	return wikiPageID, nextJobID, err
}
