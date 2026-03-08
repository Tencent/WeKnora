package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

const (
	// DefaultLLMTemperature Use low temperature for more deterministic results
	DefaultLLMTemperature = 0.1

	// PMIWeight Proportion of PMI in calculating relationship weight
	PMIWeight = 0.6

	// StrengthWeight Proportion of relationship strength in calculating relationship weight
	StrengthWeight = 0.4

	// IndirectRelationWeightDecay Decay coefficient for indirect relationship weights
	IndirectRelationWeightDecay = 0.5

	// MaxConcurrentEntityExtractions Maximum concurrency for entity extraction
	MaxConcurrentEntityExtractions = 4

	// MaxConcurrentRelationExtractions Maximum concurrency for relationship extraction
	MaxConcurrentRelationExtractions = 4

	// DefaultRelationBatchSize Default batch size for relationship extraction
	DefaultRelationBatchSize = 5

	// MinEntitiesForRelation Minimum number of entities required for relationship extraction
	MinEntitiesForRelation = 2

	// MinWeightValue Minimum weight value to avoid division by zero
	MinWeightValue = 1.0

	// WeightScaleFactor Weight scaling factor to normalize weights to 1-10 range
	WeightScaleFactor = 9.0
)

// ChunkRelation represents a relationship between two Chunks
type ChunkRelation struct {
	// Weight relationship weight, calculated based on PMI and strength
	Weight float64

	// Degree total degree of related entities
	Degree int
}

// graphBuilder implements knowledge graph construction functionality
type graphBuilder struct {
	config           *config.Config
	entityMap        map[string]*types.Entity       // Entities indexed by ID
	entityMapByTitle map[string]*types.Entity       // Entities indexed by title
	relationshipMap  map[string]*types.Relationship // Relationship mapping
	chatModel        chat.Chat
	chunkGraph       map[string]map[string]*ChunkRelation // Document chunk relationship graph
	mutex            sync.RWMutex                         // Mutex for concurrent operations
}

// NewGraphBuilder creates a new graph builder
func NewGraphBuilder(config *config.Config, chatModel chat.Chat) types.GraphBuilder {
	logger.Info(context.Background(), "Creating new graph builder")
	return &graphBuilder{
		config:           config,
		chatModel:        chatModel,
		entityMap:        make(map[string]*types.Entity),
		entityMapByTitle: make(map[string]*types.Entity),
		relationshipMap:  make(map[string]*types.Relationship),
		chunkGraph:       make(map[string]map[string]*ChunkRelation),
	}
}

// extractEntities extracts entities from text chunks
// It uses LLM to analyze text content and identify relevant entities
func (b *graphBuilder) extractEntities(ctx context.Context, chunk *types.Chunk) ([]*types.Entity, error) {
	log := logger.GetLogger(ctx)
	log.Infof("Extracting entities from chunk: %s", chunk.ID)

	if chunk.Content == "" {
		log.Warn("Empty chunk content, skipping entity extraction")
		return []*types.Entity{}, nil
	}

	// Create prompt for entity extraction
	thinking := false
	messages := []chat.Message{
		{
			Role:    "system",
			Content: b.config.Conversation.ExtractEntitiesPrompt,
		},
		{
			Role:    "user",
			Content: chunk.Content,
		},
	}

	// Call LLM to extract entities
	log.Debug("Calling LLM to extract entities")
	resp, err := b.chatModel.Chat(ctx, messages, &chat.ChatOptions{
		Temperature: DefaultLLMTemperature,
		Thinking:    &thinking,
	})
	if err != nil {
		log.WithError(err).Error("Failed to extract entities from chunk")
		return nil, fmt.Errorf("LLM entity extraction failed: %w", err)
	}

	// Parse JSON response
	var extractedEntities []*types.Entity
	if err := common.ParseLLMJsonResponse(resp.Content, &extractedEntities); err != nil {
		log.WithError(err).Errorf("Failed to parse entity extraction response, rsp content: %s", resp.Content)
		return nil, fmt.Errorf("failed to parse entity extraction response: %w", err)
	}
	log.Infof("Extracted %d entities from chunk", len(extractedEntities))

	// Print detailed entity information in a clear format
	log.Info("=========== EXTRACTED ENTITIES ===========")
	for i, entity := range extractedEntities {
		if entity == nil {
			continue
		}
		log.Infof("[Entity %d] Title: '%s', Description: '%s'", i+1, entity.Title, entity.Description)
	}
	log.Info("=========================================")

	var entities []*types.Entity

	// Process entities and update entityMap
	b.mutex.Lock()
	defer b.mutex.Unlock()

	for _, entity := range extractedEntities {
		if entity == nil {
			continue
		}
		if entity.Title == "" || entity.Description == "" {
			log.WithField("entity", entity).Warn("Invalid entity with empty title or description")
			continue
		}
		if existEntity, exists := b.entityMapByTitle[entity.Title]; !exists {
			// This is a new entity
			entity.ID = uuid.New().String()
			entity.ChunkIDs = []string{chunk.ID}
			entity.Frequency = 1
			b.entityMapByTitle[entity.Title] = entity
			b.entityMap[entity.ID] = entity
			entities = append(entities, entity)
			log.Debugf("New entity added: %s (ID: %s)", entity.Title, entity.ID)
		} else {
			if existEntity == nil {
				log.Warnf("existEntity is nil, skip update")
				continue
			}
			// Entity already exists, update its ChunkIDs
			if !slices.Contains(existEntity.ChunkIDs, chunk.ID) {
				existEntity.ChunkIDs = append(existEntity.ChunkIDs, chunk.ID)
				log.Debugf("Updated existing entity: %s with chunk: %s", entity.Title, chunk.ID)
			}
			existEntity.Frequency++
			entities = append(entities, existEntity)
		}
	}

	log.Infof("Completed entity extraction for chunk %s: %d entities", chunk.ID, len(entities))
	return entities, nil
}

