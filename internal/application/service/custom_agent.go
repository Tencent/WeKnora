package service

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
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
	repo           interfaces.CustomAgentRepository
	chunkRepo      interfaces.ChunkRepository
	kbService      interfaces.KnowledgeBaseService
	kbShareService interfaces.KBShareService
	wikiPageRepo   interfaces.WikiPageRepository
	tagRepo        interfaces.KnowledgeTagRepository
	knowledgeRepo  interfaces.KnowledgeRepository
}

// NewCustomAgentService creates a new custom agent service
func NewCustomAgentService(
	repo interfaces.CustomAgentRepository,
	chunkRepo interfaces.ChunkRepository,
	kbService interfaces.KnowledgeBaseService,
	kbShareService interfaces.KBShareService,
	wikiPageRepo interfaces.WikiPageRepository,
	tagRepo interfaces.KnowledgeTagRepository,
	knowledgeRepo interfaces.KnowledgeRepository,
) interfaces.CustomAgentService {
	return &customAgentService{
		repo:           repo,
		chunkRepo:      chunkRepo,
		kbService:      kbService,
		kbShareService: kbShareService,
		wikiPageRepo:   wikiPageRepo,
		tagRepo:        tagRepo,
		knowledgeRepo:  knowledgeRepo,
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

	// Record the creator. Mirrors KnowledgeBase.CreatorID — needed by
	// RBAC's RequireOwnershipOrRole so Contributors can edit their own
	// agents. Synthetic system-{tenantID} users (X-API-Key path) leave
	// the field empty via IsSyntheticUserID, which makes the agent
	// tenant-owned (Admin+ only).
	if uid, ok := types.UserIDFromContext(ctx); ok && !types.IsSyntheticUserID(uid) {
		agent.CreatedBy = uid
	}

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
	if err := agent.Config.QuestionSuggestions.Validate(); err != nil {
		return nil, err
	}

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
			agent.EnsureDefaults()
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

	agent.EnsureDefaults()
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
	agent.EnsureDefaults()
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
		agent.EnsureDefaults()
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
	if err := existingAgent.Config.QuestionSuggestions.Validate(); err != nil {
		return nil, err
	}

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
		if err := existingAgent.Config.QuestionSuggestions.Validate(); err != nil {
			return nil, err
		}

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
	if err := newAgent.Config.QuestionSuggestions.Validate(); err != nil {
		return nil, err
	}

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
	// The clone is owned by whoever ran the copy, not the original
	// creator — same reasoning as CopyKnowledgeBase. Skip synthetic
	// API-key users.
	if uid, ok := types.UserIDFromContext(ctx); ok && !types.IsSyntheticUserID(uid) {
		newAgent.CreatedBy = uid
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
	tagIDs []string,
	limit int,
) ([]types.SuggestedQuestion, error) {
	return s.getSuggestedQuestions(ctx, agentID, kbIDs, knowledgeIDs, tagIDs, limit, true)
}

func (s *customAgentService) GetKnowledgeSuggestedQuestions(
	ctx context.Context,
	agentID string,
	kbIDs []string,
	knowledgeIDs []string,
	tagIDs []string,
	limit int,
) ([]types.SuggestedQuestion, error) {
	return s.getSuggestedQuestions(ctx, agentID, kbIDs, knowledgeIDs, tagIDs, limit, false)
}

func (s *customAgentService) getSuggestedQuestions(
	ctx context.Context,
	agentID string,
	kbIDs []string,
	knowledgeIDs []string,
	tagIDs []string,
	limit int,
	includeCurated bool,
) ([]types.SuggestedQuestion, error) {
	if limit <= 0 {
		limit = 6
	}

	if err := types.AuthorizeTenantAPIKeyKnowledgeTargets(ctx, kbIDs, knowledgeIDs); err != nil {
		return nil, err
	}
	if err := types.AuthorizeTenantAPIKeyOptionalTagIDs(ctx, tagIDs); err != nil {
		return nil, err
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

	if includeCurated {
		suggestionConfig := agent.Config.QuestionSuggestions
		if suggestionConfig == nil || !suggestionConfig.Starters.Enabled {
			return []types.SuggestedQuestion{}, nil
		}
		if limit > suggestionConfig.Starters.Count {
			limit = suggestionConfig.Starters.Count
		}
		// Add curated agent prompts first (highest priority).
		if suggestionConfig.Starters.Mode == types.SuggestionModeCurated ||
			suggestionConfig.Starters.Mode == types.SuggestionModeHybrid {
			for _, prompt := range suggestionConfig.Starters.Items {
				if strings.TrimSpace(prompt) == "" {
					continue
				}
				result = append(result, types.SuggestedQuestion{
					Question: prompt,
					Source:   "agent_config",
				})
			}
		}
		if suggestionConfig.Starters.Mode == types.SuggestionModeCurated {
			return s.truncateQuestions(result, limit), nil
		}
	}

	if len(tagIDs) > 0 {
		resolved, err := s.resolveKnowledgeIDsFromTags(ctx, tenantID, tagIDs)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"agent_id": agentID,
				"tag_ids":  tagIDs,
			})
			return s.truncateQuestions(result, limit), nil
		}
		knowledgeIDs = mergeUniqueStrings(knowledgeIDs, resolved)
		if len(knowledgeIDs) == 0 {
			return s.truncateQuestions(result, limit), nil
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
			// Honor the agent's implicit/explicit capability requirements so
			// e.g. a quick-answer (RAG-only) agent doesn't surface wiki-only
			// KBs whose wiki pages it could never answer from. Same filter
			// the @ mention dropdown applies on the frontend.
			capFilter := tools.DeriveKBFilterForAgent(agent.Config.AgentMode, agent.Config.AllowedTools)
			for _, kb := range kbs {
				if !capFilter.IsEmpty() &&
					!tools.KBSatisfiesAgentRequirements(kb.Capabilities(), agent.Config.AgentMode, agent.Config.AllowedTools) {
					continue
				}
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

	filteredKBIDs, err := types.FilterKnowledgeBasesForTenantAPIKeyScope(ctx, kbIDs, effectiveKBIDs)
	if err != nil {
		return nil, err
	}
	effectiveKBIDs = filteredKBIDs

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

	// 3. Collect candidate chunks from both FAQ and Document KBs,
	//    grouped by knowledge_id for diversity.
	//    knowledgeID -> list of questions
	buckets := make(map[string][]types.SuggestedQuestion)

	// Determine query scope
	queryKBIDs := effectiveKBIDs
	queryKnowledgeIDs := knowledgeIDs

	// Fetch a large pool so DB-level random sampling covers multiple documents.
	fetchLimit := remaining * 5
	if fetchLimit < 20 {
		fetchLimit = 20
	}

	// Resolve each KB to the tenant whose chunks should be queried. Cross-tenant
	// KBs reached via an organization share map to the source tenant ID; the chunk
	// rows live under that tenant. Without this grouping a caller in tenant A
	// querying a KB shared from tenant B would hit `tenant_id = A` and get zero
	// rows back — the symptom is "suggested questions never appear for shared KBs".
	kbGroups := s.groupKBIDsByEffectiveTenant(ctx, tenantID, queryKBIDs)
	// Always keep the caller's tenant in the iteration so knowledge_ids-only
	// requests (no kbIDs) still execute one query under the caller's tenant.
	if len(queryKBIDs) == 0 {
		kbGroups[tenantID] = nil
	}

	// Collect FAQ recommended chunks
	for groupTenantID, groupKBIDs := range kbGroups {
		faqChunks, err := s.chunkRepo.ListRecommendedFAQChunks(ctx, groupTenantID, groupKBIDs, queryKnowledgeIDs, fetchLimit)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"agent_id":  agentID,
				"tenant_id": groupTenantID,
			})
			continue
		}
		for _, chunk := range faqChunks {
			meta, err := chunk.FAQMetadata()
			if err != nil || meta == nil || meta.StandardQuestion == "" {
				continue
			}
			if seen[meta.StandardQuestion] {
				continue
			}
			seen[meta.StandardQuestion] = true
			buckets[chunk.KnowledgeID] = append(buckets[chunk.KnowledgeID], types.SuggestedQuestion{
				Question:        meta.StandardQuestion,
				Source:          "faq",
				KnowledgeBaseID: chunk.KnowledgeBaseID,
			})
		}
	}

	// Collect Document chunks with generated questions
	for groupTenantID, groupKBIDs := range kbGroups {
		docChunks, err := s.chunkRepo.ListRecentDocumentChunksWithQuestions(ctx, groupTenantID, groupKBIDs, queryKnowledgeIDs, fetchLimit)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"agent_id":  agentID,
				"tenant_id": groupTenantID,
			})
			continue
		}
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
			buckets[chunk.KnowledgeID] = append(buckets[chunk.KnowledgeID], types.SuggestedQuestion{
				Question:        q,
				Source:          "document",
				KnowledgeBaseID: chunk.KnowledgeBaseID,
			})
		}
	}

	// Collect Wiki pages as a fallback source. This covers Wiki-only KBs where no
	// document chunks carry AI-generated questions (question_generation is skipped
	// when the KB does not need an embedding model). knowledge_id filter is
	// intentionally ignored here because wiki pages are authored at the KB level
	// and are not 1:1 with source knowledge items.
	//
	// Skip entirely for quick-answer (RAG-only) agents: those can't ever
	// retrieve a wiki page, so surfacing wiki-derived suggestions would lure
	// the user into asking questions the agent will then answer with empty
	// context. Smart-reasoning agents that opt in to wiki tools keep this.
	if agent.Config.AgentMode != types.AgentModeQuickAnswer && s.wikiPageRepo != nil {
		for groupTenantID, groupKBIDs := range kbGroups {
			if len(groupKBIDs) == 0 {
				continue
			}
			wikiPages, err := s.wikiPageRepo.ListRecentForSuggestions(ctx, groupTenantID, groupKBIDs, fetchLimit)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"agent_id":  agentID,
					"tenant_id": groupTenantID,
				})
				continue
			}
			locale, _ := types.LanguageFromContext(ctx)
			for _, page := range wikiPages {
				q := wikiSuggestionFromPage(page, locale)
				if q == "" || seen[q] {
					continue
				}
				seen[q] = true
				// Use page.ID as the bucket key so round-robin mixes pages from
				// different wiki entries rather than clumping them.
				buckets[page.ID] = append(buckets[page.ID], types.SuggestedQuestion{
					Question:        q,
					Source:          "wiki",
					KnowledgeBaseID: page.KnowledgeBaseID,
				})
			}
		}
	}

	// 4. Shuffle within each bucket, then round-robin across buckets
	//    to ensure diversity across different documents.
	bucketKeys := make([]string, 0, len(buckets))
	for k, qs := range buckets {
		bucketKeys = append(bucketKeys, k)
		rand.Shuffle(len(qs), func(i, j int) { qs[i], qs[j] = qs[j], qs[i] })
		buckets[k] = qs
	}
	rand.Shuffle(len(bucketKeys), func(i, j int) {
		bucketKeys[i], bucketKeys[j] = bucketKeys[j], bucketKeys[i]
	})

	// Round-robin pick one question from each document in turn.
	offsets := make(map[string]int, len(bucketKeys))
	for len(result) < limit {
		picked := false
		for _, key := range bucketKeys {
			if len(result) >= limit {
				break
			}
			qs := buckets[key]
			idx := offsets[key]
			if idx < len(qs) {
				result = append(result, qs[idx])
				offsets[key] = idx + 1
				picked = true
			}
		}
		if !picked {
			break
		}
	}

	return s.truncateQuestions(result, limit), nil
}

