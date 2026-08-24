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
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/client/tongyi"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/chunk"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	"github.com/Tencent/WeKnora/internal/custom/service/subtitle"
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
	// 找前驱 transcription job 的 result_payload
	var prev model.VideoProcessingJob
	if err := h.DB.Where("video_id = ? AND job_type = ? AND status = ?",
		video.ID, "transcription", "succeeded").
		Order("completed_at DESC").First(&prev).Error; err != nil {
		return fmt.Errorf("无成功 transcription job: %w", err)
	}

	var payload struct {
		TaskID    string `json:"task_id"`
		RawResult string `json:"raw_result"`
	}
	if err := json.Unmarshal([]byte(prev.ResultPayload), &payload); err != nil {
		return fmt.Errorf("parse transcription result: %w", err)
	}

	// 下载并解析转写 JSON
	transcript, err := h.Tongyi.DownloadResult(ctx, payload.RawResult)
	if err != nil {
		return fmt.Errorf("download transcript: %w", err)
	}
	if len(transcript.Transcripts) == 0 {
		return fmt.Errorf("听悟结果不含转写文件")
	}

	// 汇总所有段落（多文件拼接，本版本取第一个文件的段落）
	src := transcript.Transcripts[0]
	paragraphs := make([]subtitle.TranscriptParagraph, 0, len(src.Paragraphs))
	for _, p := range src.Paragraphs {
		paragraphs = append(paragraphs, subtitle.TranscriptParagraph{
			ParagraphID: p.ParagraphID,
			SpeakerID:   p.SpeakerID,
			StartMs:     p.StartMs,
			EndMs:       p.EndMs,
			Sentences:   toSentences(p.Sentences),
		})
	}

	// 生成 SRT
	srt := subtitle.ParagraphsToSRT(paragraphs)

	// 入对象存储
	objectKey := fmt.Sprintf("subtitles/%s/transcript.srt", video.ID)
	if err := uploadBytes(ctx, h.MinIO, objectKey, []byte(srt), "application/x-subrip"); err != nil {
		return fmt.Errorf("upload srt: %w", err)
	}
	subtitleURL := h.MinIO.PublicURL(objectKey)

	if err := h.DB.Model(video).Update("subtitle_file_url", subtitleURL).Error; err != nil {
		return fmt.Errorf("update subtitle url: %w", err)
	}

	// 把段落结果暂存到本 job 的 result_payload，供 index 读取
	storePayload, err := json.Marshal(map[string]any{
		"paragraphs": paragraphs,
		"language":   "zh",
	})
	if err != nil {
		return fmt.Errorf("marshal subtitle result: %w", err)
	}

	// 触发 index job
	indexJob := model.VideoProcessingJob{
		ID:             uuid.NewString(),
		VideoID:        video.ID,
		JobType:        "index",
		Provider:       "weknora",
		Status:         "pending",
		MaxAttempts:    3,
		IdempotencyKey: fmt.Sprintf("index:%s:%x", video.ID, sha256.Sum256(storePayload)),
		ResultPayload:  string(storePayload),
	}
	return h.DB.Transaction(func(tx *gorm.DB) error {
		var locked model.Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", video.ID).Error; err != nil {
			return fmt.Errorf("lock video revision: %w", err)
		}
		revision := locked.TranscriptRevision + 1
		if err := tx.Model(&locked).Update("transcript_revision", revision).Error; err != nil {
			return fmt.Errorf("advance transcript revision: %w", err)
		}
		indexJob.InputPayload = fmt.Sprintf(`{"revision":%d}`, revision)
		if err := tx.Model(job).Update("result_payload", string(storePayload)).Error; err != nil {
			return fmt.Errorf("save subtitle result: %w", err)
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true}).Create(&indexJob).Error; err != nil {
			return fmt.Errorf("enqueue index job: %w", err)
		}
		return nil
	})
}

// IndexHandler 句子级分块 + 12 字段 metadata + 入 WeKnora KB（VP-T009 + CP-T005）
type IndexHandler struct {
	DB           *gorm.DB
	WeKnora      *weknora.Client
	Splitter     *chunk.Splitter
	Orchestrator *skill.Orchestrator
}