// extractRelationships extracts relationships between entities
// It analyzes semantic connections between multiple entities and establishes relationships
func (b *graphBuilder) extractRelationships(ctx context.Context,
	chunks []*types.Chunk, entities []*types.Entity,
) error {
	log := logger.GetLogger(ctx)
	log.Infof("Extracting relationships from %d entities across %d chunks", len(entities), len(chunks))

	if len(entities) < MinEntitiesForRelation {
		log.Info("Not enough entities to form relationships (minimum 2)")
		return nil
	}

	// Serialize entities to build prompt
	entitiesJSON, err := json.Marshal(entities)
	if err != nil {
		log.WithError(err).Error("Failed to serialize entities to JSON")
		return fmt.Errorf("failed to serialize entities: %w", err)
	}

	// Merge chunk contents
	content := b.mergeChunkContents(chunks)
	if content == "" {
		log.Warn("No content to extract relationships from")
		return nil
	}

	// Create relationship extraction prompt
	thinking := false
	messages := []chat.Message{
		{
			Role:    "system",
			Content: b.config.Conversation.ExtractRelationshipsPrompt,
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("Entities: %s\n\nText: %s", string(entitiesJSON), content),
		},
	}

	// Call LLM to extract relationships
	log.Debug("Calling LLM to extract relationships")
	resp, err := b.chatModel.Chat(ctx, messages, &chat.ChatOptions{
		Temperature: DefaultLLMTemperature,
		Thinking:    &thinking,
	})
	if err != nil {
		log.WithError(err).Error("Failed to extract relationships")
		return fmt.Errorf("LLM relationship extraction failed: %w", err)
	}

	// Parse JSON response
	var extractedRelationships []*types.Relationship
	if err := common.ParseLLMJsonResponse(resp.Content, &extractedRelationships); err != nil {
		log.WithError(err).Error("Failed to parse relationship extraction response")
		return fmt.Errorf("failed to parse relationship extraction response: %w", err)
	}
	log.Infof("Extracted %d relationships", len(extractedRelationships))

	// Print detailed relationship information in a clear format
	log.Info("========= EXTRACTED RELATIONSHIPS =========")
	for i, rel := range extractedRelationships {
		if rel == nil {
			continue
		}
		log.Infof("[Relation %d] Source: '%s', Target: '%s', Description: '%s', Strength: %d",
			i+1, rel.Source, rel.Target, rel.Description, rel.Strength)
	}
	log.Info("===========================================")

	// Process relationships and update relationshipMap
	b.mutex.Lock()
	defer b.mutex.Unlock()

	relationshipsAdded := 0
	relationshipsUpdated := 0
	for _, relationship := range extractedRelationships {
		if relationship == nil {
			continue
		}
		key := fmt.Sprintf("%s#%s", relationship.Source, relationship.Target)
		relationChunkIDs := b.findRelationChunkIDs(relationship.Source, relationship.Target, entities)
		if len(relationChunkIDs) == 0 {
			log.Debugf("Skipping relationship %s -> %s: no common chunks", relationship.Source, relationship.Target)
			continue
		}
		if existingRel, exists := b.relationshipMap[key]; !exists {
			// This is a new relationship
			relationship.ID = uuid.New().String()
			relationship.ChunkIDs = relationChunkIDs
			b.relationshipMap[key] = relationship
			relationshipsAdded++
			log.Debugf("New relationship added: %s -> %s (ID: %s)",
				relationship.Source, relationship.Target, relationship.ID)
		} else {
			// This relationship already exists, update its properties
			if existingRel == nil {
				log.Warnf("existingRel is nil, skip update")
				continue
			}
			chunkIDsAdded := 0
			for _, chunkID := range relationChunkIDs {
				if !slices.Contains(existingRel.ChunkIDs, chunkID) {
					existingRel.ChunkIDs = append(existingRel.ChunkIDs, chunkID)
					chunkIDsAdded++
				}
			}
			// Update strength, considering weighted average of existing strength and new relationship strength
			if len(existingRel.ChunkIDs) > 0 {
				existingRel.Strength = (existingRel.Strength*len(existingRel.ChunkIDs) + relationship.Strength) /
					(len(existingRel.ChunkIDs) + 1)
			}

			if chunkIDsAdded > 0 {
				relationshipsUpdated++
				log.Debugf("Updated relationship: %s -> %s with %d new chunks",
					relationship.Source, relationship.Target, chunkIDsAdded)
			}
		}
	}

	log.Infof("Relationship extraction completed: added %d, updated %d relationships",
		relationshipsAdded, relationshipsUpdated)
	return nil
}

