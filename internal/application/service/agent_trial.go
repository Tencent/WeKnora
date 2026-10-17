package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// agentTrialTimeout bounds one trial; the try_agent tool timeout in the
	// agent package must stay above it.
	agentTrialTimeout             = 4 * time.Minute
	agentTrialCompleteWaitTimeout = 15 * time.Second
	agentTrialTitle               = "Agent trial"
)

// AgentTrialRunner runs the assistant's try_agent calls. The agent service
// needs it before the session service exists, so the session service is
// bound once the container has built it.
type AgentTrialRunner struct {
	agents   interfaces.CustomAgentService
	messages interfaces.MessageService

	mu       sync.RWMutex
	sessions interfaces.SessionService
}

// NewAgentTrialRunner creates a runner that refuses trials until Bind.
func NewAgentTrialRunner(
	agents interfaces.CustomAgentService, messages interfaces.MessageService,
) *AgentTrialRunner {
	return &AgentTrialRunner{agents: agents, messages: messages}
}

// Bind supplies the session service that runs the trials.
func (r *AgentTrialRunner) Bind(sessions interfaces.SessionService) {
	r.mu.Lock()
	r.sessions = sessions
	r.mu.Unlock()
}

func (r *AgentTrialRunner) sessionService() interfaces.SessionService {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions
}

// triableAgent loads the agent, refusing the built-ins that serve the product
// itself and any agent that could manage WeKnora: a trial must not become a
// way to run the operation catalog without the assistant's approval gate.
func (r *AgentTrialRunner) triableAgent(ctx context.Context, agentID string) (*types.CustomAgent, error) {
	switch agentID {
	case types.BuiltinWeKnoraAssistantID, types.BuiltinWikiFixerID, types.BuiltinSkillInstallerID:
		return nil, errors.New("this built-in agent serves WeKnora itself and cannot be tried")
	}
	agent, err := r.agents.GetAgentByID(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("agent %s was not found: %w", agentID, err)
	}
	if agent == nil {
		return nil, fmt.Errorf("agent %s was not found", agentID)
	}
	if types.GrantsOperationCatalog(agent) {
		return nil, errors.New("this agent manages WeKnora and cannot be tried")
	}
	return agent, nil
}

// AgentName returns the name of a triable agent.
func (r *AgentTrialRunner) AgentName(ctx context.Context, agentID string) (string, error) {
	agent, err := r.triableAgent(ctx, agentID)
	if err != nil {
		return "", err
	}
	return agent.Name, nil
}

// TryAgent asks the agent question in a session of its own, then removes it.
func (r *AgentTrialRunner) TryAgent(ctx context.Context, agentID, question string) (*tools.AgentTrial, error) {
	sessions := r.sessionService()
	if sessions == nil {
		return nil, errors.New("agent trials are not available on this server")
	}
	agent, err := r.triableAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return nil, errors.New("the trial has no workspace")
	}
	session, err := sessions.CreateSession(ctx, &types.Session{
		TenantID: tenantID,
		UserID:   types.SessionOwnerIDFromContext(ctx),
		Kind:     types.SessionKindAgentTrial,
		Title:    agentTrialTitle,
	})
	if err != nil {
		return nil, fmt.Errorf("create the trial conversation: %w", err)
	}
	defer func() {
		if err := sessions.DeleteSession(context.WithoutCancel(ctx), session.ID); err != nil {
			logger.Warnf(ctx, "[AgentTrial] remove trial session %s: %v", session.ID, err)
		}
	}()

	mode := "quick_answer"
	if agent.IsAgentMode() {
		mode = "smart_reasoning"
	}
	started := time.Now()
	trial := r.run(ctx, sessions, session, agent, question)
	trial.AgentName = agent.Name
	trial.AgentMode = mode
	trial.DurationMs = time.Since(started).Milliseconds()
	return trial, nil
}

func (r *AgentTrialRunner) run(
	ctx context.Context, sessions interfaces.SessionService, session *types.Session,
	agent *types.CustomAgent, question string,
) *tools.AgentTrial {
	ctx, cancel := context.WithTimeout(ctx, agentTrialTimeout)
	defer cancel()

	requestID := uuid.New().String()
	userMsg, err := r.messages.CreateMessage(ctx, &types.Message{
		SessionID: session.ID, Role: "user", Content: question, RequestID: requestID,
		CreatedAt: time.Now(), IsCompleted: true, Channel: types.ChannelWeb,
	})
	if err != nil {
		return &tools.AgentTrial{Error: fmt.Sprintf("create the question message: %v", err)}
	}
	assistantMsg, err := r.messages.CreateMessage(ctx, &types.Message{
		SessionID: session.ID, Role: "assistant", RequestID: requestID,
		CreatedAt: time.Now(), Channel: types.ChannelWeb,
	})
	if err != nil {
		return &tools.AgentTrial{Error: fmt.Sprintf("create the answer message: %v", err)}
	}

	c := newTrialCollector()
	bus := event.NewEventBus()
	c.subscribe(bus)

	useAgent := agent.IsAgentMode()
	go func() {
		if useAgent {
			defer c.closeDone()
			defer c.closeComplete()
		}
		req := &types.QARequest{
			Session:            session,
			Query:              question,
			AssistantMessageID: assistantMsg.ID,
			UserMessageID:      userMsg.ID,
			CustomAgent:        agent,
			WebSearchEnabled:   agent.Config.WebSearchEnabled,
		}
		var runErr error
		if useAgent {
			runErr = sessions.AgentQA(ctx, req, bus)
		} else {
			runErr = sessions.KnowledgeQA(ctx, req, bus)
		}
		if runErr != nil {
			c.fail(runErr.Error())
		}
	}()

	select {
	case <-c.done:
		if useAgent {
			timer := time.NewTimer(agentTrialCompleteWaitTimeout)
			select {
			case <-c.complete:
			case <-ctx.Done():
			case <-timer.C:
			}
			timer.Stop()
		}
	case <-ctx.Done():
		trial := c.result()
		trial.Error = fmt.Sprintf("the agent did not finish within %s", agentTrialTimeout)
		return trial
	}
	return c.result()
}

