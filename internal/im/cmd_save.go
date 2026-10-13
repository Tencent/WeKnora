package im

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	appservice "github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
)

// urlKnowledgeCreator is the KnowledgeService surface needed by /save.
// Kept narrow so unit tests can stub it without the full KnowledgeService.
type urlKnowledgeCreator interface {
	CreateKnowledgeFromURL(
		ctx context.Context,
		kbID string,
		url string,
		fileName string,
		fileType string,
		enableMultimodel *bool,
		title string,
		tagIDs []string,
		channel string,
		processOverrides *types.KnowledgeProcessOverrides,
	) (*types.Knowledge, error)
}

// SaveCommand implements /save <url>.
//
// It submits a webpage URL to the knowledge base configured on the current IM
// channel (knowledge_base_id). Parsing and embedding continue asynchronously;
// success only means the ingest job was created.
type SaveCommand struct {
	knowledgeService urlKnowledgeCreator
}

func newSaveCommand(knowledgeService urlKnowledgeCreator) *SaveCommand {
	return &SaveCommand{knowledgeService: knowledgeService}
}

func (c *SaveCommand) Name() string { return "save" }
func (c *SaveCommand) Description() string {
	return "将网页保存到当前 IM 渠道配置的知识库，例如：/save https://example.com"
}

// Usage returns the detailed usage line shown by /help save.
func (c *SaveCommand) Usage() string {
	return "用法：/save <网页链接>"
}

func (c *SaveCommand) Execute(ctx context.Context, cmdCtx *CommandContext, args []string) (*CommandResult, error) {
	if len(args) == 0 {
		return &CommandResult{Content: "用法：/save <网页链接>"}, nil
	}

	rawURL := strings.TrimSpace(args[0])
	if rawURL == "" {
		return &CommandResult{Content: "用法：/save <网页链接>"}, nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return &CommandResult{
			Content: "链接无效，请提供以 http:// 或 https:// 开头的网页地址。",
		}, nil
	}

	kbID := strings.TrimSpace(cmdCtx.KnowledgeBaseID)
	if kbID == "" {
		return &CommandResult{
			Content: "尚未配置保存目标知识库，请先在 WeKnora「设置 → IM 集成」中为当前渠道选择文件知识库。",
		}, nil
	}

	channel := imPlatformToChannel(cmdCtx.Platform)
	_, err = c.knowledgeService.CreateKnowledgeFromURL(
		ctx,
		kbID,
		rawURL,
		"", // fileName — empty so the URL is treated as a webpage crawl
		"", // fileType
		nil,
		"",
		nil,
		channel,
		nil,
	)
	if err != nil {
		if errors.Is(err, appservice.ErrInvalidURL) {
			return &CommandResult{
				Content: "链接无效或不受支持，请检查后重试。",
			}, nil
		}
		return nil, fmt.Errorf("create knowledge from url: %w", err)
	}

	return &CommandResult{
		Content: fmt.Sprintf("✅ 已提交到知识库\n%s", rawURL),
	}, nil
}