// findRelationChunkIDs finds common document chunk IDs between two entities
func (b *graphBuilder) findRelationChunkIDs(source, target string, entities []*types.Entity) []string {
	relationChunkIDs := make(map[string]struct{})

	// Collect all document chunk IDs for source and target entities
	for _, entity := range entities {
		if entity == nil {
			continue
		}
		if entity.Title == source || entity.Title == target {
			for _, chunkID := range entity.ChunkIDs {
				relationChunkIDs[chunkID] = struct{}{}
			}
		}
	}

	if len(relationChunkIDs) == 0 {
		return []string{}
	}

	// Convert map keys to slice
	result := make([]string, 0, len(relationChunkIDs))
	for chunkID := range relationChunkIDs {
		result = append(result, chunkID)
	}
	return result
}

// mergeChunkContents merges content from multiple document chunks
// It accounts for overlapping portions between chunks to ensure coherent content
func (b *graphBuilder) mergeChunkContents(chunks []*types.Chunk) string {
	if len(chunks) == 0 {
		return ""
	}

	chunkContents := chunks[0].Content
	preChunk := chunks[0]

	for i := 1; i < len(chunks); i++ {
		// Only add non-overlapping content parts
		if preChunk.EndAt > chunks[i].StartAt {
			// Calculate overlap starting position
			startPos := preChunk.EndAt - chunks[i].StartAt
			if startPos >= 0 && startPos < len([]rune(chunks[i].Content)) {
				chunkContents = chunkContents + string([]rune(chunks[i].Content)[startPos:])
			}
		} else {
			// If there's no overlap between chunks, add all content
			chunkContents = chunkContents + chunks[i].Content
		}
		preChunk = chunks[i]
	}

	return chunkContents
}

