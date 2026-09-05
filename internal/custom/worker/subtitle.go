// Package worker subtitle_generate + index job（VP-T006/007/008/009）。
//
// 设计要点：
//   - subtitle_generate job：从 transcription.result_payload 读听悟 JSON，
//     生成 SRT → 上传对象存储 → 回写 subtitle_file_url → 触发 index job
//   - index job：句子级分块 → 12 字段 metadata → 入 WeKnora KB → 回写
//     transcript_knowledge_id + ready_at（触发 content-pipeline）
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/client/mps"
	"github.com/Tencent/WeKnora/internal/custom/client/tongyi"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/chunk"
	"github.com/Tencent/WeKnora/internal/custom/service/evidence"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	"github.com/Tencent/WeKnora/internal/custom/service/subtitle"
	transcriptservice "github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

// SubtitleGenerateHandler 生成 SRT + 入对象存储 + 触发 index
type SubtitleGenerateHandler struct {
	DB     *gorm.DB
	MinIO  *objstore.Client
	Tongyi *tongyi.Client
}

// NewSubtitleGenerateHandler 构造
func NewSubtitleGenerateHandler(db *gorm.DB, m *objstore.Client, t *tongyi.Client) *SubtitleGenerateHandler {
	return &SubtitleGenerateHandler{DB: db, MinIO: m, Tongyi: t}
}

// JobType job 类型
func (h *SubtitleGenerateHandler) JobType() string { return "subtitle_generate" }

// Run 从 transcription.result_payload 读听悟 JSON，下载、转 SRT、入对象、触发 index
func (h *SubtitleGenerateHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	provider := normalizeProvider(job.Provider)
	if provider != mps.Provider && h.Tongyi == nil {
		return fmt.Errorf("听悟 client 未配置")
	}
	if video == nil || video.ID == "" {
		return fmt.Errorf("video is missing")
	}
	// 优先读取当前字幕任务明确绑定的转写任务，兼容旧任务时才回退到最新成功任务。
	var prev model.VideoProcessingJob
	var input struct {
		TranscriptionJobID string `json:"transcription_job_id"`
	}
	if strings.TrimSpace(job.InputPayload) != "" {
		if err := json.Unmarshal([]byte(job.InputPayload), &input); err != nil {
			return fmt.Errorf("parse subtitle input: %w", err)
		}
	}
	query := h.DB.Where("video_id = ? AND job_type = ? AND status = ?", video.ID, "transcription", "succeeded")
	if input.TranscriptionJobID != "" {
		query = query.Where("id = ?", input.TranscriptionJobID)
	}
	if err := query.Order("completed_at DESC").First(&prev).Error; err != nil {
		return fmt.Errorf("无成功 transcription job: %w", err)
	}

	var payload struct {
		TaskID    string      `json:"task_id"`
		RawResult string      `json:"raw_result"`
		Provider  string      `json:"provider"`
		MPSResult *mps.Result `json:"mps_result"`
	}
	if err := json.Unmarshal([]byte(prev.ResultPayload), &payload); err != nil {
		return fmt.Errorf("parse transcription result: %w", err)
	}
	if provider == mps.Provider && payload.MPSResult != nil {
		paragraphs := mpsSegmentsToParagraphs(payload.MPSResult.Segments)
		directURL := accessibleSubtitleURL(ctx, payload.MPSResult.SubtitlePath)
		return h.persistSubtitleAndEnqueueIndex(ctx, job, video, prev, paragraphs, directURL)
	}
	if h.MinIO == nil {
		return fmt.Errorf("对象存储 client 未配置")
	}
	if strings.TrimSpace(payload.RawResult) == "" {
		return fmt.Errorf("transcription result payload is empty")
	}

	// 下载并解析转写 JSON
	transcript, err := h.Tongyi.DownloadResult(ctx, payload.RawResult)
	if err != nil {
		return fmt.Errorf("download transcript: %w", err)
	}
	if len(transcript.Transcripts) == 0 {
		return fmt.Errorf("听悟结果不含转写文件")
	}

	// 合并所有转写文件，不能静默丢弃多文件结果。
	paragraphs := make([]subtitle.TranscriptParagraph, 0)
	for _, src := range transcript.Transcripts {
		for _, p := range src.Paragraphs {
			paragraphs = append(paragraphs, subtitle.TranscriptParagraph{
				ParagraphID: p.ParagraphID,
				SpeakerID:   p.SpeakerID,
				StartMs:     p.StartMs,
				EndMs:       p.EndMs,
				Sentences:   toSentences(p.Sentences),
			})
		}
	}
	if err := subtitle.ValidateParagraphs(paragraphs); err != nil {
		return err
	}
	if err := subtitle.ValidateTranscriptQuality(paragraphs, video.DurationSeconds); err != nil {
		return fmt.Errorf("transcript quality gate: %w", err)
	}

	return h.persistSubtitleAndEnqueueIndex(ctx, job, video, prev, paragraphs, "")
}

