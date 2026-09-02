// Package worker 内容生产 skill job handler。
//
// 知识提取和页面组装使用 Agent；基础内容由 direct_content.go 通过 LLM 生成。
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgegraph"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	transcriptservice "github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

// AgentExecutor 触发 skill（调用 Agent Chat API）
type AgentExecutor interface {
	TriggerSkill(ctx context.Context, video *model.Video, skillName string) error
}

// BaseSkillHandler 5 个 skill handler 共用父类
type BaseSkillHandler struct {
	DB           *gorm.DB
	AgentClient  *weknora.AgentClient
	SourceReader TranscriptSourceReader
	Orchestrator *skill.Orchestrator
	AgentID      string
}

const wikiBaselinePayloadKey = "wiki_page_versions_before_skill"

const (
	transcriptSourceKnowledgeIDKey  = "transcript_source_knowledge_id"
	transcriptInputModeKey          = "transcript_input_mode"
	TranscriptInputModeFullDocument = "full_document"
	TranscriptInputModeEvidence     = "evidence_chunks"
)

// TranscriptSourceReader is the minimal read surface needed to validate the
// independent full-video source before a Wiki skill is invoked.
type TranscriptSourceReader interface {
	GetKnowledge(context.Context, string) (weknora.ManualKnowledgeResult, error)
}

func skillQuery(video *model.Video, contract skill.JobContract, jobType string) string {
	return skillQueryWithInput(video, contract, jobType, video.TranscriptKnowledgeID, TranscriptInputModeEvidence)
}

func skillQueryWithInput(video *model.Video, contract skill.JobContract, jobType, sourceKnowledgeID, inputMode string) string {
	query := fmt.Sprintf(
		"使用 $%s 处理视频。当前转写代次：%s。",
		contract.SkillName, video.TranscriptGeneration,
	)
	if inputMode == TranscriptInputModeFullDocument {
		query += fmt.Sprintf("本次 Wiki 抽取输入模式：full_document。完整视频源文档知识 ID：%s；必须只读取该整篇源文档作为抽取输入，不得读取字幕分块知识 ID，也不得把字幕分块作为抽取上下文。", sourceKnowledgeID)
	} else {
		query += fmt.Sprintf("本次输入模式：evidence_chunks。兼容源文档知识 ID：%s；完整转写分块清单已通过调用上下文提供，必须覆盖全部分块。", sourceKnowledgeID)
	}
	query += fmt.Sprintf("业务视频 ID：%s 仅用于产物归属。视频标题：%s。", video.ID, video.Title)
	if jobType == skill.JobGraph {
		query += fmt.Sprintf(
			"必须完整遵循 extract-video-knowledge 的 references/type-frameworks.md、references/wiki-schema.md 和 references/audit-rules.md：每个实体和每个知识原子都要写入独立 Wiki 页面；所有 Skill 知识对象页面的 page_type 必须使用 WeKnora 支持的 index，五类业务类型必须写入 frontmatter.type，禁止把 case、methodology、insight 作为 page_type；方法论、案例、概念、洞察必须填充对应结构维度，实体必须填充对应关键信息维度，未涉及字段留空不得编造。每个知识对象页面的 source_refs 必须填入与 evidence_ids 相同的真实转写分块 ID，确保 Wiki 检索能够按当前视频和转写代次优先召回。关系必须分两阶段写入：先写独立对象页并读取确认真实 Wiki page ID，本轮新对象之间的 relations 首次必须留空；全部目标页面确认可读后，再覆盖更新对象页补齐结构化 relations 和正文双链，禁止猜测 target_wiki_page_id。最后写入视频索引页：slug 严格使用 %q；page_type 使用 index；frontmatter 必须含 type: %s、source_video_id: %s 和 transcript_generation: %s。索引页目标可能尚不存在，首次生成时不要先读取目标 slug；读取返回 not found 不是失败，请继续直接写入。读取或引用上游产物时，必须使用 Wiki 工具返回的实际 slug，禁止根据视频标题或页面标题猜测 slug；不得用示例、占位内容或 mock 数据代替真实 Wiki 产物。"+
				"图谱理解必须以连续语义窗口处理转写：先按章节或相邻分块组织上下文，再提取跨分块成立的实体、概念、案例、方法论和洞察五类知识；实体与关系仍必须绑定最小充分证据分块，禁止把单个分块的偶然关键词直接当作关系。",
			contract.WriteSlug(video.ID), contract.ArtifactType, video.ID, video.TranscriptGeneration,
		)
	} else {
		query += fmt.Sprintf(
			"必须按 Skill 约定通过创建/覆盖 Wiki 写入唯一产物页：slug 严格使用 %q，不得使用其他产物的 slug，也不得覆盖其他类型页面；page_type 使用 index；frontmatter 必须含 type: %s、source_video_id: %s 和 transcript_generation: %s。目标产物页可能尚不存在，首次生成时不要先读取目标 slug；读取返回 not found 不是失败，请继续直接写入。读取上游产物时，必须使用 Wiki 工具返回的实际 slug，禁止根据视频标题或页面标题猜测 slug；不得用示例、占位内容或 mock 数据代替真实 Wiki 产物。",
			contract.WriteSlug(video.ID), contract.ArtifactType, video.ID, video.TranscriptGeneration,
		)
	}
	if jobType == skill.JobSummaryEnhance {
		query += fmt.Sprintf(
			"这是知识增强阶段，不是重新生成基础总结。必须先阅读知识底座索引页 ID：%s 及其可审计关联，再以当前转写代次为事实边界增强已有类型化总结；不得引入无法回指当前转写证据的事实。仅允许覆盖 %q 产物页，保留原有模板结构和用户编辑内容；若发现用户已编辑，停止写入并报告跳过。",
			video.KnowledgeBaseWikiPageID, contract.WriteSlug(video.ID),
		)
	}
	return query
}

