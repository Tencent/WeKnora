package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/llm"
	"github.com/Tencent/WeKnora/internal/custom/client/mps"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/evidence"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgeprojection"
	"github.com/Tencent/WeKnora/internal/custom/service/outline"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

type DirectContentHandler struct {
	DB            *gorm.DB
	LLM           directContentLLM
	WeKnora       *weknora.Client // fixed evidence adapter
	Wiki          *weknora.WikiClient
	Orchestrator  *skill.Orchestrator
	KnowledgeKBID string
	Job           string
}

type directContentLLM interface {
	Complete(context.Context, string) (string, error)
	CompleteJSON(context.Context, string) (string, error)
	Stream(context.Context, string, func(string) error) (string, error)
	Model() string
	PromptVersion() string
}

var errDiscardSummaryResult = errors.New("discard summary result")

func NewDirectContentHandler(db *gorm.DB, client *llm.Client, wk *weknora.Client, wiki *weknora.WikiClient, orchestrator *skill.Orchestrator, jobType string) *DirectContentHandler {
	knowledgeKBID := ""
	if orchestrator != nil {
		knowledgeKBID = orchestrator.KBID
	}
	return &DirectContentHandler{DB: db, LLM: client, WeKnora: wk, Wiki: wiki, Orchestrator: orchestrator, KnowledgeKBID: knowledgeKBID, Job: jobType}
}

func (h *DirectContentHandler) JobType() string { return h.Job }

