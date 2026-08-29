package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/llm"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
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
	var output generatedContent
	if err := parseLLMJSONResponse(raw, &output); err != nil {
		return fmt.Errorf("parse %s output: %w", h.Job, err)
	}
	if strings.TrimSpace(output.Content) == "" {
		return fmt.Errorf("generate %s returned empty content", h.Job)
	}
	contract, ok := skill.Contract(h.Job)
	if !ok {
		return fmt.Errorf("unknown direct content job: %s", h.Job)
	}
	page, err := h.Wiki.UpsertPage(ctx, h.WeKnora.KBID(), weknora.WikiPageWrite{
		Slug:     contract.WriteSlug(video.ID),
		Title:    fallbackTitle(output.Title, video.Title, h.Job),
		PageType: "index",
		Status:   "published",
		Summary:  output.Summary,
		Content:  pageContent(contract.ArtifactType, video.ID, generation, output.Content),
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

func parseLLMJSONResponse(content string, target any) error {
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
	builder.WriteString("你是视频内容生产模型。只能依据给定转写生成结果，不得补充转写中没有的事实。请只返回一个 JSON 对象，字段为 title、summary、content；不要输出 Markdown 代码围栏、解释文字、HTML 或 XML。content 使用 Markdown。\n")
	switch jobType {
	case skill.JobOutline:
		builder.WriteString("任务：生成章节导航。content 必须按以下结构输出：每章使用二级标题；章节标题后写“时间：HH:MM:SS–HH:MM:SS”；使用“### 本章核心内容”和“### 关键知识点”两个三级标题；知识点使用列表项，并在每项末尾写一个可定位的时间戳。\n")
		builder.WriteString("章节必须覆盖完整视频且按时间顺序排列。每章默认只保留 1～2 个全片关键知识点，只有存在独立结论、方法或动作时才增加，最多 3 个；全片优先控制在 8～16 个，不得为了覆盖每句转写而切碎。合并同义观点、例子和论据。\n")
		builder.WriteString("知识点标题必须是 4～10 个汉字或 2～5 个英文单词的短语/结论式短标题，使用“方法名”“动作+对象”或“核心结论”结构，不写完整句。核心内容写成一段能独立理解的概括。可在“### 原文”下保留少量可追溯原文，但不得把原文当作知识点，也不得用原文替代核心内容。\n")
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
	return fmt.Sprintf("---\ntype: %s\nsource_video_id: %s\ntranscript_generation: %s\n---\n\n%s", pageType, videoID, generation, strings.TrimSpace(content))
}
