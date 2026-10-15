package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// AgentTrialRequest supplies a single bounded example, never tool instructions.
type AgentTrialRequest struct {
	// Example user message for the saved system prompt.
	Prompt string `json:"prompt" binding:"required,max=4000" extensions:"x-agent,x-agent-max-bytes=16000"`
}

// AgentTrialResponse contains only the visible model answer.
type AgentTrialResponse struct {
	Answer string `json:"answer" extensions:"x-agent,x-agent-max-runes=8000"`
	Mode   string `json:"mode" extensions:"x-agent"`
}

// TrialAgent godoc
// @Summary Test an agent's prompt and model with one example
// @Tags 智能体
// @Accept json
// @Produce json
// @Param id path string true "Agent ID" extensions(x-agent=agent_id)
// @Param request body handler.AgentTrialRequest true "Example prompt"
// @Success 200 {object} handler.DataResponse{data=handler.AgentTrialResponse}
// @x-agent {"id":"agent.trial","risk":"destructive","target":"agent.get","label":"name"}
// @x-agent-hint "One model call, at most 2048 output tokens and 45 seconds. Tests the saved system prompt and model only: no tools, MCP, skills, retrieval, memory or history. Costs model usage; never treats a successful response as validation of those disabled capabilities."
// @Router /agents/{id}/trial [post]
func (h *CustomAgentHandler) TrialAgent(c *gin.Context) {
	var req AgentTrialRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provide a prompt of 1–4000 characters."})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing workspace context"})
		return
	}
	agent, err := h.service.GetAgentByID(ctx, c.Param("id"))
	if err != nil || agent == nil || agent.IsBuiltin || agent.TenantID != tenantID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Custom agent not found in this workspace"})
		return
	}
	if h.models == nil || agent.Config.ModelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Choose a model before running a trial"})
		return
	}
	if len(agent.Config.SystemPrompt) > 128000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "System prompt exceeds the trial limit"})
		return
	}
	model, err := h.models.GetChatModel(ctx, agent.Config.ModelID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Model unavailable; inspect its configuration in Settings"})
		return
	}
	response, err := model.Chat(ctx, []chat.Message{
		{Role: "system", Content: agent.Config.SystemPrompt},
		{Role: "user", Content: req.Prompt},
	}, &chat.ChatOptions{MaxTokens: 2048, Temperature: agent.Config.Temperature, ToolChoice: "none"})
	if err != nil || response == nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Trial failed; inspect model availability in Settings"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": AgentTrialResponse{Answer: response.Content, Mode: "prompt_and_model_only"}})
}