func (s *customAgentService) resolveKnowledgeIDsFromTags(
	ctx context.Context,
	tenantID uint64,
	tagIDs []string,
) ([]string, error) {
	if len(tagIDs) == 0 || s.tagRepo == nil || s.knowledgeRepo == nil {
		return nil, nil
	}
	tags, err := s.tagRepo.GetByIDs(ctx, tenantID, tagIDs)
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return nil, nil
	}
	byKB := make(map[string][]string)
	for _, tag := range tags {
		byKB[tag.KnowledgeBaseID] = append(byKB[tag.KnowledgeBaseID], tag.ID)
	}
	return mergeKnowledgeIDsFromTagGroups(ctx, s.knowledgeRepo, tenantID, byKB)
}

func mergeKnowledgeIDsFromTagGroups(
	ctx context.Context,
	knowledgeRepo interfaces.KnowledgeRepository,
	tenantID uint64,
	byKB map[string][]string,
) ([]string, error) {
	seen := make(map[string]bool)
	var out []string
	for kbID, ids := range byKB {
		kids, err := knowledgeRepo.ListIDsByTagIDs(ctx, tenantID, kbID, ids)
		if err != nil {
			return nil, err
		}
		for _, kid := range kids {
			if !seen[kid] {
				seen[kid] = true
				out = append(out, kid)
			}
		}
	}
	return out, nil
}