// BuildGraph constructs the knowledge graph
// It serves as the main entry point for the graph building process, coordinating all components
func (b *graphBuilder) BuildGraph(ctx context.Context, chunks []*types.Chunk) error {
	log := logger.GetLogger(ctx)
	log.Infof("Building knowledge graph from %d chunks", len(chunks))
	startTime := time.Now()

	// Concurrently extract entities from each document chunk
	chunkEntities := make([][]*types.Entity, len(chunks))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(MaxConcurrentEntityExtractions) // Limit concurrency

	for i, chunk := range chunks {
		i, chunk := i, chunk // Create local variables to avoid closure issues
		g.Go(func() error {
			log.Debugf("Processing chunk %d/%d (ID: %s)", i+1, len(chunks), chunk.ID)
			entities, err := b.extractEntities(gctx, chunk)
			if err != nil {
				log.WithError(err).Errorf("Failed to extract entities from chunk %s", chunk.ID)
				return fmt.Errorf("entity extraction failed for chunk %s: %w", chunk.ID, err)
			}
			chunkEntities[i] = entities
			return nil
		})
	}

	// Wait for all entity extractions to complete
	if err := g.Wait(); err != nil {
		log.WithError(err).Error("Entity extraction failed")
		return fmt.Errorf("entity extraction process failed: %w", err)
	}

	// Count total extracted entities
	totalEntityCount := 0
	for _, entities := range chunkEntities {
		totalEntityCount += len(entities)
	}
	log.Infof("Successfully extracted %d total entities across %d chunks",
		totalEntityCount, len(chunks))

	// Process relationships in batches concurrently
	relationChunkSize := DefaultRelationBatchSize
	log.Infof("Processing relationships concurrently in batches of %d chunks", relationChunkSize)

	// prepare relationship extraction batches
	var relationBatches []struct {
		batchChunks         []*types.Chunk
		relationUseEntities []*types.Entity
		batchIndex          int
	}

	for i, batchChunks := range utils.ChunkSlice(chunks, relationChunkSize) {
		start := i * relationChunkSize
		end := start + relationChunkSize
		if end > len(chunkEntities) {
			end = len(chunkEntities)
		}

		// Merge all entities in this batch
		relationUseEntities := make([]*types.Entity, 0)
		for j := start; j < end; j++ {
			if j < len(chunkEntities) {
				relationUseEntities = append(relationUseEntities, chunkEntities[j]...)
			}
		}

		if len(relationUseEntities) < MinEntitiesForRelation {
			log.Debugf("Skipping batch %d: not enough entities (%d)", i+1, len(relationUseEntities))
			continue
		}

		relationBatches = append(relationBatches, struct {
			batchChunks         []*types.Chunk
			relationUseEntities []*types.Entity
			batchIndex          int
		}{
			batchChunks:         batchChunks,
			relationUseEntities: relationUseEntities,
			batchIndex:          i,
		})
	}

	// extract relationships concurrently
	relG, relGctx := errgroup.WithContext(ctx)
	relG.SetLimit(MaxConcurrentRelationExtractions) // use dedicated relationship extraction concurrency limit

	for _, batch := range relationBatches {
		relG.Go(func() error {
			log.Debugf("Processing relationship batch %d (chunks %d)", batch.batchIndex+1, len(batch.batchChunks))
			err := b.extractRelationships(relGctx, batch.batchChunks, batch.relationUseEntities)
			if err != nil {
				log.WithError(err).Errorf("Failed to extract relationships for batch %d", batch.batchIndex+1)
			}
			return nil // continue to process other batches even if the current batch fails
		})
	}

	// wait for all relationship extractions to complete
	if err := relG.Wait(); err != nil {
		log.WithError(err).Error("Some relationship extraction tasks failed")
		// but we continue to process the next steps because some relationship extractions are still useful
	}

	// Calculate relationship weights
	log.Info("Calculating weights for relationships")
	b.calculateWeights(ctx)

	// Calculate entity degrees
	log.Info("Calculating degrees for entities")
	b.calculateDegrees(ctx)

	// Build Chunk graph
	log.Info("Building chunk relationship graph")
	b.buildChunkGraph(ctx)

	log.Infof("Graph building completed in %.2f seconds: %d entities, %d relationships",
		time.Since(startTime).Seconds(), len(b.entityMap), len(b.relationshipMap))

	// generate knowledge graph visualization diagram
	mermaidDiagram := b.generateKnowledgeGraphDiagram(ctx)
	log.Info("Knowledge graph visualization diagram:")
	log.Info(mermaidDiagram)

	return nil
}

