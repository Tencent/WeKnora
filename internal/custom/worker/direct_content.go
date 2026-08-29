package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/llm"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/outline"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

type DirectContentHandler struct {
	DB           *gorm.DB
	LLM          *llm.Client
	WeKnora      *weknora.Client
	Wiki         *weknora.WikiClient
	Orchestrator *skill.Orchestrator
	Job          string
}

type generatedContent struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Content string `json:"content"`
}

func NewDirectContentHandler(db *gorm.DB, client *llm.Client, wk *weknora.Client, wiki *weknora.WikiClient, orchestrator *skill.Orchestrator, jobType string) *DirectContentHandler {
	return &DirectContentHandler{DB: db, LLM: client, WeKnora: wk, Wiki: wiki, Orchestrator: orchestrator, Job: jobType}
}

func (h *DirectContentHandler) JobType() string { return h.Job }

func (h *DirectContentHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	if h.LLM == nil || h.WeKnora == nil || h.Wiki == nil || h.Orchestrator == nil {
		return fmt.Errorf("direct content handler dependencies are not configured")
	}
	if h.Job != skill.JobOutline && h.Job != skill.JobSummary && h.Job != skill.JobSummaryEnhance {
		return fmt.Errorf("unsupported direct content job: %s", h.Job)
	}
	if h.Job == skill.JobSummary || h.Job == skill.JobSummaryEnhance {
		protected, err := h.Orchestrator.IsSummaryUserEditProtected(ctx, video.ID)
		if err != nil {
			return fmt.Errorf("check summary user edit protection: %w", err)
		}
		if protected {
			return nil
		}
	}

	generation := strings.TrimSpace(job.TranscriptGeneration)
	if generation == "" {
		generation = strings.TrimSpace(video.TranscriptGeneration)
	}
	if generation == "" || generation != strings.TrimSpace(video.TranscriptGeneration) {
		return fmt.Errorf("视频 %s 的转写代次不可用或已过期", video.ID)
	}
	reader := transcript.NewReader(h.DB, h.WeKnora)
	chunks, err := reader.Read(ctx, video.ID, generation)
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
	raw, err := h.LLM.Complete(ctx, prompt)
	if err != nil {
		return fmt.Errorf("generate %s: %w", h.Job, err)
	}
	contract, ok := skill.Contract(h.Job)
	if !ok {
		return fmt.Errorf("unknown direct content job: %s", h.Job)
	}
	pageTitle := ""
	pageSummary := ""
	pageBody := ""
	if h.Job == skill.JobOutline {
		var document outline.Document
		if err := parseLLMJSONResponse(raw, &document); err != nil {
			return fmt.Errorf("parse %s output: %w", h.Job, err)
		}
		knownChunkIDs := make(map[string]struct{}, len(chunks))
		for _, chunk := range chunks {
			knownChunkIDs[chunk.ID] = struct{}{}
		}
		normalizeOutlineEvidenceChunkIDs(&document, chunks)
		if err := outline.Validate(document, video.DurationSeconds, knownChunkIDs); err != nil {
			return fmt.Errorf("validate %s output: %w", h.Job, err)
		}
		canonical, err := outline.Marshal(document)
		if err != nil {
			return fmt.Errorf("marshal %s output: %w", h.Job, err)
		}
		pageTitle = video.Title + "_大纲"
		pageBody = canonical
	} else {
		var output generatedContent
		if err := parseLLMJSONResponse(raw, &output); err != nil {
			return fmt.Errorf("parse %s output: %w", h.Job, err)
		}
		if strings.TrimSpace(output.Content) == "" {
			return fmt.Errorf("generate %s returned empty content", h.Job)
		}
		pageTitle = fallbackTitle(output.Title, video.Title, h.Job)
		pageSummary = output.Summary
		pageBody = output.Content
	}
	page, err := h.Wiki.UpsertPage(ctx, h.WeKnora.KBID(), weknora.WikiPageWrite{
		Slug:     contract.WriteSlug(video.ID),
		Title:    pageTitle,
		PageType: "index",
		Status:   "published",
		Summary:  pageSummary,
		Content:  pageContent(contract.ArtifactType, video.ID, generation, pageBody),
	})
	if err != nil {
		return fmt.Errorf("save %s page: %w", h.Job, err)
	}
	result, _ := json.Marshal(map[string]any{
		"provider":              "llm",
		"model":                 h.LLM.Model(),
		"prompt_version":        h.LLM.PromptVersion(),
		"wiki_page_id":          page.ID,
		"transcript_generation": generation,
	})
	if err := h.DB.WithContext(ctx).Model(job).Update("result_payload", string(result)).Error; err != nil {
		return fmt.Errorf("save %s result: %w", h.Job, err)
	}
	if _, _, err := h.Orchestrator.AfterSkillCompleteWithID(ctx, video.ID, h.Job, page.ID); err != nil {
		return err
	}
	return nil
}

