package im

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// InfoCommand implements /info.
// It shows the bound agent's profile and capabilities so IM users can
// understand what the bot can do without leaving the chat.
type InfoCommand struct {
	kbService interfaces.KnowledgeBaseService
}

func newInfoCommand(kbService interfaces.KnowledgeBaseService) *InfoCommand {
	return &InfoCommand{kbService: kbService}
}

func (c *InfoCommand) Name() string        { return "info" }
func (c *InfoCommand) Description() string { return "查看当前智能体的信息与能力" }

func (c *InfoCommand) Execute(ctx context.Context, cmdCtx *CommandContext, _ []string) (*CommandResult, error) {
	var sb strings.Builder

	// Note: Feishu card markdown only renders **bold** when it occupies the
	// entire inline segment. "**label：**value" on the same line will show
	// raw asterisks. Always keep bold text self-contained on its own line.

	// ── Header ──
	name := cmdCtx.AgentName
	if name == "" {
		name = "未命名智能体"
	}
	sb.WriteString(fmt.Sprintf("🤖 **%s**\n", name))
	if cmdCtx.CustomAgent != nil && cmdCtx.CustomAgent.Description != "" {
		sb.WriteString(fmt.Sprintf("> %s\n", cmdCtx.CustomAgent.Description))
	}

	if cmdCtx.CustomAgent == nil {
		sb.WriteString("\n未绑定智能体，发送 `/help` 查看可用指令。")
		return &CommandResult{Content: sb.String()}, nil
	}

	cfg := cmdCtx.CustomAgent.Config

	// ── Mode ──
	if cmdCtx.CustomAgent.IsAgentMode() {
		sb.WriteString("\n🧠 **Agent模式**\n")
		sb.WriteString("支持多步思考、工具调用（ReAct）\n")
	} else {
		sb.WriteString("\n🧠 **Agent模式**\n")
		sb.WriteString("基于知识库检索直接回答（RAG）\n")
	}

	// ── Knowledge bases ──
	// KBSelectionMode: "all" uses every KB under the tenant (IDs list is empty),
	// "selected" uses the explicit KnowledgeBases list, "none"/empty means disabled.
	sb.WriteString("\n📚 **知识库**\n")
	if cfg.KBSelectionMode == "all" {
		kbs, err := c.kbService.ListKnowledgeBasesByTenantID(ctx, cmdCtx.TenantID)
		if err == nil && len(kbs) > 0 {
			for _, kb := range kbs {
				sb.WriteString(fmt.Sprintf("  · %s\n", kb.Name))
			}
			sb.WriteString(fmt.Sprintf("  共 %d 个（全部启用）\n", len(kbs)))
		} else {
			sb.WriteString("  全部启用\n")
		}
	} else if len(cfg.KnowledgeBases) > 0 {
		kbs, err := c.kbService.ListKnowledgeBasesByTenantID(ctx, cmdCtx.TenantID)
		if err == nil {
			nameMap := make(map[string]string, len(kbs))
			for _, kb := range kbs {
				nameMap[kb.ID] = kb.Name
			}
			for _, id := range cfg.KnowledgeBases {
				label := id
				if n, ok := nameMap[id]; ok {
					label = n
				}
				sb.WriteString(fmt.Sprintf("  · %s\n", label))
			}
		} else {
			sb.WriteString(fmt.Sprintf("  已选择 %d 个\n", len(cfg.KnowledgeBases)))
		}
	} else {
		sb.WriteString("  未配置\n")
	}

	// ── Skills ──
	sb.WriteString("\n⚡ **Skills**\n")
	if cfg.SkillsSelectionMode == "all" {
		sb.WriteString("  全部启用\n")
	} else if cfg.SkillsSelectionMode == "selected" && len(cfg.SelectedSkills) > 0 {
		for _, s := range cfg.SelectedSkills {
			sb.WriteString(fmt.Sprintf("  · %s\n", s))
		}
	} else {
		sb.WriteString("  未配置\n")
	}

	// ── MCP ──
	sb.WriteString("\n🔌 **MCP 服务**\n")
	if cfg.MCPSelectionMode == "all" {
		sb.WriteString("  全部接入\n")
	} else if cfg.MCPSelectionMode == "selected" && len(cfg.MCPServices) > 0 {
		sb.WriteString(fmt.Sprintf("  已接入 %d 个服务\n", len(cfg.MCPServices)))
	} else {
		sb.WriteString("  未配置\n")
	}

	// ── Web search ──
	sb.WriteString("\n🌐 **网络搜索**\n")
	if cfg.WebSearchEnabled {
		sb.WriteString("  已启用\n")
	} else {
		sb.WriteString("  未启用\n")
	}

	// ── Footer ──
	outputLabel := "流式输出"
	if cmdCtx.ChannelOutputMode == "full" {
		outputLabel = "完整输出"
	}
	sb.WriteString(fmt.Sprintf("\n⚙️ **输出模式**\n  %s\n", outputLabel))
	sb.WriteString("\n---\n发送 `/help` 查看所有可用指令")

	return &CommandResult{Content: sb.String()}, nil
}