// calculateWeights calculates relationship weights
// It uses Point Mutual Information (PMI) and strength values to calculate relationship weights
func (b *graphBuilder) calculateWeights(ctx context.Context) {
	log := logger.GetLogger(ctx)
	log.Info("Calculating relationship weights using PMI and strength")

	// Calculate total entity occurrences
	totalEntityOccurrences := 0
	entityFrequency := make(map[string]int)

	for _, entity := range b.entityMap {
		if entity == nil {
			continue
		}
		frequency := len(entity.ChunkIDs)
		entityFrequency[entity.Title] = frequency
		totalEntityOccurrences += frequency
	}

	// Calculate total relationship occurrences
	totalRelOccurrences := 0
	for _, rel := range b.relationshipMap {
		if rel == nil {
			continue
		}
		totalRelOccurrences += len(rel.ChunkIDs)
	}

	// Skip calculation if insufficient data
	if totalEntityOccurrences == 0 || totalRelOccurrences == 0 {
		log.Warn("Insufficient data for weight calculation")
		return
	}

	// Track maximum PMI and Strength values for normalization
	maxPMI := 0.0
	maxStrength := MinWeightValue // Avoid division by zero

	// First calculate PMI and find maximum values
	pmiValues := make(map[string]float64)
	for _, rel := range b.relationshipMap {
		if rel == nil {
			continue
		}
		sourceFreq := entityFrequency[rel.Source]
		targetFreq := entityFrequency[rel.Target]
		relFreq := len(rel.ChunkIDs)

		if sourceFreq > 0 && targetFreq > 0 && relFreq > 0 {
			sourceProbability := float64(sourceFreq) / float64(totalEntityOccurrences)
			targetProbability := float64(targetFreq) / float64(totalEntityOccurrences)
			relProbability := float64(relFreq) / float64(totalRelOccurrences)

			// PMI calculation: log(P(x,y) / (P(x) * P(y)))
			pmi := math.Max(math.Log2(relProbability/(sourceProbability*targetProbability)), 0)
			pmiValues[rel.ID] = pmi

			if pmi > maxPMI {
				maxPMI = pmi
			}
		}

		// Record maximum Strength value
		if float64(rel.Strength) > maxStrength {
			maxStrength = float64(rel.Strength)
		}
	}

	// Combine PMI and Strength to calculate final weights
	for _, rel := range b.relationshipMap {
		pmi := pmiValues[rel.ID]

		// Normalize PMI and Strength (0-1 range)
		normalizedPMI := 0.0
		if maxPMI > 0 {
			normalizedPMI = pmi / maxPMI
		}

		normalizedStrength := float64(rel.Strength) / maxStrength

		// Combine PMI and Strength using configured weights
		combinedWeight := normalizedPMI*PMIWeight + normalizedStrength*StrengthWeight

		// Scale weight to 1-10 range
		scaledWeight := 1.0 + WeightScaleFactor*combinedWeight

		rel.Weight = scaledWeight
	}

	log.Infof("Weight calculation completed for %d relationships", len(b.relationshipMap))
}

// calculateDegrees calculates entity degrees
// Degree represents the number of connections an entity has with other entities, a key metric in graph structures
func (b *graphBuilder) calculateDegrees(ctx context.Context) {
	log := logger.GetLogger(ctx)
	log.Info("Calculating entity degrees")

	// Calculate in-degree and out-degree for each entity
	inDegree := make(map[string]int)
	outDegree := make(map[string]int)

	for _, rel := range b.relationshipMap {
		outDegree[rel.Source]++
		inDegree[rel.Target]++
	}

	// Set degree for each entity
	for _, entity := range b.entityMap {
		if entity == nil {
			continue
		}
		entity.Degree = inDegree[entity.Title] + outDegree[entity.Title]
	}

	// Set combined degree for relationships
	for _, rel := range b.relationshipMap {
		if rel == nil {
			continue
		}
		sourceEntity := b.getEntityByTitle(rel.Source)
		targetEntity := b.getEntityByTitle(rel.Target)

		if sourceEntity != nil && targetEntity != nil {
			rel.CombinedDegree = sourceEntity.Degree + targetEntity.Degree
		}
	}

	log.Info("Entity degree calculation completed")
}

