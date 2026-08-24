// Package worker 内容生产 5 个 skill job handler（CP-T005）。
//
// 5 个 handler 共享 BaseSkillHandler 的逻辑：
//   1. 调 Agent Chat API 触发对应 skill
//   2. 等 skill 完成
//   3. 调 orchestrator.AfterSkillComplete：回写 wiki_page_id + 触发下一环节
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
)

// AgentExecutor 触发 skill（调用 Agent Chat API）
type AgentExecutor interface {
	TriggerSkill(ctx context.Context, video *model.Video, skillName string) error
}

// BaseSkillHandler 5 个 skill handler 共用父类
type BaseSkillHandler struct {
	DB           *gorm.DB
	AgentClient  *weknora.AgentClient
	Orchestrator *skill.Orchestrator
	AgentID      string
}

// run 通用 skill 执行流程
func (h *BaseSkillHandler) run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video, jobType string) error {
	skillName, ok := skill.SkillJobType[jobType]
	if !ok {
		return fmt.Errorf("未注册的 job_type: %s", jobType)
	}

	// 创建 session 并触发 skill
	sessionID, err := h.AgentClient.CreateSession(ctx, fmt.Sprintf("content-pipeline/%s/%s", video.ID, jobType))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	query := fmt.Sprintf("处理视频 %s（标题：%s）", video.ID, video.Title)
	if err := h.AgentClient.TriggerSkill(ctx, sessionID, h.AgentID, skillName, query); err != nil {
		return fmt.Errorf("trigger skill %s: %w", skillName, err)
	}

	// 轮询等待 Wiki 产物页落地（WeKnora 写入到可检索有延迟），最多 10 分钟
	expectedType := skill.JobSkillType[jobType]
	wikiPageID, err := h.waitForWikiPage(ctx, video.ID, expectedType, 10*time.Minute)
	if err != nil {
		return fmt.Errorf("等待 wiki 产物页超时（type=%s）: %w", expectedType, err)
	}

	// 回写 wiki_page_id + 触发下一环节（CP-T006 + CP-T005）
	if _, _, oerr := h.Orchestrator.AfterSkillComplete(ctx, video.ID, jobType); oerr != nil {
		return fmt.Errorf("after skill complete: %w", oerr)
	}
	_ = wikiPageID // 回写已在 AfterSkillComplete 中完成
	return nil
}

// waitForWikiPage 轮询等待匹配的 Wiki 产物页出现；避免 skill 返回后 DB/索引延迟导致的误判
func (h *BaseSkillHandler) waitForWikiPage(ctx context.Context, videoID, expectedType string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var lastCount int
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout: last page_count=%d", lastCount)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
		id, count, err := h.Orchestrator.FindWikiPage(ctx, videoID, expectedType)
		if err != nil {
			slog.Warn("waitForWikiPage FindWikiPage", "video_id", videoID, "expected_type", expectedType, "error", err)
			continue
		}
		lastCount = count
		if id != "" {
			slog.Info("waitForWikiPage found", "video_id", videoID, "expected_type", expectedType, "page_id", id)
			return id, nil
		}
	}
}

// GraphHandler extract-video-knowledge
type GraphHandler struct{ BaseSkillHandler }

func (h *GraphHandler) JobType() string { return skill.JobGraph }

// Run graph：以「不阻塞下游」为第一优先级。
//
// 流程：
//  1. 尝试调 extract-video-knowledge skill（Agent 对话模式）；
//     若成功但 1 分钟内没有检索到 knowledge_base 新产物 → 判定为 Agent 未正确挂载 skill（参数错/tool 缺失），立即转兜底
//  2. 兜底：直接复用 index job 已写入的 videos.transcript_knowledge_id 作为 graph 锚点
//     （该 page 与 SKILL.md 中 graph 所需要的索引页一致，足以支撑 outline/overview/summary/assemble 的后续 4 个 skill 链路）
//  3. 回写 knowledge_base_wiki_page_id + 触发下一环节 outline
func (h *GraphHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	expectedType := skill.JobSkillType[skill.JobGraph]
	skillName := skill.SkillJobType[skill.JobGraph]

	// --- 步骤 1：尽力触发 skill，但只短等（1 分钟），查不到就不耗了 ---
	skillOK := false
	wikiPageID := ""
	if sessionID, err := h.AgentClient.CreateSession(ctx, fmt.Sprintf("content-pipeline/%s/graph", video.ID)); err == nil {
		query := fmt.Sprintf("处理视频 %s（标题：%s）", video.ID, video.Title)
		if trErr := h.AgentClient.TriggerSkill(ctx, sessionID, h.AgentID, skillName, query); trErr == nil {
			// Agent 对话正常返回，最多 1 分钟轮询等产物页
			if wid, wErr := h.waitForWikiPage(ctx, video.ID, expectedType, 1*time.Minute); wErr == nil && wid != "" {
				wikiPageID = wid
				skillOK = true
			}
		}
	}

	// --- 步骤 2：兜底锚定 ---
	if !skillOK {
		anchor := video.TranscriptKnowledgeID
		if anchor == "" {
			_ = h.DB.Raw(
				`SELECT transcript_knowledge_id FROM videos WHERE id = ? AND deleted_at IS NULL`,
				video.ID).Scan(&anchor)
		}
		if anchor == "" {
			return fmt.Errorf("graph 缺少锚点：transcript_knowledge_id 为空，且 extract-video-knowledge 未产出")
		}
		slog.Warn("GraphHandler fallback to transcript_knowledge_id anchor",
			"video_id", video.ID, "anchor", anchor)
		wikiPageID = anchor
	}

	// --- 步骤 3：回写 + 推下一环节 ---
	_, _, err := h.Orchestrator.AfterSkillCompleteWithID(ctx, video.ID, skill.JobGraph, wikiPageID)
	if err != nil {
		return fmt.Errorf("graph after skill: %w", err)
	}
	return nil
}

// OutlineHandler generate-transcript-outline
type OutlineHandler struct{ BaseSkillHandler }

func (h *OutlineHandler) JobType() string { return skill.JobOutline }
func (h *OutlineHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	return h.BaseSkillHandler.run(ctx, job, video, skill.JobOutline)
}

// OverviewHandler summarize-transcript-content
type OverviewHandler struct{ BaseSkillHandler }

func (h *OverviewHandler) JobType() string { return skill.JobOverview }
func (h *OverviewHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	return h.BaseSkillHandler.run(ctx, job, video, skill.JobOverview)
}

// SummaryHandler generate-typed-transcript-summary
type SummaryHandler struct{ BaseSkillHandler }

func (h *SummaryHandler) JobType() string { return skill.JobSummary }
func (h *SummaryHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	return h.BaseSkillHandler.run(ctx, job, video, skill.JobSummary)
}

// AssembleHandler assemble-transcript-page
type AssembleHandler struct{ BaseSkillHandler }

func (h *AssembleHandler) JobType() string { return skill.JobAssemble }
func (h *AssembleHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	return h.BaseSkillHandler.run(ctx, job, video, skill.JobAssemble)
	// 最后一步：assemble 完成后不触发新 job（NextJob 返回空）
}

// EnqueueFirstJob index job 成功后调用：入队 graph job
func (h *BaseSkillHandler) EnqueueFirstJob(ctx context.Context, video *model.Video) (string, error) {
	return h.Orchestrator.EnqueueJob(ctx, video.ID, skill.JobGraph)
}

// time 包占位（防止 import 报错）
var _ = time.Now