package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	askTimeout             = 4 * time.Minute
	askCompleteWaitTimeout = 15 * time.Second
	askSessionTitleMaxLen  = 60
	askChannel             = "mcp"
)

func askTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolAsk,
		mcp.WithDescription("Ask the workspace a question and get a synthesized answer with citations. "+
			"WeKnora retrieves from the knowledge bases in scope and runs the agent configured on this "+
			"endpoint. Pass the returned session_id on follow-up questions to keep the conversation going. "+
			"This can take up to a few minutes for agentic runs; prefer search_knowledge when you only need "+
			"raw passages."),
		mcp.WithString("question", mcp.Required(), mcp.Description("The question to answer")),
		mcp.WithString("session_id",
			mcp.Description("Session id returned by a previous ask call, to continue that conversation")),
		mcp.WithArray("knowledge_base_ids", mcp.WithStringItems(),
			mcp.Description("Optional knowledge base ids or names to retrieve from; defaults to the "+
				"endpoint scope or the agent's own configuration")),
		// Not read-only: it creates a session and messages, and the configured
		// agent may run its own tools. Clients must not auto-approve it.
		mcp.WithReadOnlyHintAnnotation(false),
	)
}

type askReference struct {
	KnowledgeID    string  `json:"knowledge_id"`
	KnowledgeTitle string  `json:"knowledge_title,omitempty"`
	ChunkID        string  `json:"chunk_id"`
	Score          float64 `json:"score,omitempty"`
	Excerpt        string  `json:"excerpt"`
}

func (s *Server) handleAsk(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	question, err := req.RequireString("question")
	if err != nil || strings.TrimSpace(question) == "" {
		return mcp.NewToolResultError("question is required"), nil
	}
	question = strings.TrimSpace(question)

	agent, err := s.resolveAskAgent(ctx, ep)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	kbIDs, err := s.resolveAskKnowledgeBases(ctx, ep, agent, req.GetStringSlice("knowledge_base_ids", nil))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	session, err := s.resolveAskSession(ctx, ep, req.GetString("session_id", ""), question)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	answer, refs, err := s.runQA(ctx, session, agent, question, kbIDs)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("failed to answer", err), nil
	}
	structured := map[string]any{
		"answer":     answer,
		"session_id": session.ID,
		"agent_id":   agent.ID,
		"references": refs,
	}
	var text string
	if len(refs) > 0 {
		var b strings.Builder
		b.WriteString(answer)
		b.WriteString("\n\nSources:\n")
		for i, r := range refs {
			title := r.KnowledgeTitle
			if title == "" {
				title = r.KnowledgeID
			}
			fmt.Fprintf(&b, "[%d] %s (document %s)\n", i+1, title, r.KnowledgeID)
		}
		fmt.Fprintf(&b, "\nsession_id: %s", session.ID)
		text = b.String()
	} else {
		text = fmt.Sprintf("%s\n\nsession_id: %s", answer, session.ID)
	}
	return mcp.NewToolResultStructured(structured, text), nil
}

// resolveAskAgent picks the agent configured on the endpoint. Callers cannot
// name an agent: letting a client choose any tenant agent (or an internal
// builtin such as the wiki fixer or skill installer, which carry write and
// shell tools) would bypass the endpoint's tool allowlist.
func (s *Server) resolveAskAgent(ctx context.Context, ep *types.MCPEndpoint) (*types.CustomAgent, error) {
	agentID := strings.TrimSpace(ep.DefaultAgentID)
	if agentID == "" {
		agentID = types.BuiltinQuickAnswerID
	}
	agent, err := s.agentService.GetAgentByID(ctx, agentID)
	if err != nil || agent == nil {
		return nil, fmt.Errorf("the agent configured on this endpoint (%q) is not available", agentID)
	}
	if !types.MCPEndpointAgentAllowed(agent, ep.TenantID) {
		return nil, fmt.Errorf("the agent configured on this endpoint (%q) is not available", agentID)
	}
	return agent, nil
}