func mpsSegmentsToParagraphs(segments []mps.Segment) []subtitle.TranscriptParagraph {
	paragraphs := make([]subtitle.TranscriptParagraph, 0, len(segments))
	for _, seg := range segments {
		if strings.TrimSpace(seg.Text) == "" || seg.EndMs <= seg.StartMs {
			continue
		}
		paragraphs = append(paragraphs, subtitle.TranscriptParagraph{
			ParagraphID: seg.SourceSegmentID, SpeakerID: seg.SpeakerID, StartMs: seg.StartMs, EndMs: seg.EndMs,
			Sentences: []subtitle.TranscriptSentence{{SentenceID: seg.SourceSegmentID, Text: seg.Text, StartMs: seg.StartMs, EndMs: seg.EndMs}},
		})
	}
	return paragraphs
}

func (h *SubtitleGenerateHandler) persistSubtitleAndEnqueueIndex(ctx context.Context, job *model.VideoProcessingJob, video *model.Video, prev model.VideoProcessingJob, paragraphs []subtitle.TranscriptParagraph, directSubtitleURL string) error {
	if len(paragraphs) == 0 {
		return fmt.Errorf("转写结果不含有效字幕片段")
	}
	if err := subtitle.ValidateParagraphs(paragraphs); err != nil {
		return err
	}
	if err := subtitle.ValidateTranscriptQuality(paragraphs, video.DurationSeconds); err != nil {
		return fmt.Errorf("transcript quality gate: %w", err)
	}

	subtitleURL := strings.TrimSpace(directSubtitleURL)
	srt := ""
	if subtitleURL == "" {
		if h.MinIO == nil {
			return fmt.Errorf("对象存储 client 未配置")
		}
		// 听悟和 MPS COS 地址不可访问时的兼容回退。
		srt = subtitle.ParagraphsToSRT(paragraphs)
		objectKey := fmt.Sprintf("subtitles/%s/transcript.srt", video.ID)
		if err := uploadBytes(ctx, h.MinIO, objectKey, []byte(srt), "application/x-subrip"); err != nil {
			return fmt.Errorf("upload srt: %w", err)
		}
		subtitleURL = h.MinIO.PublicURL(objectKey)
		if strings.TrimSpace(subtitleURL) == "" {
			return fmt.Errorf("subtitle public url empty")
		}
	}

	// 把段落结果暂存到本 job 的 result_payload，供 index 读取
	storePayload, err := json.Marshal(map[string]any{
		"paragraphs": paragraphs,
		"language":   "zh",
	})
	if err != nil {
		return fmt.Errorf("marshal subtitle result: %w", err)
	}

	var indexJob model.VideoProcessingJob
	if err := h.DB.Transaction(func(tx *gorm.DB) error {
		var locked model.Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", video.ID).Error; err != nil {
			return fmt.Errorf("lock video revision: %w", err)
		}
		revision := locked.TranscriptRevision + 1
		if err := tx.Model(&locked).Update("transcript_revision", revision).Error; err != nil {
			return fmt.Errorf("advance transcript revision: %w", err)
		}
		indexJob = model.VideoProcessingJob{
			ID:                   uuid.NewString(),
			VideoID:              video.ID,
			JobType:              "index",
			TranscriptGeneration: job.TranscriptGeneration,
			Provider:             "weknora",
			Status:               "pending",
			MaxAttempts:          3,
			IdempotencyKey:       fmt.Sprintf("index:%s:%d:%x", video.ID, revision, sha256.Sum256(storePayload)),
			ResultPayload:        string(storePayload),
		}
		indexJob.InputPayload = fmt.Sprintf(`{"revision":%d}`, revision)
		if err := tx.Model(&model.Video{}).Where("id = ?", video.ID).Update("subtitle_file_url", subtitleURL).Error; err != nil {
			return fmt.Errorf("update subtitle url: %w", err)
		}
		if err := tx.Model(job).Update("result_payload", string(storePayload)).Error; err != nil {
			return fmt.Errorf("save subtitle result: %w", err)
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true}).Create(&indexJob).Error; err != nil {
			return fmt.Errorf("enqueue index job: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	slog.Info("subtitle generation completed", "video_id", video.ID, "job_id", job.ID, "transcription_job_id", prev.ID, "paragraph_count", len(paragraphs), "subtitle_bytes", len(srt), "index_job_id", indexJob.ID)
	return nil
}

func accessibleSubtitleURL(ctx context.Context, raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return ""
	}
	request.Header.Set("Range", "bytes=0-32")
	client := &http.Client{Timeout: 8 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ""
	}
	return raw
}

// IndexHandler 句子级分块 + 12 字段 metadata + 入 WeKnora KB（VP-T009 + CP-T005）
type IndexHandler struct {
	DB               *gorm.DB
	WeKnora          *weknora.Client // fixed evidence adapter
	KnowledgeWeKnora *weknora.Client // fixed full-document adapter
	Splitter         *chunk.Splitter
	Orchestrator     *skill.Orchestrator
	SourceWriter     *transcriptservice.SourceWriter
}

// NewIndexHandler keeps the historical constructor available for callers that
// only build the evidence worker. Such a handler fails closed if it is asked
// to create a source document without an explicit knowledge adapter.
func NewIndexHandler(db *gorm.DB, evidenceClient *weknora.Client, orch *skill.Orchestrator) *IndexHandler {
	return NewIndexHandlerWithKnowledge(db, evidenceClient, nil, orch)
}

// NewIndexHandlerWithKnowledge constructs the two fixed-role adapters used by
// the P2 pipeline.
func NewIndexHandlerWithKnowledge(db *gorm.DB, evidenceClient, knowledgeClient *weknora.Client, orch *skill.Orchestrator) *IndexHandler {
	return &IndexHandler{
		DB: db, WeKnora: evidenceClient, KnowledgeWeKnora: knowledgeClient,
		Splitter: chunk.NewSplitter(), Orchestrator: orch,
		SourceWriter: transcriptservice.NewSourceWriter(db, knowledgeClient),
	}
}

// JobType job 类型
func (h *IndexHandler) JobType() string { return "index" }

// Run 句子级分块 → 入 WeKnora KB → 回写 transcript_knowledge_id
func (h *IndexHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	if h.WeKnora == nil || strings.TrimSpace(h.WeKnora.KBID()) == "" {
		return fmt.Errorf(config.KBRouteEvidenceMissing)
	}
	if h.KnowledgeWeKnora == nil || strings.TrimSpace(h.KnowledgeWeKnora.KBID()) == "" {
		return fmt.Errorf(config.KBRouteKnowledgeMissing)
	}
	if h.WeKnora.KBID() == h.KnowledgeWeKnora.KBID() {
		return fmt.Errorf(config.KBRouteRoleConflict)
	}
	if h.Splitter == nil {
		h.Splitter = chunk.NewSplitter()
	}
	var payload struct {
		Paragraphs []subtitle.TranscriptParagraph `json:"paragraphs"`
		Language   string                         `json:"language"`
	}
	if err := json.Unmarshal([]byte(job.ResultPayload), &payload); err != nil {
		return fmt.Errorf("parse subtitle_generate result: %w", err)
	}
	var jobInput struct {
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal([]byte(job.InputPayload), &jobInput); err != nil {
		return fmt.Errorf("parse index revision: %w", err)
	}
	if jobInput.Revision <= 0 {
		return fmt.Errorf("index revision 必须大于 0")
	}

	// 分块
	results := h.Splitter.Split(chunk.SplitInputs{
		VideoID:         video.ID,
		VideoType:       video.VideoType,
		SourceFilename:  video.FileURL,
		DurationSeconds: video.DurationSeconds,
		Language:        payload.Language,
		Paragraphs:      payload.Paragraphs,
	})
	if len(results) == 0 {
		return fmt.Errorf("分块结果为空")
	}

	// 方案 A：逐条调用 WeKnora 手工 Markdown 知识接口。
	// 12 项定位信息并入正文，避免依赖 WeKnora 不存在的批量分块写入接口。
	// 内容生成号、本地 checkpoint 与稳定标题共同保证重试只补缺项并支持重转写。
	kbID := h.WeKnora.KBID()
	type preparedChunk struct {
		Index              int
		EvidenceSentenceID string
		SourceSegmentID    string
		SpeakerID          string
		StartMs            int
		EndMs              int
		Text               string
		Content            string
		ContentHash        string
	}
	prepared := make([]preparedChunk, 0, len(results))
	generationHash := sha256.New()
	for _, b := range results {
		metadataJSON, err := json.MarshalIndent(b.Metadata.ToMap(), "", "  ")
		if err != nil {
			return fmt.Errorf("marshal chunk metadata %d: %w", b.Metadata.ChunkIndex, err)
		}
		content := fmt.Sprintf("## 视频定位信息\n\n```json\n%s\n```\n\n## 原文\n\n%s", metadataJSON, b.Content)
		contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
		_, _ = generationHash.Write([]byte(contentHash))
		prepared = append(prepared, preparedChunk{Index: b.Metadata.ChunkIndex, SourceSegmentID: b.Metadata.SentenceID, StartMs: b.Metadata.StartMs, EndMs: b.Metadata.EndMs, Text: b.Content, Content: content, ContentHash: contentHash})
	}
	// Include the evidence contract version in the generation digest. Keeping
	// the stored value at 64 hex characters preserves the existing column size
	// while ensuring pre-P1 checkpoints cannot be mistaken for this generation.
	versionedHash := sha256.New()
	_, _ = versionedHash.Write([]byte("evidence-generation-v1|"))
	_, _ = versionedHash.Write(generationHash.Sum(nil))
	generation := fmt.Sprintf("%x", versionedHash.Sum(nil))
	for index := range prepared {
		item := &prepared[index]
		sentence, err := evidence.BuildSentence(evidence.Input{
			VideoID: video.ID, TranscriptGeneration: generation, Ordinal: index,
			SourceSentenceID: item.SourceSegmentID, Text: item.Text,
			SpeakerID: results[index].Metadata.SpeakerID, StartMs: item.StartMs, EndMs: item.EndMs,
		})
		if err != nil {
			return fmt.Errorf("freeze evidence sentence %d: %w", index, err)
		}
		item.EvidenceSentenceID = sentence.ID
		item.SpeakerID = sentence.SpeakerID
	}
	manifest := make([]evidence.Sentence, 0, len(prepared))
	for _, item := range prepared {
		manifest = append(manifest, evidence.Sentence{
			ID: item.EvidenceSentenceID, VideoID: video.ID, TranscriptGeneration: generation,
			Ordinal: item.Index, SourceSentenceID: item.SourceSegmentID, Text: item.Text,
			SpeakerID: item.SpeakerID, StartMs: item.StartMs, EndMs: item.EndMs,
		})
	}
	if err := evidence.ValidateManifest(manifest, video.ID, generation); err != nil {
		return fmt.Errorf("validate evidence sentence manifest: %w", err)
	}
	// Add the immutable ID to the stored positioning metadata only after the
	// generation is known, avoiding a circular generation/ID dependency.
	for index := range prepared {
		result := results[index]
		result.Metadata.TranscriptGeneration = generation
		result.Metadata.EvidenceSentenceID = prepared[index].EvidenceSentenceID
		metadataJSON, err := json.MarshalIndent(result.Metadata.ToMap(), "", "  ")
		if err != nil {
			return fmt.Errorf("marshal evidence metadata %d: %w", result.Metadata.ChunkIndex, err)
		}
		content := fmt.Sprintf("## 视频定位信息\n\n```json\n%s\n```\n\n## 原文\n\n%s", metadataJSON, prepared[index].Text)
		prepared[index].Content = content
		prepared[index].ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	}
	if err := h.DB.Model(job).Update("transcript_generation", generation).Error; err != nil {
		return fmt.Errorf("bind index job transcript generation: %w", err)
	}
	job.TranscriptGeneration = generation

	var compatibilityAnchorID string
	for _, item := range prepared {
		title := fmt.Sprintf("transcript/%s/%s/%06d", video.ID, generation, item.Index)
		var checkpoint model.VideoTranscriptChunk
		dbErr := h.DB.Where("video_id = ? AND generation = ? AND chunk_index = ?", video.ID, generation, item.Index).First(&checkpoint).Error
		if dbErr != nil && dbErr != gorm.ErrRecordNotFound {
			return fmt.Errorf("load transcript checkpoint %d: %w", item.Index, dbErr)
		}
		if dbErr == nil && checkpoint.ContentHash != item.ContentHash {
			return fmt.Errorf("transcript chunk %d hash mismatch in generation %s", item.Index, generation)
		}

		if checkpoint.KnowledgeID == "" {
			created, err := h.WeKnora.FindManualKnowledgeByTitle(ctx, kbID, title)
			if err != nil {
				return fmt.Errorf("reconcile transcript chunk %d: %w", item.Index, err)
			}
			if created == nil {
				value, createErr := h.WeKnora.CreateManualKnowledge(ctx, kbID, weknora.ManualKnowledgeInput{
					Title: title, Content: item.Content, Status: "publish", Channel: "api",
				})
				if createErr != nil {
					return fmt.Errorf("ingest transcript chunk %d: %w", item.Index, createErr)
				}
				created = &value
			}
			checkpoint = model.VideoTranscriptChunk{
				VideoID: video.ID, Generation: generation, Revision: jobInput.Revision, ChunkIndex: item.Index, KnowledgeID: created.ID,
				EvidenceSentenceID: item.EvidenceSentenceID, SourceSegmentID: item.SourceSegmentID, SpeakerID: item.SpeakerID,
				StartMs: item.StartMs, EndMs: item.EndMs,
				ContentHash: item.ContentHash, Status: "created",
			}
			result := h.DB.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "video_id"}, {Name: "generation"}, {Name: "chunk_index"}},
				DoNothing: true,
			}).Create(&checkpoint)
			if result.Error != nil {
				return fmt.Errorf("save transcript checkpoint %d: %w", item.Index, result.Error)
			}
			if result.RowsAffected == 0 {
				var existing model.VideoTranscriptChunk
				if err := h.DB.Where("video_id = ? AND generation = ? AND chunk_index = ?", video.ID, generation, item.Index).First(&existing).Error; err != nil {
					return fmt.Errorf("load concurrent transcript checkpoint %d: %w", item.Index, err)
				}
				if existing.KnowledgeID != checkpoint.KnowledgeID || existing.EvidenceSentenceID != checkpoint.EvidenceSentenceID || existing.ContentHash != checkpoint.ContentHash {
					return fmt.Errorf("transcript chunk %d immutable checkpoint conflict", item.Index)
				}
				checkpoint = existing
			}
		} else {
			if checkpoint.EvidenceSentenceID != item.EvidenceSentenceID || checkpoint.SourceSegmentID != item.SourceSegmentID || checkpoint.SpeakerID != item.SpeakerID || checkpoint.StartMs != item.StartMs || checkpoint.EndMs != item.EndMs {
				return fmt.Errorf("transcript chunk %d immutable evidence mapping mismatch in generation %s", item.Index, generation)
			}
		}
		if compatibilityAnchorID == "" {
			compatibilityAnchorID = checkpoint.KnowledgeID
		}
	}

	if h.SourceWriter == nil {
		h.SourceWriter = transcriptservice.NewSourceWriter(h.DB, h.KnowledgeWeKnora)
	}
	// Create the Wiki-only source before activating the generation. A failed
	// source write therefore cannot advance the video or enqueue downstream
	// Wiki/content work.
	document, err := transcriptservice.BuildFromJSON(transcriptservice.RawInput{
		VideoID: video.ID, TranscriptGeneration: generation, Title: video.Title,
		DurationSeconds: video.DurationSeconds, Provider: "tingwu", Payload: []byte(job.ResultPayload),
	})
	if err != nil {
		return fmt.Errorf("build transcript source document: %w", err)
	}
	source, err := h.SourceWriter.Ensure(ctx, transcriptservice.SourceInput{Document: document, TaskID: job.ID})
	if err != nil {
		return fmt.Errorf("create transcript source document: %w", err)
	}
	if err := h.persistSourceResult(job, source); err != nil {
		return err
	}
	// Keep source-document ingestion parallel to evidence indexing. The source
	// must succeed before generation activation, but it need not wait for every
	// evidence chunk to become searchable.
	if err := h.waitUntilSearchable(ctx, video.ID, generation, len(prepared)); err != nil {
		return err
	}
	// 先原子切换到已完成的新 generation；远端旧数据清理失败不能破坏当前可用版本。
	activated := false
	if err := h.DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Video{}).
			Where("id = ? AND transcript_active_revision < ?", video.ID, jobInput.Revision).
			Updates(map[string]any{
				"transcript_knowledge_id": compatibilityAnchorID, "transcript_generation": generation,
				"transcript_active_revision": jobInput.Revision, "status": model.VideoStatusProcessing,
				"knowledge_base_wiki_page_id": "", "knowledge_audit_status": "",
				"outline_wiki_page_id": "", "outline_draft_wiki_page_id": "", "outline_result_stage": "",
				"overview_wiki_page_id": "", "summary_wiki_page_id": "", "summary_draft_wiki_page_id": "", "summary_result_stage": "",
				"summary_wiki_page_version": 0, "summary_source": "", "summary_knowledge_enhanced": false,
				"summary_user_edited": false, "transcript_page_wiki_page_id": "",
			})
		if result.Error != nil {
			return result.Error
		}
		activated = result.RowsAffected == 1
		if !activated {
			return nil
		}
		if err := retirePendingContentJobs(tx, video.ID, generation); err != nil {
			return err
		}
		return tx.Model(&model.VideoTranscriptChunk{}).
			Where("video_id = ? AND revision < ?", video.ID, jobInput.Revision).
			Update("status", "cleanup_pending").Error
	}); err != nil {
		return fmt.Errorf("activate transcript generation: %w", err)
	}
	if !activated {
		var current model.Video
		if err := h.DB.Select("transcript_generation", "transcript_active_revision").First(&current, "id = ?", video.ID).Error; err != nil {
			return fmt.Errorf("load active transcript generation: %w", err)
		}
		if current.TranscriptGeneration == generation && current.TranscriptActiveRevision == jobInput.Revision {
			if err := retirePendingContentJobs(h.DB, video.ID, generation); err != nil {
				return err
			}
			if err := h.enqueueContentPipeline(ctx, video.ID); err != nil {
				return err
			}
			return h.deleteRetiredGenerations(ctx, video.ID)
		}
		if current.TranscriptActiveRevision > jobInput.Revision {
			if err := h.DB.Model(&model.VideoTranscriptChunk{}).
				Where("video_id = ? AND generation = ?", video.ID, generation).
				Update("status", "cleanup_pending").Error; err != nil {
				return fmt.Errorf("retire stale transcript generation: %w", err)
			}
			return h.deleteRetiredGenerations(ctx, video.ID)
		}
		return fmt.Errorf("transcript generation activation was not applied")
	}

	// 触发内容生产第一环：extract-video-knowledge（CP-T005）
	if err := h.enqueueContentPipeline(ctx, video.ID); err != nil {
		return err
	}
	slog.Info("transcript index completed", "video_id", video.ID, "job_id", job.ID, "generation", generation, "revision", jobInput.Revision, "chunk_count", len(prepared))
	if err := h.deleteRetiredGenerations(ctx, video.ID); err != nil {
		return err
	}
	return nil
}

