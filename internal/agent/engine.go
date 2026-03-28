package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	agentmemory "github.com/Tencent/WeKnora/internal/agent/memory"
	"github.com/Tencent/WeKnora/internal/agent/skills"
	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/common"
	appconfig "github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// AgentEngine is the core engine for running ReAct agents
type AgentEngine struct {
	config               *types.AgentConfig
	toolRegistry         *agenttools.ToolRegistry
	chatModel            chat.Chat
	eventBus             *event.EventBus
	knowledgeBasesInfo   []*KnowledgeBaseInfo      // Detailed knowledge base information for prompt
	selectedDocs         []*SelectedDocumentInfo   // User-selected documents (via @ mention)
	contextManager       interfaces.ContextManager // Context manager for writing agent conversation to LLM context
	sessionID            string                    // Session ID for context management
	systemPromptTemplate string                    // System prompt template (optional, uses default if empty)
	skillsManager        *skills.Manager           // Skills manager for Progressive Disclosure (optional)
	appConfig            *appconfig.Config         // Application config for prompt template resolution (optional)
	imageDescriber       ImageDescriberFunc        // VLM function for describing images in tool results (optional)
	tokenEstimator       *agenttoken.Estimator     // Token estimator for context window management
	memoryConsolidator   *agentmemory.Consolidator // Memory consolidator for LLM-powered summarization (optional)
	lastUsage            types.TokenUsage          // Token usage from the most recent LLM call
	lastSentMsgCount     int                       // Number of messages sent in the most recent LLM call
}

// ImageDescriberFunc generates a text description of an image.
// Signature matches vlm.VLM.Predict so it can be injected without importing the vlm package.
type ImageDescriberFunc func(ctx context.Context, imgBytes []byte, prompt string) (string, error)

// NewAgentEngine creates a new agent engine
func NewAgentEngine(
	config *types.AgentConfig,
	chatModel chat.Chat,
	toolRegistry *agenttools.ToolRegistry,
	eventBus *event.EventBus,
	knowledgeBasesInfo []*KnowledgeBaseInfo,
	selectedDocs []*SelectedDocumentInfo,
	contextManager interfaces.ContextManager,
	sessionID string,
	systemPromptTemplate string,
) *AgentEngine {
	if eventBus == nil {
		eventBus = event.NewEventBus()
	}
	tokenEst, err := agenttoken.NewEstimator()
	if err != nil {
		return nil
	}
	engine := &AgentEngine{
		config:               config,
		toolRegistry:         toolRegistry,
		chatModel:            chatModel,
		eventBus:             eventBus,
		knowledgeBasesInfo:   knowledgeBasesInfo,
		selectedDocs:         selectedDocs,
		contextManager:       contextManager,
		sessionID:            sessionID,
		systemPromptTemplate: systemPromptTemplate,
		tokenEstimator:       tokenEst,
	}

	// Initialize memory consolidator if context window management is configured
	if config.MaxContextTokens > 0 {
		engine.memoryConsolidator = agentmemory.NewConsolidator(
			chatModel, tokenEst, config.MaxContextTokens, 0,
		)
	}

	return engine
}

// NewAgentEngineWithSkills creates a new agent engine with skills support
func NewAgentEngineWithSkills(
	config *types.AgentConfig,
	chatModel chat.Chat,
	toolRegistry *agenttools.ToolRegistry,
	eventBus *event.EventBus,
	knowledgeBasesInfo []*KnowledgeBaseInfo,
	selectedDocs []*SelectedDocumentInfo,
	contextManager interfaces.ContextManager,
	sessionID string,
	systemPromptTemplate string,
	skillsManager *skills.Manager,
) *AgentEngine {
	engine := NewAgentEngine(
		config,
		chatModel,
		toolRegistry,
		eventBus,
		knowledgeBasesInfo,
		selectedDocs,
		contextManager,
		sessionID,
		systemPromptTemplate,
	)
	engine.skillsManager = skillsManager
	return engine
}

// SetAppConfig sets the application config for prompt template resolution.
// This allows the engine to read default prompts from config/prompt_templates/ YAML files.
func (e *AgentEngine) SetAppConfig(cfg *appconfig.Config) {
	e.appConfig = cfg
}