func (h *DirectContentHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	if h.DB == nil || h.WeKnora == nil || h.Wiki == nil || h.Orchestrator == nil {
		return fmt.Errorf("direct content handler dependencies are not configured")
	}
	if strings.TrimSpace(h.KnowledgeKBID) == "" {
		return fmt.Errorf("knowledge_base_routing:knowledge_kb_missing")
	}
	if h.WeKnora.KBID() == h.KnowledgeKBID {
		return fmt.Errorf("knowledge_base_routing:kb_role_conflict")
	}
	if h.Job != skill.JobOutline && h.Job != skill.JobSummary && h.Job != skill.JobSummaryEnhance {
		return fmt.Errorf("unsupported direct content job: %s", h.Job)
	}
	if h.Job == skill.JobSummary || h.Job == skill.JobSummaryEnhance {
		explicitRegeneration := skill.IsExplicitSummaryRegeneration(job.InputPayload)
		protected, err := h.Orchestrator.IsSummaryUserEditProtected(ctx, video.ID)
		if err != nil {
			return fmt.Errorf("check summary user edit protection: %w", err)
		}
		if protected && !explicitRegeneration {
			return nil
		}
	}

	generation := strings.TrimSpace(job.TranscriptGeneration)
	if generation == "" {
		generation = strings.TrimSpace(video.TranscriptGeneration)
	}
	if generation == "" {
		return fmt.Errorf("视频 %s 的转写代次不可用或已过期", video.ID)
	}
	if h.Job == skill.JobOutline && job.ResultStage != "draft" {
		promoted, err := h.promoteOutlineDraft(ctx, job, video, generation)
		if err != nil {
			return err
		}
		if promoted {
			return nil
		}
	}
	if h.LLM == nil {
		return fmt.Errorf("direct content LLM dependency is not configured")
	}
	chunks, err := h.readContentChunks(ctx, video, generation, job)
	if err != nil {
		return err
	}
	prompt, err := buildDirectContentPrompt(video, h.Job, chunks)
	if err != nil {
		return err
	}
	if h.Job == skill.JobSummaryEnhance {
		prompt, err = h.addEnhancementContext(ctx, video, prompt)
		if err != nil {
			return err
		}
	}
	if skill.IsExplicitSummaryRegeneration(job.InputPayload) {
		prompt += "\n这是用户明确发起的历史总结重生成：允许覆盖旧总结，必须重新生成符合当前 JSON 契约且带观点级原文证据的内容。"
	}
	contract, ok := skill.Contract(h.Job)
	if !ok {
		return fmt.Errorf("unknown direct content job: %s", h.Job)
	}
	raw := ""
	isStreamingOutlineDraft := h.Job == skill.JobOutline && job.ResultStage == "draft"
	if isStreamingOutlineDraft {
		raw, err = h.streamOutlineDraft(ctx, prompt, video, generation, chunks, contract.WriteSlug(video.ID)+"/draft", job.ID)
		if err != nil && (strings.Contains(err.Error(), "stream contains no content") || strings.Contains(err.Error(), "decode llm stream event")) {
			// Some OpenAI-compatible gateways accept stream=true but still return
			// one regular completion. Preserve compatibility with those gateways.
			raw, err = h.LLM.Complete(ctx, prompt)
		}
	}
	if err != nil {
		return fmt.Errorf("generate %s: %w", h.Job, err)
	}
	pageTitle := ""
	pageSummary := ""
	pageBody := ""
	generatedSummaryType := ""
	var outlineDocument outline.Document
	if h.Job == skill.JobOutline {
		knownChunkIDs := make(map[string]struct{}, len(chunks))
		for _, chunk := range chunks {
			knownChunkIDs[chunk.ID] = struct{}{}
		}
		transcriptEndSeconds, err := transcript.EffectiveEndSeconds(chunks)
		if err != nil {
			return fmt.Errorf("validate %s input: %w", h.Job, err)
		}
		var validationErr error
		for attempt := 0; attempt < 2; attempt++ {
			if !isStreamingOutlineDraft || attempt > 0 {
				generationPrompt := prompt
				if attempt > 0 {
					generationPrompt = outlineRetryPrompt(prompt, validationErr)
				}
				raw, err = h.LLM.CompleteJSON(ctx, generationPrompt)
				if err != nil {
					validationErr = fmt.Errorf("generate %s output: %w", h.Job, err)
					if isInvalidStructuredOutput(err) {
						continue
					}
					return validationErr
				}
			}
			outlineDocument = outline.Document{}
			if err := parseLLMJSONResponse(raw, &outlineDocument); err != nil {
				validationErr = fmt.Errorf("parse %s output: %w", h.Job, err)
				continue
			}
			normalizeOutlineEvidenceChunkIDs(&outlineDocument, chunks)
			if err := outline.ValidateWithTranscriptEnd(outlineDocument, video.DurationSeconds, transcriptEndSeconds, knownChunkIDs); err != nil {
				validationErr = fmt.Errorf("validate %s output: %w", h.Job, err)
				continue
			}
			validationErr = nil
			break
		}
		if validationErr != nil {
			return validationErr
		}
		if err := outline.ValidateAndResolve(&outlineDocument, video.DurationSeconds, chunks); err != nil {
			return fmt.Errorf("validate %s evidence: %w", h.Job, err)
		}
		canonical, err := outline.Marshal(outlineDocument)
		if err != nil {
			return fmt.Errorf("marshal %s output: %w", h.Job, err)
		}
		pageTitle = video.Title + "_大纲"
		pageBody = canonical
	} else {
		knownChunkIDs := make(map[string]struct{}, len(chunks))
		for _, chunk := range chunks {
			knownChunkIDs[chunk.ID] = struct{}{}
		}
		var document summary.Document
		var validationErr error
		for attempt := 0; attempt < 2; attempt++ {
			generationPrompt := prompt
			if attempt > 0 {
				generationPrompt = prompt + fmt.Sprintf("\n上一轮总结未通过严格校验，必须修正后重新输出完整 JSON。校验错误：%s。字段名必须严格使用 schemaVersion、videoType、classification、sections、evidenceChunkIds，禁止使用 schema_version、video_type、evidence_chunk_ids；schemaVersion 必须为数字 %d。classification 必须包含 confidence、reason 和 evidenceChunkIds；先确认 videoType，再从候选模板中逐项复制该类型的全部 section.id 和 section.title，禁止使用其他类型的章节、禁止改名、禁止遗漏；只能从上文转写分块列表复制判型和正文 evidenceChunkIds，不得创造、猜测或引用不存在的 ID；可以使用纯知识 ID或带 |分片序号的显示 ID，系统会归一化。", validationErr.Error(), summary.SchemaVersion)
			}
			raw, err = h.LLM.CompleteJSON(ctx, generationPrompt)
			if err != nil {
				validationErr = fmt.Errorf("generate %s output: %w", h.Job, err)
				if isInvalidStructuredOutput(err) {
					continue
				}
				return validationErr
			}
			document = summary.Document{}
			if err := parseLLMJSONResponse(raw, &document); err != nil {
				validationErr = fmt.Errorf("parse %s output: %w", h.Job, err)
				continue
			}
			summary.NormalizeEvidenceChunkIDs(&document, chunks)
			if h.Job == skill.JobSummary {
				if err := summary.NormalizeOrchestrationProfileReferences(&document, knownChunkIDs); err != nil {
					validationErr = fmt.Errorf("normalize %s orchestration profile: %w", h.Job, err)
					continue
				}
			}
			expectedVideoType := video.VideoType
			if h.Job == skill.JobSummary {
				// The first summary is routed from the transcript itself. The upload
				// type is only a weak hint and must not lock the framework.
				expectedVideoType = ""
			}
			validateSummary := summary.Validate
			if h.Job == skill.JobSummary && job.ResultStage != "draft" {
				validateSummary = summary.ValidateGenerated
			}
			if err := validateSummary(document, expectedVideoType, knownChunkIDs); err != nil {
				validationErr = fmt.Errorf("validate %s output: %w", h.Job, err)
				continue
			}
			if h.Job == skill.JobSummary {
				if err := summary.ValidateClassification(document, knownChunkIDs); err != nil {
					validationErr = fmt.Errorf("validate %s classification: %w", h.Job, err)
					continue
				}
			}
			if err := summary.ResolveEvidence(&document, chunks); err != nil {
				validationErr = fmt.Errorf("resolve %s evidence: %w", h.Job, err)
				continue
			}
			if h.Job == skill.JobSummaryEnhance {
				if err := h.bindSummaryKnowledgeRefs(ctx, video, &document, generation); err != nil {
					validationErr = fmt.Errorf("bind %s knowledge references: %w", h.Job, err)
					continue
				}
				if err := h.validateEnhancedSummary(ctx, video, &document); err != nil {
					validationErr = fmt.Errorf("validate %s structure: %w", h.Job, err)
					continue
				}
			}
			generatedSummaryType = document.VideoType
			validationErr = nil
			break
		}
		if validationErr != nil {
			return validationErr
		}
		canonical, err := json.Marshal(document)
		if err != nil {
			return fmt.Errorf("marshal %s output: %w", h.Job, err)
		}
		pageTitle = video.Title + "_知识总结"
		pageBody = string(canonical)
	}
	if h.Job == skill.JobSummary || h.Job == skill.JobSummaryEnhance {
		if err := h.ensureSummaryWriteIsCurrent(ctx, video.ID, generation, skill.IsExplicitSummaryRegeneration(job.InputPayload)); err != nil {
			if errors.Is(err, errDiscardSummaryResult) {
				return nil
			}
			return err
		}
	}
	pageSlug := contract.WriteSlug(video.ID)
	if job.ResultStage == "draft" {
		pageSlug += "/draft"
		// A draft may have started before indexing activated a newer
		// transcript generation. Re-check the durable video state before
		// writing the shared draft slug so a late old response cannot replace
		// the current draft page.
		var current model.Video
		if err := h.DB.WithContext(ctx).Select("transcript_generation").First(&current, "id = ?", video.ID).Error; err != nil {
			return fmt.Errorf("check draft transcript generation: %w", err)
		}
		if strings.TrimSpace(current.TranscriptGeneration) != "" && strings.TrimSpace(current.TranscriptGeneration) != generation {
			slog.Info("discard stale draft result", "video_id", video.ID, "job_id", job.ID, "job_generation", generation, "active_generation", current.TranscriptGeneration)
			return nil
		}
	}
	page, err := h.Wiki.UpsertPage(ctx, h.KnowledgeKBID, weknora.WikiPageWrite{
		Slug:     pageSlug,
		Title:    pageTitle,
		PageType: "index",
		Status:   "published",
		Summary:  pageSummary,
		Content:  pageContent(contract.ArtifactType, video.ID, generation, pageBody),
	})
	if err != nil {
		return fmt.Errorf("save %s page: %w", h.Job, err)
	}
	if h.Job == skill.JobSummary && job.ResultStage != "draft" && generatedSummaryType != "" {
		if err := h.DB.WithContext(ctx).Model(&model.Video{}).Where("id = ?", video.ID).Update("video_type", generatedSummaryType).Error; err != nil {
			return fmt.Errorf("save classified video type: %w", err)
		}
	}
	result, _ := json.Marshal(map[string]any{
		"provider":              "llm",
		"model":                 h.LLM.Model(),
		"prompt_version":        h.LLM.PromptVersion(),
		"wiki_page_id":          page.ID,
		"transcript_generation": generation,
		"result_stage":          job.ResultStage,
	})
	if err := h.DB.WithContext(ctx).Model(job).Update("result_payload", string(result)).Error; err != nil {
		return fmt.Errorf("save %s result: %w", h.Job, err)
	}
	if job.ResultStage == "draft" {
		field := "outline_draft_wiki_page_id"
		stageField := "outline_result_stage"
		if h.Job == skill.JobSummary {
			field, stageField = "summary_draft_wiki_page_id", "summary_result_stage"
		}
		if err := h.persistDraftReference(ctx, video.ID, generation, field, stageField, page.ID, job.ID); err != nil {
			return err
		}
		return nil
	}
	var completeErr error
	if skill.IsExplicitSummaryRegeneration(job.InputPayload) {
		_, _, completeErr = h.Orchestrator.AfterExplicitSummaryRegeneration(ctx, video.ID, h.Job, page.ID)
	} else {
		_, _, completeErr = h.Orchestrator.AfterSkillCompleteWithID(ctx, video.ID, h.Job, page.ID)
	}
	if completeErr != nil {
		return completeErr
	}
	stageField := "outline_result_stage"
	if h.Job == skill.JobSummary || h.Job == skill.JobSummaryEnhance {
		stageField = "summary_result_stage"
	}
	if err := h.DB.WithContext(ctx).Model(&model.Video{}).Where("id = ?", video.ID).Update(stageField, "final_ready").Error; err != nil {
		return err
	}
	return nil
}