func (h *BaseSkillHandler) transcriptKnowledgeIDs(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) ([]string, error) {
	generation := strings.TrimSpace(job.TranscriptGeneration)
	if generation == "" {
		generation = strings.TrimSpace(video.TranscriptGeneration)
	}
	if generation == "" || generation != strings.TrimSpace(video.TranscriptGeneration) {
		return nil, fmt.Errorf("视频 %s 的转写代次不可用或已过期", video.ID)
	}
	var chunks []model.VideoTranscriptChunk
	if err := h.DB.WithContext(ctx).
		Where("video_id = ? AND generation = ?", video.ID, generation).
		Order("chunk_index ASC").Find(&chunks).Error; err != nil {
		return nil, fmt.Errorf("读取完整转写分块清单: %w", err)
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("视频 %s 的转写分块清单为空", video.ID)
	}
	ids := make([]string, 0, len(chunks))
	seen := make(map[string]struct{}, len(chunks))
	for index, chunk := range chunks {
		if chunk.ChunkIndex != index || chunk.Status != "completed" || strings.TrimSpace(chunk.KnowledgeID) == "" {
			return nil, fmt.Errorf("视频 %s 的转写分块不完整: index=%d status=%s", video.ID, chunk.ChunkIndex, chunk.Status)
		}
		if _, exists := seen[chunk.KnowledgeID]; exists {
			return nil, fmt.Errorf("视频 %s 的转写分块存在重复知识 ID", video.ID)
		}
		seen[chunk.KnowledgeID] = struct{}{}
		ids = append(ids, chunk.KnowledgeID)
	}
	return ids, nil
}

type wikiInputManifest struct {
	Mode         string
	SourceID     string
	KnowledgeIDs []string
}

