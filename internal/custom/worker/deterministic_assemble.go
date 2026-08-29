package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	outlinecontract "github.com/Tencent/WeKnora/internal/custom/service/outline"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
)

type DeterministicAssembleHandler struct {
	DB           *gorm.DB
	Wiki         *weknora.WikiClient
	Orchestrator *skill.Orchestrator
	KBID         string
}

func NewDeterministicAssembleHandler(db *gorm.DB, wiki *weknora.WikiClient, orchestrator *skill.Orchestrator, kbID string) *DeterministicAssembleHandler {
	return &DeterministicAssembleHandler{DB: db, Wiki: wiki, Orchestrator: orchestrator, KBID: kbID}
}

func (h *DeterministicAssembleHandler) JobType() string { return skill.JobAssemble }

func (h *DeterministicAssembleHandler) Run(ctx context.Context, job *model.VideoProcessingJob, video *model.Video) error {
	if h.DB == nil || h.Wiki == nil || h.Orchestrator == nil {
		return fmt.Errorf("deterministic assemble dependencies are not configured")
	}
	generation := strings.TrimSpace(job.TranscriptGeneration)
	if generation == "" {
		generation = strings.TrimSpace(video.TranscriptGeneration)
	}
	if generation == "" || generation != strings.TrimSpace(video.TranscriptGeneration) {
		return fmt.Errorf("视频 %s 的转写代次不可用或已过期", video.ID)
	}
	outline, err := h.readCurrentPage(ctx, video.OutlineWikiPageID, generation)
	if err != nil {
		return fmt.Errorf("read outline page: %w", err)
	}
	summary, err := h.readCurrentPage(ctx, video.SummaryWikiPageID, generation)
	if err != nil {
		return fmt.Errorf("read summary page: %w", err)
	}
	knowledge := "关联知识暂未就绪。基础内容已可用，知识增强完成后将单独更新。"
	if strings.TrimSpace(video.KnowledgeBaseWikiPageID) != "" && video.KnowledgeAuditStatus != "failed" {
		if page, readErr := h.readCurrentPage(ctx, video.KnowledgeBaseWikiPageID, generation); readErr == nil {
			knowledge = stripFrontmatter(page.Content)
		}
	}

	outlineContent := outlinecontract.RenderOrLegacy(outline.Content)
	content := composeTranscriptPage(video.Title, video.ID, generation, outlineContent, summary.Content, knowledge)
	page, err := h.Wiki.UpsertPage(ctx, h.KBID, weknora.WikiPageWrite{
		Slug:     "transcript-page/" + video.ID,
		Title:    video.Title,
		PageType: "index",
		Status:   "published",
		Content:  content,
	})
	if err != nil {
		return fmt.Errorf("save transcript page: %w", err)
	}
	result, _ := json.Marshal(map[string]any{
		"provider":              "backend",
		"wiki_page_id":          page.ID,
		"transcript_generation": generation,
	})
	if err := h.DB.WithContext(ctx).Model(job).Update("result_payload", string(result)).Error; err != nil {
		return fmt.Errorf("save assemble result: %w", err)
	}
	_, _, err = h.Orchestrator.AfterSkillCompleteWithID(ctx, video.ID, skill.JobAssemble, page.ID)
	return err
}

func (h *DeterministicAssembleHandler) readCurrentPage(ctx context.Context, pageID, generation string) (*weknora.WikiPage, error) {
	if strings.TrimSpace(pageID) == "" {
		return nil, fmt.Errorf("page id is empty")
	}
	page, err := h.Wiki.GetPageByID(ctx, h.KBID, pageID)
	if err != nil {
		return nil, err
	}
	if page == nil || strings.TrimSpace(page.Content) == "" {
		return nil, fmt.Errorf("page %s is not readable", pageID)
	}
	frontmatter := page.ParsedFrontmatter()
	if actual, _ := frontmatter["transcript_generation"].(string); strings.TrimSpace(actual) != generation {
		return nil, fmt.Errorf("page %s transcript generation mismatch", pageID)
	}
	return page, nil
}

func composeTranscriptPage(title, videoID, generation, outline, summary, knowledge string) string {
	return fmt.Sprintf("---\ntype: transcript_page\nsource_video_id: %s\ntranscript_generation: %s\n---\n\n# %s\n\n## 语义时间轴\n\n当前页面内容均绑定转写代次 `%s`，视频引用以转写分块和时间范围为准。\n\n## 内容大纲\n\n%s\n\n## 智能总结\n\n%s\n\n## 相关知识\n\n%s\n\n## 相关文字稿与学习路径\n\n请先通过内容大纲定位章节，再通过智能总结深入学习；需要核对事实时回到对应转写分块。\n\n## 证据与质量说明\n\n本页由后端按已读取的内容产物确定性组装，视频 ID 为 `%s`。\n", videoID, generation, strings.TrimSpace(title), generation, stripFrontmatter(outline), stripFrontmatter(summary), strings.TrimSpace(knowledge), videoID)
}

func stripFrontmatter(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			return strings.TrimSpace(strings.Join(lines[index+1:], "\n"))
		}
	}
	return trimmed
}