func isInvalidStructuredOutput(err error) bool {
	var invalid interface{ InvalidOutput() bool }
	return errors.As(err, &invalid) && invalid.InvalidOutput()
}

// bindSummaryKnowledgeRefs is deliberately deterministic: the model chooses
// summary wording and evidence chunks, while only audited object pages can
// supply knowledge_refs. This keeps page identity and evidence ownership
// outside model output.
func (h *DirectContentHandler) bindSummaryKnowledgeRefs(ctx context.Context, video *model.Video, document *summary.Document, generation string) error {
	pages, err := h.Wiki.ListAllPages(ctx, h.KnowledgeKBID, "")
	if err != nil {
		return fmt.Errorf("list knowledge object pages: %w", err)
	}
	scope := knowledgeprojection.SummaryReferenceScope{
		VideoID: video.ID, TranscriptGeneration: generation,
		Pages: make(map[string]knowledgeprojection.ObjectPageResult),
	}
	bindings := make([]knowledgeprojection.SummaryKnowledgeBinding, 0)
	for _, page := range pages {
		if page.PageType != "index" || strings.TrimSpace(page.ID) == "" {
			continue
		}
		frontmatter := page.ParsedFrontmatter()
		if strings.TrimSpace(frontmatterString(frontmatter, "source_video_id")) != video.ID ||
			strings.TrimSpace(frontmatterString(frontmatter, "transcript_generation")) != generation ||
			strings.ToLower(strings.TrimSpace(frontmatterString(frontmatter, "audit_status"))) != "passed" {
			continue
		}
		objectType := strings.TrimSpace(frontmatterString(frontmatter, "primary_type"))
		if objectType == "" {
			objectType = strings.TrimSpace(frontmatterString(frontmatter, "type"))
		}
		if !isSummaryKnowledgeType(objectType) {
			continue
		}
		evidenceIDs := frontmatterStringSlice(frontmatter, "evidence_ids")
		chunkRefs := frontmatterStringSlice(frontmatter, "chunk_refs")
		if len(evidenceIDs) == 0 && len(chunkRefs) == 0 {
			continue
		}
		scope.Pages[page.ID] = knowledgeprojection.ObjectPageResult{
			WikiPageID: page.ID, SourceVideoID: video.ID, TranscriptGeneration: generation,
		}
		bindings = append(bindings, knowledgeprojection.SummaryKnowledgeBinding{
			WikiPageID: page.ID, EvidenceIDs: evidenceIDs, ChunkRefs: chunkRefs,
		})
	}
	if len(bindings) == 0 {
		return fmt.Errorf("no audited knowledge object pages for active generation")
	}
	return knowledgeprojection.BindSummaryKnowledgeReferences(document, bindings, scope)
}

func isSummaryKnowledgeType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "entity", "concept", "methodology", "case", "insight":
		return true
	default:
		return false
	}
}

func frontmatterString(frontmatter map[string]any, key string) string {
	value, _ := frontmatter[key].(string)
	return value
}

func frontmatterStringSlice(frontmatter map[string]any, key string) []string {
	value := frontmatter[key]
	result := make([]string, 0)
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, strings.TrimSpace(text))
			}
		}
	case []string:
		for _, item := range typed {
			if strings.TrimSpace(item) != "" {
				result = append(result, strings.TrimSpace(item))
			}
		}
	}
	return result
}