// retirePendingContentJobs removes queued work from a transcript generation
// that has just been replaced. Running jobs are left alone because their
// external model call cannot be cancelled safely; their artifact source
// validation prevents them from being attached to the new generation.
func retirePendingContentJobs(tx *gorm.DB, videoID, activeGeneration string) error {
	return tx.Model(&model.VideoProcessingJob{}).
		Where("video_id = ? AND status = ? AND job_type IN ?", videoID, "pending", []string{"graph", "outline", "summary", "summary_enhance", "assemble"}).
		Where("COALESCE(transcript_generation, '') <> ?", activeGeneration).
		Updates(map[string]any{
			"status":         "cancelled",
			"error_category": "superseded",
			"error_code":     "stale_transcript_generation",
			"error_message":  "queued content job superseded by active transcript generation",
			"completed_at":   time.Now().UTC(),
		}).Error
}

func (h *IndexHandler) persistSourceResult(job *model.VideoProcessingJob, source transcriptservice.SourceResult) error {
	var payload map[string]any
	if strings.TrimSpace(job.ResultPayload) != "" {
		if err := json.Unmarshal([]byte(job.ResultPayload), &payload); err != nil {
			return fmt.Errorf("parse index result for source result: %w", err)
		}
	}
	if payload == nil {
		payload = make(map[string]any)
	}
	payload["transcript_source_knowledge_id"] = source.KnowledgeID
	payload["transcript_source_knowledge_base_id"] = source.KnowledgeBaseID
	payload["transcript_source_content_hash"] = source.ContentHash
	payload["transcript_source_action"] = source.Action
	if source.AuditEventID != "" {
		payload["transcript_source_audit_event_id"] = source.AuditEventID
	}
	value, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal transcript source result: %w", err)
	}
	if err := h.DB.Model(job).Update("result_payload", string(value)).Error; err != nil {
		return fmt.Errorf("save transcript source result: %w", err)
	}
	job.ResultPayload = string(value)
	return nil
}

