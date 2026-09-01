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
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
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
	if skill.IsExplicitSummaryRegeneration(job.InputPayload) {
		prompt += "\n这是用户明确发起的历史总结重生成：允许覆盖旧总结，必须重新生成符合当前 JSON 契约且带观点级原文证据的内容。"
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
		knownChunkIDs := make(map[string]struct{}, len(chunks))
		for _, chunk := range chunks {
			knownChunkIDs[chunk.ID] = struct{}{}
		}
		transcriptEndSeconds, err := transcript.EffectiveEndSeconds(chunks)
		if err != nil {
			return fmt.Errorf("validate %s input: %w", h.Job, err)
		}
		var document outline.Document
		var validationErr error
		for attempt := 0; attempt < 2; attempt++ {
			if attempt > 0 {
				raw, err = h.LLM.Complete(ctx, outlineRetryPrompt(prompt, validationErr))
				if err != nil {
					return fmt.Errorf("generate %s retry: %w", h.Job, err)
				}
			}
			document = outline.Document{}
			if err := parseLLMJSONResponse(raw, &document); err != nil {
				validationErr = fmt.Errorf("parse %s output: %w", h.Job, err)
				continue
			}
			normalizeOutlineEvidenceChunkIDs(&document, chunks)
			if err := outline.ValidateWithTranscriptEnd(document, video.DurationSeconds, transcriptEndSeconds, knownChunkIDs); err != nil {
				validationErr = fmt.Errorf("validate %s output: %w", h.Job, err)
				continue
			}
			validationErr = nil
			break
		}
		if validationErr != nil {
			return validationErr
		}
		canonical, err := outline.Marshal(document)
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
			if attempt > 0 {
				retryPrompt := prompt + "\n上一轮总结未通过严格校验，必须修正后重新输出完整 JSON。校验错误：" + validationErr.Error() + "。字段名必须严格使用 schemaVersion、videoType、sections、evidenceChunkIds，禁止使用 schema_version、video_type、evidence_chunk_ids；schemaVersion 必须为数字 1。只能从上文转写分块列表复制 evidenceChunkIds，不得创造、猜测或引用不存在的 ID；可以使用纯知识 ID或带 |分片序号的显示 ID，系统会归一化。"
				raw, err = h.LLM.Complete(ctx, retryPrompt)
				if err != nil {
					return fmt.Errorf("generate %s retry: %w", h.Job, err)
				}
			}
			document = summary.Document{}
			if err := parseLLMJSONResponse(raw, &document); err != nil {
				validationErr = fmt.Errorf("parse %s output: %w", h.Job, err)
				continue
			}
			summary.NormalizeEvidenceChunkIDs(&document, chunks)
			if err := summary.Validate(document, video.VideoType, knownChunkIDs); err != nil {
				validationErr = fmt.Errorf("validate %s output: %w", h.Job, err)
				continue
			}
			if err := summary.ResolveEvidence(&document, chunks); err != nil {
				validationErr = fmt.Errorf("resolve %s evidence: %w", h.Job, err)
				continue
			}
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
	var completeErr error
	if skill.IsExplicitSummaryRegeneration(job.InputPayload) {
		_, _, completeErr = h.Orchestrator.AfterExplicitSummaryRegeneration(ctx, video.ID, h.Job, page.ID)
	} else {
		_, _, completeErr = h.Orchestrator.AfterSkillCompleteWithID(ctx, video.ID, h.Job, page.ID)
	}
	if completeErr != nil {
		return completeErr
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
		builder.WriteString("章节必须覆盖从 0 秒开始到最后一个有效转写时间点，并按时间顺序排列；视频末尾没有转写内容时，不得伪造章节或时间范围。优先生成 4～8 章，只有主题发生明显转折时才拆章。时间只填数字秒数，不要填格式化时间字符串。每章只保留 1～2 个全片关键知识点，只有存在独立结论、方法或动作时才增加，绝不为覆盖每句转写而切碎；全片最多 12 个。合并同义观点、例子和论据。\n")
		builder.WriteString("章节核心内容控制在 80 个汉字以内，知识点标题控制在 10 个汉字以内。知识点标题必须是短语或结论式短标题，使用“方法名”“动作+对象”或“核心结论”结构，不写完整句。每个章节必须有核心内容和至少一个知识点；evidence_chunk_ids 必须使用给定转写分块 ID，不要拼接分块序号。\n")
	case skill.JobSummary:
		builder.WriteString(summaryPrompt(video.VideoType, false))
	case skill.JobSummaryEnhance:
		builder.WriteString(summaryPrompt(video.VideoType, true))
	default:
		return "", fmt.Errorf("unsupported direct content job: %s", jobType)
	}
	builder.WriteString(fmt.Sprintf("视频标题：%s\n视频类型：%s\n转写分块：每个分块使用 ID=转写分块ID、TIME_MS=开始毫秒-结束毫秒；evidence chunk ID 只复制 ID= 后的值。\n", video.Title, video.VideoType))
	for _, chunk := range chunks {
		builder.WriteString(fmt.Sprintf("ID=%s\nTIME_MS=%d-%d\n%s\n\n", chunk.ID, chunk.StartMs, chunk.EndMs, transcript.OriginalText(chunk.Content)))
	}
	if builder.Len() > 240000 {
		return "", fmt.Errorf("transcript input exceeds direct llm context limit")
	}
	return builder.String(), nil
}

func summaryPrompt(videoType string, enhancement bool) string {
	framework, ok := summary.Framework(videoType)
	if !ok {
		return fmt.Sprintf("视频类型 %q 没有可用的总结框架。\n", videoType)
	}
	sectionShape := make([]string, 0, len(framework))
	for _, section := range framework {
		sectionShape = append(sectionShape, fmt.Sprintf(`{"id":%q,"title":%q,"blocks":[{"id":"block-1","kind":"paragraph","text":"本节内容","evidenceChunkIds":["转写分块ID"]}]}`, section.ID, section.Title))
	}
	mode := "生成"
	if enhancement {
		mode = "生成增强版"
	}
	return fmt.Sprintf("任务：%s类型化智能总结。只返回一个 JSON 对象，不要输出 Markdown、代码围栏、解释文字、HTML 或 XML。\n"+
		"JSON 契约：必须返回 {\"schemaVersion\":1,\"videoType\":%q,\"sections\":[%s]}。sections 必须严格按以下标题和顺序输出：%s。\n"+
		"每个 section 必须包含至少一个 block；block.kind 只能是 paragraph 或 bullet，block.text 必须是可直接展示的纯文本，不得包含 Markdown 标记；每个 block 必须提供 evidenceChunkIds，且只能引用给定转写分块 ID。一个 block 可以引用多个分块。\n"+
		"章节证据不足时保留章节并明确写出信息不足，不得删除、合并、改名或补充转写之外的事实。%s\n",
		mode, videoType, strings.Join(sectionShape, ","), frameworkTitles(framework), enhancementInstruction(enhancement))
}

func frameworkTitles(framework []summary.FrameworkSection) string {
	titles := make([]string, 0, len(framework))
	for _, section := range framework {
		titles = append(titles, section.Title)
	}
	return strings.Join(titles, "、")
}

func enhancementInstruction(enhancement bool) string {
	if enhancement {
		return "仅补充知识底座和转写共同证明的内容，并保持所有 section 的 id、title 和顺序不变。"
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