func (h *BaseSkillHandler) wikiInput(ctx context.Context, job *model.VideoProcessingJob, video *model.Video, jobType string) (wikiInputManifest, error) {
	var payload map[string]json.RawMessage
	if strings.TrimSpace(job.InputPayload) != "" {
		if err := json.Unmarshal([]byte(job.InputPayload), &payload); err != nil {
			return wikiInputManifest{}, fmt.Errorf("parse wiki input manifest: %w", err)
		}
	}
	mode := ""
	if raw := payload[transcriptInputModeKey]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &mode); err != nil {
			return wikiInputManifest{}, fmt.Errorf("parse wiki input mode: %w", err)
		}
	}
	sourceID := ""
	if raw := payload[transcriptSourceKnowledgeIDKey]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &sourceID); err != nil {
			return wikiInputManifest{}, fmt.Errorf("parse transcript source knowledge id: %w", err)
		}
	}
	if mode == "" {
		if jobType == skill.JobGraph {
			mode = TranscriptInputModeFullDocument
		} else {
			mode = TranscriptInputModeEvidence
		}
	}
	switch mode {
	case TranscriptInputModeFullDocument:
		if strings.TrimSpace(sourceID) == "" {
			return wikiInputManifest{}, fmt.Errorf("transcript_source_validation:source_missing: full-document Wiki input requires transcript source knowledge id")
		}
		generation := strings.TrimSpace(job.TranscriptGeneration)
		if generation == "" {
			generation = strings.TrimSpace(video.TranscriptGeneration)
		}
		var binding model.VideoTranscriptSource
		if err := h.DB.WithContext(ctx).Where("video_id = ? AND transcript_generation = ?", video.ID, generation).First(&binding).Error; err != nil {
			return wikiInputManifest{}, fmt.Errorf("transcript_source_validation:source_binding_missing: load source binding: %w", err)
		}
		if binding.Status != transcriptservice.SourceStatusCreated || binding.KnowledgeID != sourceID {
			return wikiInputManifest{}, fmt.Errorf("transcript_source_validation:source_binding_invalid: source binding is not ready for active generation")
		}
		if h.SourceReader == nil {
			return wikiInputManifest{}, fmt.Errorf("transcript_source_validation:source_reader_missing: source document reader is not configured")
		}
		knowledge, err := h.SourceReader.GetKnowledge(ctx, sourceID)
		if err != nil {
			return wikiInputManifest{}, fmt.Errorf("transcript source read: %w", err)
		}
		if _, err := transcriptservice.ValidateSourceContent(knowledge.Content, video.ID, generation, video.DurationSeconds); err != nil {
			return wikiInputManifest{}, err
		}
		return wikiInputManifest{Mode: mode, SourceID: sourceID, KnowledgeIDs: []string{sourceID}}, nil
	case TranscriptInputModeEvidence:
		if jobType == skill.JobGraph {
			return wikiInputManifest{}, fmt.Errorf("transcript_source_validation:input_mode_invalid: graph Wiki input must use full_document")
		}
		ids, err := h.transcriptKnowledgeIDs(ctx, job, video)
		if err != nil {
			return wikiInputManifest{}, err
		}
		return wikiInputManifest{Mode: mode, SourceID: sourceID, KnowledgeIDs: ids}, nil
	default:
		return wikiInputManifest{}, fmt.Errorf("transcript_source_validation:input_mode_invalid: unsupported transcript input mode %q", mode)
	}
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
func (h *BaseSkillHandler) run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video, jobType string) (runErr error) {
	startedAt := time.Now()
	inputMode, sourceID := "", ""
	defer func() {
		slog.Info("wiki skill run finished", "video_id", video.ID, "job_id", job.ID, "job_type", jobType,
			"transcript_generation", job.TranscriptGeneration, "input_mode", inputMode, "source_document_id", sourceID,
			"elapsed_ms", time.Since(startedAt).Milliseconds(), "error", runErr)
	}()
	contract, ok := skill.Contract(jobType)
	if !ok {
		return fmt.Errorf("未注册的 job_type: %s", jobType)
	}
	explicitRegeneration := skill.IsExplicitSummaryRegeneration(job.InputPayload)
	if jobType == skill.JobSummary || jobType == skill.JobSummaryEnhance {
		protected, err := h.Orchestrator.IsSummaryUserEditProtected(ctx, video.ID)
		if err != nil {
			return fmt.Errorf("check summary user edit protection: %w", err)
		}
		if protected && !explicitRegeneration {
			if err := h.DB.WithContext(ctx).Model(&model.Video{}).Where("id = ?", video.ID).Updates(map[string]any{
				"summary_user_edited": true, "summary_source": "user_edited",
			}).Error; err != nil {
				return fmt.Errorf("persist summary user edit protection: %w", err)
			}
			slog.Info("skip automatic summary generation for user-edited summary", "video_id", video.ID, "job_id", job.ID)
			return nil
		}
	}
	input, err := h.wikiInput(ctx, job, video, jobType)
	if err != nil {
		return err
	}
	inputMode, sourceID = input.Mode, input.SourceID
	knowledgeIDs := input.KnowledgeIDs
	baseline, err := h.wikiBaseline(ctx, job, video.ID)
	if err != nil {
		return fmt.Errorf("snapshot wiki pages before %s: %w", contract.SkillName, err)
	}

	// 创建 session 并触发 skill
	sessionID, err := h.AgentClient.CreateSession(ctx, fmt.Sprintf("content-pipeline/%s/%s", video.ID, jobType))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	query := skillQueryWithInput(video, contract, jobType, sourceID, inputMode)
	if explicitRegeneration {
		query += "这是用户明确发起的历史总结重生成：允许覆盖旧的用户编辑总结，必须按当前类型化 JSON 契约重新写入；不要跳过写入。"
	}
	if err := h.AgentClient.TriggerSkill(ctx, sessionID, h.AgentID, contract.SkillName, query, knowledgeIDs); err != nil {
		if !isMissingWikiPageError(err) {
			return fmt.Errorf("trigger skill %s: %w", contract.SkillName, err)
		}
		slog.Warn("skill stopped on an expected first-run missing wiki page; retrying with recovery instruction",
			"video_id", video.ID, "job_type", jobType, "error", err)
		recoverySessionID, sessionErr := h.AgentClient.CreateSession(ctx, fmt.Sprintf("content-pipeline/%s/%s-recovery", video.ID, jobType))
		if sessionErr != nil {
			return fmt.Errorf("trigger skill %s recovery session: %w (initial error: %v)", contract.SkillName, sessionErr, err)
		}
		recoveryQuery := query + " 这是首次生成恢复流程：目标产物页可能尚不存在，不要先读取目标 slug；请直接调用创建/覆盖 Wiki 写入。读取返回 not found 不是失败，继续完成写入。"
		if retryErr := h.AgentClient.TriggerSkill(ctx, recoverySessionID, h.AgentID, contract.SkillName, recoveryQuery, knowledgeIDs); retryErr != nil {
			return fmt.Errorf("trigger skill %s after missing-page recovery: %w (initial error: %v)", contract.SkillName, retryErr, err)
		}
	}

	// 轮询等待 Wiki 产物页落地（WeKnora 写入到可检索有延迟），最多 10 分钟
	wikiPageID, err := h.waitForWikiPage(ctx, video.ID, jobType, baseline, 10*time.Minute)
	if err != nil {
		return fmt.Errorf("等待 wiki 产物页超时（type=%s）: %w", contract.ArtifactType, err)
	}

	// 回写 wiki_page_id；基础内容齐备时由编排器调度组装
	var oerr error
	if explicitRegeneration {
		_, _, oerr = h.Orchestrator.AfterExplicitSummaryRegeneration(ctx, video.ID, jobType, wikiPageID)
	} else {
		_, _, oerr = h.Orchestrator.AfterSkillCompleteWithID(ctx, video.ID, jobType, wikiPageID)
	}
	if oerr != nil && !errors.Is(oerr, skill.ErrSummaryUserEditProtected) {
		return fmt.Errorf("after skill complete: %w", oerr)
	}
	_ = wikiPageID // 回写已在 AfterSkillComplete 中完成
	return nil
}

