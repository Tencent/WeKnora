package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// Custom agent related errors
var (
	ErrAgentNotFound       = errors.New("agent not found")
	ErrCannotModifyBuiltin = errors.New("cannot modify built-in agent basic info")
	ErrCannotDeleteBuiltin = errors.New("cannot delete built-in agent")
	ErrAgentNameRequired   = errors.New("agent name is required")
)

// customAgentService implements the CustomAgentService interface
type customAgentService struct {
	repo      interfaces.CustomAgentRepository
	chunkRepo interfaces.ChunkRepository
	kbService interfaces.KnowledgeBaseService
}

// NewCustomAgentService creates a new custom agent service
func NewCustomAgentService(
	repo interfaces.CustomAgentRepository,
	chunkRepo interfaces.ChunkRepository,
	kbService interfaces.KnowledgeBaseService,
) interfaces.CustomAgentService {
	return &customAgentService{
		repo:      repo,
		chunkRepo: chunkRepo,
		kbService: kbService,
	}
}

// CreateAgent creates a new custom agent
func (s *customAgentService) CreateAgent(ctx context.Context, agent *types.CustomAgent) (*types.CustomAgent, error) {
	// Validate required fields
	if strings.TrimSpace(agent.Name) == "" {
		return nil, ErrAgentNameRequired
	}

	// Generate UUID and set creation timestamps
	if agent.ID == "" {
		agent.ID = uuid.New().String()
	}

	// Get tenant ID from context
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, ErrInvalidTenantID
	}
	agent.TenantID = tenantID

	// Set timestamps
	agent.CreatedAt = time.Now()
	agent.UpdatedAt = time.Now()

	// Ensure agent mode is set for user-created agents
	if agent.Config.AgentMode == "" {
		agent.Config.AgentMode = types.AgentModeQuickAnswer
	}

	// Cannot create built-in agents
	agent.IsBuiltin = false

	// Set defaults
	agent.EnsureDefaults()

	logger.Infof(ctx, "Creating custom agent, ID: %s, tenant ID: %d, name: %s, agent_mode: %s",
		agent.ID, agent.TenantID, agent.Name, agent.Config.AgentMode)

	if err := s.repo.CreateAgent(ctx, agent); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id":  agent.ID,
			"tenant_id": agent.TenantID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Custom agent created successfully, ID: %s, name: %s", agent.ID, agent.Name)
	return agent, nil
}

// GetAgentByID retrieves an agent by its ID (including built-in agents)
func (s *customAgentService) GetAgentByID(ctx context.Context, id string) (*types.CustomAgent, error) {
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		return nil, errors.New("agent ID cannot be empty")
	}

	// Get tenant ID from context
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, ErrInvalidTenantID
	}

	// Check if it's a built-in agent using the registry
	if types.IsBuiltinAgentID(id) {
		// Try to get from database first (for customized config)
		agent, err := s.repo.GetAgentByID(ctx, id, tenantID)
		if err == nil {
			// Found in database, return with customized config
			return agent, nil
		}
		// Not in database, return default built-in agent from registry (i18n-aware)
		if builtinAgent := types.GetBuiltinAgentWithContext(ctx, id, tenantID); builtinAgent != nil {
			return builtinAgent, nil
		}
	}

	// Query from database
	agent, err := s.repo.GetAgentByID(ctx, id, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrCustomAgentNotFound) {
			return nil, ErrAgentNotFound
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		return nil, err
	}

	return agent, nil
}

// GetAgentByIDAndTenant retrieves an agent by ID and tenant (for shared agents; does not resolve built-in)
func (s *customAgentService) GetAgentByIDAndTenant(ctx context.Context, id string, tenantID uint64) (*types.CustomAgent, error) {
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		return nil, errors.New("agent ID cannot be empty")
	}
	agent, err := s.repo.GetAgentByID(ctx, id, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrCustomAgentNotFound) {
			return nil, ErrAgentNotFound
		}
		return nil, err
	}
	return agent, nil
}

