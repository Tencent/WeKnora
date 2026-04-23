package im

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	searchMaxResults    = 5
	searchContentMaxLen = 200 // runes shown per result
)

// SearchCommand implements /search <query>.
//
// It runs a hybrid search (vector + keywords) against the user's selected
// knowledge bases—or the bot-level defaults when no override is active—and
// returns the raw matching passages without AI summarisation. This is useful
// when the user needs to inspect source text directly.
type SearchCommand struct {
	sessionService interfaces.SessionService
	kbService      interfaces.KnowledgeBaseService
}

func newSearchCommand(sessionService interfaces.SessionService, kbService interfaces.KnowledgeBaseService) *SearchCommand {
	return &SearchCommand{sessionService: sessionService, kbService: kbService}
}

func (c *SearchCommand) Name() string { return "search" }
func (c *SearchCommand) Description() string {
	return "直接检索知识库原文（不经 AI 总结），例如：/search 退款政策"
}

func (c *SearchCommand) Execute(ctx context.Context, cmdCtx *CommandContext, args []string) (*CommandResult, error) {
	if len(args) == 0 {
		return &CommandResult{
			Content: "请输入搜索内容，例如：`/search 退款政策`",
		}, nil
	}

	query := strings.Join(args, " ")

	// Resolve which KBs to search, mirroring the logic in the QA pipeline's
	// resolveKnowledgeBasesFromAgent so that /search covers the same scope.
	var kbIDs []string
	if cmdCtx.CustomAgent != nil {
		switch cmdCtx.CustomAgent.Config.KBSelectionMode {
		case "all":
			allKBs, err := c.kbService.ListKnowledgeBases(ctx)
			if err == nil {
				// Same tool-capability filter as the QA pipeline's
				// resolveKnowledgeBasesFromAgent (`all` branch) so `/search`
				// agrees with what the agent's tools can actually reach.
				filter := tools.DeriveKBFilterFromTools(cmdCtx.CustomAgent.Config.AllowedTools)
				skipped := 0
				for _, kb := range allKBs {
					if !filter.IsEmpty() &&
						!tools.KBSatisfiesToolRequirements(kb.Capabilities(), cmdCtx.CustomAgent.Config.AllowedTools) {
						skipped++
						continue
					}
					kbIDs = append(kbIDs, kb.ID)
				}
				if skipped > 0 {
					logger.Infof(ctx,
						"/search(agent=%s, mode=all): tool-capability filter removed %d of %d KBs",
						cmdCtx.CustomAgent.ID, skipped, len(allKBs))
				}
			}
		case "none":
			// No knowledge bases configured — will return empty results.
		case "selected":
			kbIDs = cmdCtx.CustomAgent.Config.KnowledgeBases
		default:
			// Backward compatibility: fall back to configured list.
			kbIDs = cmdCtx.CustomAgent.Config.KnowledgeBases
		}
	}

	results, err := c.sessionService.SearchKnowledge(ctx, kbIDs, nil, query)
	if err != nil {
		return nil, fmt.Errorf("search knowledge: %w", err)
	}

	if len(results) == 0 {
		return &CommandResult{
			Content: fmt.Sprintf("未在知识库中找到与「%s」相关的内容。", query),
		}, nil
	}

	// Cap the number of results shown in IM (wall of text is unhelpful).
	shown := results
	if len(shown) > searchMaxResults {
		shown = shown[:searchMaxResults]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 **搜索「%s」** — 找到 %d 条结果\n\n", query, len(results)))

	for i, r := range shown {
		// Trim content to a readable length.
		content := []rune(r.Content)
		suffix := ""
		if len(content) > searchContentMaxLen {
			content = content[:searchContentMaxLen]
			suffix = "…"
		}

		// Source label: prefer title, fall back to filename.
		source := r.KnowledgeTitle
		if source == "" {
			source = r.KnowledgeFilename
		}

		sb.WriteString(fmt.Sprintf("**[%d]** %s\n> %s%s\n", i+1, source, string(content), suffix))

		if r.Score > 0 {
			sb.WriteString(fmt.Sprintf("匹配度：%.0f%%\n", r.Score*100))
		}
		sb.WriteString("\n")
	}

	if len(results) > searchMaxResults {
		sb.WriteString(fmt.Sprintf("_（仅显示前 %d 条，共 %d 条）_", searchMaxResults, len(results)))
	}

	return &CommandResult{Content: sb.String()}, nil
}