// buildChunkGraph builds relationship graph between Chunks
// It creates a network of relationships between document chunks based on entity relationships
func (b *graphBuilder) buildChunkGraph(ctx context.Context) {
	log := logger.GetLogger(ctx)
	log.Info("Building chunk relationship graph")

	// Create document chunk relationship graph based on entity relationships
	for _, rel := range b.relationshipMap {
		if rel == nil {
			continue
		}
		// Ensure source and target entities exist for the relationship
		sourceEntity := b.entityMapByTitle[rel.Source]
		targetEntity := b.entityMapByTitle[rel.Target]

		if sourceEntity == nil || targetEntity == nil {
			log.Warnf("Missing entity for relationship %s -> %s", rel.Source, rel.Target)
			continue
		}

		// Build Chunk graph - connect all related document chunks
		for _, sourceChunkID := range sourceEntity.ChunkIDs {
			if _, exists := b.chunkGraph[sourceChunkID]; !exists {
				b.chunkGraph[sourceChunkID] = make(map[string]*ChunkRelation)
			}

			for _, targetChunkID := range targetEntity.ChunkIDs {
				if _, exists := b.chunkGraph[targetChunkID]; !exists {
					b.chunkGraph[targetChunkID] = make(map[string]*ChunkRelation)
				}

				relation := &ChunkRelation{
					Weight: rel.Weight,
					Degree: rel.CombinedDegree,
				}

				b.chunkGraph[sourceChunkID][targetChunkID] = relation
				b.chunkGraph[targetChunkID][sourceChunkID] = relation
			}
		}
	}

	log.Infof("Chunk graph built with %d nodes", len(b.chunkGraph))
}

// GetAllEntities returns all entities
func (b *graphBuilder) GetAllEntities() []*types.Entity {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	entities := make([]*types.Entity, 0, len(b.entityMap))
	for _, entity := range b.entityMap {
		entities = append(entities, entity)
	}
	return entities
}

// GetAllRelationships returns all relationships
func (b *graphBuilder) GetAllRelationships() []*types.Relationship {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	relationships := make([]*types.Relationship, 0, len(b.relationshipMap))
	for _, relationship := range b.relationshipMap {
		relationships = append(relationships, relationship)
	}
	return relationships
}

// GetRelationChunks retrieves document chunks directly related to the given chunkID
// It returns a list of related document chunk IDs sorted by weight and degree
func (b *graphBuilder) GetRelationChunks(chunkID string, topK int) []string {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	log := logger.GetLogger(context.Background())
	log.Debugf("Getting related chunks for %s (topK=%d)", chunkID, topK)

	// Create weighted chunk structure for sorting
	type weightedChunk struct {
		id     string
		weight float64
		degree int
	}

	// Collect related chunks with their weights and degrees
	weightedChunks := make([]weightedChunk, 0)
	for relationChunkID, relation := range b.chunkGraph[chunkID] {
		if relation == nil {
			continue
		}
		weightedChunks = append(weightedChunks, weightedChunk{
			id:     relationChunkID,
			weight: relation.Weight,
			degree: relation.Degree,
		})
	}

	// Sort by weight and degree in descending order
	slices.SortFunc(weightedChunks, func(a, b weightedChunk) int {
		// Sort by weight first
		if a.weight > b.weight {
			return -1 // Descending order
		} else if a.weight < b.weight {
			return 1
		}

		// If weights are equal, sort by degree
		if a.degree > b.degree {
			return -1 // Descending order
		} else if a.degree < b.degree {
			return 1
		}

		return 0
	})

	// Take top K results
	resultCount := len(weightedChunks)
	if topK > 0 && topK < resultCount {
		resultCount = topK
	}

	// Extract chunk IDs
	chunks := make([]string, 0, resultCount)
	for i := 0; i < resultCount; i++ {
		chunks = append(chunks, weightedChunks[i].id)
	}

	log.Debugf("Found %d related chunks for %s (limited to %d)",
		len(weightedChunks), chunkID, resultCount)
	return chunks
}