// ListAgents lists all agents for the current tenant (including built-in agents)
func (s *customAgentService) ListAgents(ctx context.Context) ([]*types.CustomAgent, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, ErrInvalidTenantID
	}

	// Get all agents from database (including built-in agents with customized config)
	allAgents, err := s.repo.ListAgentsByTenantID(ctx, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		return nil, err
	}

	// Track which built-in agents exist in database
	builtinInDB := make(map[string]bool)
	for _, agent := range allAgents {
		if types.IsBuiltinAgentID(agent.ID) {
			builtinInDB[agent.ID] = true
		}
	}

	// Build result: built-in agents first, then custom agents
	builtinIDs := types.GetBuiltinAgentIDs()
	result := make([]*types.CustomAgent, 0, len(allAgents)+len(builtinIDs))

	// Add built-in agents in order
	for _, builtinID := range builtinIDs {
		if builtinInDB[builtinID] {
			// Use customized config from database
			for _, agent := range allAgents {
				if agent.ID == builtinID {
					result = append(result, agent)
					break
				}
			}
		} else {
			// Use default built-in agent (i18n-aware)
			if agent := types.GetBuiltinAgentWithContext(ctx, builtinID, tenantID); agent != nil {
				result = append(result, agent)
			}
		}
	}

	// Add custom agents
	for _, agent := range allAgents {
		if !types.IsBuiltinAgentID(agent.ID) {
			result = append(result, agent)
		}
	}

	return result, nil
}

// UpdateAgent updates an agent's information
func (s *customAgentService) UpdateAgent(ctx context.Context, agent *types.CustomAgent) (*types.CustomAgent, error) {
	if agent.ID == "" {
		logger.Error(ctx, "Agent ID is empty")
		return nil, errors.New("agent ID cannot be empty")
	}

	// Get tenant ID from context
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, ErrInvalidTenantID
	}

	// Handle built-in agents specially using registry
	if types.IsBuiltinAgentID(agent.ID) {
		return s.updateBuiltinAgent(ctx, agent, tenantID)
	}

	// Get existing agent
	existingAgent, err := s.repo.GetAgentByID(ctx, agent.ID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrCustomAgentNotFound) {
			return nil, ErrAgentNotFound
		}
		return nil, err
	}

	// Cannot modify built-in status
	if existingAgent.IsBuiltin {
		return nil, ErrCannotModifyBuiltin
	}

	// Validate name
	if strings.TrimSpace(agent.Name) == "" {
		return nil, ErrAgentNameRequired
	}

	// Update fields
	existingAgent.Name = agent.Name
	existingAgent.Description = agent.Description
	existingAgent.Avatar = agent.Avatar
	existingAgent.Config = agent.Config
	existingAgent.UpdatedAt = time.Now()

	// Ensure defaults
	existingAgent.EnsureDefaults()

	logger.Infof(ctx, "Updating custom agent, ID: %s, name: %s", agent.ID, agent.Name)

	if err := s.repo.UpdateAgent(ctx, existingAgent); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": agent.ID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Custom agent updated successfully, ID: %s", agent.ID)
	return existingAgent, nil
}

// updateBuiltinAgent updates a built-in agent's configuration (but not basic info)
func (s *customAgentService) updateBuiltinAgent(ctx context.Context, agent *types.CustomAgent, tenantID uint64) (*types.CustomAgent, error) {
	// Get the default built-in agent from registry (i18n-aware)
	defaultAgent := types.GetBuiltinAgentWithContext(ctx, agent.ID, tenantID)
	if defaultAgent == nil {
		return nil, ErrAgentNotFound
	}

	// Try to get existing customized config from database
	existingAgent, err := s.repo.GetAgentByID(ctx, agent.ID, tenantID)
	if err != nil && !errors.Is(err, repository.ErrCustomAgentNotFound) {
		return nil, err
	}

	if existingAgent != nil {
		// Update existing record - only update config, keep basic info unchanged
		existingAgent.Config = agent.Config
		existingAgent.UpdatedAt = time.Now()
		existingAgent.EnsureDefaults()

		logger.Infof(ctx, "Updating built-in agent config, ID: %s", agent.ID)

		if err := s.repo.UpdateAgent(ctx, existingAgent); err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"agent_id": agent.ID,
			})
			return nil, err
		}

		logger.Infof(ctx, "Built-in agent config updated successfully, ID: %s", agent.ID)
		return existingAgent, nil
	}

	// Create new record for built-in agent with customized config
	newAgent := &types.CustomAgent{
		ID:          defaultAgent.ID,
		Name:        defaultAgent.Name,
		Description: defaultAgent.Description,
		Avatar:      defaultAgent.Avatar,
		IsBuiltin:   true,
		TenantID:    tenantID,
		Config:      agent.Config,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	newAgent.EnsureDefaults()

	logger.Infof(ctx, "Creating built-in agent config record, ID: %s, tenant ID: %d", agent.ID, tenantID)

	if err := s.repo.CreateAgent(ctx, newAgent); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id":  agent.ID,
			"tenant_id": tenantID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Built-in agent config record created successfully, ID: %s", agent.ID)
	return newAgent, nil
}

