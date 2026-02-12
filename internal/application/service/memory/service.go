package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
)

// MemoryService implements the MemoryService interface
type MemoryService struct {
	repo         interfaces.MemoryRepository
	modelService interfaces.ModelService
}

// NewMemoryService creates a new memory service
func NewMemoryService(repo interfaces.MemoryRepository, modelService interfaces.ModelService) interfaces.MemoryService {
	return &MemoryService{
		repo:         repo,
		modelService: modelService,
	}
}

const extractGraphPrompt = `
You are an AI assistant that extracts knowledge graphs from conversations.
Given the following conversation, extract entities and relationships.
Output the result in JSON format with the following structure:
{
  "summary": "A brief summary of the conversation",
  "entities": [
    {
      "title": "Entity Name",
      "type": "Entity Type (e.g., Person, Location, Concept)",
      "description": "Description of the entity"
    }
  ],
  "relationships": [
    {
      "source": "Source Entity Name",
      "target": "Target Entity Name",
      "description": "Description of the relationship",
      "weight": 1.0
    }
  ]
}

Conversation:
%s
`

const extractKeywordsPrompt = `
You are an AI assistant that extracts search keywords from a user query.
Given the following query, extract relevant keywords for searching a knowledge graph.
Output the result in JSON format:
{
  "keywords": ["keyword1", "keyword2"]
}

Query:
%s
`

type extractionResult struct {
	Summary       string                `json:"summary" jsonschema:"a brief summary of the conversation"`
	Entities      []*types.Entity       `json:"entities"`
	Relationships []*types.Relationship `json:"relationships"`
}

type keywordsResult struct {
	Keywords []string `json:"keywords" jsonschema:"relevant keywords for searching a knowledge graph"`
}

func (s *MemoryService) getChatModel(ctx context.Context) (chat.Chat, error) {
	// Find the first available KnowledgeQA model
	models, err := s.modelService.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %v", err)
	}

	var modelID string
	for _, model := range models {
		if model.Type == types.ModelTypeKnowledgeQA {
			modelID = model.ID
			break
		}
	}

	if modelID == "" {
		return nil, fmt.Errorf("no KnowledgeQA model found")
	}

	return s.modelService.GetChatModel(ctx, modelID)
}

// AddEpisode adds a new episode to the memory graph
func (s *MemoryService) AddEpisode(ctx context.Context, userID string, sessionID string, messages []types.Message) error {
	if !s.repo.IsAvailable(ctx) {
		return fmt.Errorf("memory repository is not available")
	}
	chatModel, err := s.getChatModel(ctx)
	if err != nil {
		return err
	}

	// 1. Construct conversation string
	var conversation string
	for _, msg := range messages {
		conversation += fmt.Sprintf("%s: %s\n", msg.Role, msg.Content)
	}

	// 2. Call LLM to extract graph
	prompt := fmt.Sprintf(extractGraphPrompt, conversation)
	resp, err := chatModel.Chat(ctx, []chat.Message{{Role: "user", Content: prompt}}, &chat.ChatOptions{
		Format: utils.GenerateSchema[extractionResult](),
	})
	if err != nil {
		return fmt.Errorf("failed to call LLM: %v", err)
	}

	var result extractionResult
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return fmt.Errorf("failed to parse LLM response: %v", err)
	}

	// 3. Create Episode object
	episode := &types.Episode{
		ID:        uuid.New().String(),
		UserID:    userID,
		SessionID: sessionID,
		Summary:   result.Summary,
		CreatedAt: time.Now(),
	}

	// 4. Save to repository
	if err := s.repo.SaveEpisode(ctx, episode, result.Entities, result.Relationships); err != nil {
		return fmt.Errorf("failed to save episode: %v", err)
	}

	return nil
}

// RetrieveMemory retrieves relevant memory context based on the current query and user
func (s *MemoryService) RetrieveMemory(ctx context.Context, userID string, query string) (*types.MemoryContext, error) {
	if !s.repo.IsAvailable(ctx) {
		return nil, fmt.Errorf("memory repository is not available")
	}
	chatModel, err := s.getChatModel(ctx)
	if err != nil {
		return nil, err
	}

	// 1. Extract keywords
	prompt := fmt.Sprintf(extractKeywordsPrompt, query)
	resp, err := chatModel.Chat(ctx, []chat.Message{{Role: "user", Content: prompt}}, &chat.ChatOptions{
		Format: utils.GenerateSchema[keywordsResult](),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call LLM: %v", err)
	}

	var result keywordsResult
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %v", err)
	}

	// 2. Retrieve related episodes
	episodes, err := s.repo.FindRelatedEpisodes(ctx, userID, result.Keywords, 5)
	if err != nil {
		return nil, fmt.Errorf("failed to find related episodes: %v", err)
	}

	// 3. Construct MemoryContext
	memoryContext := &types.MemoryContext{
		RelatedEpisodes: make([]types.Episode, len(episodes)),
	}
	for i, ep := range episodes {
		memoryContext.RelatedEpisodes[i] = *ep
	}

	return memoryContext, nil
}