// GetIndirectRelationChunks retrieves document chunks indirectly related to the given chunkID
// It returns document chunk IDs found through second-degree connections
func (b *graphBuilder) GetIndirectRelationChunks(chunkID string, topK int) []string {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	log := logger.GetLogger(context.Background())
	log.Debugf("Getting indirectly related chunks for %s (topK=%d)", chunkID, topK)

	// Create weighted chunk structure for sorting
	type weightedChunk struct {
		id     string
		weight float64
		degree int
	}

	// Get directly related chunks (first-degree connections)
	directChunks := make(map[string]struct{})
	directChunks[chunkID] = struct{}{} // Add original chunkID
	for directChunkID := range b.chunkGraph[chunkID] {
		directChunks[directChunkID] = struct{}{}
	}
	log.Debugf("Found %d directly related chunks to exclude", len(directChunks))

	// Use map to deduplicate and store second-degree connections
	indirectChunkMap := make(map[string]*ChunkRelation)

	// Get first-degree connections
	for directChunkID, directRelation := range b.chunkGraph[chunkID] {
		if directRelation == nil {
			continue
		}
		// Get second-degree connections
		for indirectChunkID, indirectRelation := range b.chunkGraph[directChunkID] {
			if indirectRelation == nil {
				continue
			}
			// Skip self and all direct connections
			if _, isDirect := directChunks[indirectChunkID]; isDirect {
				continue
			}

			// Weight decay: second-degree relationship weight is the product of two direct relationship weights
			// multiplied by decay coefficient
			combinedWeight := directRelation.Weight * indirectRelation.Weight * IndirectRelationWeightDecay
			// Degree calculation: take the maximum degree from the two path segments
			combinedDegree := max(directRelation.Degree, indirectRelation.Degree)

			// If already exists, take the higher weight
			if existingRel, exists := indirectChunkMap[indirectChunkID]; !exists ||
				combinedWeight > existingRel.Weight {
				indirectChunkMap[indirectChunkID] = &ChunkRelation{
					Weight: combinedWeight,
					Degree: combinedDegree,
				}
			}
		}
	}

	// Convert to sortable slice
	weightedChunks := make([]weightedChunk, 0, len(indirectChunkMap))
	for id, relation := range indirectChunkMap {
		if relation == nil {
			continue
		}
		weightedChunks = append(weightedChunks, weightedChunk{
			id:     id,
			weight: relation.Weight,
			degree: relation.Degree,
		})
	}

	// Sort by weight and degree in descending order
	slices.SortFunc(weightedChunks, func(a, b weightedChunk) int {
		// Sort by weight first
		if a.weight > b.weight {
			return -1 // Descending order
		} else if a.weight < b.weight {
			return 1
		}

		// If weights are equal, sort by degree
		if a.degree > b.degree {
			return -1 // Descending order
		} else if a.degree < b.degree {
			return 1
		}

		return 0
	})

	// Take top K results
	resultCount := len(weightedChunks)
	if topK > 0 && topK < resultCount {
		resultCount = topK
	}

	// Extract chunk IDs
	chunks := make([]string, 0, resultCount)
	for i := 0; i < resultCount; i++ {
		chunks = append(chunks, weightedChunks[i].id)
	}

	log.Debugf("Found %d indirect related chunks for %s (limited to %d)",
		len(weightedChunks), chunkID, resultCount)
	return chunks
}

// getEntityByTitle retrieves an entity by its title
func (b *graphBuilder) getEntityByTitle(title string) *types.Entity {
	return b.entityMapByTitle[title]
}

// dfs depth-first search to find connected components
func dfs(entityTitle string,
	adjacencyList map[string]map[string]*types.Relationship,
	visited map[string]bool, component *[]string,
) {
	visited[entityTitle] = true
	*component = append(*component, entityTitle)

	// traverse all relationships of the current entity
	for targetEntity := range adjacencyList[entityTitle] {
		if !visited[targetEntity] {
			dfs(targetEntity, adjacencyList, visited, component)
		}
	}

	// check reverse relationships (check if other entities point to the current entity)
	for source, targets := range adjacencyList {
		for target := range targets {
			if target == entityTitle && !visited[source] {
				dfs(source, adjacencyList, visited, component)
			}
		}
	}
}

