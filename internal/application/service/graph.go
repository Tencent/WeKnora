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
	// DefaultLLMTemperature 더 결정적인 결과를 위해 낮은 온도 사용
	DefaultLLMTemperature = 0.1

	// PMIWeight 관계 가중치 계산 시 PMI의 비율
	PMIWeight = 0.6

	// StrengthWeight 관계 가중치 계산 시 관계 강도의 비율
	StrengthWeight = 0.4

	// IndirectRelationWeightDecay 간접 관계 가중치에 대한 감쇠 계수
	IndirectRelationWeightDecay = 0.5

	// MaxConcurrentEntityExtractions 엔티티 추출 최대 동시성
	MaxConcurrentEntityExtractions = 4

	// MaxConcurrentRelationExtractions 관계 추출 최대 동시성
	MaxConcurrentRelationExtractions = 4

	// DefaultRelationBatchSize 관계 추출 기본 배치 크기
	DefaultRelationBatchSize = 5

	// MinEntitiesForRelation 관계 추출에 필요한 최소 엔티티 수
	MinEntitiesForRelation = 2

	// MinWeightValue 0으로 나누기를 피하기 위한 최소 가중치 값
	MinWeightValue = 1.0

	// WeightScaleFactor 가중치를 1-10 범위로 정규화하기 위한 배율
	WeightScaleFactor = 9.0
)

// ChunkRelation 두 Chunk 간의 관계를 나타냄
type ChunkRelation struct {
	// Weight 관계 가중치, PMI와 강도를 기반으로 계산됨
	Weight float64

	// Degree 관련 엔티티의 총 차수
	Degree int
}

// graphBuilder 지식 그래프 구축 기능 구현
type graphBuilder struct {
	config           *config.Config
	entityMap        map[string]*types.Entity       // ID로 인덱싱된 엔티티
	entityMapByTitle map[string]*types.Entity       // 제목으로 인덱싱된 엔티티
	relationshipMap  map[string]*types.Relationship // 관계 매핑
	chatModel        chat.Chat
	chunkGraph       map[string]map[string]*ChunkRelation // 문서 청크 관계 그래프
	mutex            sync.RWMutex                         // 동시 작업을 위한 뮤텍스
}

// NewGraphBuilder 새로운 그래프 빌더 생성
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