// DeleteAgent deletes an agent
func (s *customAgentService) DeleteAgent(ctx context.Context, id string) error {
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		return errors.New("agent ID cannot be empty")
	}

	// Cannot delete built-in agents using registry check
	if types.IsBuiltinAgentID(id) {
		return ErrCannotDeleteBuiltin
	}

	// Get tenant ID from context
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return ErrInvalidTenantID
	}

	// Get existing agent to verify ownership
	existingAgent, err := s.repo.GetAgentByID(ctx, id, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrCustomAgentNotFound) {
			return ErrAgentNotFound
		}
		return err
	}

	// Cannot delete built-in agents
	if existingAgent.IsBuiltin {
		return ErrCannotDeleteBuiltin
	}

	logger.Infof(ctx, "Deleting custom agent, ID: %s", id)

	if err := s.repo.DeleteAgent(ctx, id, tenantID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		return err
	}

	logger.Infof(ctx, "Custom agent deleted successfully, ID: %s", id)
	return nil
}

// CopyAgent creates a copy of an existing agent
func (s *customAgentService) CopyAgent(ctx context.Context, id string) (*types.CustomAgent, error) {
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		return nil, errors.New("agent ID cannot be empty")
	}

	// Get tenant ID from context
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, ErrInvalidTenantID
	}

	// Get the source agent
	sourceAgent, err := s.GetAgentByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Create a new agent with copied data
	newAgent := &types.CustomAgent{
		ID:          uuid.New().String(),
		Name:        sourceAgent.Name + " (副本)",
		Description: sourceAgent.Description,
		Avatar:      sourceAgent.Avatar,
		IsBuiltin:   false, // Copied agents are never built-in
		TenantID:    tenantID,
		Config:      sourceAgent.Config,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// Ensure defaults
	newAgent.EnsureDefaults()

	logger.Infof(ctx, "Copying agent, source ID: %s, new ID: %s", id, newAgent.ID)

	if err := s.repo.CreateAgent(ctx, newAgent); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"source_agent_id": id,
			"new_agent_id":    newAgent.ID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Agent copied successfully, source ID: %s, new ID: %s", id, newAgent.ID)
	return newAgent, nil
}