// NewIndexHandler 构造
func NewIndexHandler(db *gorm.DB, wk *weknora.Client, orch *skill.Orchestrator) *IndexHandler {
	return &IndexHandler{DB: db, WeKnora: wk, Splitter: chunk.NewSplitter(), Orchestrator: orch}
}

// JobType job 类型
func (h *IndexHandler) JobType() string { return "index" }

// Run 句子级分块 → 入 WeKnora KB → 回写 transcript_knowledge_id
func (h *IndexHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
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
		Index       int
		Content     string
		ContentHash string
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
		prepared = append(prepared, preparedChunk{Index: b.Metadata.ChunkIndex, Content: content, ContentHash: contentHash})
	}
	generation := fmt.Sprintf("%x", generationHash.Sum(nil))

	var firstKnowledgeID string
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
				ContentHash: item.ContentHash, Status: "created",
			}
			if err := h.DB.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "video_id"}, {Name: "generation"}, {Name: "chunk_index"}},
				DoUpdates: clause.AssignmentColumns([]string{"knowledge_id", "content_hash", "status", "updated_at"}),
			}).Create(&checkpoint).Error; err != nil {
				return fmt.Errorf("save transcript checkpoint %d: %w", item.Index, err)
			}
		}
		if firstKnowledgeID == "" {
			firstKnowledgeID = checkpoint.KnowledgeID
		}
	}

	if err := h.waitUntilSearchable(ctx, video.ID, generation); err != nil {
		return err
	}
	// 先原子切换到已完成的新 generation；远端旧数据清理失败不能破坏当前可用版本。
	activated := false
	if err := h.DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Video{}).
			Where("id = ? AND transcript_active_revision < ?", video.ID, jobInput.Revision).
			Updates(map[string]any{
				"transcript_knowledge_id": firstKnowledgeID, "transcript_generation": generation,
				"transcript_active_revision": jobInput.Revision, "status": model.VideoStatusProcessing,
			})
		if result.Error != nil {
			return result.Error
		}
		activated = result.RowsAffected == 1
		if !activated {
			return nil
		}
		return tx.Model(&model.VideoTranscriptChunk{}).
			Where("video_id = ? AND revision < ?", video.ID, jobInput.Revision).
			Update("status", "cleanup_pending").Error
	}); err != nil {
		return fmt.Errorf("activate transcript generation: %w", err)
	}
	if !activated {
		if err := h.DB.Model(&model.VideoTranscriptChunk{}).
			Where("video_id = ? AND generation = ?", video.ID, generation).
			Update("status", "cleanup_pending").Error; err != nil {
			return fmt.Errorf("retire stale transcript generation: %w", err)
		}
		return h.deleteRetiredGenerations(ctx, video.ID)
	}

	// 触发内容生产第一环：extract-video-knowledge（CP-T005）
	if h.Orchestrator != nil {
		if _, err := h.Orchestrator.EnqueueJob(ctx, video.ID, skill.JobGraph); err != nil {
			// 失败不阻塞视频播放；内容链路可后续手动触发。
			return fmt.Errorf("enqueue graph job: %w", err)
		}
	}
	if err := h.deleteRetiredGenerations(ctx, video.ID); err != nil {
		return err
	}
	return nil
}

func (h *IndexHandler) waitUntilSearchable(ctx context.Context, videoID, generation string) error {
	deadline := time.NewTimer(10 * time.Minute)
	ticker := time.NewTicker(2 * time.Second)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		var checkpoints []model.VideoTranscriptChunk
		if err := h.DB.Where("video_id = ? AND generation = ?", videoID, generation).Order("chunk_index ASC").Find(&checkpoints).Error; err != nil {
			return fmt.Errorf("list transcript checkpoints: %w", err)
		}
		allCompleted := len(checkpoints) > 0
		for i := range checkpoints {
			if checkpoints[i].Status == "completed" {
				continue
			}
			knowledge, err := h.WeKnora.GetKnowledge(ctx, checkpoints[i].KnowledgeID)
			if err != nil {
				return fmt.Errorf("check knowledge %s: %w", checkpoints[i].KnowledgeID, err)
			}
			switch knowledge.ParseStatus {
			case "completed":
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