func (h *DirectContentHandler) streamOutlineDraft(ctx context.Context, prompt string, video *model.Video, generation string, chunks []transcript.Chunk, pageSlug, jobID string) (string, error) {
	knownChunkIDs := make(map[string]struct{}, len(chunks))
	for _, chunk := range chunks {
		knownChunkIDs[chunk.ID] = struct{}{}
	}
	var accumulated strings.Builder
	published := 0
	raw, err := h.LLM.Stream(ctx, prompt, func(delta string) error {
		accumulated.WriteString(delta)
		chapters := completeOutlineChapters(accumulated.String())
		for published < len(chapters) {
			document := outline.Document{SchemaVersion: outline.SchemaVersion, Chapters: append([]outline.Chapter(nil), chapters[:published+1]...)}
			normalizeOutlineEvidenceChunkIDs(&document, chunks)
			if err := outline.ValidatePartial(document, video.DurationSeconds, knownChunkIDs); err != nil {
				break
			}
			if err := h.publishOutlinePrefix(ctx, video, generation, pageSlug, document, jobID); err != nil {
				return err
			}
			published++
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

func (h *DirectContentHandler) publishOutlinePrefix(ctx context.Context, video *model.Video, generation, pageSlug string, document outline.Document, jobID string) error {
	content, err := outline.Marshal(document)
	if err != nil {
		return fmt.Errorf("marshal outline progress: %w", err)
	}
	page, err := h.Wiki.UpsertPage(ctx, h.KnowledgeKBID, weknora.WikiPageWrite{
		Slug: pageSlug, Title: video.Title + "_大纲", PageType: "index", Status: "published",
		Content: pageContentWithProgress("outline", video.ID, generation, content, true, len(document.Chapters), 0),
	})
	if err != nil {
		return fmt.Errorf("save outline progress: %w", err)
	}
	if err := h.persistDraftReference(ctx, video.ID, generation, "outline_draft_wiki_page_id", "outline_result_stage", page.ID, jobID); err != nil {
		return fmt.Errorf("save outline progress reference: %w", err)
	}
	return nil
}

func completeOutlineChapters(raw string) []outline.Chapter {
	marker := strings.Index(raw, `"chapters"`)
	if marker < 0 {
		return nil
	}
	start := strings.IndexByte(raw[marker:], '[')
	if start < 0 {
		return nil
	}
	start += marker + 1
	chapters := make([]outline.Chapter, 0, 8)
	for index := start; index < len(raw); {
		for index < len(raw) && (raw[index] == ' ' || raw[index] == '\n' || raw[index] == '\r' || raw[index] == '\t' || raw[index] == ',') {
			index++
		}
		if index >= len(raw) || raw[index] != '{' {
			break
		}
		end := balancedJSONObjectEnd(raw, index)
		if end < 0 {
			break
		}
		var chapter outline.Chapter
		if json.Unmarshal([]byte(raw[index:end]), &chapter) != nil {
			break
		}
		chapters = append(chapters, chapter)
		index = end
	}
	return chapters
}

func balancedJSONObjectEnd(raw string, start int) int {
	depth := 0
	quoted, escaped := false, false
	for index := start; index < len(raw); index++ {
		char := raw[index]
		if quoted {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				quoted = false
			}
			continue
		}
		switch char {
		case '"':
			quoted = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index + 1
			}
		}
	}
	return -1
}

func (h *DirectContentHandler) persistDraftReference(ctx context.Context, videoID, generation, field, stageField, pageID, jobID string) error {
	result := h.DB.WithContext(ctx).Model(&model.Video{}).
		Where("id = ? AND (transcript_generation = '' OR transcript_generation = ?) AND ("+stageField+" IS NULL OR "+stageField+" <> ?)", videoID, generation, "final_ready").
		Updates(map[string]any{field: pageID, stageField: "draft_ready"})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		slog.Info("discard stale draft reference", "video_id", videoID, "job_id", jobID, "job_generation", generation)
	}
	return nil
}

func (h *DirectContentHandler) ensureSummaryWriteIsCurrent(ctx context.Context, videoID, generation string, explicitRegeneration bool) error {
	var current model.Video
	if err := h.DB.WithContext(ctx).Select("id", "transcript_generation").First(&current, "id = ?", videoID).Error; err != nil {
		return fmt.Errorf("recheck summary transcript generation: %w", err)
	}
	if strings.TrimSpace(current.TranscriptGeneration) != strings.TrimSpace(generation) {
		return fmt.Errorf("%w for video %s: transcript generation changed", errDiscardSummaryResult, videoID)
	}
	if !explicitRegeneration {
		protected, err := h.Orchestrator.IsSummaryUserEditProtected(ctx, videoID)
		if err != nil {
			return fmt.Errorf("recheck summary user edit protection: %w", err)
		}
		if protected {
			return fmt.Errorf("%w for video %s: summary is user edited", errDiscardSummaryResult, videoID)
		}
	}
	return nil
}

func (h *DirectContentHandler) validateEnhancedSummary(ctx context.Context, video *model.Video, enhanced *summary.Document) error {
	if strings.TrimSpace(video.SummaryWikiPageID) == "" {
		return fmt.Errorf("base summary page is missing")
	}
	page, err := h.Wiki.GetPageByID(ctx, h.KnowledgeKBID, video.SummaryWikiPageID)
	if err != nil {
		return fmt.Errorf("read base summary: %w", err)
	}
	if page == nil || strings.TrimSpace(page.Content) == "" {
		return fmt.Errorf("base summary page is not readable")
	}
	base, err := summary.ParseStored(page.Content)
	if err != nil {
		return fmt.Errorf("parse base summary: %w", err)
	}
	if err := summary.Validate(base, video.VideoType, nil); err != nil {
		return fmt.Errorf("validate base summary: %w", err)
	}
	if err := summary.PreserveOrchestrationProfile(&base, enhanced); err != nil {
		return err
	}
	return summary.ValidateEnhancement(base, *enhanced)
}

// readContentChunks keeps the two-stage exception narrow: outline and summary
// drafts may use the just-finished MPS result for immediate first-pass content,
// while final and enhancement stages remain gated on the immutable
// current-generation evidence index.
func (h *DirectContentHandler) readContentChunks(ctx context.Context, video *model.Video, generation string, job *model.VideoProcessingJob) ([]transcript.Chunk, error) {
	if job.ResultStage == "draft" && (h.Job == skill.JobOutline || h.Job == skill.JobSummary) {
		return h.readDraftChunks(ctx, video.ID, generation, job)
	}
	if generation != strings.TrimSpace(video.TranscriptGeneration) {
		return nil, fmt.Errorf("视频 %s 的转写代次不可用或已过期", video.ID)
	}
	records, err := evidence.NewEvidenceIndex(h.DB, h.WeKnora).Read(ctx, video.ID, generation)
	if err != nil {
		return nil, fmt.Errorf("read %s evidence index: %w", h.Job, err)
	}
	return evidenceRecordsToTranscriptChunks(records), nil
}

// promoteOutlineDraft reuses a valid draft produced directly from the MPS
// result. Once the active evidence index is ready, the draft is normalized to
// current knowledge IDs, revalidated, and copied to the final outline slug.
// This keeps the normal path from invoking the model twice for one transcript.
func (h *DirectContentHandler) promoteOutlineDraft(ctx context.Context, job *model.VideoProcessingJob, video *model.Video, generation string) (bool, error) {
	if strings.TrimSpace(video.OutlineDraftWikiPageID) == "" {
		return false, nil
	}
	draftPage, err := h.Wiki.GetPageByID(ctx, h.KnowledgeKBID, video.OutlineDraftWikiPageID)
	if err != nil {
		return false, fmt.Errorf("read outline draft: %w", err)
	}
	if draftPage == nil {
		return false, nil
	}
	frontmatter := draftPage.ParsedFrontmatter()
	pageType, _ := frontmatter["type"].(string)
	sourceVideoID, _ := frontmatter["source_video_id"].(string)
	pageGeneration, _ := frontmatter["transcript_generation"].(string)
	if pageType != "outline" || sourceVideoID != video.ID || strings.TrimSpace(pageGeneration) != generation {
		return false, nil
	}
	document, err := outline.Parse(draftPage.Content)
	if err != nil {
		slog.Warn("outline draft is not promotable", "video_id", video.ID, "page_id", draftPage.ID, "error", err)
		return false, nil
	}
	records, err := evidence.NewEvidenceIndex(h.DB, h.WeKnora).Read(ctx, video.ID, generation)
	if err != nil {
		return false, fmt.Errorf("read outline promotion evidence index: %w", err)
	}
	chunks := evidenceRecordsToTranscriptChunks(records)
	normalizeOutlineEvidenceChunkIDs(&document, chunks)
	if err := outline.ValidateAndResolve(&document, video.DurationSeconds, chunks); err != nil {
		slog.Warn("outline draft is not promotable", "video_id", video.ID, "page_id", draftPage.ID, "error", err)
		return false, nil
	}
	canonical, err := outline.Marshal(document)
	if err != nil {
		return false, fmt.Errorf("marshal promoted outline: %w", err)
	}
	contract, ok := skill.Contract(skill.JobOutline)
	if !ok {
		return false, fmt.Errorf("unknown outline contract")
	}
	page, err := h.Wiki.UpsertPage(ctx, h.KnowledgeKBID, weknora.WikiPageWrite{
		Slug:     contract.WriteSlug(video.ID),
		Title:    video.Title + "_大纲",
		PageType: "index",
		Status:   "published",
		Content:  pageContent(contract.ArtifactType, video.ID, generation, canonical),
	})
	if err != nil {
		return false, fmt.Errorf("save promoted outline: %w", err)
	}
	result, _ := json.Marshal(map[string]any{
		"provider":              "draft_promotion",
		"wiki_page_id":          page.ID,
		"transcript_generation": generation,
		"result_stage":          "final",
		"source_draft_page_id":  draftPage.ID,
	})
	if err := h.DB.WithContext(ctx).Model(job).Update("result_payload", string(result)).Error; err != nil {
		return false, fmt.Errorf("save promoted outline result: %w", err)
	}
	if _, _, err := h.Orchestrator.AfterSkillCompleteWithID(ctx, video.ID, skill.JobOutline, page.ID); err != nil {
		return false, fmt.Errorf("complete promoted outline: %w", err)
	}
	if err := h.DB.WithContext(ctx).Model(&model.Video{}).Where("id = ?", video.ID).Update("outline_result_stage", "final_ready").Error; err != nil {
		return false, fmt.Errorf("mark promoted outline final: %w", err)
	}
	return true, nil
}

func evidenceRecordsToTranscriptChunks(records []evidence.Record) []transcript.Chunk {
	chunks := make([]transcript.Chunk, 0, len(records))
	for _, record := range records {
		chunks = append(chunks, transcript.Chunk{
			ID: record.KnowledgeID, EvidenceSentenceID: record.EvidenceSentenceID,
			SourceSentenceID: record.SourceSentenceID, SpeakerID: record.SpeakerID,
			Index: record.ChunkIndex, Content: record.Text, StartMs: record.StartMs, EndMs: record.EndMs,
		})
	}
	return chunks
}

func (h *DirectContentHandler) readDraftChunks(ctx context.Context, videoID, generation string, job *model.VideoProcessingJob) ([]transcript.Chunk, error) {
	var input struct {
		TranscriptionJobID string `json:"transcription_job_id"`
	}
	if err := json.Unmarshal([]byte(job.InputPayload), &input); err != nil || input.TranscriptionJobID == "" {
		return nil, fmt.Errorf("draft transcription job reference is missing")
	}
	var source model.VideoProcessingJob
	if err := h.DB.WithContext(ctx).First(&source, "id = ? AND video_id = ?", input.TranscriptionJobID, videoID).Error; err != nil {
		return nil, fmt.Errorf("load draft transcription result: %w", err)
	}
	var payload struct {
		MPSResult *mps.Result `json:"mps_result"`
	}
	if err := json.Unmarshal([]byte(source.ResultPayload), &payload); err != nil || payload.MPSResult == nil {
		return nil, fmt.Errorf("draft MPS result is unavailable")
	}
	chunks := make([]transcript.Chunk, 0, len(payload.MPSResult.Segments))
	manifest := make([]evidence.Sentence, 0, len(payload.MPSResult.Segments))
	for i, segment := range payload.MPSResult.Segments {
		if strings.TrimSpace(segment.Text) == "" || segment.EndMs <= segment.StartMs {
			continue
		}
		id := segment.SourceSegmentID
		if id == "" {
			id = fmt.Sprintf("mps:%s:%06d", source.ExternalTaskID, i)
		}
		ordinal := len(chunks)
		sentence, err := evidence.BuildSentence(evidence.Input{
			VideoID: videoID, TranscriptGeneration: generation, Ordinal: ordinal,
			SourceSentenceID: id, Text: segment.Text, SpeakerID: segment.SpeakerID,
			StartMs: segment.StartMs, EndMs: segment.EndMs,
		})
		if err != nil {
			return nil, fmt.Errorf("freeze draft evidence sentence %d: %w", ordinal, err)
		}
		chunks = append(chunks, transcript.Chunk{ID: id, EvidenceSentenceID: sentence.ID, SourceSentenceID: id, SpeakerID: segment.SpeakerID, Index: ordinal, Content: segment.Text, StartMs: segment.StartMs, EndMs: segment.EndMs})
		manifest = append(manifest, sentence)
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("draft transcript has no timed segments")
	}
	if err := evidence.ValidateManifest(manifest, videoID, generation); err != nil {
		return nil, fmt.Errorf("validate draft evidence sentence manifest: %w", err)
	}
	return chunks, nil
}

func normalizeOutlineEvidenceChunkIDs(document *outline.Document, chunks []transcript.Chunk) {
	aliases := make(map[string]string, len(chunks)*2)
	for _, chunk := range chunks {
		aliases[chunk.ID] = chunk.ID
		aliases[fmt.Sprintf("%s|%06d", chunk.ID, chunk.Index)] = chunk.ID
		if sourceID := strings.TrimSpace(chunk.SourceSentenceID); sourceID != "" {
			aliases[sourceID] = chunk.ID
			aliases[fmt.Sprintf("%s|%06d", sourceID, chunk.Index)] = chunk.ID
		}
	}
	for chapterIndex := range document.Chapters {
		chapter := &document.Chapters[chapterIndex]
		for index, chunkID := range chapter.EvidenceChunkIDs {
			if normalized, ok := aliases[chunkID]; ok {
				chapter.EvidenceChunkIDs[index] = normalized
			}
		}
		for pointIndex := range chapter.KnowledgePoints {
			point := &chapter.KnowledgePoints[pointIndex]
			for index, chunkID := range point.EvidenceChunkIDs {
				if normalized, ok := aliases[chunkID]; ok {
					point.EvidenceChunkIDs[index] = normalized
				}
			}
		}
	}
}

func outlineRetryPrompt(prompt string, validationErr error) string {
	return prompt + "\n上一轮章节导航未通过严格校验，必须修正后重新输出完整 JSON。校验错误：" + validationErr.Error() + "。字段名必须严格使用 schema_version、chapters、chapter_index、chapter_title、start_seconds、end_seconds、chapter_summary、knowledge_points、title、seconds、evidence_chunk_ids；schema_version 必须为数字 1。章节必须从 0 秒开始，覆盖最后一个有效转写时间点，按时间顺序且不得重叠；只能从上文转写分块列表复制 evidence_chunk_ids，不得创造、猜测或引用不存在的 ID。"
}

func parseLLMJSONResponse(content string, target any) error {
	content = strings.TrimPrefix(strings.TrimSpace(content), "\ufeff")
	if err := json.Unmarshal([]byte(content), target); err == nil {
		return nil
	}

	cleaned := stripLLMReasoning(content)
	if cleaned != content {
		if err := json.Unmarshal([]byte(cleaned), target); err == nil {
			return nil
		}
	}

	for _, candidate := range balancedJSONObjectCandidates(cleaned) {
		if err := json.Unmarshal([]byte(candidate), target); err == nil {
			return nil
		}
	}

	return fmt.Errorf("response does not contain a valid JSON object")
}

func balancedJSONObjectCandidates(content string) []string {
	candidates := make([]string, 0, 1)
	for start := 0; start < len(content); start++ {
		if content[start] != '{' {
			continue
		}
		depth := 0
		inString := false
		escaped := false
		for index := start; index < len(content); index++ {
			character := content[index]
			if inString {
				if escaped {
					escaped = false
				} else if character == '\\' {
					escaped = true
				} else if character == '"' {
					inString = false
				}
				continue
			}
			switch character {
			case '"':
				inString = true
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					candidates = append(candidates, content[start:index+1])
					index = len(content)
				}
			}
		}
	}
	return candidates
}