// SetImageDescriber sets the VLM function for generating text descriptions of images
// in tool results. When set, MCP tool result images are automatically analyzed and
// their descriptions are appended to the tool message content.
// This follows the same pattern as Handler.analyzeImageAttachments() in the handler layer.
func (e *AgentEngine) SetImageDescriber(fn ImageDescriberFunc) {
	e.imageDescriber = fn
}

// SetSkillsManager sets the skills manager for the engine
func (e *AgentEngine) SetSkillsManager(manager *skills.Manager) {
	e.skillsManager = manager
}

// GetSkillsManager returns the skills manager
func (e *AgentEngine) GetSkillsManager() *skills.Manager {
	return e.skillsManager
}

// estimateCurrentTokens returns the best estimate of the current context token count.
// When API-reported usage from a previous round is available, it uses that as a
// baseline and only BPE-estimates the delta (newly appended messages). Otherwise it
// falls back to a full BPE estimation of all messages.
func (e *AgentEngine) estimateCurrentTokens(messages []chat.Message) int {
	if e.lastUsage.TotalTokens > 0 && e.lastSentMsgCount > 0 && e.lastSentMsgCount < len(messages) {
		delta := e.tokenEstimator.EstimateMessages(messages[e.lastSentMsgCount:])
		return e.lastUsage.TotalTokens + delta
	}
	return e.tokenEstimator.EstimateMessages(messages)
}

// Execute executes the agent with conversation history and streaming output
// All events are emitted to EventBus and handled by subscribers (like Handler layer)
func (e *AgentEngine) Execute(
	ctx context.Context,
	sessionID, messageID, query string,
	llmContext []chat.Message,
	imageURLs ...[]string,
) (*types.AgentState, error) {
	logger.Infof(ctx, "[Agent] Starting execution: session=%s, message=%s, query_len=%d, context_msgs=%d",
		sessionID, messageID, len(query), len(llmContext))
	// Ensure tools are cleaned up after execution
	defer e.toolRegistry.Cleanup(ctx)

	common.PipelineInfo(ctx, "Agent", "execute_start", map[string]interface{}{
		"session_id":   sessionID,
		"message_id":   messageID,
		"query":        query,
		"context_msgs": len(llmContext),
	})

	// Initialize state
	state := &types.AgentState{
		RoundSteps:    []types.AgentStep{},
		KnowledgeRefs: []*types.SearchResult{},
		IsComplete:    false,
		CurrentRound:  0,
	}

	// Build system prompt using progressive RAG prompt
	// If skills are enabled, include skills metadata (Level 1 - Progressive Disclosure)
	// Extract user language from context for prompt placeholder
	language := types.LanguageNameFromContext(ctx)
	var systemPrompt string
	if e.skillsManager != nil && e.skillsManager.IsEnabled() {
		skillsMetadata := e.skillsManager.GetAllMetadata()
		systemPrompt = BuildSystemPromptWithOptions(
			e.knowledgeBasesInfo,
			e.config.WebSearchEnabled,
			e.selectedDocs,
			&BuildSystemPromptOptions{
				SkillsMetadata: skillsMetadata,
				Language:       language,
				Config:         e.appConfig,
			},
			e.systemPromptTemplate,
		)
	} else {
		systemPrompt = BuildSystemPromptWithOptions(
			e.knowledgeBasesInfo,
			e.config.WebSearchEnabled,
			e.selectedDocs,
			&BuildSystemPromptOptions{
				Language: language,
				Config:   e.appConfig,
			},
			e.systemPromptTemplate,
		)
	}
	logger.Debugf(ctx, "[Agent] SystemPrompt: %d chars", len(systemPrompt))

	// Initialize messages with history
	var imgs []string
	if len(imageURLs) > 0 {
		imgs = imageURLs[0]
	}
	messages := e.buildMessagesWithLLMContext(systemPrompt, query, sessionID, llmContext, imgs)

	// Get tool definitions for function calling
	tools := e.buildToolsForLLM()
	toolListStr := strings.Join(listToolNames(tools), ", ")
	logger.Infof(ctx, "[Agent] Ready: %d messages, %d tools [%s], %d images",
		len(messages), len(tools), toolListStr, len(imgs))
	common.PipelineInfo(ctx, "Agent", "tools_ready", map[string]interface{}{
		"session_id": sessionID,
		"tool_count": len(tools),
		"tools":      toolListStr,
	})

	_, err := e.executeLoop(ctx, state, query, messages, tools, sessionID, messageID)
	if err != nil {
		logger.Errorf(ctx, "[Agent] Execution failed: %v", err)
		e.eventBus.Emit(ctx, event.Event{
			ID:        generateEventID("error"),
			Type:      event.EventError,
			SessionID: sessionID,
			Data: event.ErrorData{
				Error:     err.Error(),
				Stage:     "agent_execution",
				SessionID: sessionID,
			},
		})
		return nil, err
	}

	logger.Infof(ctx, "[Agent] Completed: %d rounds, %d steps, complete=%v",
		state.CurrentRound, len(state.RoundSteps), state.IsComplete)
	common.PipelineInfo(ctx, "Agent", "execute_complete", map[string]interface{}{
		"session_id": sessionID,
		"rounds":     state.CurrentRound,
		"steps":      len(state.RoundSteps),
		"complete":   state.IsComplete,
	})
	return state, nil
}

