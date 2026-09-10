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

	"github.com/Tencent/WeKnora/internal/contentprovenance"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	customknowledge "github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgegraph"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	transcriptservice "github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

// AgentClient is the small seam used by content workers. Production uses the
// fixed knowledge-KB adapter; tests can prove a path never reaches Agent.
type AgentClient interface {
	CreateSession(context.Context, string) (string, error)
	TriggerSkill(context.Context, string, string, string, string, []string, *contentprovenance.Job) error
}

// BaseSkillHandler 5 个 skill handler 共用父类
type BaseSkillHandler struct {
	DB              *gorm.DB
	AgentClient     AgentClient
	SourceReader    TranscriptSourceReader
	SourceWriter    *transcriptservice.SourceWriter
	Orchestrator    *skill.Orchestrator
	AgentID         string
	KnowledgeBaseID string
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
			"当前知识对象提交外壳版本为 %s。开始处理前必须通过 read_skill 完整读取 SKILL.md、references/type-frameworks.md、references/wiki-schema.md 和 references/audit-rules.md；组装输出前再完整读取 references/output-examples.md。file_path 必须包含 references/ 目录。V2 Skill 只负责五类候选、复合对象拆解、规范标题建议和当前视频当前代次的一组 evidence_contribution；不得自行决定最终知识对象 ID、Wiki 页面 ID 或规范 slug。只把通过审计的候选提交给 wiki_write_page，候选身份只是提案；必须实际调用 wiki_write_page，最终报告不能代替 wiki_write_page 工具调用。写入前从完整源文档的 evidence_sentence_ids 数组逐字复制真实证据 ID，禁止使用 ev-001、c1、段落号或其他自造编号；chunk_refs 只使用读取工具返回的 chunk_id。语义不确定或冲突时停止该候选并记录 review_required，但必须继续处理其他候选，不得换 slug 绕过。写入后必须使用工具返回的 knowledge_object_id、wiki_page_id、规范标题和 slug 完成回读、关系补写和视频索引；至少一次成功写入并回读后才可生成视频索引和最终报告。证据贡献的源文档只能是 %s，source_document_id 与 source_refs 的唯一值必须原样使用该 ID，证据正文仍只保存在证据知识库。图谱理解必须使用连续语义窗口，五类对象和关系都绑定最小充分证据。最后写入视频索引页：slug 严格使用 %q，page_type 使用 index，frontmatter 使用 type: %s、source_video_id: %s、source_document_id: %s、source_refs: [%s]、transcript_generation: %s、title: %s 和 audit_status: aligned；索引页不得包含 id、knowledge_object_id 或 primary_type；索引只引用工具已返回并回读的规范页面。不得使用示例、占位内容或 mock 数据。",
			customknowledge.WikiObjectContractVersion, strings.TrimSpace(sourceKnowledgeID), contract.WriteSlug(video.ID), contract.ArtifactType, video.ID, strings.TrimSpace(sourceKnowledgeID), strings.TrimSpace(sourceKnowledgeID), video.TranscriptGeneration, strings.TrimSpace(video.Title)+"_知识底座",
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
		if strings.TrimSpace(h.KnowledgeBaseID) == "" {
			return wikiInputManifest{}, fmt.Errorf("knowledge_base_routing:knowledge_kb_missing")
		}
		if strings.TrimSpace(sourceID) == "" {
			return wikiInputManifest{}, fmt.Errorf("transcript_source_validation:source_missing: full-document Wiki input requires transcript source knowledge id")
		}
		generation := strings.TrimSpace(job.TranscriptGeneration)
		if generation == "" {
			generation = strings.TrimSpace(video.TranscriptGeneration)
		}
		var binding model.VideoTranscriptSource
		if err := h.DB.WithContext(ctx).Where(
			"video_id = ? AND transcript_generation = ? AND knowledge_base_id = ?",
			video.ID, generation, h.KnowledgeBaseID,
		).First(&binding).Error; err != nil {
			return wikiInputManifest{}, fmt.Errorf("transcript_source_validation:source_binding_missing: load source binding: %w", err)
		}
		if binding.KnowledgeBaseID != h.KnowledgeBaseID {
			return wikiInputManifest{}, fmt.Errorf("knowledge_base_routing:source_ownership_mismatch")
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
		if actual := strings.TrimSpace(knowledge.KnowledgeBaseID); actual != "" && actual != h.KnowledgeBaseID {
			return wikiInputManifest{}, fmt.Errorf("knowledge_base_routing:source_ownership_mismatch: expected=%s actual=%s", h.KnowledgeBaseID, actual)
		}
		doc, err := transcriptservice.ValidateSourceContent(knowledge.Content, video.ID, generation, video.DurationSeconds)
		if err != nil {
			return wikiInputManifest{}, err
		}
		var chunks []model.VideoTranscriptChunk
		if err := h.DB.WithContext(ctx).
			Where("video_id = ? AND generation = ?", video.ID, generation).
			Order("chunk_index ASC").Find(&chunks).Error; err != nil {
			return wikiInputManifest{}, fmt.Errorf("load active transcript evidence manifest: %w", err)
		}
		manifest := make([]transcriptservice.EvidenceManifestItem, 0, len(chunks))
		for ordinal, chunk := range chunks {
			if chunk.ChunkIndex != ordinal || chunk.Status != "completed" {
				return wikiInputManifest{}, fmt.Errorf("transcript_source_validation:%s: active evidence sentence %d is incomplete", transcriptservice.SourceValidationEvidence, ordinal)
			}
			manifest = append(manifest, transcriptservice.EvidenceManifestItem{
				EvidenceSentenceID: chunk.EvidenceSentenceID,
				SourceSentenceID:   chunk.SourceSegmentID,
				SpeakerID:          chunk.SpeakerID,
				StartMs:            chunk.StartMs,
				EndMs:              chunk.EndMs,
			})
		}
		if err := transcriptservice.ValidateSourceEvidenceManifest(doc, manifest); err != nil {
			normalized, changed, normalizeErr := transcriptservice.NormalizeSourceEvidenceManifest(doc, manifest)
			if normalizeErr != nil {
				return wikiInputManifest{}, normalizeErr
			}
			if !changed {
				return wikiInputManifest{}, err
			}
			if h.SourceWriter == nil {
				return wikiInputManifest{}, fmt.Errorf("transcript_source_validation:source_repair_unavailable: source document writer is not configured")
			}
			repaired, repairErr := h.SourceWriter.Ensure(ctx, transcriptservice.SourceInput{Document: normalized, TaskID: job.ID})
			if repairErr != nil {
				return wikiInputManifest{}, fmt.Errorf("transcript source repair: %w", repairErr)
			}
			if repaired.KnowledgeID != sourceID || repaired.KnowledgeBaseID != h.KnowledgeBaseID {
				return wikiInputManifest{}, fmt.Errorf("transcript_source_validation:source_binding_invalid: repaired source identity changed")
			}
			knowledge, err = h.SourceReader.GetKnowledge(ctx, sourceID)
			if err != nil {
				return wikiInputManifest{}, fmt.Errorf("transcript source reread after repair: %w", err)
			}
			if actual := strings.TrimSpace(knowledge.KnowledgeBaseID); actual != "" && actual != h.KnowledgeBaseID {
				return wikiInputManifest{}, fmt.Errorf("knowledge_base_routing:source_ownership_mismatch: expected=%s actual=%s", h.KnowledgeBaseID, actual)
			}
			doc, err = transcriptservice.ValidateSourceContent(knowledge.Content, video.ID, generation, video.DurationSeconds)
			if err != nil {
				return wikiInputManifest{}, err
			}
			if err := transcriptservice.ValidateSourceEvidenceManifest(doc, manifest); err != nil {
				return wikiInputManifest{}, err
			}
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
	if strings.TrimSpace(h.KnowledgeBaseID) == "" {
		return fmt.Errorf("knowledge_base_routing:knowledge_kb_missing")
	}
	if h.AgentClient == nil {
		return fmt.Errorf("knowledge_base_routing:knowledge_agent_missing")
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
	if jobType == skill.JobGraph {
		if err := h.ensureGraphIndexSeed(ctx, video); err != nil {
			return err
		}
		versions, snapshotErr := h.Orchestrator.SnapshotWikiPageVersions(ctx, video.ID)
		if snapshotErr != nil {
			return fmt.Errorf("snapshot graph attempt wiki pages: %w", snapshotErr)
		}
		baseline = skill.WikiPageBaseline{Versions: versions, JobCreatedAt: time.Now().UTC()}
	}

	// 创建 session 并触发 skill
	productionGeneration := strings.TrimSpace(job.TranscriptGeneration)
	if productionGeneration == "" {
		productionGeneration = strings.TrimSpace(video.TranscriptGeneration)
	}
	if productionGeneration == "" {
		return fmt.Errorf("content pipeline session requires a transcript generation")
	}
	productionJob, err := authenticatedProductionJob(job, video, jobType, productionGeneration)
	if err != nil {
		return err
	}
	sessionID, err := h.AgentClient.CreateSession(ctx, fmt.Sprintf("content-pipeline/%s/%s/%s/%s", video.ID, productionGeneration, jobType, job.ID))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	query := skillQueryWithInput(video, contract, jobType, sourceID, inputMode)
	if explicitRegeneration {
		query += "这是用户明确发起的历史总结重生成：允许覆盖旧的用户编辑总结，必须按当前类型化 JSON 契约重新写入；不要跳过写入。"
	}
	if err := h.AgentClient.TriggerSkill(ctx, sessionID, h.AgentID, contract.SkillName, query, knowledgeIDs, productionJob); err != nil {
		if !isMissingWikiPageError(err) {
			return fmt.Errorf("trigger skill %s: %w", contract.SkillName, err)
		}
		slog.Warn("skill stopped on an expected first-run missing wiki page; retrying with recovery instruction",
			"video_id", video.ID, "job_type", jobType, "error", err)
		recoverySessionID, sessionErr := h.AgentClient.CreateSession(ctx, fmt.Sprintf("content-pipeline/%s/%s/%s-recovery/%s", video.ID, productionGeneration, jobType, job.ID))
		if sessionErr != nil {
			return fmt.Errorf("trigger skill %s recovery session: %w (initial error: %v)", contract.SkillName, sessionErr, err)
		}
		recoveryQuery := query + " 这是首次生成恢复流程：目标产物页可能尚不存在，不要先读取目标 slug；请直接调用创建/覆盖 Wiki 写入。读取返回 not found 不是失败，继续完成写入。"
		if retryErr := h.AgentClient.TriggerSkill(ctx, recoverySessionID, h.AgentID, contract.SkillName, recoveryQuery, knowledgeIDs, productionJob); retryErr != nil {
			return fmt.Errorf("trigger skill %s after missing-page recovery: %w (initial error: %v)", contract.SkillName, retryErr, err)
		}
	}
	if jobType == skill.JobGraph {
		if err := h.repairP3KnowledgeOnce(ctx, video, query, knowledgeIDs, baseline, productionJob); err != nil {
			return err
		}
	}

	// 轮询等待 Wiki 产物页落地（WeKnora 写入到可检索有延迟），最多 10 分钟
	wikiPageID, err := h.waitForWikiPage(ctx, video.ID, jobType, baseline, 10*time.Minute)
	if err != nil {
		return wrapWikiArtifactWaitError(contract.ArtifactType, err)
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

func authenticatedProductionJob(
	job *model.VideoProcessingJob,
	video *model.Video,
	jobType, generation string,
) (*contentprovenance.Job, error) {
	if job == nil || video == nil || strings.TrimSpace(job.ID) == "" ||
		strings.TrimSpace(job.VideoID) != strings.TrimSpace(video.ID) ||
		strings.TrimSpace(job.JobType) != strings.TrimSpace(jobType) ||
		strings.TrimSpace(job.TranscriptGeneration) != strings.TrimSpace(generation) {
		return nil, fmt.Errorf("content pipeline provenance requires a persisted task matching video, generation, and job type")
	}
	return &contentprovenance.Job{
		TaskID:               job.ID,
		VideoID:              video.ID,
		TranscriptGeneration: generation,
		JobType:              jobType,
	}, nil
}

func wrapWikiArtifactWaitError(artifactType string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return fmt.Errorf("等待 wiki 产物页超时（type=%s）: %w", artifactType, err)
	}
	return fmt.Errorf("Wiki 产物页未通过验收（type=%s）: %w", artifactType, err)
}

// repairP3KnowledgeOnce turns strict validation failures into one bounded,
// actionable Agent repair. A task retry still owns the outer retry budget; this
// method only repairs pages written by the current Agent run.
func (h *BaseSkillHandler) repairP3KnowledgeOnce(
	ctx context.Context,
	video *model.Video,
	query string,
	knowledgeIDs []string,
	baseline skill.WikiPageBaseline,
	productionJob *contentprovenance.Job,
) error {
	artifacts, invalidObjects, err := h.inspectP3KnowledgeAfter(ctx, video.ID, video.TranscriptGeneration, video.Title, baseline)
	if err != nil {
		return fmt.Errorf("inspect generated P3 knowledge: %w", err)
	}
	if artifacts != nil && artifacts.Index != nil {
		return nil
	}
	diagnostics := "未生成可用的视频索引页，或当前代次没有任何通过契约的知识对象页"
	if len(invalidObjects) > 0 {
		diagnostics = strings.Join(invalidObjects, "; ")
	}
	repairSessionID, err := h.AgentClient.CreateSession(ctx, fmt.Sprintf("content-pipeline/%s/%s/%s-contract-repair/%s", video.ID, video.TranscriptGeneration, skill.JobGraph, productionJob.TaskID))
	if err != nil {
		return fmt.Errorf("create graph contract repair session: %w", err)
	}
	repairQuery := query + " 本轮生成的候选 Wiki 页面未通过后端契约校验。校验结果：" + diagnostics +
		"。只修复当前视频、当前转写代次的页面；逐页读取现有内容后使用 wiki_write_page 完整覆盖，确保 frontmatter.core_content 为非空的一句话概括，每次调用必须显式提供 slug、title、summary、content、page_type、source_refs 六个必填参数，禁止使用 wiki_replace_text，禁止追加第二段 frontmatter。修复后再次读取并确认对象页与视频索引页均满足原契约。"
	contract, ok := skill.Contract(skill.JobGraph)
	if !ok {
		return fmt.Errorf("unknown graph skill contract")
	}
	if err := h.AgentClient.TriggerSkill(ctx, repairSessionID, h.AgentID, contract.SkillName, repairQuery, knowledgeIDs, productionJob); err != nil {
		return fmt.Errorf("repair generated P3 knowledge contract: %w", err)
	}
	return nil
}

// ensureGraphIndexSeed makes the fixed video index slug readable before the
// Agent starts. The Agent may inspect that slug before writing it; a pending
// seed avoids a first-run not-found failure without being accepted as a P3
// completion artifact.
func (h *BaseSkillHandler) ensureGraphIndexSeed(ctx context.Context, video *model.Video) error {
	if h.Orchestrator == nil || h.Orchestrator.Wiki == nil {
		return fmt.Errorf("native Wiki graph reconciliation is not configured")
	}
	if strings.TrimSpace(h.KnowledgeBaseID) == "" {
		return fmt.Errorf("knowledge_base_routing:knowledge_kb_missing")
	}
	contract, ok := skill.Contract(skill.JobGraph)
	if !ok {
		return fmt.Errorf("unknown graph skill contract")
	}
	slug := contract.WriteSlug(video.ID)
	content := graphIndexSeedContent(video.ID, video.TranscriptGeneration, video.Title)
	page, err := h.Orchestrator.Wiki.EnsurePage(ctx, h.KnowledgeBaseID, weknora.WikiPageWrite{
		Slug:     slug,
		Title:    strings.TrimSpace(video.Title) + "_知识底座",
		PageType: "index",
		Status:   "published",
		Content:  content,
	})
	if err != nil {
		return fmt.Errorf("ensure graph index seed: %w", err)
	}
	if page == nil || strings.TrimSpace(page.ID) == "" {
		return fmt.Errorf("ensure graph index seed returned empty page")
	}
	slog.Info("graph index seed ready", "video_id", video.ID, "index_page_id", page.ID, "created_or_reused", true)
	return nil
}

func graphIndexSeedContent(videoID, generation, title string) string {
	title = strings.TrimSpace(title)
	return fmt.Sprintf("---\ntype: knowledge_base\nsource_video_id: %s\ntranscript_generation: %s\ntitle: %q\naudit_status: pending\n---\n\n# %s_知识底座\n\n状态：待 extract-video-knowledge 完成知识对象抽取与审计后更新。\n", strings.TrimSpace(videoID), strings.TrimSpace(generation), title+"_知识底座", title)
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
	if jobType == skill.JobGraph {
		generation, title, err := h.videoIdentity(ctx, videoID)
		if err != nil {
			return "", err
		}
		return h.waitForP3Knowledge(ctx, videoID, generation, title, baseline, timeout)
	}

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

type p3KnowledgeArtifacts struct {
	Index       *weknora.WikiPage
	ObjectCount int
}

// videoIdentity returns the active generation used to validate a Wiki
// artifact. The worker receives a video snapshot, but waiting can outlive that
// snapshot while a retry or another process updates the row.
func (h *BaseSkillHandler) videoIdentity(ctx context.Context, videoID string) (string, string, error) {
	if h.DB == nil {
		return "", "", fmt.Errorf("load video identity: database is not configured")
	}
	var video model.Video
	if err := h.DB.WithContext(ctx).Select("transcript_generation", "title").First(&video, "id = ?", videoID).Error; err != nil {
		return "", "", fmt.Errorf("load video identity: %w", err)
	}
	if strings.TrimSpace(video.TranscriptGeneration) == "" {
		return "", "", fmt.Errorf("video %s has no active transcript generation", videoID)
	}
	return strings.TrimSpace(video.TranscriptGeneration), strings.TrimSpace(video.Title), nil
}

// findP3Knowledge accepts only the artifacts described by the video knowledge
// contract. Native Wiki entity/concept pages and the KB-global index are not a
// completion signal: the index must be video-scoped and at least one object
// page must pass the full P3 object validator for the active generation.
func (h *BaseSkillHandler) findP3Knowledge(
	ctx context.Context,
	videoID, generation, title string,
) (*p3KnowledgeArtifacts, error) {
	artifacts, _, err := h.inspectP3Knowledge(ctx, videoID, generation, title)
	return artifacts, err
}

func (h *BaseSkillHandler) inspectP3Knowledge(
	ctx context.Context,
	videoID, generation, title string,
) (*p3KnowledgeArtifacts, []string, error) {
	return h.inspectP3KnowledgeAfter(ctx, videoID, generation, title, skill.WikiPageBaseline{})
}

func (h *BaseSkillHandler) inspectP3KnowledgeAfter(
	ctx context.Context,
	videoID, generation, title string,
	baseline skill.WikiPageBaseline,
) (*p3KnowledgeArtifacts, []string, error) {
	if h.Orchestrator == nil || h.Orchestrator.Wiki == nil {
		return nil, nil, fmt.Errorf("native Wiki graph reconciliation is not configured")
	}
	if strings.TrimSpace(h.KnowledgeBaseID) == "" {
		return nil, nil, fmt.Errorf("knowledge_base_routing:knowledge_kb_missing")
	}
	generation = strings.TrimSpace(generation)
	if generation == "" {
		return nil, nil, fmt.Errorf("video %s has no active transcript generation", videoID)
	}
	pages, err := h.Orchestrator.Wiki.ListAllPages(ctx, h.KnowledgeBaseID, "")
	if err != nil {
		return nil, nil, fmt.Errorf("list Wiki pages for P3 knowledge: %w", err)
	}

	var index *weknora.WikiPage
	invalidObjects := make([]string, 0)
	indexCandidateFound := false
	for i := range pages {
		candidate := pages[i]
		if candidate.Slug != "video/"+strings.TrimSpace(videoID) {
			continue
		}
		if !isWikiPageInAttempt(candidate, baseline) {
			continue
		}
		indexCandidateFound = true
		page, readErr := h.readWikiPageForValidation(ctx, candidate)
		if readErr != nil {
			return nil, nil, readErr
		}
		if page == nil {
			invalidObjects = append(invalidObjects, fmt.Sprintf("%s: video index page is empty or unreadable", candidate.ID))
			continue
		}
		if validationErr := validateP3KnowledgeIndex(*page, videoID, generation, title); validationErr != nil {
			invalidObjects = append(invalidObjects, fmt.Sprintf("%s: %v", candidate.ID, validationErr))
			continue
		}
		if index == nil {
			index = page
		}
	}
	if !indexCandidateFound {
		if baseline.Versions != nil {
			invalidObjects = append(invalidObjects, fmt.Sprintf("video/%s: video index page was not created or updated in the current attempt", strings.TrimSpace(videoID)))
		} else {
			invalidObjects = append(invalidObjects, fmt.Sprintf("video/%s: video index page is missing", strings.TrimSpace(videoID)))
		}
	}

	type validatedObjectPage struct {
		page       weknora.WikiPage
		validation customknowledge.WikiObjectValidation
	}
	validatedObjects := make([]validatedObjectPage, 0)
	for i := range pages {
		candidate := pages[i]
		if candidate.Slug == "video/"+strings.TrimSpace(videoID) {
			continue
		}
		if !isWikiPageInAttempt(candidate, baseline) {
			continue
		}
		page, readErr := h.readWikiPageForValidation(ctx, candidate)
		if readErr != nil {
			return nil, nil, readErr
		}
		if page == nil {
			continue
		}
		if !isCurrentP3ObjectCandidate(*page, videoID, generation) {
			continue
		}
		validation, validationErr := customknowledge.ValidateWikiObjectWritePage(page.Content, page.PageType, videoID, generation)
		if validationErr != nil {
			invalidObjects = append(invalidObjects, fmt.Sprintf("%s: %v", page.ID, validationErr))
			continue
		}
		validatedObjects = append(validatedObjects, validatedObjectPage{page: *page, validation: validation})
	}

	objectCount := 0
	validatedPages := make([]weknora.WikiPage, 0, len(validatedObjects))
	identityPages := make([]weknora.WikiPage, 0, len(validatedObjects))
	if len(validatedObjects) > 0 {
		if h.DB == nil {
			return nil, nil, fmt.Errorf("validate P3 knowledge evidence: database is not configured")
		}
		var chunks []model.VideoTranscriptChunk
		if err := h.DB.WithContext(ctx).
			Where("video_id = ? AND generation = ? AND status = ?", videoID, generation, "completed").
			Find(&chunks).Error; err != nil {
			return nil, nil, fmt.Errorf("validate P3 knowledge evidence: %w", err)
		}
		byEvidence, byIndex := knowledgegraph.BuildEvidenceChunkIndex(chunks)
		for _, candidate := range validatedObjects {
			if evidenceErr := validateP3KnowledgeEvidence(candidate.validation, byEvidence, byIndex); evidenceErr != nil {
				invalidObjects = append(invalidObjects, fmt.Sprintf("%s: %v", candidate.page.ID, evidenceErr))
				continue
			}
			objectCount++
			validatedPages = append(validatedPages, candidate.page)
			identityPages = append(identityPages, candidate.page)
		}
		// Attempt-scoped validation prevents historical malformed pages from
		// poisoning retries. Semantic identity is different: every valid page in
		// the active generation must participate or an unchanged duplicate can
		// silently return to the graph after the current attempt succeeds.
		for i := range pages {
			candidate := pages[i]
			if candidate.Slug == "video/"+strings.TrimSpace(videoID) || isWikiPageInAttempt(candidate, baseline) ||
				!isCurrentP3ObjectCandidate(candidate, videoID, generation) {
				continue
			}
			page, readErr := h.readWikiPageForValidation(ctx, candidate)
			if readErr != nil {
				return nil, nil, readErr
			}
			if page == nil {
				continue
			}
			validation, validationErr := customknowledge.ValidateWikiObjectWritePage(page.Content, page.PageType, videoID, generation)
			if validationErr != nil || validateP3KnowledgeEvidence(validation, byEvidence, byIndex) != nil {
				continue
			}
			identityPages = append(identityPages, *page)
		}
		if len(identityPages) > 1 {
			if identityErr := knowledgegraph.ValidateSemanticIdentityCompletion(videoID, generation, identityPages); identityErr != nil {
				invalidObjects = append(invalidObjects, identityErr.Error())
			}
		}
	}
	if objectCount == 0 {
		invalidObjects = append(invalidObjects, "当前代次没有任何知识对象候选页")
	} else if objectCount > 1 {
		if _, _, relationErr := knowledgegraph.ValidateFormalRelationCompletion(videoID, generation, validatedPages); relationErr != nil {
			invalidObjects = append(invalidObjects, relationErr.Error())
		}
	}
	if index == nil || objectCount == 0 || len(invalidObjects) > 0 {
		return nil, invalidObjects, nil
	}
	return &p3KnowledgeArtifacts{Index: index, ObjectCount: objectCount}, nil, nil
}

func validateP3KnowledgeEvidence(
	object customknowledge.WikiObjectValidation,
	byEvidence map[string]model.VideoTranscriptChunk,
	byIndex map[string]model.VideoTranscriptChunk,
) error {
	validate := func(evidenceID string) error {
		if _, ok := knowledgegraph.ResolveEvidenceChunk(
			byEvidence, byIndex, object.SourceVideoID, object.TranscriptGeneration, evidenceID,
		); !ok {
			return fmt.Errorf("evidence %q is missing from the current transcript generation", evidenceID)
		}
		return nil
	}
	for _, evidenceID := range object.EvidenceIDs {
		if err := validate(evidenceID); err != nil {
			return err
		}
	}
	for _, relation := range object.Relations {
		for _, evidenceID := range relation.EvidenceIDs {
			if err := validate(evidenceID); err != nil {
				return fmt.Errorf("relation %q: %w", relation.RelationID, err)
			}
		}
	}
	return nil
}

func isWikiPageInAttempt(page weknora.WikiPage, baseline skill.WikiPageBaseline) bool {
	if baseline.Versions == nil {
		return true
	}
	previousVersion, existed := baseline.Versions[page.ID]
	return !existed || page.Version > previousVersion
}

func isCurrentP3ObjectCandidate(page weknora.WikiPage, videoID, generation string) bool {
	frontmatter := page.ParsedFrontmatter()
	parsedIdentityMatches := strings.TrimSpace(wikiFrontmatterString(frontmatter, "source_video_id")) == strings.TrimSpace(videoID) &&
		strings.TrimSpace(wikiFrontmatterString(frontmatter, "transcript_generation")) == strings.TrimSpace(generation)
	if parsedIdentityMatches {
		for _, key := range []string{"type", "primary_type"} {
			value := customknowledge.KnowledgeType(strings.ToLower(strings.TrimSpace(wikiFrontmatterString(frontmatter, key))))
			if customknowledge.IsKnowledgeType(value) {
				return true
			}
		}
	}
	// ParsedFrontmatter intentionally fails closed on malformed YAML. Keep such
	// pages in the validation set when their raw identity and object namespace
	// still prove they belong to this exact video generation; otherwise a
	// duplicate key or broken scalar is silently ignored until the task times out.
	if rawFrontmatterScalar(page.Content, "source_video_id") != strings.TrimSpace(videoID) ||
		rawFrontmatterScalar(page.Content, "transcript_generation") != strings.TrimSpace(generation) {
		return false
	}
	for _, prefix := range []string{"entity/", "concept/", "case/", "methodology/", "insight/", "knowledge-object/"} {
		if strings.HasPrefix(strings.TrimSpace(page.Slug), prefix) {
			return true
		}
	}
	return false
}

func rawFrontmatterScalar(content, key string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	prefix := key + ":"
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		if strings.HasPrefix(line, prefix) {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), `"'`)
		}
	}
	return ""
}

func (h *BaseSkillHandler) readWikiPageForValidation(ctx context.Context, candidate weknora.WikiPage) (*weknora.WikiPage, error) {
	page, err := h.Orchestrator.Wiki.GetPage(ctx, h.KnowledgeBaseID, candidate.Slug)
	if err != nil {
		return nil, fmt.Errorf("read P3 Wiki page %s: %w", candidate.ID, err)
	}
	if page == nil || page.ID != candidate.ID || strings.TrimSpace(page.Content) == "" {
		return nil, nil
	}
	return page, nil
}

func (h *BaseSkillHandler) waitForP3Knowledge(
	ctx context.Context,
	videoID, generation, title string,
	baseline skill.WikiPageBaseline,
	timeout time.Duration,
) (string, error) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var lastObjectCount int
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for P3 knowledge artifacts: object_count=%d", lastObjectCount)
		}
		artifacts, invalidObjects, err := h.inspectP3KnowledgeAfter(ctx, videoID, generation, title, baseline)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			slog.Warn("waitForP3Knowledge find artifacts", "video_id", videoID, "error", err)
		} else if len(invalidObjects) > 0 {
			return "", fmt.Errorf("P3 knowledge object validation failed: %s", strings.Join(invalidObjects, "; "))
		} else if artifacts != nil && artifacts.Index != nil {
			lastObjectCount = artifacts.ObjectCount
			slog.Info("waitForP3Knowledge found", "video_id", videoID, "index_page_id", artifacts.Index.ID, "object_count", artifacts.ObjectCount)
			return artifacts.Index.ID, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func isP3KnowledgeIndex(page weknora.WikiPage, videoID, generation, title string) bool {
	return validateP3KnowledgeIndex(page, videoID, generation, title) == nil
}

func validateP3KnowledgeIndex(page weknora.WikiPage, videoID, generation, title string) error {
	return customknowledge.ValidateVideoKnowledgeIndexPage(
		page.Content, page.PageType, page.Slug, videoID, generation, title,
	)
}

func wikiFrontmatterString(frontmatter map[string]any, key string) string {
	value, _ := frontmatter[key].(string)
	return value
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
//  1. 已有合规 P3 产物时直接回写索引页，不重复调用 Agent；
//  2. 产物缺失或不合规时调用 extract-video-knowledge skill，等待严格产物；
//  3. 回写 knowledge_base_wiki_page_id，不触发 outline/summary。

func (h *GraphHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	if h.Orchestrator == nil || h.Orchestrator.Wiki == nil {
		return fmt.Errorf("native Wiki graph reconciliation is not configured")
	}
	if strings.TrimSpace(h.KnowledgeBaseID) == "" {
		return fmt.Errorf("knowledge_base_routing:knowledge_kb_missing")
	}
	generation, title, err := h.videoIdentity(ctx, video.ID)
	if err != nil {
		return err
	}
	if artifacts, err := h.findP3Knowledge(ctx, video.ID, generation, title); err != nil {
		return err
	} else if artifacts != nil && artifacts.Index != nil {
		if _, _, err := h.Orchestrator.AfterSkillCompleteWithID(ctx, video.ID, skill.JobGraph, artifacts.Index.ID); err != nil {
			return fmt.Errorf("record existing P3 knowledge: %w", err)
		}
		if err := h.projectP3Knowledge(ctx, video.ID, artifacts.Index); err != nil {
			return err
		}
		slog.Info("P3 knowledge already reconciled",
			"video_id", video.ID, "job_id", job.ID,
			"index_page_id", artifacts.Index.ID, "object_count", artifacts.ObjectCount)
		return nil
	}

	if err := h.BaseSkillHandler.run(ctx, job, video, skill.JobGraph); err != nil {
		return err
	}
	index, err := h.recordedP3KnowledgeIndex(ctx, video.ID, generation, title)
	if err != nil {
		return err
	}
	artifacts, invalidObjects, err := h.inspectP3Knowledge(ctx, video.ID, generation, title)
	if err != nil {
		return fmt.Errorf("validate completed P3 knowledge: %w", err)
	}
	if artifacts == nil || artifacts.Index == nil || len(invalidObjects) > 0 {
		diagnostics := "completed extraction has no valid P3 knowledge artifacts"
		if len(invalidObjects) > 0 {
			diagnostics = strings.Join(invalidObjects, "; ")
		}
		return fmt.Errorf("P3 knowledge object validation failed: %s", diagnostics)
	}
	if artifacts.Index.ID != index.ID {
		return fmt.Errorf("P3 knowledge object validation failed: recorded index does not match the validated video index")
	}
	if err := h.projectP3Knowledge(ctx, video.ID, index); err != nil {
		return err
	}
	slog.Info("P3 knowledge extracted",
		"video_id", video.ID, "job_id", job.ID, "page_producer", "extract-video-knowledge")
	return nil
}

func (h *GraphHandler) recordedP3KnowledgeIndex(ctx context.Context, videoID, generation, title string) (*weknora.WikiPage, error) {
	var current model.Video
	if err := h.DB.WithContext(ctx).Select("knowledge_base_wiki_page_id").First(&current, "id = ?", videoID).Error; err != nil {
		return nil, fmt.Errorf("project P3 knowledge: reload recorded index: %w", err)
	}
	if strings.TrimSpace(current.KnowledgeBaseWikiPageID) == "" {
		return nil, fmt.Errorf("project P3 knowledge: completed Wiki index is not recorded")
	}
	page, err := h.Orchestrator.Wiki.GetPageByID(ctx, h.KnowledgeBaseID, current.KnowledgeBaseWikiPageID)
	if err != nil {
		return nil, fmt.Errorf("project P3 knowledge: read recorded index: %w", err)
	}
	if page == nil {
		return nil, fmt.Errorf("project P3 knowledge: recorded Wiki index is unavailable")
	}
	if err := validateP3KnowledgeIndex(*page, videoID, generation, title); err != nil {
		return nil, fmt.Errorf("project P3 knowledge: recorded Wiki index is invalid: %w", err)
	}
	return page, nil
}

func (h *GraphHandler) projectP3Knowledge(ctx context.Context, videoID string, indexPage *weknora.WikiPage) error {
	if h.Graph == nil {
		return nil
	}
	var current model.Video
	if err := h.DB.WithContext(ctx).First(&current, "id = ?", videoID).Error; err != nil {
		return fmt.Errorf("project P3 knowledge: reload video: %w", err)
	}
	if err := h.Graph.ProjectVideo(ctx, &current, indexPage); err != nil {
		return fmt.Errorf("project P3 knowledge: %w", err)
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