var llmReasoningBlockPattern = regexp.MustCompile(`(?is)<think>.*?</think>|<analysis>.*?</analysis>`)

func stripLLMReasoning(content string) string {
	return strings.TrimSpace(llmReasoningBlockPattern.ReplaceAllString(content, ""))
}

func (h *DirectContentHandler) addEnhancementContext(ctx context.Context, video *model.Video, prompt string) (string, error) {
	if strings.TrimSpace(video.SummaryWikiPageID) == "" || strings.TrimSpace(video.KnowledgeBaseWikiPageID) == "" {
		return "", fmt.Errorf("summary enhancement requires summary and knowledge base pages")
	}
	summary, err := h.Wiki.GetPageByID(ctx, h.KnowledgeKBID, video.SummaryWikiPageID)
	if err != nil || summary == nil || strings.TrimSpace(summary.Content) == "" {
		if err == nil {
			err = fmt.Errorf("summary page is not readable")
		}
		return "", fmt.Errorf("read initial summary: %w", err)
	}
	knowledge, err := h.Wiki.GetPageByID(ctx, h.KnowledgeKBID, video.KnowledgeBaseWikiPageID)
	if err != nil || knowledge == nil || strings.TrimSpace(knowledge.Content) == "" {
		if err == nil {
			err = fmt.Errorf("knowledge base page is not readable")
		}
		return "", fmt.Errorf("read knowledge base: %w", err)
	}
	enhancedPrompt := prompt + fmt.Sprintf("\n这是知识增强阶段。保留初版总结的结构，只补充经过知识底座审计且能回指转写的内容。\n初版总结：\n%s\n\n知识底座：\n%s\n", summary.Content, knowledge.Content)
	if len(enhancedPrompt) > 240000 {
		return "", fmt.Errorf("summary enhancement input exceeds direct llm context limit")
	}
	return enhancedPrompt, nil
}

