// Package worker 内容生产 5 个 skill job handler（CP-T005）。
//
// 5 个 handler 共享 BaseSkillHandler 的逻辑：
//  1. 调 Agent Chat API 触发对应 skill
//  2. 等 skill 完成
//  3. 调 orchestrator.AfterSkillComplete：回写 wiki_page_id + 触发下一环节
package worker

import (
	"context"
	"encoding/json"
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

const wikiBaselinePayloadKey = "wiki_page_versions_before_skill"

func skillQuery(video *model.Video, skillName string) string {
	return fmt.Sprintf(
		"使用 $%s 处理视频。源文档知识 ID：%s。业务视频 ID：%s 仅用于产物归属。视频标题：%s。",
		skillName, video.TranscriptKnowledgeID, video.ID, video.Title,
	)
}

func (h *BaseSkillHandler) wikiBaseline(
	ctx context.Context,
	job *model.VideoProcessingJob,
	videoID string,
) (skill.WikiPageBaseline, error) {
	payload := make(map[string]json.RawMessage)
	if job.InputPayload != "" {
		if err := json.Unmarshal([]byte(job.InputPayload), &payload); err != nil {
			return skill.WikiPageBaseline{}, fmt.Errorf("decode skill job input: %w", err)
		}
	}
	if raw, ok := payload[wikiBaselinePayloadKey]; ok {
		var baseline skill.WikiPageBaseline
		if err := json.Unmarshal(raw, &baseline); err != nil {
			return skill.WikiPageBaseline{}, fmt.Errorf("decode wiki baseline: %w", err)
		}
		return baseline, nil
	}

	versions, err := h.Orchestrator.SnapshotWikiPageVersions(ctx, videoID)
	if err != nil {
		return skill.WikiPageBaseline{}, err
	}
	baseline := skill.WikiPageBaseline{Versions: versions, JobCreatedAt: job.CreatedAt}
	rawBaseline, err := json.Marshal(baseline)
	if err != nil {
		return skill.WikiPageBaseline{}, fmt.Errorf("encode wiki baseline: %w", err)
	}
	payload[wikiBaselinePayloadKey] = rawBaseline
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return skill.WikiPageBaseline{}, fmt.Errorf("encode skill job input: %w", err)
	}
	result := h.DB.WithContext(ctx).Model(&model.VideoProcessingJob{}).
		Where("id = ?", job.ID).
		Update("input_payload", string(rawPayload))
	if result.Error != nil {
		return skill.WikiPageBaseline{}, fmt.Errorf("persist wiki baseline: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return skill.WikiPageBaseline{}, fmt.Errorf("persist wiki baseline: job not found: %s", job.ID)
	}
	job.InputPayload = string(rawPayload)
	return baseline, nil
}

// run 通用 skill 执行流程
func (h *BaseSkillHandler) run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video, jobType string) error {
	contract, ok := skill.Contract(jobType)
	if !ok {
		return fmt.Errorf("未注册的 job_type: %s", jobType)
	}
	if video.TranscriptKnowledgeID == "" {
		return fmt.Errorf("视频 %s 缺少转写知识文档 ID", video.ID)
	}
	baseline, err := h.wikiBaseline(ctx, job, video.ID)
	if err != nil {
		return fmt.Errorf("snapshot wiki pages before %s: %w", contract.SkillName, err)
	}

	// 创建 session 并触发 skill
	sessionID, err := h.AgentClient.CreateSession(ctx, fmt.Sprintf("content-pipeline/%s/%s", video.ID, jobType))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	query := skillQuery(video, contract.SkillName)
	if err := h.AgentClient.TriggerSkill(ctx, sessionID, h.AgentID, contract.SkillName, query); err != nil {
		return fmt.Errorf("trigger skill %s: %w", contract.SkillName, err)
	}

	// 轮询等待 Wiki 产物页落地（WeKnora 写入到可检索有延迟），最多 10 分钟
	wikiPageID, err := h.waitForWikiPage(ctx, video.ID, jobType, baseline, 10*time.Minute)
	if err != nil {
		return fmt.Errorf("等待 wiki 产物页超时（type=%s）: %w", contract.ArtifactType, err)
	}

	// 回写 wiki_page_id + 触发下一环节（CP-T006 + CP-T005）
	if _, _, oerr := h.Orchestrator.AfterSkillCompleteWithID(ctx, video.ID, jobType, wikiPageID); oerr != nil {
		return fmt.Errorf("after skill complete: %w", oerr)
	}
	_ = wikiPageID // 回写已在 AfterSkillComplete 中完成
	return nil
}

// waitForWikiPage 轮询等待匹配的 Wiki 产物页出现；避免 skill 返回后 DB/索引延迟导致的误判
func (h *BaseSkillHandler) waitForWikiPage(
	ctx context.Context,
	videoID, jobType string,
	baseline skill.WikiPageBaseline,
	timeout time.Duration,
) (string, error) {
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
		id, count, err := h.Orchestrator.FindWikiPageAfter(ctx, videoID, jobType, baseline)
		if err != nil {
			slog.Warn("waitForWikiPage FindWikiPage", "video_id", videoID, "job_type", jobType, "error", err)
			continue
		}
		lastCount = count
		if id != "" {
			slog.Info("waitForWikiPage found", "video_id", videoID, "job_type", jobType, "page_id", id)
			return id, nil
		}
	}
}

// GraphHandler extract-video-knowledge
type GraphHandler struct{ BaseSkillHandler }

func (h *GraphHandler) JobType() string { return skill.JobGraph }

// Run graph：只有确认 knowledge_base Wiki 产物可读后才推进下游。
//
// 流程：
//  1. 尝试调 extract-video-knowledge skill（Agent 对话模式）；
//     若成功但 1 分钟内没有检索到 knowledge_base 新产物，则任务失败
//  2. 回写 knowledge_base_wiki_page_id + 触发下一环节 outline
func (h *GraphHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	contract, _ := skill.Contract(skill.JobGraph)
	if video.TranscriptKnowledgeID == "" {
		return fmt.Errorf("视频 %s 缺少转写知识文档 ID", video.ID)
	}
	baseline, err := h.wikiBaseline(ctx, job, video.ID)
	if err != nil {
		return fmt.Errorf("snapshot wiki pages before %s: %w", contract.SkillName, err)
	}

	// --- 步骤 1：触发 skill，并短等 1 分钟确认产物可读 ---
	sessionID, err := h.AgentClient.CreateSession(ctx, fmt.Sprintf("content-pipeline/%s/graph", video.ID))
	if err != nil {
		return fmt.Errorf("graph create session: %w", err)
	}
	query := skillQuery(video, contract.SkillName)
	if err := h.AgentClient.TriggerSkill(ctx, sessionID, h.AgentID, contract.SkillName, query); err != nil {
		return fmt.Errorf("graph trigger skill %s: %w", contract.SkillName, err)
	}
	wikiPageID, err := h.waitForWikiPage(ctx, video.ID, skill.JobGraph, baseline, time.Minute)
	if err != nil {
		return fmt.Errorf("graph wait for readable knowledge_base wiki page: %w", err)
	}

	// --- 步骤 2：回写 + 推下一环节 ---
	_, _, err = h.Orchestrator.AfterSkillCompleteWithID(ctx, video.ID, skill.JobGraph, wikiPageID)
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