// resolveAskKnowledgeBases picks the retrieval scope for one ask call:
// explicit selectors win; otherwise a restricted endpoint pins its own list;
// otherwise the agent's configuration decides, except that an agent with no
// knowledge bases of its own falls back to everything the endpoint can see
// so the call does not fail with "no search targets".
func (s *Server) resolveAskKnowledgeBases(
	ctx context.Context, ep *types.MCPEndpoint, agent *types.CustomAgent, requested []string,
) ([]string, error) {
	hasRequest := false
	for _, r := range requested {
		if strings.TrimSpace(r) != "" {
			hasRequest = true
			break
		}
	}
	if hasRequest {
		kbs, err := s.selectKnowledgeBases(ctx, ep, requested)
		if err != nil {
			return nil, err
		}
		return knowledgeBaseIDs(kbs), nil
	}
	if ep.RestrictsKnowledgeBases() {
		kbs, err := s.allowedKnowledgeBases(ctx, ep)
		if err != nil {
			return nil, err
		}
		return knowledgeBaseIDs(kbs), nil
	}
	mode := strings.ToLower(strings.TrimSpace(agent.Config.KBSelectionMode))
	agentHasNoKBs := mode == "none" || ((mode == "selected" || mode == "") && len(agent.Config.KnowledgeBases) == 0)
	if agentHasNoKBs {
		kbs, err := s.allowedKnowledgeBases(ctx, ep)
		if err != nil {
			return nil, err
		}
		return knowledgeBaseIDs(kbs), nil
	}
	return nil, nil
}