func isMissingWikiPageError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "Wiki page '") && strings.Contains(message, "' not found")
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
type GraphHandler struct {
	BaseSkillHandler
	Graph knowledgegraph.Store
}

func (h *GraphHandler) JobType() string { return skill.JobGraph }

// Run graph：知识提取独立执行，不推进基础内容任务。
//
// 流程：
//  1. 尝试调 extract-video-knowledge skill（Agent 对话模式）；
//     若成功但 1 分钟内没有检索到 knowledge_base 新产物，则任务失败
//  2. 回写 knowledge_base_wiki_page_id，不触发 outline/summary
func (h *GraphHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) (runErr error) {
	startedAt := time.Now()
	inputMode, sourceID := "", ""
	defer func() {
		slog.Info("wiki skill run finished", "video_id", video.ID, "job_id", job.ID, "job_type", skill.JobGraph,
			"transcript_generation", job.TranscriptGeneration, "input_mode", inputMode, "source_document_id", sourceID,
			"elapsed_ms", time.Since(startedAt).Milliseconds(), "error", runErr)
	}()
	if h.Graph == nil {
		return fmt.Errorf("Wiki graph projection is not configured")
	}
	contract, _ := skill.Contract(skill.JobGraph)
	input, err := h.wikiInput(ctx, job, video, skill.JobGraph)
	if err != nil {
		return err
	}
	inputMode, sourceID = input.Mode, input.SourceID
	knowledgeIDs := input.KnowledgeIDs
	baseline, err := h.wikiBaseline(ctx, job, video.ID)
	if err != nil {
		return fmt.Errorf("snapshot wiki pages before %s: %w", contract.SkillName, err)
	}

	// --- 步骤 1：触发 skill，并短等 1 分钟确认产物可读 ---
	sessionID, err := h.AgentClient.CreateSession(ctx, fmt.Sprintf("content-pipeline/%s/graph", video.ID))
	if err != nil {
		return fmt.Errorf("graph create session: %w", err)
	}
	query := skillQueryWithInput(video, contract, skill.JobGraph, sourceID, inputMode)
	if err := h.AgentClient.TriggerSkill(ctx, sessionID, h.AgentID, contract.SkillName, query, knowledgeIDs); err != nil {
		return fmt.Errorf("graph trigger skill %s: %w", contract.SkillName, err)
	}
	wikiPageID, err := h.waitForWikiPage(ctx, video.ID, skill.JobGraph, baseline, time.Minute)
	if err != nil {
		return fmt.Errorf("graph wait for readable knowledge_base wiki page: %w", err)
	}

	indexPage, err := h.Orchestrator.Wiki.GetPageByID(ctx, h.Orchestrator.KBID, wikiPageID)
	if err != nil {
		return fmt.Errorf("read knowledge base Wiki page for graph projection: %w", err)
	}
	if indexPage == nil || strings.TrimSpace(indexPage.Content) == "" {
		return fmt.Errorf("knowledge base Wiki page is unavailable for graph projection: %s", wikiPageID)
	}
	if err := h.Graph.ProjectVideo(ctx, video, indexPage); err != nil {
		return fmt.Errorf("project Wiki graph: %w", err)
	}
	// Only acknowledge the skill after the real Wiki projection succeeds.
	if _, _, err = h.Orchestrator.AfterSkillCompleteWithID(ctx, video.ID, skill.JobGraph, wikiPageID); err != nil {
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

// SummaryHandler generate-typed-transcript-summary
type SummaryHandler struct{ BaseSkillHandler }

func (h *SummaryHandler) JobType() string { return skill.JobSummary }
func (h *SummaryHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	return h.BaseSkillHandler.run(ctx, job, video, skill.JobSummary)
}

type SummaryEnhanceHandler struct{ BaseSkillHandler }

func (h *SummaryEnhanceHandler) JobType() string { return skill.JobSummaryEnhance }
func (h *SummaryEnhanceHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	return h.BaseSkillHandler.run(ctx, job, video, skill.JobSummaryEnhance)
}

// AssembleHandler assemble-transcript-page
type AssembleHandler struct{ BaseSkillHandler }

func (h *AssembleHandler) JobType() string { return skill.JobAssemble }
func (h *AssembleHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	return h.BaseSkillHandler.run(ctx, job, video, skill.JobAssemble)
}

// EnqueueFirstJob 在当前转写代次激活后入队基础内容与知识增强任务。
func (h *BaseSkillHandler) EnqueueFirstJob(ctx context.Context, video *model.Video) (string, error) {
	if err := h.Orchestrator.EnqueueContentPipeline(ctx, video.ID); err != nil {
		return "", err
	}
	return "", nil
}

// time 包占位（防止 import 报错）
var _ = time.Now