func normalizeOutlineEvidenceChunkIDs(document *outline.Document, chunks []transcript.Chunk) {
	aliases := make(map[string]string, len(chunks)*2)
	for _, chunk := range chunks {
		aliases[chunk.ID] = chunk.ID
		aliases[fmt.Sprintf("%s|%06d", chunk.ID, chunk.Index)] = chunk.ID
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

func parseLLMJSONResponse(content string, target any) error {
	content = stripLLMReasoning(content)
	err := json.Unmarshal([]byte(content), target)
	if err == nil {
		return nil
	}

	if fenceStart := strings.Index(content, "```"); fenceStart >= 0 {
		fenced := content[fenceStart+3:]
		fenced = strings.TrimLeft(fenced, " \t\r\n")
		if strings.HasPrefix(fenced, "json") {
			fenced = strings.TrimLeft(fenced[4:], " \t\r\n")
		}
		if fenceEnd := strings.Index(fenced, "```"); fenceEnd >= 0 {
			if fenceErr := json.Unmarshal([]byte(strings.TrimSpace(fenced[:fenceEnd])), target); fenceErr == nil {
				return nil
			}
		}
	}

	objectStart := strings.IndexByte(content, '{')
	objectEnd := strings.LastIndexByte(content, '}')
	if objectStart >= 0 && objectEnd > objectStart {
		if objectErr := json.Unmarshal([]byte(content[objectStart:objectEnd+1]), target); objectErr == nil {
			return nil
		}
	}

	return err
}

var llmReasoningBlockPattern = regexp.MustCompile(`(?is)<think>.*?</think>|<analysis>.*?</analysis>`)

func stripLLMReasoning(content string) string {
	return strings.TrimSpace(llmReasoningBlockPattern.ReplaceAllString(content, ""))
}

func (h *DirectContentHandler) addEnhancementContext(ctx context.Context, video *model.Video, prompt string) (string, error) {
	if strings.TrimSpace(video.SummaryWikiPageID) == "" || strings.TrimSpace(video.KnowledgeBaseWikiPageID) == "" {
		return "", fmt.Errorf("summary enhancement requires summary and knowledge base pages")
	}
	summary, err := h.Wiki.GetPageByID(ctx, h.WeKnora.KBID(), video.SummaryWikiPageID)
	if err != nil || summary == nil || strings.TrimSpace(summary.Content) == "" {
		if err == nil {
			err = fmt.Errorf("summary page is not readable")
		}
		return "", fmt.Errorf("read initial summary: %w", err)
	}
	knowledge, err := h.Wiki.GetPageByID(ctx, h.WeKnora.KBID(), video.KnowledgeBaseWikiPageID)
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
		builder.WriteString("章节必须覆盖完整视频且按时间顺序排列；优先生成 4～8 章，只有主题发生明显转折时才拆章。时间只填数字秒数，不要填格式化时间字符串。每章只保留 1～2 个全片关键知识点，只有存在独立结论、方法或动作时才增加，绝不为覆盖每句转写而切碎；全片最多 12 个。合并同义观点、例子和论据。\n")
		builder.WriteString("章节核心内容控制在 80 个汉字以内，知识点标题控制在 10 个汉字以内。知识点标题必须是短语或结论式短标题，使用“方法名”“动作+对象”或“核心结论”结构，不写完整句。每个章节必须有核心内容和至少一个知识点；evidence_chunk_ids 必须使用给定转写分块 ID，不要拼接分块序号。\n")
	case skill.JobSummary:
		builder.WriteString("任务：生成类型化智能总结。视频类型决定组织方式，但不能虚构模板字段；缺少证据时明确说明。\n")
	case skill.JobSummaryEnhance:
		builder.WriteString("任务：生成类型化智能总结增强版。仅补充可由知识底座和转写共同证明的内容。\n")
	default:
		return "", fmt.Errorf("unsupported direct content job: %s", jobType)
	}
	builder.WriteString(fmt.Sprintf("视频标题：%s\n视频类型：%s\n转写分块：\n", video.Title, video.VideoType))
	for _, chunk := range chunks {
		builder.WriteString(fmt.Sprintf("[%s|%06d]\n%s\n\n", chunk.ID, chunk.Index, chunk.Content))
	}
	if builder.Len() > 240000 {
		return "", fmt.Errorf("transcript input exceeds direct llm context limit")
	}
	return builder.String(), nil
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
	if pageType == "outline" {
		schema = "schema_version: 1\n"
	}
	return fmt.Sprintf("---\ntype: %s\nsource_video_id: %s\ntranscript_generation: %s\n%s---\n\n%s", pageType, videoID, generation, schema, strings.TrimSpace(content))
}