// GetSuggestedQuestions returns suggested questions for the agent based on its
// associated knowledge bases.
func (s *customAgentService) GetSuggestedQuestions(
	ctx context.Context,
	agentID string,
	kbIDs []string,
	knowledgeIDs []string,
	limit int,
) ([]types.SuggestedQuestion, error) {
	if limit <= 0 {
		limit = 6
	}

	// Get tenant ID from context
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, ErrInvalidTenantID
	}

	// Get agent configuration
	agent, err := s.GetAgentByID(ctx, agentID)
	if err != nil {
		return nil, err
	}

	var result []types.SuggestedQuestion

	// 1. Add agent config suggested_prompts first (highest priority)
	if len(agent.Config.SuggestedPrompts) > 0 {
		for _, prompt := range agent.Config.SuggestedPrompts {
			if strings.TrimSpace(prompt) == "" {
				continue
			}
			result = append(result, types.SuggestedQuestion{
				Question: prompt,
				Source:   "agent_config",
			})
		}
	}

	// 2. Determine knowledge base scope
	effectiveKBIDs := kbIDs
	if len(effectiveKBIDs) == 0 && len(knowledgeIDs) == 0 {
		// Use agent's KB configuration
		switch agent.Config.KBSelectionMode {
		case "all":
			kbs, err := s.kbService.ListKnowledgeBases(ctx)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"agent_id": agentID,
				})
				// Return what we have so far (agent_config suggestions)
				return s.truncateQuestions(result, limit), nil
			}
			for _, kb := range kbs {
				effectiveKBIDs = append(effectiveKBIDs, kb.ID)
			}
		case "selected":
			effectiveKBIDs = agent.Config.KnowledgeBases
		case "none":
			// No KB access, return agent_config suggestions only
			return s.truncateQuestions(result, limit), nil
		default:
			// Default to agent's configured KBs
			effectiveKBIDs = agent.Config.KnowledgeBases
		}
	}

	if len(effectiveKBIDs) == 0 && len(knowledgeIDs) == 0 {
		return s.truncateQuestions(result, limit), nil
	}

	// Deduplicate questions we've already collected
	seen := make(map[string]bool)
	for _, q := range result {
		seen[q.Question] = true
	}

	remaining := limit - len(result)
	if remaining <= 0 {
		return s.truncateQuestions(result, limit), nil
	}

	// 3. Collect all candidate chunks from both FAQ and Document KBs,
	//    then sort by updated_at uniformly (not FAQ-first).
	type candidate struct {
		question  types.SuggestedQuestion
		updatedAt time.Time
	}
	var candidates []candidate

	// Determine query scope
	queryKBIDs := effectiveKBIDs
	queryKnowledgeIDs := knowledgeIDs

	// Fetch more than needed from each source, we'll merge-sort and truncate
	fetchLimit := remaining * 2
	if fetchLimit < 10 {
		fetchLimit = 10
	}

	// Collect FAQ recommended chunks
	faqChunks, err := s.chunkRepo.ListRecommendedFAQChunks(ctx, tenantID, queryKBIDs, queryKnowledgeIDs, fetchLimit)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": agentID,
		})
	} else {
		for _, chunk := range faqChunks {
			meta, err := chunk.FAQMetadata()
			if err != nil || meta == nil || meta.StandardQuestion == "" {
				continue
			}
			if seen[meta.StandardQuestion] {
				continue
			}
			seen[meta.StandardQuestion] = true
			candidates = append(candidates, candidate{
				question: types.SuggestedQuestion{
					Question:        meta.StandardQuestion,
					Source:          "faq",
					KnowledgeBaseID: chunk.KnowledgeBaseID,
				},
				updatedAt: chunk.UpdatedAt,
			})
		}
	}

	// Collect Document chunks with generated questions
	docChunks, err := s.chunkRepo.ListRecentDocumentChunksWithQuestions(ctx, tenantID, queryKBIDs, queryKnowledgeIDs, fetchLimit)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": agentID,
		})
	} else {
		for _, chunk := range docChunks {
			meta, err := chunk.DocumentMetadata()
			if err != nil || meta == nil || len(meta.GeneratedQuestions) == 0 {
				continue
			}
			q := meta.GeneratedQuestions[0].Question
			if q == "" || seen[q] {
				continue
			}
			seen[q] = true
			candidates = append(candidates, candidate{
				question: types.SuggestedQuestion{
					Question:        q,
					Source:          "document",
					KnowledgeBaseID: chunk.KnowledgeBaseID,
				},
				updatedAt: chunk.UpdatedAt,
			})
		}
	}

	// 4. Sort all candidates by updated_at descending (newest first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].updatedAt.After(candidates[j].updatedAt)
	})

	// 5. Pick top N
	for _, c := range candidates {
		if len(result) >= limit {
			break
		}
		result = append(result, c.question)
	}

	return s.truncateQuestions(result, limit), nil
}

// truncateQuestions truncates the question list to the specified limit
func (s *customAgentService) truncateQuestions(questions []types.SuggestedQuestion, limit int) []types.SuggestedQuestion {
	if len(questions) > limit {
		return questions[:limit]
	}
	return questions
}