// trialCollector gathers what a trial run streams.
type trialCollector struct {
	mu        sync.Mutex
	answer    strings.Builder
	final     string
	refs      []*types.SearchResult
	calls     []tools.AgentTrialToolCall
	callIndex map[string]int
	errText   string

	done, complete         chan struct{}
	doneOnce, completeOnce sync.Once
}

func newTrialCollector() *trialCollector {
	return &trialCollector{
		callIndex: map[string]int{},
		done:      make(chan struct{}),
		complete:  make(chan struct{}),
	}
}

func (c *trialCollector) closeDone()     { c.doneOnce.Do(func() { close(c.done) }) }
func (c *trialCollector) closeComplete() { c.completeOnce.Do(func() { close(c.complete) }) }

func (c *trialCollector) fail(msg string) {
	c.mu.Lock()
	if c.errText == "" {
		c.errText = msg
	}
	c.mu.Unlock()
	c.closeDone()
	c.closeComplete()
}

func (c *trialCollector) subscribe(bus *event.EventBus) {
	bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentFinalAnswerData)
		if !ok {
			return nil
		}
		c.mu.Lock()
		c.answer.WriteString(data.Content)
		c.mu.Unlock()
		if data.Done {
			c.closeDone()
		}
		return nil
	})
	bus.On(event.EventAgentToolCall, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentToolCallData)
		if !ok {
			return nil
		}
		c.mu.Lock()
		c.callIndex[data.ToolCallID] = len(c.calls)
		c.calls = append(c.calls, tools.AgentTrialToolCall{Name: data.ToolName})
		c.mu.Unlock()
		return nil
	})
	bus.On(event.EventAgentToolResult, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentToolResultData)
		if !ok {
			return nil
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		i, seen := c.callIndex[data.ToolCallID]
		if !seen {
			i = len(c.calls)
			c.calls = append(c.calls, tools.AgentTrialToolCall{Name: data.ToolName})
		}
		c.calls[i].Success = data.Success
		c.calls[i].Error = data.Error
		return nil
	})
	bus.On(event.EventAgentReferences, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentReferencesData)
		if !ok {
			return nil
		}
		c.mu.Lock()
		c.refs = appendSearchResults(c.refs, data.References)
		c.mu.Unlock()
		return nil
	})
	bus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentCompleteData)
		if !ok {
			return nil
		}
		c.mu.Lock()
		if data.FinalAnswer != "" {
			c.final = data.FinalAnswer
		}
		if refs := appendSearchResults(nil, data.KnowledgeRefs); len(refs) > 0 {
			c.refs = refs
		}
		c.mu.Unlock()
		c.closeComplete()
		return nil
	})
	bus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			return nil
		}
		c.fail(data.Error)
		return nil
	})
}

func (c *trialCollector) result() *tools.AgentTrial {
	c.mu.Lock()
	defer c.mu.Unlock()
	answer := c.answer.String()
	if strings.TrimSpace(answer) == "" {
		answer = c.final
	}
	trial := &tools.AgentTrial{
		Answer:    answer,
		ToolCalls: append([]tools.AgentTrialToolCall(nil), c.calls...),
		Error:     c.errText,
	}
	seen := map[string]bool{}
	for _, ref := range c.refs {
		if ref == nil {
			continue
		}
		key := ref.ID
		if key == "" {
			key = fmt.Sprintf("%s:%d", ref.KnowledgeID, ref.ChunkIndex)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		trial.References = append(trial.References, tools.AgentTrialReference{
			Title: ref.KnowledgeTitle, ChunkIndex: ref.ChunkIndex, Score: ref.Score,
		})
	}
	return trial
}

func appendSearchResults(dst []*types.SearchResult, raw interface{}) []*types.SearchResult {
	switch v := raw.(type) {
	case []*types.SearchResult:
		return append(dst, v...)
	case []interface{}:
		for _, item := range v {
			if sr, ok := item.(*types.SearchResult); ok {
				dst = append(dst, sr)
			}
		}
	}
	return dst
}