func buildDirectContentPrompt(video *model.Video, jobType string, chunks []transcript.Chunk) (string, error) {
	var builder strings.Builder
	builder.WriteString("你是视频内容生产模型。只能依据给定转写生成结果，不得补充转写中没有的事实。请只返回一个 JSON 对象；不要输出 Markdown 代码围栏、解释文字、HTML 或 XML。\n")
	switch jobType {
	case skill.JobOutline:
		builder.WriteString("任务：生成章节导航。只返回 JSON 对象：{\"schema_version\":1,\"chapters\":[{\"chapter_index\":1,\"chapter_title\":\"短标题\",\"start_seconds\":0,\"end_seconds\":41,\"chapter_summary\":\"本章核心内容\",\"knowledge_points\":[{\"title\":\"短语标题\",\"seconds\":12,\"evidence_chunk_ids\":[\"转写分块ID\"]}],\"evidence_chunk_ids\":[\"转写分块ID\"]}]}. 不要输出 Markdown、代码围栏、解释文字、HTML 或 XML。\n")
		builder.WriteString("章节必须覆盖从 0 秒开始到最后一个有效转写时间点，并按时间顺序排列；视频末尾没有转写内容时，不得伪造章节或时间范围。优先生成 4～8 章，只有主题发生明显转折时才拆章。时间只填数字秒数，不要填格式化时间字符串。每章只保留 1～2 个全片关键知识点，只有存在独立结论、方法或动作时才增加，绝不为覆盖每句转写而切碎；全片最多 12 个。合并同义观点、例子和论据。\n")
		builder.WriteString("章节核心内容控制在 80 个汉字以内，知识点标题控制在 10 个汉字以内。知识点标题必须是短语或结论式短标题，使用“方法名”“动作+对象”或“核心结论”结构，不写完整句。每个章节必须有核心内容和至少一个知识点；evidence_chunk_ids 必须使用给定转写分块 ID，不要拼接分块序号。\n")
	case skill.JobSummary:
		builder.WriteString(summaryPrompt("", false))
	case skill.JobSummaryEnhance:
		builder.WriteString(summaryPrompt(video.VideoType, true))
	default:
		return "", fmt.Errorf("unsupported direct content job: %s", jobType)
	}
	builder.WriteString(fmt.Sprintf("视频标题：%s\n上传时视频类型提示（仅弱提示，不得单独决定模板）：%s\n转写分块：每个分块使用 ID=转写分块ID、EVIDENCE_SENTENCE_ID=不可变证据句ID、TIME_MS=开始毫秒-结束毫秒；evidence chunk ID 只复制 ID= 后的值，原文依据和时间跳转必须通过同一分块回溯到 EVIDENCE_SENTENCE_ID。\n", video.Title, video.VideoType))
	for _, chunk := range chunks {
		builder.WriteString(fmt.Sprintf("ID=%s\nEVIDENCE_SENTENCE_ID=%s\nTIME_MS=%d-%d\n%s\n\n", chunk.ID, chunk.EvidenceSentenceID, chunk.StartMs, chunk.EndMs, transcript.OriginalText(chunk.Content)))
	}
	if builder.Len() > 240000 {
		return "", fmt.Errorf("transcript input exceeds direct llm context limit")
	}
	return builder.String(), nil
}

