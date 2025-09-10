// Package chatpipline provides chat pipeline processing capabilities
// Including query rewriting, history processing, model invocation and other features
package chatpipline

import (
	"bytes"
	"context"
	"html/template"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginRewrite is a plugin for rewriting user queries
// It uses historical dialog context and large language models to optimize the user's original query
type PluginRewrite struct {
	modelService   interfaces.ModelService   // Model service for calling large language models
	messageService interfaces.MessageService // Message service for retrieving historical messages
	config         *config.Config            // System configuration
}

// reg is a regular expression used to match and remove content between <think></think> tags
var reg = regexp.MustCompile(`(?s)<think>.*?</think>`)

// NewPluginRewrite creates a new query rewriting plugin instance
// Also registers the plugin with the event manager
func NewPluginRewrite(eventManager *EventManager,
	modelService interfaces.ModelService, messageService interfaces.MessageService,
	config *config.Config,
) *PluginRewrite {
	res := &PluginRewrite{
		modelService:   modelService,
		messageService: messageService,
		config:         config,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the list of event types this plugin responds to
// This plugin only responds to REWRITE_QUERY events
func (p *PluginRewrite) ActivationEvents() []types.EventType {
	return []types.EventType{types.REWRITE_QUERY}
}

// OnEvent processes triggered events
// When receiving a REWRITE_QUERY event, it rewrites the user query using conversation history and the language model
func (p *PluginRewrite) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	// Initialize rewritten query as original query
	chatManage.RewriteQuery = chatManage.Query

	// Get conversation history
	history, err := p.messageService.GetRecentMessagesBySession(ctx, chatManage.SessionID, 20)
	if err != nil {
		logger.Errorf(ctx, "Failed to get conversation history, session_id: %s, error: %v", chatManage.SessionID, err)
	}

	// Convert historical messages to conversation history structure
	historyMap := make(map[string]*types.History)

	// Process historical messages, grouped by requestID
	for _, message := range history {
		history, ok := historyMap[message.RequestID]
		if !ok {
			history = &types.History{}
		}
		if message.Role == "user" {
			// User message as query
			history.Query = message.Content
			history.CreateAt = message.CreatedAt
		} else {
			// System message as answer, while removing thinking process
			history.Answer = reg.ReplaceAllString(message.Content, "")
			history.KnowledgeReferences = message.KnowledgeReferences
		}
		historyMap[message.RequestID] = history
	}

	// Convert to list and filter incomplete conversations
	historyList := make([]*types.History, 0)
	for _, history := range historyMap {
		if history.Answer != "" && history.Query != "" {
			historyList = append(historyList, history)
		}
	}

	// Sort by time, keep the most recent conversations
	sort.Slice(historyList, func(i, j int) bool {
		return historyList[i].CreateAt.After(historyList[j].CreateAt)
	})

	// Limit the number of historical records
	if len(historyList) > p.config.Conversation.MaxRounds {
		historyList = historyList[:p.config.Conversation.MaxRounds]
	}

	// Reverse to chronological order
	slices.Reverse(historyList)
	chatManage.History = historyList

	userTmpl, err := template.New("rewriteContent").Parse(p.config.Conversation.RewritePromptUser)
	if err != nil {
		logger.Errorf(ctx, "Failed to execute template, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}
	systemTmpl, err := template.New("rewriteContent").Parse(p.config.Conversation.RewritePromptSystem)
	if err != nil {
		logger.GetLogger(ctx).Errorf("Failed to execute template, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}
	currentTime := time.Now().Format("2006-01-02 15:04:05")
	var userContent, systemContent bytes.Buffer
	err = userTmpl.Execute(&userContent, map[string]interface{}{
		"Query":        chatManage.Query,
		"CurrentTime":  currentTime,
		"Yesterday":    time.Now().AddDate(0, 0, -1).Format("2006-01-02"),
		"Conversation": historyList,
	})
	if err != nil {
		logger.GetLogger(ctx).Errorf("Failed to execute template, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}
	err = systemTmpl.Execute(&systemContent, map[string]interface{}{
		"Query":        chatManage.Query,
		"CurrentTime":  currentTime,
		"Yesterday":    time.Now().AddDate(0, 0, -1).Format("2006-01-02"),
		"Conversation": historyList,
	})
	if err != nil {
		logger.Errorf(ctx, "Failed to execute template, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}
	rewriteModel, err := p.modelService.GetChatModel(ctx, chatManage.ChatModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get model, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}

	// Call model to rewrite query
	thinking := false
	response, err := rewriteModel.Chat(ctx, []chat.Message{
		{
			Role:    "system",
			Content: systemContent.String(),
		},
		{
			Role:    "user",
			Content: userContent.String(),
		},
	}, &chat.ChatOptions{
		Temperature:         0.3,
		MaxCompletionTokens: 50,
		Thinking:            &thinking,
	})
	if err != nil {
		logger.Errorf(ctx, "Failed to execute model, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}

	if response.Content != "" {
		// Update rewritten query
		chatManage.RewriteQuery = response.Content
	}
	logger.GetLogger(ctx).Infof("Rewritten query, session_id: %s, rewrite_query: %s",
		chatManage.SessionID, chatManage.RewriteQuery)
	return next()
}