func (h *IndexHandler) enqueueContentPipeline(ctx context.Context, videoID string) error {
	if h.Orchestrator == nil {
		return nil
	}
	return h.Orchestrator.EnqueueContentPipeline(ctx, videoID)
}

func (h *IndexHandler) waitUntilSearchable(ctx context.Context, videoID, generation string, expectedChunkCount int) error {
	deadline := time.NewTimer(10 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		var checkpoints []model.VideoTranscriptChunk
		if err := h.DB.Where("video_id = ? AND generation = ?", videoID, generation).Order("chunk_index ASC").Find(&checkpoints).Error; err != nil {
			return fmt.Errorf("list transcript checkpoints: %w", err)
		}
		if len(checkpoints) > expectedChunkCount {
			return fmt.Errorf("转写分块数量超过预期: expected=%d actual=%d", expectedChunkCount, len(checkpoints))
		}
		allCompleted := len(checkpoints) == expectedChunkCount && expectedChunkCount > 0
		for i := range checkpoints {
			if checkpoints[i].ChunkIndex != i || strings.TrimSpace(checkpoints[i].KnowledgeID) == "" {
				return fmt.Errorf("转写分块清单不完整: expected_index=%d actual_index=%d", i, checkpoints[i].ChunkIndex)
			}
			if checkpoints[i].Status == "completed" {
				continue
			}
			knowledge, err := h.WeKnora.GetKnowledge(ctx, checkpoints[i].KnowledgeID)
			if err != nil {
				return fmt.Errorf("check knowledge %s: %w", checkpoints[i].KnowledgeID, err)
			}
			switch knowledge.ParseStatus {
			case "completed":
				searchable, err := h.WeKnora.IsKnowledgeSearchable(ctx, h.WeKnora.KBID(), checkpoints[i].KnowledgeID)
				if err != nil {
					return fmt.Errorf("检索验证知识 %s: %w", checkpoints[i].KnowledgeID, err)
				}
				if !searchable {
					allCompleted = false
					continue
				}
				if err := h.DB.Model(&checkpoints[i]).Update("status", "completed").Error; err != nil {
					return fmt.Errorf("complete transcript checkpoint: %w", err)
				}
			case "failed":
				return fmt.Errorf("knowledge %s parse failed: %s", knowledge.ID, knowledge.ErrorMessage)
			default:
				allCompleted = false
			}
		}
		if allCompleted {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("等待 WeKnora 完成字幕解析超时")
		case <-ticker.C:
		}
	}
}