func summaryPrompt(videoType string, enhancement bool) string {
	framework, ok := summary.Framework(videoType)
	if videoType != "" && !ok {
		return fmt.Sprintf("视频类型 %q 没有可用的总结框架。\n", videoType)
	}
	frameworks := []struct {
		videoType string
		sections  []summary.FrameworkSection
	}{}
	if videoType == "" {
		for _, candidate := range summary.Frameworks() {
			frameworks = append(frameworks, struct {
				videoType string
				sections  []summary.FrameworkSection
			}{videoType: candidate.VideoType, sections: candidate.Sections})
		}
	} else {
		frameworks = append(frameworks, struct {
			videoType string
			sections  []summary.FrameworkSection
		}{videoType: videoType, sections: framework})
	}
	sectionShape := make([]string, 0)
	frameworkDescriptions := make([]string, 0, len(frameworks))
	for _, candidate := range frameworks {
		if videoType != "" {
			for _, section := range candidate.sections {
				sectionShape = append(sectionShape, fmt.Sprintf(`{"id":%q,"title":%q,"blocks":[{"id":"block-%s-1","kind":"paragraph","text":"本节内容","evidenceChunkIds":["转写分块ID"]}]}`, section.ID, section.Title, section.ID))
			}
		}
		frameworkDescriptions = append(frameworkDescriptions, fmt.Sprintf("%s=%s", candidate.videoType, frameworkIdentity(candidate.sections)))
	}
	if videoType == "" {
		sectionShape = append(sectionShape, `{"id":"选中类型的章节ID","title":"选中类型的标准标题","blocks":[{"id":"block-选中类型的章节ID-1","kind":"paragraph","text":"本节内容","evidenceChunkIds":["转写分块ID"]}]}`)
	}
	mode := "生成"
	if enhancement {
		mode = "生成增强版"
	}
	videoTypeContract := `"videoType":"从候选类型中选择"`
	if videoType != "" {
		videoTypeContract = fmt.Sprintf(`"videoType":%q`, videoType)
	}
	classificationContract := `"classification":{"confidence":0.91,"reason":"基于转写的简短判型理由","evidenceChunkIds":["判型证据分块ID"]}`
	orchestrationContract := `"orchestrationProfile":{"schemaVersion":1,"primaryTopic":"当前转写支持的主主题","topicUnits":[{"title":"主题单元","abstract":"基于转写的主题摘要","contentForms":["concept_cognition"],"learningOutcomes":["可观察的学习结果"],"summaryBlockIds":["正文 block ID"],"evidenceChunkIds":["转写分块ID"]}]}`
	return fmt.Sprintf("任务：%s类型化智能总结，并在同一次模型调用中生成编排卡片。只返回一个 JSON 对象，不要输出 Markdown、代码围栏、解释文字、HTML 或 XML。\n"+
		"先依据完整转写判断主类型，再匹配模板。候选类型及固定章节 ID、标题：%s。确定 videoType 后，sections 必须逐项复制该类型的完整固定章节，禁止混用其他类型章节。\n"+
		"JSON 契约：必须返回 {\"schemaVersion\":%d,%s,%s,%s,\"sections\":[%s]}。sections 必须严格按选中类型的标题和顺序输出：%s。\n"+
		"每个 section 必须包含 blocks 数组。有可靠原文证据时才生成 block；block.kind 只能是 paragraph 或 bullet，block.text 必须是可直接展示的纯文本，不得包含 Markdown 标记；每个非空 block 必须提供 evidenceChunkIds，且只能引用给定转写分块 ID。一个 block 可以引用多个分块。knowledge_refs 与 evidence_refs 由系统在保存前生成，不要自行编造或输出。\n"+
		"orchestrationProfile 是正式总结同次生成的有界路由卡片，必须存在且 schemaVersion 为 1；只允许 1 至 5 个 topicUnits。primaryTopic、title、abstract、learningOutcomes 只能来自当前转写，不能补充常识或推断。contentForms 只能使用 skill_method、tool_operation、concept_cognition、case_analysis、humanities_reflection、process_standard；每个单元必须至少包含一个 contentForms、learningOutcomes、summaryBlockIds 和 evidenceChunkIds。summaryBlockIds 必须引用本 JSON sections 中真实存在且全局唯一的 block ID；evidenceChunkIds 必须是这些 summaryBlockIds 对应正文 block 的 evidenceChunkIds 去重后的子集，禁止引用其他分块。所有 topicUnits 的 abstract 合计目标为 300 至 500 个汉字，最多 500 个汉字；没有可靠主题依据时不要伪造，返回空卡片会被程序拒绝。\n"+
		"选择 training 时，以“培训内容体系”为主干，按讲师真实授课顺序详细还原知识；背景引入、理论讲解、案例分析、实操演示、互动问答只是常见顺序，不得强行补齐。保留原文出现的案例细节和工具使用说明；原文金句必须逐字引用，不得改写成讲师引语；方法必须写清原文明示的步骤和判断标准。固定培训章节必须全部保留，原文没有可靠依据的章节必须输出 blocks:[]，不得创建“未提及”“信息不足”“无”等占位内容，不得使用常识、推测或知识增强信息补齐原文不存在的事实。\n"+
		"不得删除、合并或改名章节。非培训类型的缺失内容按对应模板规则处理。判型置信度低于 0.75 时必须选择 general；会议判型至少引用两段不同位置的转写分块。%s%s\n",
		mode, strings.Join(frameworkDescriptions, "；"), summary.SchemaVersion, videoTypeContract, classificationContract, orchestrationContract, strings.Join(sectionShape, ","), strings.Join(frameworkDescriptions, "；"), meetingSummaryInstruction(videoType), enhancementInstruction(enhancement))
}