// executeLoop executes the main ReAct loop
// All events are emitted through EventBus with the given sessionID
func (e *AgentEngine) executeLoop(
	ctx context.Context,
	state *types.AgentState,
	query string,
	messages []chat.Message,
	tools []chat.Tool,
	sessionID string,
	messageID string,
) (*types.AgentState, error) {
	startTime := time.Now()
	common.PipelineInfo(ctx, "Agent", "loop_start", map[string]interface{}{
		"max_iterations": e.config.MaxIterations,
	})
	emptyRetries := 0
	for state.CurrentRound < e.config.MaxIterations {
		// Check for context cancellation (request timeout, user cancel, etc.)
		select {
		case <-ctx.Done():
			logger.Warnf(ctx, "[Agent] Context cancelled at round %d: %v",
				state.CurrentRound+1, ctx.Err())
			// Try to salvage existing results
			if totalTC := countTotalToolCalls(state.RoundSteps); totalTC > 0 {
				logger.Infof(ctx, "[Agent] Synthesizing final answer from %d existing tool results",
					totalTC)
				_ = e.streamFinalAnswerToEventBus(ctx, query, state, sessionID)
				state.IsComplete = true
			}
			return state, ctx.Err()
		default:
		}

		roundStart := time.Now()

		// Context window management: estimate current token count using
		// the API-reported usage from the previous round plus a BPE delta
		// for newly appended messages (assistant reply + tool results).
		currentTokens := e.estimateCurrentTokens(messages)
		beforeLen := len(messages)
		messages = e.manageContextWindow(ctx, messages, state.CurrentRound+1, currentTokens)
		if len(messages) < beforeLen {
			currentTokens = e.tokenEstimator.EstimateMessages(messages)
		}

		logger.Infof(ctx, "[Agent][Round-%d/%d] Starting: %d messages, %d tools, est_tokens=%d",
			state.CurrentRound+1, e.config.MaxIterations, len(messages), len(tools), currentTokens)
		common.PipelineInfo(ctx, "Agent", "round_start", map[string]interface{}{
			"iteration":      state.CurrentRound,
			"round":          state.CurrentRound + 1,
			"message_count":  len(messages),
			"pending_tools":  len(tools),
			"max_iterations": e.config.MaxIterations,
		})

		// 1. Think: Call LLM with function calling (includes retry + graceful degradation)
		e.lastSentMsgCount = len(messages)
		response, err := e.callLLMWithRetry(ctx, messages, tools, state, query, state.CurrentRound, sessionID)
		if err != nil {
			return state, err
		}
		if response == nil {
			break
		}
		if response.Usage.TotalTokens > 0 {
			e.lastUsage = response.Usage
			logger.Debugf(ctx, "[Agent][Round-%d] Usage: prompt=%d, completion=%d, total=%d",
				state.CurrentRound+1, response.Usage.PromptTokens,
				response.Usage.CompletionTokens, response.Usage.TotalTokens)
		}

		// Create agent step
		step := types.AgentStep{
			Iteration: state.CurrentRound,
			Thought:   response.Content,
			ToolCalls: make([]types.ToolCall, 0),
			Timestamp: time.Now(),
		}

		// 2. Analyze: Check for stop conditions (natural stop or final_answer tool)
		verdict := e.analyzeResponse(ctx, response, step, state.CurrentRound, sessionID, roundStart)
		if verdict.isDone {
			// Guard against empty content: when the LLM stops naturally with no
			// content and no tool calls (e.g., thinking-only loop without KB),
			// retry with a nudge message instead of accepting an empty answer.
			if verdict.emptyContent {
				emptyRetries++
				if emptyRetries <= maxEmptyResponseRetries {
					logger.Warnf(ctx, "[Agent][Round-%d] Empty content with stop - retrying (%d/%d)",
						state.CurrentRound+1, emptyRetries, maxEmptyResponseRetries)
					messages = append(messages, chat.Message{
						Role:    "user",
						Content: "Please provide your answer by calling the final_answer tool.",
					})
					continue
				}
				// Retries exhausted — use fallback message rather than empty answer
				logger.Warnf(ctx, "[Agent][Round-%d] Empty content after %d retries - using fallback",
					state.CurrentRound+1, maxEmptyResponseRetries)
				state.FinalAnswer = "I'm sorry, I was unable to generate a response. Please try again."
				state.IsComplete = true
				state.RoundSteps = append(state.RoundSteps, verdict.step)
				break
			}
			state.FinalAnswer = verdict.finalAnswer
			state.IsComplete = true
			state.RoundSteps = append(state.RoundSteps, verdict.step)
			break
		}

		// 3. Act: Execute tool calls
		e.executeToolCalls(ctx, response, &step, state.CurrentRound, sessionID)

		// 4. Observe: Add tool results to messages and write to context
		state.RoundSteps = append(state.RoundSteps, step)
		messages = e.appendToolResults(ctx, messages, step)
		common.PipelineInfo(ctx, "Agent", "round_end", map[string]interface{}{
			"iteration":   state.CurrentRound,
			"round":       state.CurrentRound + 1,
			"tool_calls":  len(step.ToolCalls),
			"thought_len": len(step.Thought),
		})

		// 5. Advance to next round
		state.CurrentRound++
	}

	// If loop finished without final answer, generate one
	if !state.IsComplete {
		e.handleMaxIterations(ctx, query, state, sessionID)
	}

	// Emit completion event
	e.emitCompletionEvent(ctx, state, sessionID, messageID, startTime)

	return state, nil
}