func (h *IndexHandler) deleteRetiredGenerations(ctx context.Context, videoID string) error {
	var video model.Video
	if err := h.DB.Select("transcript_generation").First(&video, "id = ?", videoID).Error; err != nil {
		return fmt.Errorf("load active transcript generation: %w", err)
	}
	var old []model.VideoTranscriptChunk
	if err := h.DB.Where("video_id = ? AND status = ? AND generation <> ?", videoID, "cleanup_pending", video.TranscriptGeneration).Find(&old).Error; err != nil {
		return fmt.Errorf("list old transcript generations: %w", err)
	}
	for _, checkpoint := range old {
		if err := h.WeKnora.DeleteKnowledge(ctx, checkpoint.KnowledgeID); err != nil {
			return fmt.Errorf("delete old transcript knowledge %s: %w", checkpoint.KnowledgeID, err)
		}
	}
	if len(old) > 0 {
		ids := make([]string, 0, len(old))
		for _, checkpoint := range old {
			ids = append(ids, checkpoint.KnowledgeID)
		}
		if err := h.DB.Where("video_id = ? AND status = ? AND knowledge_id IN ?", videoID, "cleanup_pending", ids).Delete(&model.VideoTranscriptChunk{}).Error; err != nil {
			return fmt.Errorf("delete old transcript checkpoints: %w", err)
		}
	}
	return nil
}

// toSentences 听悟 lite 句子 → subtitle 句子
func toSentences(src []tongyi.SubtitleSentenceLite) []subtitle.TranscriptSentence {
	out := make([]subtitle.TranscriptSentence, 0, len(src))
	for _, s := range src {
		out = append(out, subtitle.TranscriptSentence{
			SentenceID: s.SentenceID,
			Text:       s.Text,
			StartMs:    s.StartMs,
			EndMs:      s.EndMs,
			ChannelID:  s.ChannelID,
		})
	}
	return out
}

// bytes 占位（确保 import）
var _ = bytes.NewReader
var _ = json.Marshal