func meetingSummaryInstruction(videoType string) string {
	if videoType != "" && videoType != "meeting" {
		return ""
	}
	return "当 videoType=meeting 时：一、会议总结采用电梯演讲式表达，概括核心问题、主要共识或最终定调，原则上不超过 140 字；二、会议基本信息至少包含参会时间、参会人员、会议类型，缺失项写‘会议中未明确’；三、关键议题和共识按议题组织，每项包含决策内容、决策逻辑，可选后续影响；四、会议分歧点只记录原文明示且尚未消除的分歧；五、会议讨论详情按逻辑覆盖全部实质讨论，不按发言顺序复述；六、待办事项遵循 SMART 原则，每项包含事项描述、负责人、截止时间，可选优先级与状态，缺失项写‘会议中未明确’；七、遗留和搁置议题每项包含搁置内容、搁置原因、后续计划；八、其他只记录前述章节未覆盖的信息，没有则写‘无’。"
}

func frameworkTitles(framework []summary.FrameworkSection) string {
	titles := make([]string, 0, len(framework))
	for _, section := range framework {
		titles = append(titles, section.Title)
	}
	return strings.Join(titles, "、")
}

func frameworkIdentity(framework []summary.FrameworkSection) string {
	sections := make([]string, 0, len(framework))
	for _, section := range framework {
		sections = append(sections, fmt.Sprintf(`{"id":%q,"title":%q}`, section.ID, section.Title))
	}
	return "[" + strings.Join(sections, ",") + "]"
}

func enhancementInstruction(enhancement bool) string {
	if enhancement {
		return "仅补充知识底座和转写共同证明的内容，并保持所有 section 的 id、title 和顺序不变；orchestrationProfile 是基础总结的不可变卡片，必须原样保留，不得新增、删除、改名或改动任何主题、block 和证据引用。"
	}
	return "明确区分原文观点、忠实概括、跨段归纳和分析推断。"
}

func fallbackTitle(title, videoTitle, jobType string) string {
	if strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	labels := map[string]string{
		skill.JobOutline:        "大纲",
		skill.JobSummary:        "知识总结",
		skill.JobSummaryEnhance: "知识总结",
	}
	return strings.TrimSpace(videoTitle) + "_" + labels[jobType]
}

func pageContent(pageType, videoID, generation, content string) string {
	schema := ""
	if pageType == "outline" || pageType == "typed_summary" {
		schema = "schema_version: 1\n"
	}
	return fmt.Sprintf("---\ntype: %s\nsource_video_id: %s\ntranscript_generation: %s\n%s---\n\n%s", pageType, videoID, generation, schema, strings.TrimSpace(content))
}

func pageContentWithProgress(pageType, videoID, generation, content string, partial bool, completed, total int) string {
	schema := ""
	if pageType == "outline" || pageType == "typed_summary" {
		schema = "schema_version: 1\n"
	}
	progress := fmt.Sprintf("partial: %t\ncompleted_chapters: %d\ntotal_chapters: %d\n", partial, completed, total)
	return fmt.Sprintf("---\ntype: %s\nsource_video_id: %s\ntranscript_generation: %s\n%s%s---\n\n%s", pageType, videoID, generation, schema, progress, strings.TrimSpace(content))
}