// extractEntities 텍스트 청크에서 엔티티 추출
// LLM을 사용하여 텍스트 내용을 분석하고 관련 엔티티를 식별합니다
func (b *graphBuilder) extractEntities(ctx context.Context, chunk *types.Chunk) ([]*types.Entity, error) {
	log := logger.GetLogger(ctx)
	log.Infof("Extracting entities from chunk: %s", chunk.ID)

	if chunk.Content == "" {
		log.Warn("Empty chunk content, skipping entity extraction")
		return []*types.Entity{}, nil
	}

	// 엔티티 추출을 위한 프롬프트 생성
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

	// LLM 호출하여 엔티티 추출
	log.Debug("Calling LLM to extract entities")
	resp, err := b.chatModel.Chat(ctx, messages, &chat.ChatOptions{
		Temperature: DefaultLLMTemperature,
		Thinking:    &thinking,
	})
	if err != nil {
		log.WithError(err).Error("Failed to extract entities from chunk")
		return nil, fmt.Errorf("LLM entity extraction failed: %w", err)
	}

	// JSON 응답 파싱
	var extractedEntities []*types.Entity
	if err := common.ParseLLMJsonResponse(resp.Content, &extractedEntities); err != nil {
		log.WithError(err).Errorf("Failed to parse entity extraction response, rsp content: %s", resp.Content)
		return nil, fmt.Errorf("failed to parse entity extraction response: %w", err)
	}
	log.Infof("Extracted %d entities from chunk", len(extractedEntities))

	// 명확한 형식으로 상세 엔티티 정보 출력
	log.Info("=========== EXTRACTED ENTITIES ===========")
	for i, entity := range extractedEntities {
		if entity == nil {
			continue
		}
		log.Infof("[Entity %d] Title: '%s', Description: '%s'", i+1, entity.Title, entity.Description)
	}
	log.Info("=========================================")

	var entities []*types.Entity

	// 엔티티 처리 및 entityMap 업데이트
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
			// 새로운 엔티티임
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
			// 이미 존재하는 엔티티, ChunkIDs 업데이트
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

// extractRelationships 엔티티 간의 관계 추출
// 여러 엔티티 간의 의미적 연결을 분석하고 관계를 수립합니다
func (b *graphBuilder) extractRelationships(ctx context.Context,
	chunks []*types.Chunk, entities []*types.Entity,
) error {
	log := logger.GetLogger(ctx)
	log.Infof("Extracting relationships from %d entities across %d chunks", len(entities), len(chunks))

	if len(entities) < MinEntitiesForRelation {
		log.Info("Not enough entities to form relationships (minimum 2)")
		return nil
	}

	// 프롬프트 작성을 위해 엔티티 직렬화
	entitiesJSON, err := json.Marshal(entities)
	if err != nil {
		log.WithError(err).Error("Failed to serialize entities to JSON")
		return fmt.Errorf("failed to serialize entities: %w", err)
	}

	// 청크 내용 병합
	content := b.mergeChunkContents(chunks)
	if content == "" {
		log.Warn("No content to extract relationships from")
		return nil
	}

	// 관계 추출 프롬프트 생성
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

	// LLM 호출하여 관계 추출
	log.Debug("Calling LLM to extract relationships")
	resp, err := b.chatModel.Chat(ctx, messages, &chat.ChatOptions{
		Temperature: DefaultLLMTemperature,
		Thinking:    &thinking,
	})
	if err != nil {
		log.WithError(err).Error("Failed to extract relationships")
		return fmt.Errorf("LLM relationship extraction failed: %w", err)
	}

	// JSON 응답 파싱
	var extractedRelationships []*types.Relationship
	if err := common.ParseLLMJsonResponse(resp.Content, &extractedRelationships); err != nil {
		log.WithError(err).Error("Failed to parse relationship extraction response")
		return fmt.Errorf("failed to parse relationship extraction response: %w", err)
	}
	log.Infof("Extracted %d relationships", len(extractedRelationships))

	// 명확한 형식으로 상세 관계 정보 출력
	log.Info("========= EXTRACTED RELATIONSHIPS =========")
	for i, rel := range extractedRelationships {
		if rel == nil {
			continue
		}
		log.Infof("[Relation %d] Source: '%s', Target: '%s', Description: '%s', Strength: %d",
			i+1, rel.Source, rel.Target, rel.Description, rel.Strength)
	}
	log.Info("===========================================")

	// 관계 처리 및 relationshipMap 업데이트
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
			// 새로운 관계임
			relationship.ID = uuid.New().String()
			relationship.ChunkIDs = relationChunkIDs
			b.relationshipMap[key] = relationship
			relationshipsAdded++
			log.Debugf("New relationship added: %s -> %s (ID: %s)",
				relationship.Source, relationship.Target, relationship.ID)
		} else {
			// 이미 존재하는 관계, 속성 업데이트
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
			// 강도 업데이트, 기존 강도와 새 관계 강도의 가중 평균 고려
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

// findRelationChunkIDs 두 엔티티 간의 공통 문서 청크 ID 찾기
func (b *graphBuilder) findRelationChunkIDs(source, target string, entities []*types.Entity) []string {
	relationChunkIDs := make(map[string]struct{})

	// 소스 및 타겟 엔티티에 대한 모든 문서 청크 ID 수집
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

	// 맵 키를 슬라이스로 변환
	result := make([]string, 0, len(relationChunkIDs))
	for chunkID := range relationChunkIDs {
		result = append(result, chunkID)
	}
	return result
}

// mergeChunkContents 여러 문서 청크의 내용 병합
// 일관된 내용을 보장하기 위해 청크 간의 중복 부분을 고려합니다
func (b *graphBuilder) mergeChunkContents(chunks []*types.Chunk) string {
	if len(chunks) == 0 {
		return ""
	}

	chunkContents := chunks[0].Content
	preChunk := chunks[0]

	for i := 1; i < len(chunks); i++ {
		// 중복되지 않는 내용 부분만 추가
		if preChunk.EndAt > chunks[i].StartAt {
			// 중복 시작 위치 계산
			startPos := preChunk.EndAt - chunks[i].StartAt
			if startPos >= 0 && startPos < len([]rune(chunks[i].Content)) {
				chunkContents = chunkContents + string([]rune(chunks[i].Content)[startPos:])
			}
		} else {
			// 청크 간 중복이 없는 경우 모든 내용 추가
			chunkContents = chunkContents + chunks[i].Content
		}
		preChunk = chunks[i]
	}

	return chunkContents
}

// BuildGraph 지식 그래프 구축
// 그래프 구축 프로세스의 주요 진입점 역할을 하며 모든 구성 요소를 조정합니다
func (b *graphBuilder) BuildGraph(ctx context.Context, chunks []*types.Chunk) error {
	log := logger.GetLogger(ctx)
	log.Infof("Building knowledge graph from %d chunks", len(chunks))
	startTime := time.Now()

	// 각 문서 청크에서 동시에 엔티티 추출
	chunkEntities := make([][]*types.Entity, len(chunks))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(MaxConcurrentEntityExtractions) // 동시성 제한

	for i, chunk := range chunks {
		i, chunk := i, chunk // 클로저 문제 방지를 위한 로컬 변수 생성
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

	// 모든 엔티티 추출이 완료될 때까지 대기
	if err := g.Wait(); err != nil {
		log.WithError(err).Error("Entity extraction failed")
		return fmt.Errorf("entity extraction process failed: %w", err)
	}

	// 총 추출된 엔티티 수 계산
	totalEntityCount := 0
	for _, entities := range chunkEntities {
		totalEntityCount += len(entities)
	}
	log.Infof("Successfully extracted %d total entities across %d chunks",
		totalEntityCount, len(chunks))

	// 배치를 통해 동시에 관계 처리
	relationChunkSize := DefaultRelationBatchSize
	log.Infof("Processing relationships concurrently in batches of %d chunks", relationChunkSize)

	// 관계 추출 배치 준비
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

		// 이 배치의 모든 엔티티 병합
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

	// 동시에 관계 추출
	relG, relGctx := errgroup.WithContext(ctx)
	relG.SetLimit(MaxConcurrentRelationExtractions) // 전용 관계 추출 동시성 제한 사용

	for _, batch := range relationBatches {
		relG.Go(func() error {
			log.Debugf("Processing relationship batch %d (chunks %d)", batch.batchIndex+1, len(batch.batchChunks))
			err := b.extractRelationships(relGctx, batch.batchChunks, batch.relationUseEntities)
			if err != nil {
				log.WithError(err).Errorf("Failed to extract relationships for batch %d", batch.batchIndex+1)
			}
			return nil // 현재 배치가 실패하더라도 다른 배치 계속 처리
		})
	}

	// 모든 관계 추출이 완료될 때까지 대기
	if err := relG.Wait(); err != nil {
		log.WithError(err).Error("Some relationship extraction tasks failed")
		// 그러나 일부 관계 추출은 여전히 유용하므로 다음 단계 처리를 계속합니다.
	}

	// 관계 가중치 계산
	log.Info("Calculating weights for relationships")
	b.calculateWeights(ctx)

	// 엔티티 차수 계산
	log.Info("Calculating degrees for entities")
	b.calculateDegrees(ctx)

	// Chunk 그래프 구축
	log.Info("Building chunk relationship graph")
	b.buildChunkGraph(ctx)

	log.Infof("Graph building completed in %.2f seconds: %d entities, %d relationships",
		time.Since(startTime).Seconds(), len(b.entityMap), len(b.relationshipMap))

	// 지식 그래프 시각화 다이어그램 생성
	mermaidDiagram := b.generateKnowledgeGraphDiagram(ctx)
	log.Info("Knowledge graph visualization diagram:")
	log.Info(mermaidDiagram)

	return nil
}

// calculateWeights 관계 가중치 계산
// PMI(Point Mutual Information)와 강도 값을 사용하여 관계 가중치를 계산합니다
func (b *graphBuilder) calculateWeights(ctx context.Context) {
	log := logger.GetLogger(ctx)
	log.Info("Calculating relationship weights using PMI and strength")

	// 총 엔티티 발생 횟수 계산
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

	// 총 관계 발생 횟수 계산
	totalRelOccurrences := 0
	for _, rel := range b.relationshipMap {
		if rel == nil {
			continue
		}
		totalRelOccurrences += len(rel.ChunkIDs)
	}

	// 데이터가 부족하면 계산 건너뛰기
	if totalEntityOccurrences == 0 || totalRelOccurrences == 0 {
		log.Warn("Insufficient data for weight calculation")
		return
	}

	// 정규화를 위해 최대 PMI 및 강도 값 추적
	maxPMI := 0.0
	maxStrength := MinWeightValue // 0으로 나누기 방지

	// 먼저 PMI를 계산하고 최대값 찾기
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

			// PMI 계산: log(P(x,y) / (P(x) * P(y)))
			pmi := math.Max(math.Log2(relProbability/(sourceProbability*targetProbability)), 0)
			pmiValues[rel.ID] = pmi

			if pmi > maxPMI {
				maxPMI = pmi
			}
		}

		// 최대 강도 값 기록
		if float64(rel.Strength) > maxStrength {
			maxStrength = float64(rel.Strength)
		}
	}

	// PMI와 강도를 결합하여 최종 가중치 계산
	for _, rel := range b.relationshipMap {
		pmi := pmiValues[rel.ID]

		// PMI 및 강도 정규화 (0-1 범위)
		normalizedPMI := 0.0
		if maxPMI > 0 {
			normalizedPMI = pmi / maxPMI
		}

		normalizedStrength := float64(rel.Strength) / maxStrength

		// 구성된 가중치를 사용하여 PMI와 강도 결합
		combinedWeight := normalizedPMI*PMIWeight + normalizedStrength*StrengthWeight

		// 가중치를 1-10 범위로 스케일링
		scaledWeight := 1.0 + WeightScaleFactor*combinedWeight

		rel.Weight = scaledWeight
	}

	log.Infof("Weight calculation completed for %d relationships", len(b.relationshipMap))
}

// calculateDegrees 엔티티 차수 계산
// 차수는 엔티티가 다른 엔티티와 연결된 수를 나타내며, 그래프 구조의 핵심 지표입니다
func (b *graphBuilder) calculateDegrees(ctx context.Context) {
	log := logger.GetLogger(ctx)
	log.Info("Calculating entity degrees")

	// 각 엔티티의 진입 차수 및 진출 차수 계산
	inDegree := make(map[string]int)
	outDegree := make(map[string]int)

	for _, rel := range b.relationshipMap {
		outDegree[rel.Source]++
		inDegree[rel.Target]++
	}

	// 각 엔티티의 차수 설정
	for _, entity := range b.entityMap {
		if entity == nil {
			continue
		}
		entity.Degree = inDegree[entity.Title] + outDegree[entity.Title]
	}

	// 관계의 결합 차수 설정
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

// buildChunkGraph Chunk 간의 관계 그래프 구축
// 엔티티 관계를 기반으로 문서 청크 간의 관계 네트워크를 생성합니다
func (b *graphBuilder) buildChunkGraph(ctx context.Context) {
	log := logger.GetLogger(ctx)
	log.Info("Building chunk relationship graph")

	// 엔티티 관계를 기반으로 문서 청크 관계 그래프 생성
	for _, rel := range b.relationshipMap {
		if rel == nil {
			continue
		}
		// 관계의 소스 및 타겟 엔티티가 존재하는지 확인
		sourceEntity := b.entityMapByTitle[rel.Source]
		targetEntity := b.entityMapByTitle[rel.Target]

		if sourceEntity == nil || targetEntity == nil {
			log.Warnf("Missing entity for relationship %s -> %s", rel.Source, rel.Target)
			continue
		}

		// Chunk 그래프 구축 - 관련된 모든 문서 청크 연결
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

// GetAllEntities 모든 엔티티 반환
func (b *graphBuilder) GetAllEntities() []*types.Entity {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	entities := make([]*types.Entity, 0, len(b.entityMap))
	for _, entity := range b.entityMap {
		entities = append(entities, entity)
	}
	return entities
}

// GetAllRelationships 모든 관계 반환
func (b *graphBuilder) GetAllRelationships() []*types.Relationship {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	relationships := make([]*types.Relationship, 0, len(b.relationshipMap))
	for _, relationship := range b.relationshipMap {
		relationships = append(relationships, relationship)
	}
	return relationships
}

// GetRelationChunks 주어진 chunkID와 직접 관련된 문서 청크 검색
// 가중치와 차수로 정렬된 관련 문서 청크 ID 목록을 반환합니다
func (b *graphBuilder) GetRelationChunks(chunkID string, topK int) []string {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	log := logger.GetLogger(context.Background())
	log.Debugf("Getting related chunks for %s (topK=%d)", chunkID, topK)

	// 정렬을 위한 가중치 청크 구조체 생성
	type weightedChunk struct {
		id     string
		weight float64
		degree int
	}

	// 가중치와 차수를 포함하여 관련 청크 수집
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

	// 가중치와 차수로 내림차순 정렬
	slices.SortFunc(weightedChunks, func(a, b weightedChunk) int {
		// 가중치로 먼저 정렬
		if a.weight > b.weight {
			return -1 // 내림차순
		} else if a.weight < b.weight {
			return 1
		}

		// 가중치가 같으면 차수로 정렬
		if a.degree > b.degree {
			return -1 // 내림차순
		} else if a.degree < b.degree {
			return 1
		}

		return 0
	})

	// 상위 K개 결과 가져오기
	resultCount := len(weightedChunks)
	if topK > 0 && topK < resultCount {
		resultCount = topK
	}

	// 청크 ID 추출
	chunks := make([]string, 0, resultCount)
	for i := 0; i < resultCount; i++ {
		chunks = append(chunks, weightedChunks[i].id)
	}

	log.Debugf("Found %d related chunks for %s (limited to %d)",
		len(weightedChunks), chunkID, resultCount)
	return chunks
}

// GetIndirectRelationChunks 주어진 chunkID와 간접적으로 관련된 문서 청크 검색
// 2차 연결을 통해 발견된 문서 청크 ID를 반환합니다
func (b *graphBuilder) GetIndirectRelationChunks(chunkID string, topK int) []string {
	b.mutex.RLock()
	defer b.mutex.RUnlock()

	log := logger.GetLogger(context.Background())
	log.Debugf("Getting indirectly related chunks for %s (topK=%d)", chunkID, topK)

	// 정렬을 위한 가중치 청크 구조체 생성
	type weightedChunk struct {
		id     string
		weight float64
		degree int
	}

	// 직접 관련된 청크 가져오기 (1차 연결)
	directChunks := make(map[string]struct{})
	directChunks[chunkID] = struct{}{} // 원본 chunkID 추가
	for directChunkID := range b.chunkGraph[chunkID] {
		directChunks[directChunkID] = struct{}{}
	}
	log.Debugf("Found %d directly related chunks to exclude", len(directChunks))

	// 중복 제거 및 2차 연결 저장을 위한 맵 사용
	indirectChunkMap := make(map[string]*ChunkRelation)

	// 1차 연결 가져오기
	for directChunkID, directRelation := range b.chunkGraph[chunkID] {
		if directRelation == nil {
			continue
		}
		// 2차 연결 가져오기
		for indirectChunkID, indirectRelation := range b.chunkGraph[directChunkID] {
			if indirectRelation == nil {
				continue
			}
			// 자기 자신과 모든 직접 연결 건너뛰기
			if _, isDirect := directChunks[indirectChunkID]; isDirect {
				continue
			}

			// 가중치 감쇠: 2차 관계 가중치는 두 직접 관계 가중치의 곱에 감쇠 계수를 곱한 값
			combinedWeight := directRelation.Weight * indirectRelation.Weight * IndirectRelationWeightDecay
			// 차수 계산: 두 경로 세그먼트에서 최대 차수 사용
			combinedDegree := max(directRelation.Degree, indirectRelation.Degree)

			// 이미 존재하면 더 높은 가중치 선택
			if existingRel, exists := indirectChunkMap[indirectChunkID]; !exists ||
				combinedWeight > existingRel.Weight {
				indirectChunkMap[indirectChunkID] = &ChunkRelation{
					Weight: combinedWeight,
					Degree: combinedDegree,
				}
			}
		}
	}

	// 정렬 가능한 슬라이스로 변환
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

	// 가중치와 차수로 내림차순 정렬
	slices.SortFunc(weightedChunks, func(a, b weightedChunk) int {
		// 가중치로 먼저 정렬
		if a.weight > b.weight {
			return -1 // 내림차순
		} else if a.weight < b.weight {
			return 1
		}

		// 가중치가 같으면 차수로 정렬
		if a.degree > b.degree {
			return -1 // 내림차순
		} else if a.degree < b.degree {
			return 1
		}

		return 0
	})

	// 상위 K개 결과 가져오기
	resultCount := len(weightedChunks)
	if topK > 0 && topK < resultCount {
		resultCount = topK
	}

	// 청크 ID 추출
	chunks := make([]string, 0, resultCount)
	for i := 0; i < resultCount; i++ {
		chunks = append(chunks, weightedChunks[i].id)
	}

	log.Debugf("Found %d indirect related chunks for %s (limited to %d)",
		len(weightedChunks), chunkID, resultCount)
	return chunks
}

// getEntityByTitle 제목으로 엔티티 검색
func (b *graphBuilder) getEntityByTitle(title string) *types.Entity {
	return b.entityMapByTitle[title]
}

// dfs 연결된 컴포넌트를 찾기 위한 깊이 우선 탐색
func dfs(entityTitle string,
	adjacencyList map[string]map[string]*types.Relationship,
	visited map[string]bool, component *[]string,
) {
	visited[entityTitle] = true
	*component = append(*component, entityTitle)

	// 현재 엔티티의 모든 관계 순회
	for targetEntity := range adjacencyList[entityTitle] {
		if !visited[targetEntity] {
			dfs(targetEntity, adjacencyList, visited, component)
		}
	}

	// 역방향 관계 확인 (다른 엔티티가 현재 엔티티를 가리키는지 확인)
	for source, targets := range adjacencyList {
		for target := range targets {
			if target == entityTitle && !visited[source] {
				dfs(source, adjacencyList, visited, component)
			}
		}
	}
}

// generateKnowledgeGraphDiagram 지식 그래프 시각화를 위한 Mermaid 다이어그램 생성
func (b *graphBuilder) generateKnowledgeGraphDiagram(ctx context.Context) string {
	log := logger.GetLogger(ctx)
	log.Info("Generating knowledge graph visualization diagram...")

	var sb strings.Builder

	// Mermaid 다이어그램 헤더
	sb.WriteString("```mermaid\ngraph TD\n")
	sb.WriteString("  %% entity style definition\n")
	sb.WriteString("  classDef entity fill:#f9f,stroke:#333,stroke-width:1px;\n")
	sb.WriteString("  classDef highFreq fill:#bbf,stroke:#333,stroke-width:2px;\n\n")

	// 모든 엔티티 가져오기 및 빈도로 정렬
	entities := b.GetAllEntities()
	slices.SortFunc(entities, func(a, b *types.Entity) int {
		if a.Frequency > b.Frequency {
			return -1
		} else if a.Frequency < b.Frequency {
			return 1
		}
		return 0
	})

	// 모든 관계 가져오기 및 가중치로 정렬
	relationships := b.GetAllRelationships()
	slices.SortFunc(relationships, func(a, b *types.Relationship) int {
		if a.Weight > b.Weight {
			return -1
		} else if a.Weight < b.Weight {
			return 1
		}
		return 0
	})

	// 엔티티 ID 매핑 생성
	entityMap := make(map[string]string) // 엔티티 제목을 노드 ID로 매핑 저장
	for i, entity := range entities {
		nodeID := fmt.Sprintf("E%d", i)
		entityMap[entity.Title] = nodeID
	}

	// 그래프 구조를 나타내기 위한 인접 리스트 생성
	adjacencyList := make(map[string]map[string]*types.Relationship)
	for _, entity := range entities {
		adjacencyList[entity.Title] = make(map[string]*types.Relationship)
	}

	// 인접 리스트 채우기
	for _, rel := range relationships {
		if _, sourceExists := entityMap[rel.Source]; sourceExists {
			if _, targetExists := entityMap[rel.Target]; targetExists {
				adjacencyList[rel.Source][rel.Target] = rel
			}
		}
	}

	// DFS를 사용하여 연결된 컴포넌트(서브그래프) 찾기
	visited := make(map[string]bool)
	subgraphs := make([][]string, 0) // 각 서브그래프의 엔티티 제목 저장

	for _, entity := range entities {
		if !visited[entity.Title] {
			component := make([]string, 0)
			dfs(entity.Title, adjacencyList, visited, &component)
			if len(component) > 0 {
				subgraphs = append(subgraphs, component)
			}
		}
	}

	// Mermaid 서브그래프 생성
	subgraphCount := 0
	for _, component := range subgraphs {
		// 이 컴포넌트가 관계를 가지고 있는지 확인
		hasRelations := false
		nodeCount := len(component)

		// 노드가 1개만 있는 경우, 관계가 있는지 확인
		if nodeCount == 1 {
			entityTitle := component[0]
			// 이 엔티티가 어떤 관계의 소스 또는 타겟으로 나타나는지 확인
			for _, rel := range relationships {
				if rel.Source == entityTitle || rel.Target == entityTitle {
					hasRelations = true
					break
				}
			}

			// 노드가 1개뿐이고 관계가 없으면 이 서브그래프 건너뛰기
			if !hasRelations {
				continue
			}
		} else if nodeCount > 1 {
			// 노드가 1개 이상인 서브그래프는 관계가 있어야 함
			hasRelations = true
		}

		// 서브그래프에 여러 엔티티가 있거나 적어도 하나의 관계가 있는 경우에만 그리기
		if hasRelations {
			subgraphCount++
			sb.WriteString(fmt.Sprintf("\n  subgraph 서브그래프%d\n", subgraphCount))

			// 이 서브그래프의 모든 엔티티 추가
			entitiesInComponent := make(map[string]bool)
			for _, entityTitle := range component {
				nodeID := entityMap[entityTitle]
				entitiesInComponent[entityTitle] = true

				// 각 엔티티에 대한 노드 정의 추가
				entity := b.entityMapByTitle[entityTitle]
				if entity != nil {
					sb.WriteString(fmt.Sprintf("    %s[\"%s\"]\n", nodeID, entityTitle))
				}
			}

			// 이 서브그래프의 관계 추가
			for _, rel := range relationships {
				if entitiesInComponent[rel.Source] && entitiesInComponent[rel.Target] {
					sourceID := entityMap[rel.Source]
					targetID := entityMap[rel.Target]

					linkStyle := "-->"
					// 관계 강도에 따라 링크 스타일 조정
					if rel.Strength > 7 {
						linkStyle = "==>"
					}

					sb.WriteString(fmt.Sprintf("    %s %s|%s| %s\n",
						sourceID, linkStyle, rel.Description, targetID))
				}
			}

			// 서브그래프 종료
			sb.WriteString("  end\n")

			// 스타일 클래스 적용
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

	// Mermaid 다이어그램 닫기
	sb.WriteString("```\n")

	log.Infof("Knowledge graph visualization diagram generated with %d subgraphs", subgraphCount)
	return sb.String()
}