// ---------------------------------------------------------------------------
// Tool result image VLM description helpers
// ---------------------------------------------------------------------------

const toolImageAnalysisPrompt = "Describe the content of this image in detail. " +
	"If it contains text, extract all readable text. " +
	"If it contains charts or diagrams, describe the data and structure."

// describeImages generates text descriptions for tool result images using the
// configured imageDescriber (VLM). Each image is decoded from a data URI and
// analyzed independently. Failures are logged and skipped gracefully.
// This follows the same pattern as Handler.analyzeImageAttachments().
func (e *AgentEngine) describeImages(ctx context.Context, imageDataURIs []string) []string {
	if e.imageDescriber == nil {
		return nil
	}
	var descriptions []string
	for i, dataURI := range imageDataURIs {
		if ctx.Err() != nil {
			logger.Warnf(ctx, "[Agent] Context cancelled, skipping remaining %d tool result images", len(imageDataURIs)-i)
			break
		}
		imgBytes, err := decodeDataURIBytes(dataURI)
		if err != nil {
			logger.Warnf(ctx, "[Agent] Failed to decode tool result image %d: %v", i, err)
			continue
		}
		desc, err := e.imageDescriber(ctx, imgBytes, toolImageAnalysisPrompt)
		if err != nil {
			logger.Warnf(ctx, "[Agent] VLM analysis failed for tool result image %d: %v", i, err)
			continue
		}
		descriptions = append(descriptions, strings.TrimSpace(desc))
	}
	return descriptions
}

// decodeDataURIBytes extracts raw bytes from a "data:mime;base64,..." URI.
// Retries with RawStdEncoding when standard base64 decoding fails (some MCP
// servers omit trailing '=' padding).
func decodeDataURIBytes(dataURI string) ([]byte, error) {
	if !strings.HasPrefix(dataURI, "data:") {
		return nil, fmt.Errorf("not a data URI")
	}
	idx := strings.Index(dataURI, ";base64,")
	if idx < 0 {
		return nil, fmt.Errorf("unsupported data URI encoding (expected base64)")
	}
	raw := dataURI[idx+8:]
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// Retry without padding — some MCP servers omit trailing '='
		decoded, err = base64.RawStdEncoding.DecodeString(raw)
	}
	return decoded, err
}