func (s *Server) resolveAskSession(
	ctx context.Context, ep *types.MCPEndpoint, sessionID, question string,
) (*types.Session, error) {
	ownerID := types.MCPEndpointPrincipal(ep.TenantID, ep.ID).StorageID()
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		session, err := s.sessionService.GetSessionByID(ctx, ep.TenantID, sessionID)
		if err != nil || session == nil || session.UserID != ownerID {
			return nil, fmt.Errorf("session %q was not found on this endpoint", sessionID)
		}
		return session, nil
	}
	title := question
	if runes := []rune(title); len(runes) > askSessionTitleMaxLen {
		title = string(runes[:askSessionTitleMaxLen])
	}
	created, err := s.sessionService.CreateSession(ctx, &types.Session{
		TenantID:    ep.TenantID,
		Title:       title,
		Description: "MCP endpoint " + ep.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	if err := s.sessionService.SetSessionOwnerID(ctx, ep.TenantID, created.ID, ownerID); err != nil {
		return nil, fmt.Errorf("failed to bind session: %w", err)
	}
	created.UserID = ownerID
	return created, nil
}

// runQA executes one turn synchronously and returns the final answer and
// the references it cited. It mirrors the IM channel's non-streaming path.
func (s *Server) runQA(
	ctx context.Context, session *types.Session, agent *types.CustomAgent, question string, kbIDs []string,
) (string, []askReference, error) {
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()

	requestID := uuid.New().String()
	userMsg, err := s.messageService.CreateMessage(ctx, &types.Message{
		SessionID:   session.ID,
		Role:        "user",
		Content:     question,
		RequestID:   requestID,
		CreatedAt:   time.Now(),
		IsCompleted: true,
		Channel:     askChannel,
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to create user message: %w", err)
	}
	assistantMsg, err := s.messageService.CreateMessage(ctx, &types.Message{
		SessionID:   session.ID,
		Role:        "assistant",
		RequestID:   requestID,
		CreatedAt:   time.Now(),
		IsCompleted: false,
		Channel:     askChannel,
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to create assistant message: %w", err)
	}

	var (
		mu       sync.Mutex
		answer   strings.Builder
		final    string
		refs     []*types.SearchResult
		qaErr    error
		done     = make(chan struct{})
		complete = make(chan struct{})
		doneOnce sync.Once
		compOnce sync.Once
	)
	closeDone := func() { doneOnce.Do(func() { close(done) }) }
	closeComplete := func() { compOnce.Do(func() { close(complete) }) }

	bus := event.NewEventBus()
	bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}
		mu.Lock()
		answer.WriteString(data.Content)
		mu.Unlock()
		if data.Done {
			closeDone()
		}
		return nil
	})
	bus.On(event.EventAgentReferences, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentReferencesData)
		if !ok {
			return nil
		}
		mu.Lock()
		collectReferences(&refs, data.References)
		mu.Unlock()
		return nil
	})
	bus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentCompleteData)
		if !ok {
			return nil
		}
		mu.Lock()
		if data.FinalAnswer != "" {
			final = data.FinalAnswer
		}
		if len(data.KnowledgeRefs) > 0 {
			var completeRefs []*types.SearchResult
			collectReferences(&completeRefs, data.KnowledgeRefs)
			if len(completeRefs) > 0 {
				refs = completeRefs
			}
		}
		mu.Unlock()
		closeComplete()
		return nil
	})
	bus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			return nil
		}
		mu.Lock()
		qaErr = fmt.Errorf("%s", data.Error)
		mu.Unlock()
		closeDone()
		closeComplete()
		return nil
	})

	useAgent := agent.IsAgentMode()
	go func() {
		if useAgent {
			defer closeDone()
			defer closeComplete()
		}
		req := &types.QARequest{
			Session:            session,
			Query:              question,
			AssistantMessageID: assistantMsg.ID,
			UserMessageID:      userMsg.ID,
			CustomAgent:        agent,
			KnowledgeBaseIDs:   kbIDs,
			// Web search is never enabled from the MCP surface: the builtin
			// quick-answer agent turns it on by default, which would make an
			// otherwise read-only endpoint reach the public internet.
			WebSearchEnabled: false,
		}
		var runErr error
		if useAgent {
			runErr = s.sessionService.AgentQA(ctx, req, bus)
		} else {
			runErr = s.sessionService.KnowledgeQA(ctx, req, bus)
		}
		if runErr != nil {
			mu.Lock()
			if qaErr == nil {
				qaErr = runErr
			}
			mu.Unlock()
			closeDone()
			closeComplete()
		}
	}()

	select {
	case <-done:
		if useAgent {
			timer := time.NewTimer(askCompleteWaitTimeout)
			select {
			case <-complete:
			case <-ctx.Done():
			case <-timer.C:
				logger.Warnf(ctx, "[mcpserver] ask timed out waiting for agent completion: session=%s", session.ID)
			}
			timer.Stop()
		}
	case <-ctx.Done():
		assistantMsg.Content = "The answer was interrupted before completion."
		assistantMsg.IsCompleted = true
		if updateErr := s.messageService.UpdateMessage(context.WithoutCancel(ctx), assistantMsg); updateErr != nil {
			logger.Warnf(ctx, "[mcpserver] failed to finalize interrupted message: %v", updateErr)
		}
		return "", nil, fmt.Errorf("answer timed out: %w", ctx.Err())
	}

	mu.Lock()
	text := answer.String()
	if strings.TrimSpace(text) == "" && final != "" {
		text = final
	}
	collected := append([]*types.SearchResult(nil), refs...)
	runErr := qaErr
	mu.Unlock()

	if strings.TrimSpace(text) == "" && runErr != nil {
		assistantMsg.Content = "The answer failed: " + runErr.Error()
		assistantMsg.IsCompleted = true
		if updateErr := s.messageService.UpdateMessage(context.WithoutCancel(ctx), assistantMsg); updateErr != nil {
			logger.Warnf(ctx, "[mcpserver] failed to finalize failed message: %v", updateErr)
		}
		return "", nil, runErr
	}
	if strings.TrimSpace(text) == "" {
		text = "No answer could be produced for this question."
	}
	assistantMsg.Content = text
	assistantMsg.IsCompleted = true
	if len(collected) > 0 {
		assistantMsg.KnowledgeReferences = types.References(collected)
	}
	if err := s.messageService.UpdateMessage(context.WithoutCancel(ctx), assistantMsg); err != nil {
		logger.Warnf(ctx, "[mcpserver] failed to persist assistant message: %v", err)
	}
	return text, summarizeReferences(collected), nil
}

func collectReferences(dst *[]*types.SearchResult, raw interface{}) {
	switch v := raw.(type) {
	case []*types.SearchResult:
		*dst = append(*dst, v...)
	case []interface{}:
		for _, item := range v {
			if sr, ok := item.(*types.SearchResult); ok {
				*dst = append(*dst, sr)
			}
		}
	}
}

const askExcerptMaxRunes = 300

func summarizeReferences(refs []*types.SearchResult) []askReference {
	out := make([]askReference, 0, len(refs))
	seen := map[string]struct{}{}
	for _, r := range refs {
		if r == nil {
			continue
		}
		key := r.ID
		if key == "" {
			key = r.KnowledgeID + ":" + fmt.Sprint(r.ChunkIndex)
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		excerpt := strings.TrimSpace(r.Content)
		if runes := []rune(excerpt); len(runes) > askExcerptMaxRunes {
			excerpt = string(runes[:askExcerptMaxRunes]) + "…"
		}
		out = append(out, askReference{
			KnowledgeID:    r.KnowledgeID,
			KnowledgeTitle: r.KnowledgeTitle,
			ChunkID:        r.ID,
			Score:          r.Score,
			Excerpt:        excerpt,
		})
	}
	return out
}