func mergeUniqueStrings(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, s := range base {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, s := range extra {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// truncateQuestions truncates the question list to the specified limit
func (s *customAgentService) truncateQuestions(questions []types.SuggestedQuestion, limit int) []types.SuggestedQuestion {
	if len(questions) > limit {
		return questions[:limit]
	}
	return questions
}

// wikiSuggestionFromPage converts a wiki page into a human-readable suggested
// question string. The template is chosen per page type so the chip reads
// naturally for that kind of content:
//   - concept: "What is <title>?" works for abstract terms (RAG, embedding,
//     idempotency…).
//   - entity / summary: "Tell me about <title>" is neutral and works for
//     people, places, organizations, products and document summaries where
//     "what is <name>?" would read awkwardly ("什么是张三？").
//   - everything else (synthesis, comparison, …): the raw title is already a
//     good topical query on its own.
func wikiSuggestionFromPage(page *types.WikiPage, locale string) string {
	if page == nil {
		return ""
	}
	title := strings.TrimSpace(page.Title)
	if title == "" {
		return ""
	}
	switch page.PageType {
	case types.WikiPageTypeConcept:
		if isEnglishLocale(locale) {
			return "What is " + title + "?"
		}
		return "什么是" + title + "？"
	case types.WikiPageTypeEntity, types.WikiPageTypeSummary:
		if isEnglishLocale(locale) {
			return "Tell me about " + title
		}
		return "介绍一下" + title
	default:
		return title
	}
}

// groupKBIDsByEffectiveTenant resolves each kbID to the tenant whose chunk
// rows back that KB, so cross-tenant shares can be queried correctly:
//   - In-tenant KBs map to the caller's tenant id.
//   - KBs owned by another tenant are included only if the caller's tenant
//     has at least Viewer access via an organization share, in which case
//     the KB maps to its source tenant id (where the chunks actually live).
//   - KBs the caller cannot reach (no membership, no share) are silently
//     dropped — the suggestion endpoint never returns 403, it just shows
//     nothing for that KB, mirroring how search results are scoped.
//
// The result is keyed by effective tenant id so the caller can issue one
// chunk / wiki query per tenant group. Returns an empty (non-nil) map when
// kbIDs is empty.
func (s *customAgentService) groupKBIDsByEffectiveTenant(
	ctx context.Context,
	callerTenantID uint64,
	kbIDs []string,
) map[uint64][]string {
	out := make(map[uint64][]string)
	if len(kbIDs) == 0 {
		return out
	}
	kbs, err := s.kbService.GetKnowledgeBasesByIDsOnly(ctx, kbIDs)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"kb_ids": kbIDs,
		})
		// Fall back to caller's tenant so at least in-tenant KBs are queryable;
		// chunk repo filtering will drop anything that doesn't match.
		out[callerTenantID] = append(out[callerTenantID], kbIDs...)
		return out
	}
	kbByID := make(map[string]*types.KnowledgeBase, len(kbs))
	for _, kb := range kbs {
		if kb != nil {
			kbByID[kb.ID] = kb
		}
	}
	callerRole := types.TenantRoleFromContext(ctx)
	for _, kbID := range kbIDs {
		kb := kbByID[kbID]
		if kb == nil {
			continue
		}
		if kb.TenantID == callerTenantID {
			out[callerTenantID] = append(out[callerTenantID], kbID)
			continue
		}
		if s.kbShareService == nil {
			continue
		}
		ok, err := s.kbShareService.HasTenantKBPermission(ctx, kbID, callerTenantID, callerRole, types.OrgRoleViewer)
		if err != nil || !ok {
			continue
		}
		out[kb.TenantID] = append(out[kb.TenantID], kbID)
	}
	return out
}

// isEnglishLocale reports whether the locale string is an English variant.
// Unknown / empty locales fall back to Chinese, matching the product default.
func isEnglishLocale(locale string) bool {
	switch locale {
	case "en-US", "en", "en-GB":
		return true
	}
	return false
}