// generateKnowledgeGraphDiagram generate Mermaid diagram for knowledge graph
func (b *graphBuilder) generateKnowledgeGraphDiagram(ctx context.Context) string {
	log := logger.GetLogger(ctx)
	log.Info("Generating knowledge graph visualization diagram...")

	var sb strings.Builder

	// Mermaid diagram header
	sb.WriteString("```mermaid\ngraph TD\n")
	sb.WriteString("  %% entity style definition\n")
	sb.WriteString("  classDef entity fill:#f9f,stroke:#333,stroke-width:1px;\n")
	sb.WriteString("  classDef highFreq fill:#bbf,stroke:#333,stroke-width:2px;\n\n")

	// get all entities and sort by frequency
	entities := b.GetAllEntities()
	slices.SortFunc(entities, func(a, b *types.Entity) int {
		if a.Frequency > b.Frequency {
			return -1
		} else if a.Frequency < b.Frequency {
			return 1
		}
		return 0
	})

	// get relationships and sort by weight
	relationships := b.GetAllRelationships()
	slices.SortFunc(relationships, func(a, b *types.Relationship) int {
		if a.Weight > b.Weight {
			return -1
		} else if a.Weight < b.Weight {
			return 1
		}
		return 0
	})

	// create entity ID mapping
	entityMap := make(map[string]string) // store entity title to node ID mapping
	for i, entity := range entities {
		nodeID := fmt.Sprintf("E%d", i)
		entityMap[entity.Title] = nodeID
	}

	// create adjacency list to represent graph structure
	adjacencyList := make(map[string]map[string]*types.Relationship)
	for _, entity := range entities {
		adjacencyList[entity.Title] = make(map[string]*types.Relationship)
	}

	// fill adjacency list
	for _, rel := range relationships {
		if _, sourceExists := entityMap[rel.Source]; sourceExists {
			if _, targetExists := entityMap[rel.Target]; targetExists {
				adjacencyList[rel.Source][rel.Target] = rel
			}
		}
	}

	// use DFS to find connected components (subgraphs)
	visited := make(map[string]bool)
	subgraphs := make([][]string, 0) // store entity titles in each subgraph

	for _, entity := range entities {
		if !visited[entity.Title] {
			component := make([]string, 0)
			dfs(entity.Title, adjacencyList, visited, &component)
			if len(component) > 0 {
				subgraphs = append(subgraphs, component)
			}
		}
	}

	// generate Mermaid subgraphs
	subgraphCount := 0
	for _, component := range subgraphs {
		// check if this component has relationships
		hasRelations := false
		nodeCount := len(component)

		// if there is only 1 node, check if it has relationships
		if nodeCount == 1 {
			entityTitle := component[0]
			// check if this entity appears as source or target in any relationship
			for _, rel := range relationships {
				if rel.Source == entityTitle || rel.Target == entityTitle {
					hasRelations = true
					break
				}
			}

			// if there is only 1 node and no relationships, skip this subgraph
			if !hasRelations {
				continue
			}
		} else if nodeCount > 1 {
			// a subgraph with more than 1 node must have relationships
			hasRelations = true
		}

		// only draw if there are multiple entities or at least one relationship in the subgraph
		if hasRelations {
			subgraphCount++
			sb.WriteString(fmt.Sprintf("\n  subgraph Subgraph%d\n", subgraphCount))

			// add all entities in this subgraph
			entitiesInComponent := make(map[string]bool)
			for _, entityTitle := range component {
				nodeID := entityMap[entityTitle]
				entitiesInComponent[entityTitle] = true

				// add node definition for each entity
				entity := b.entityMapByTitle[entityTitle]
				if entity != nil {
					sb.WriteString(fmt.Sprintf("    %s[\"%s\"]\n", nodeID, entityTitle))
				}
			}

			// add relationships in this subgraph
			for _, rel := range relationships {
				if entitiesInComponent[rel.Source] && entitiesInComponent[rel.Target] {
					sourceID := entityMap[rel.Source]
					targetID := entityMap[rel.Target]

					linkStyle := "-->"
					// adjust link style based on relationship strength
					if rel.Strength > 7 {
						linkStyle = "==>"
					}

					sb.WriteString(fmt.Sprintf("    %s %s|%s| %s\n",
						sourceID, linkStyle, rel.Description, targetID))
				}
			}

			// subgraph ends
			sb.WriteString("  end\n")

			// apply style class
			for _, entityTitle := range component {
				nodeID := entityMap[entityTitle]
				entity := b.entityMapByTitle[entityTitle]
				if entity != nil {
					if entity.Frequency > 5 {
						sb.WriteString(fmt.Sprintf("  class %s highFreq;\n", nodeID))
					} else {
						sb.WriteString(fmt.Sprintf("  class %s entity;\n", nodeID))
					}
				}
			}
		}
	}

	// close Mermaid diagram
	sb.WriteString("```\n")

	log.Infof("Knowledge graph visualization diagram generated with %d subgraphs", subgraphCount)
	return sb.String()
}
