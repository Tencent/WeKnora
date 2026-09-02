// Package knowledgegraph stores the product graph projection derived from Wiki pages.
//
// The product graph is deliberately isolated from WeKnora's native GraphRAG graph.
// Wiki pages are the source of truth; Neo4j only contains a rebuildable projection.
package knowledgegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/google/uuid"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"gorm.io/gorm"
)

const defaultNamespace = "VIDSAGE_KNOWLEDGE"

const relationConfidenceThreshold = 0.70
const knowledgePageTypes = "entity,concept,index"

var allowedRelationTypes = map[string]struct{}{
	"contradicts":  {},
	"complements":  {},
	"explains":     {},
	"example_of":   {},
	"part_of":      {},
	"derived_from": {},
	"supports":     {},
	"related_to":   {},
}

type Node struct {
	ID                       string
	WikiPageID               string
	KnowledgeObjectID        string
	KnowledgeType            knowledge.KnowledgeType
	Title                    string
	Summary                  string
	SourceVideoID            string
	TranscriptGeneration     string
	AuditStatus              string
	ClassificationConfidence float64
	EvidenceIDs              []string
}

type Edge struct {
	ID               string
	SourceWikiPageID string
	TargetWikiPageID string
	RelationType     string
	Confidence       float64
	EvidenceIDs      []string
}

type Graph struct {
	Nodes []Node
	Edges []Edge
}

type Query struct {
	VideoID string
	Types   []knowledge.KnowledgeType
	Limit   int
}

type Store interface {
	ProjectVideo(context.Context, *model.Video, *weknora.WikiPage) error
	ProjectKnowledgeBase(context.Context) error
	Query(context.Context, Query) (*Graph, error)
	Close(context.Context) error
}

type StoreImpl struct {
	driver    neo4j.Driver
	database  string
	namespace string
	wiki      *weknora.WikiClient
	kbID      string
	db        *gorm.DB
}

func New(cfg config.WikiGraphConfig, wiki *weknora.WikiClient, databases ...*gorm.DB) (Store, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if strings.TrimSpace(cfg.URI) == "" || strings.TrimSpace(cfg.Username) == "" {
		return nil, fmt.Errorf("wiki graph neo4j is enabled but URI or username is empty")
	}
	driver, err := neo4j.NewDriver(cfg.URI, neo4j.BasicAuth(cfg.Username, cfg.Password, ""))
	if err != nil {
		return nil, fmt.Errorf("create wiki graph neo4j driver: %w", err)
	}
	if err := driver.VerifyConnectivity(context.Background()); err != nil {
		_ = driver.Close(context.Background())
		return nil, fmt.Errorf("verify wiki graph neo4j connectivity: %w", err)
	}
	namespace := strings.TrimSpace(cfg.Namespace)
	if namespace == "" {
		namespace = defaultNamespace
	}
	var db *gorm.DB
	if len(databases) > 0 {
		db = databases[0]
	}
	return &StoreImpl{
		driver: driver, database: strings.TrimSpace(cfg.Database), namespace: namespace,
		wiki: wiki, kbID: cfg.KnowledgeBaseID, db: db,
	}, nil
}

func (s *StoreImpl) Close(ctx context.Context) error {
	if s == nil || s.driver == nil {
		return nil
	}
	return s.driver.Close(ctx)
}

func (s *StoreImpl) ProjectVideo(ctx context.Context, video *model.Video, indexPage *weknora.WikiPage) error {
	if s == nil || s.driver == nil {
		return fmt.Errorf("wiki graph neo4j is unavailable")
	}
	if video == nil || strings.TrimSpace(video.ID) == "" {
		return fmt.Errorf("video is required for wiki graph projection")
	}
	if s.wiki == nil || strings.TrimSpace(s.kbID) == "" {
		return fmt.Errorf("wiki client and knowledge base are required for wiki graph projection")
	}
	if indexPage == nil || indexPage.ID == "" {
		return fmt.Errorf("knowledge base Wiki index page is required for wiki graph projection")
	}

	pages, err := s.wiki.ListByVideoOwned(ctx, s.kbID, video.ID, knowledgePageTypes, indexPage)
	if err != nil {
		return fmt.Errorf("list Wiki knowledge objects: %w", err)
	}
	nodes, edges, relationAudits, identityAudits, err := buildProjectionWithAudit(video, pages)
	if err != nil {
		return err
	}
	if err := persistRelationAudits(s.db, video.ID, video.TranscriptGeneration, relationAudits); err != nil {
		return err
	}
	if err := persistIdentityAudits(s.db, video.ID, video.TranscriptGeneration, identityAudits); err != nil {
		return err
	}

	session := s.driver.NewSession(ctx, neo4j.SessionConfig{
		AccessMode:   neo4j.AccessModeWrite,
		DatabaseName: s.database,
	})
	defer session.Close(ctx)

	_, err = session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		deleteRels := fmt.Sprintf(`
			MATCH (n:%s {source_video_id: $video_id})-[r]-()
			DELETE r`, s.namespace)
		if err := runAndConsume(ctx, tx, deleteRels, map[string]any{
			"video_id": video.ID,
		}); err != nil {
			return nil, fmt.Errorf("delete old Wiki graph relationships: %w", err)
		}

		deleteNodes := fmt.Sprintf(`
			MATCH (n:%s {source_video_id: $video_id})
			DETACH DELETE n`, s.namespace)
		if err := runAndConsume(ctx, tx, deleteNodes, map[string]any{
			"video_id": video.ID,
		}); err != nil {
			return nil, fmt.Errorf("delete old Wiki graph nodes: %w", err)
		}

		nodeQuery := fmt.Sprintf(`
			UNWIND $nodes AS row
			MERGE (n:%s {wiki_page_id: row.wiki_page_id})
			SET n.knowledge_object_id = row.knowledge_object_id,
				n.knowledge_type = row.knowledge_type,
				n.title = row.title,
				n.summary = row.summary,
				n.source_video_id = row.source_video_id,
				n.transcript_generation = row.transcript_generation,
				n.audit_status = row.audit_status,
				n.classification_confidence = row.classification_confidence,
				n.evidence_ids = row.evidence_ids,
				n.projection_version = $projection_version`, s.namespace)
		if err := runAndConsume(ctx, tx, nodeQuery, map[string]any{
			"nodes":              projectionNodes(nodes),
			"projection_version": "wiki-v1",
		}); err != nil {
			return nil, fmt.Errorf("write Wiki graph nodes: %w", err)
		}

		edgeQuery := fmt.Sprintf(`
			UNWIND $edges AS row
			MATCH (source:%s {wiki_page_id: row.source_wiki_page_id})
			MATCH (target:%s {wiki_page_id: row.target_wiki_page_id})
			MERGE (source)-[r:KNOWLEDGE_RELATION {relation_id: row.relation_id}]->(target)
			SET r.relation_type = row.relation_type,
				r.confidence = row.confidence,
				r.evidence_ids = row.evidence_ids,
				r.projection_version = $projection_version`, s.namespace, s.namespace)
		if len(edges) > 0 {
			if err := runAndConsume(ctx, tx, edgeQuery, map[string]any{
				"edges":              projectionEdges(edges),
				"projection_version": "wiki-v1",
			}); err != nil {
				return nil, fmt.Errorf("write Wiki graph relationships: %w", err)
			}
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	slog.Info("projected Wiki graph", "video_id", video.ID, "generation", video.TranscriptGeneration, "nodes", len(nodes), "edges", len(edges))
	return nil
}

func (s *StoreImpl) ProjectKnowledgeBase(ctx context.Context) error {
	if s == nil || s.driver == nil {
		return fmt.Errorf("wiki graph neo4j is unavailable")
	}
	if s.wiki == nil || strings.TrimSpace(s.kbID) == "" {
		return fmt.Errorf("wiki client and knowledge base are required for wiki graph projection")
	}
	pages, err := s.wiki.ListAllPages(ctx, s.kbID, "")
	if err != nil {
		return fmt.Errorf("list Wiki pages for knowledge base graph: %w", err)
	}
	nodes, edges := buildKnowledgeBaseProjection(pages)

	session := s.driver.NewSession(ctx, neo4j.SessionConfig{
		AccessMode:   neo4j.AccessModeWrite,
		DatabaseName: s.database,
	})
	defer session.Close(ctx)

	_, err = session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		deleteQuery := fmt.Sprintf(`
			MATCH (n:%s {projection_scope: $projection_scope})
			DETACH DELETE n`, s.namespace)
		if err := runAndConsume(ctx, tx, deleteQuery, map[string]any{
			"projection_scope": "knowledge_base",
		}); err != nil {
			return nil, fmt.Errorf("delete old knowledge base Wiki graph: %w", err)
		}

		nodeQuery := fmt.Sprintf(`
			UNWIND $nodes AS row
			MERGE (n:%s {wiki_page_id: row.wiki_page_id})
			SET n.knowledge_object_id = row.knowledge_object_id,
				n.knowledge_type = row.knowledge_type,
				n.title = row.title,
				n.summary = row.summary,
				n.source_video_id = row.source_video_id,
				n.transcript_generation = row.transcript_generation,
				n.audit_status = row.audit_status,
				n.classification_confidence = row.classification_confidence,
				n.evidence_ids = row.evidence_ids,
				n.projection_scope = $projection_scope,
				n.projection_version = $projection_version`, s.namespace)
		if err := runAndConsume(ctx, tx, nodeQuery, map[string]any{
			"nodes": projectionNodes(nodes), "projection_scope": "knowledge_base",
			"projection_version": "wiki-v1",
		}); err != nil {
			return nil, fmt.Errorf("write knowledge base Wiki graph nodes: %w", err)
		}

		if len(edges) == 0 {
			return nil, nil
		}
		edgeQuery := fmt.Sprintf(`
			UNWIND $edges AS row
			MATCH (source:%s {wiki_page_id: row.source_wiki_page_id})
			MATCH (target:%s {wiki_page_id: row.target_wiki_page_id})
			MERGE (source)-[r:KNOWLEDGE_RELATION {relation_id: row.relation_id}]->(target)
			SET r.relation_type = row.relation_type,
				r.confidence = row.confidence,
				r.evidence_ids = row.evidence_ids,
				r.projection_scope = $projection_scope,
				r.projection_version = $projection_version`, s.namespace, s.namespace)
		if err := runAndConsume(ctx, tx, edgeQuery, map[string]any{
			"edges": projectionEdges(edges), "projection_scope": "knowledge_base",
			"projection_version": "wiki-v1",
		}); err != nil {
			return nil, fmt.Errorf("write knowledge base Wiki graph relationships: %w", err)
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	slog.Info("projected knowledge base Wiki graph", "nodes", len(nodes), "edges", len(edges))
	return nil
}

func (s *StoreImpl) Query(ctx context.Context, query Query) (*Graph, error) {
	if s == nil || s.driver == nil {
		return nil, fmt.Errorf("wiki graph neo4j is unavailable")
	}
	if query.Limit <= 0 {
		query.Limit = 500
	}
	if query.Limit > 500 {
		query.Limit = 500
	}
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{
		AccessMode:   neo4j.AccessModeRead,
		DatabaseName: s.database,
	})
	defer session.Close(ctx)

	params := map[string]any{
		"video_id": query.VideoID,
		"types":    knowledgeTypeValues(query.Types),
		"limit":    query.Limit,
	}
	nodes := make([]Node, 0, query.Limit)
	nodeIDs := make(map[string]struct{})
	_, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		cypher := fmt.Sprintf(`
			MATCH (n:%s)
			WHERE ($video_id = '' OR n.source_video_id = $video_id)
			  AND (size($types) = 0 OR n.knowledge_type IN $types)
			  AND n.audit_status = 'passed'
			  AND n.projection_version = 'wiki-v1'
			RETURN n
			ORDER BY n.title
			LIMIT $limit`, s.namespace)
		result, err := tx.Run(ctx, cypher, params)
		if err != nil {
			return nil, err
		}
		for result.Next(ctx) {
			raw, ok := result.Record().Get("n")
			if !ok {
				continue
			}
			node, ok := raw.(neo4j.Node)
			if !ok {
				continue
			}
			item := nodeFromProps(node.Props)
			if item.WikiPageID == "" {
				continue
			}
			nodes = append(nodes, item)
			nodeIDs[item.WikiPageID] = struct{}{}
		}
		return nil, result.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("query Wiki graph nodes: %w", err)
	}

	edges := make([]Edge, 0)
	if len(nodeIDs) == 0 {
		return &Graph{Nodes: nodes, Edges: edges}, nil
	}
	_, err = session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		cypher := fmt.Sprintf(`
			MATCH (source:%s)-[r:KNOWLEDGE_RELATION]->(target:%s)
			WHERE source.wiki_page_id IN $node_ids AND target.wiki_page_id IN $node_ids
			RETURN source, target, r`, s.namespace, s.namespace)
		result, err := tx.Run(ctx, cypher, map[string]any{"node_ids": keys(nodeIDs)})
		if err != nil {
			return nil, err
		}
		for result.Next(ctx) {
			record := result.Record()
			source, sourceOK := record.Get("source")
			target, targetOK := record.Get("target")
			rel, relOK := record.Get("r")
			sourceNode, sourceIsNode := source.(neo4j.Node)
			targetNode, targetIsNode := target.(neo4j.Node)
			relation, relationIsRel := rel.(neo4j.Relationship)
			if !sourceOK || !targetOK || !relOK || !sourceIsNode || !targetIsNode || !relationIsRel {
				continue
			}
			edges = append(edges, Edge{
				ID:               stringProp(relation.Props, "relation_id"),
				SourceWikiPageID: stringProp(sourceNode.Props, "wiki_page_id"),
				TargetWikiPageID: stringProp(targetNode.Props, "wiki_page_id"),
				RelationType:     stringProp(relation.Props, "relation_type"),
				Confidence:       floatProp(relation.Props, "confidence"),
				EvidenceIDs:      stringSliceProp(relation.Props, "evidence_ids"),
			})
		}
		return nil, result.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("query Wiki graph relationships: %w", err)
	}
	return &Graph{Nodes: nodes, Edges: edges}, nil
}

type wikiObject struct {
	Node
	EntitySubType   string
	Aliases         []string
	StructureFields map[string]string
	Relations       []relation
}

type relation struct {
	ID               string
	SourceWikiPageID string
	RelationType     string
	TargetObjectID   string
	TargetWikiPageID string
	EvidenceIDs      []string
	Confidence       float64
}

func buildProjection(video *model.Video, pages []weknora.WikiPage) ([]wikiObject, []relation, error) {
	objects, edges, _, _, err := buildProjectionWithAudit(video, pages)
	return objects, edges, err
}

func buildKnowledgeBaseProjection(pages []weknora.WikiPage) ([]wikiObject, []relation) {
	objects := make([]wikiObject, 0, len(pages))
	objectByID := make(map[string]wikiObject, len(pages))
	objectByPageID := make(map[string]wikiObject, len(pages))
	for _, page := range pages {
		object, ok := parseKnowledgeBaseObject(page)
		if !ok {
			continue
		}
		objects = append(objects, object)
		objectByID[object.KnowledgeObjectID] = object
		objectByPageID[object.WikiPageID] = object
	}

	edges := make([]relation, 0)
	seen := make(map[string]struct{})
	for _, source := range objects {
		for _, rel := range source.Relations {
			if _, exists := allowedRelationTypes[rel.RelationType]; !exists {
				continue
			}
			if rel.Confidence < relationConfidenceThreshold || len(rel.EvidenceIDs) == 0 {
				continue
			}
			target, ok := objectByID[rel.TargetObjectID]
			if !ok || target.WikiPageID != rel.TargetWikiPageID {
				target, ok = objectByPageID[rel.TargetWikiPageID]
			}
			if !ok || target.WikiPageID == source.WikiPageID {
				continue
			}
			key := source.WikiPageID + "\x00" + rel.RelationType + "\x00" + target.WikiPageID
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			if rel.ID == "" {
				rel.ID = source.WikiPageID + ":" + rel.RelationType + ":" + target.WikiPageID
			}
			rel.SourceWikiPageID = source.WikiPageID
			rel.TargetObjectID = target.KnowledgeObjectID
			rel.TargetWikiPageID = target.WikiPageID
			edges = append(edges, relation{
				ID: rel.ID, SourceWikiPageID: rel.SourceWikiPageID,
				RelationType: rel.RelationType, TargetObjectID: rel.TargetObjectID,
				TargetWikiPageID: rel.TargetWikiPageID, EvidenceIDs: rel.EvidenceIDs,
				Confidence: rel.Confidence,
			})
		}
	}
	return objects, edges
}

func parseKnowledgeBaseObject(page weknora.WikiPage) (wikiObject, bool) {
	knowledgeType := legacyKnowledgeType(page.PageType)
	if knowledgeType == "" || strings.TrimSpace(page.ID) == "" || strings.TrimSpace(page.Slug) == "" {
		return wikiObject{}, false
	}
	frontmatter := page.ParsedFrontmatter()
	primaryType := strings.ToLower(strings.TrimSpace(frontmatterString(frontmatter, "primary_type")))
	compatibilityType := strings.ToLower(strings.TrimSpace(frontmatterString(frontmatter, "type")))
	if primaryType != "" && compatibilityType != "" && primaryType != compatibilityType {
		return wikiObject{}, false
	}
	rawType := firstNonEmpty(primaryType, compatibilityType)
	if rawType == "knowledge_base" || rawType == "outline" || rawType == "overview" || rawType == "typed_summary" || rawType == "transcript_page" {
		return wikiObject{}, false
	}
	if mapped := knowledge.MapSkillToKnowledgeType(rawType); mapped != "" {
		knowledgeType = mapped
	}
	evidenceIDs := firstStringSlice(
		stringSliceValue(frontmatter["evidence_ids"]),
		page.ChunkRefs,
		page.SourceRefs,
	)
	sourceVideoID := frontmatterString(frontmatter, "source_video_id")
	transcriptGeneration := frontmatterString(frontmatter, "transcript_generation")
	relations, _ := parseRelations(frontmatter["relations"])
	return wikiObject{
		Node: Node{
			ID:                       "wiki:" + page.ID,
			WikiPageID:               page.ID,
			KnowledgeObjectID:        firstNonEmpty(frontmatterString(frontmatter, "knowledge_object_id"), page.ID),
			KnowledgeType:            knowledgeType,
			Title:                    firstNonEmpty(page.Title, frontmatterString(frontmatter, "title"), frontmatterString(frontmatter, "canonical_name"), page.Slug),
			Summary:                  firstNonEmpty(page.Summary, frontmatterString(frontmatter, "summary"), frontmatterString(frontmatter, "core_content")),
			SourceVideoID:            sourceVideoID,
			TranscriptGeneration:     transcriptGeneration,
			AuditStatus:              firstNonEmpty(strings.ToLower(frontmatterString(frontmatter, "audit_status")), "passed"),
			ClassificationConfidence: firstNonZeroFloat(floatFrom(frontmatter["classification_confidence"]), 1),
			EvidenceIDs:              evidenceIDs,
		},
		EntitySubType:   firstNonEmpty(frontmatterString(frontmatter, "entity_sub_type"), "technology"),
		Aliases:         append([]string(nil), page.Aliases...),
		StructureFields: map[string]string{},
		Relations:       relations,
	}, true
}

func legacyKnowledgeType(pageType string) knowledge.KnowledgeType {
	switch strings.ToLower(strings.TrimSpace(pageType)) {
	case "entity":
		return knowledge.TypeEntity
	case "concept":
		return knowledge.TypeConcept
	case "case":
		return knowledge.TypeCase
	case "method", "methodology":
		return knowledge.TypeMethodology
	case "insight":
		return knowledge.TypeInsight
	default:
		return ""
	}
}

func firstStringSlice(values ...[]string) []string {
	for _, value := range values {
		out := compactStrings(value)
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstNonZeroFloat(values ...float64) float64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

type RelationAudit struct {
	VideoID              string
	TranscriptGeneration string
	SourceWikiPageID     string
	SourceObjectID       string
	RelationID           string
	RelationType         string
	TargetObjectID       string
	TargetWikiPageID     string
	EvidenceIDs          []string
	Confidence           float64
	Status               string
	Reason               string
}

type IdentityAudit struct {
	VideoID                string
	TranscriptGeneration   string
	SourceWikiPageID       string
	SourceObjectID         string
	CandidateWikiPageID    string
	CandidateObjectID      string
	NormalizedName         string
	SourceType             knowledge.KnowledgeType
	CandidateType          knowledge.KnowledgeType
	SourceEntitySubType    string
	CandidateEntitySubType string
	TypeMatch              bool
	TitleMatch             bool
	ContextMatch           bool
	EvidenceOverlap        bool
	Score                  float64
	Decision               string
	Reason                 string
}

func buildProjectionWithAudit(video *model.Video, pages []weknora.WikiPage) ([]wikiObject, []relation, []RelationAudit, []IdentityAudit, error) {
	pageByID := make(map[string]weknora.WikiPage, len(pages))
	objects := make([]wikiObject, 0, len(pages))
	for _, page := range pages {
		pageByID[page.ID] = page
		object, ok, err := parseObject(video, page)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if ok {
			objects = append(objects, object)
		}
	}
	objectByID := make(map[string]wikiObject, len(objects))
	for _, object := range objects {
		if previous, exists := objectByID[object.KnowledgeObjectID]; exists {
			return nil, nil, nil, nil, fmt.Errorf(
				"knowledge object %s is duplicated by Wiki pages %s and %s",
				object.KnowledgeObjectID, previous.WikiPageID, object.WikiPageID,
			)
		}
		objectByID[object.KnowledgeObjectID] = object
	}
	identityAudits := classifyIdentityPairs(video, objects)
	edges := make([]relation, 0)
	audits := make([]RelationAudit, 0)
	for _, object := range objects {
		if len(object.Relations) == 0 {
			audits = append(audits, RelationAudit{
				VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration,
				SourceWikiPageID: object.WikiPageID, SourceObjectID: object.KnowledgeObjectID,
				Status: "not_generated", Reason: "source Wiki object has no structured relations",
			})
			continue
		}
		for _, rel := range object.Relations {
			audit := RelationAudit{
				VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration,
				SourceWikiPageID: object.WikiPageID, SourceObjectID: object.KnowledgeObjectID,
				RelationID: rel.ID, RelationType: rel.RelationType,
				TargetObjectID: rel.TargetObjectID, TargetWikiPageID: rel.TargetWikiPageID,
				EvidenceIDs: append([]string(nil), rel.EvidenceIDs...), Confidence: rel.Confidence,
			}
			if _, exists := allowedRelationTypes[rel.RelationType]; !exists {
				audit.Status, audit.Reason = "invalid_type", "relation type is not allowed"
				audits = append(audits, audit)
				continue
			}
			if rel.Confidence < relationConfidenceThreshold {
				audit.Status, audit.Reason = "low_confidence", "relation confidence is below the publish threshold"
				audits = append(audits, audit)
				continue
			}
			if len(rel.EvidenceIDs) == 0 {
				audit.Status, audit.Reason = "insufficient_evidence", "relation has no evidence IDs"
				audits = append(audits, audit)
				continue
			}
			target, exists := objectByID[rel.TargetObjectID]
			if !exists || target.WikiPageID != rel.TargetWikiPageID || pageByID[target.WikiPageID].ID == "" {
				audit.Status, audit.Reason = "target_not_found", "target object ID and Wiki page ID do not resolve to the same current object"
				audits = append(audits, audit)
				continue
			}
			if rel.ID == "" {
				rel.ID = object.WikiPageID + ":" + rel.RelationType + ":" + target.WikiPageID
			}
			rel.SourceWikiPageID = object.WikiPageID
			audit.RelationID, audit.Status, audit.Reason = rel.ID, "accepted", "relation passed type, target, evidence and confidence checks"
			audits = append(audits, audit)
			edges = append(edges, rel)
		}
	}
	return objects, edges, audits, identityAudits, nil
}

func persistRelationAudits(db *gorm.DB, videoID, generation string, audits []RelationAudit) error {
	if db == nil {
		return nil
	}
	if err := db.Where("video_id = ? AND transcript_generation = ?", videoID, generation).
		Delete(&model.WikiRelationAudit{}).Error; err != nil {
		return fmt.Errorf("clear Wiki relation audits: %w", err)
	}
	if len(audits) == 0 {
		return nil
	}
	rows := make([]model.WikiRelationAudit, 0, len(audits))
	for _, audit := range audits {
		rows = append(rows, model.WikiRelationAudit{
			ID: uuid.NewString(), VideoID: audit.VideoID, TranscriptGeneration: audit.TranscriptGeneration,
			SourceWikiPageID: audit.SourceWikiPageID, SourceObjectID: audit.SourceObjectID,
			RelationID: audit.RelationID, RelationType: audit.RelationType,
			TargetObjectID: audit.TargetObjectID, TargetWikiPageID: audit.TargetWikiPageID,
			EvidenceIDs: encodeStringSlice(audit.EvidenceIDs), Confidence: audit.Confidence,
			Status: audit.Status, Reason: audit.Reason,
		})
	}
	if err := db.Create(&rows).Error; err != nil {
		return fmt.Errorf("persist Wiki relation audits: %w", err)
	}
	return nil
}

func classifyIdentityPairs(video *model.Video, objects []wikiObject) []IdentityAudit {
	audits := make([]IdentityAudit, 0)
	for leftIndex := 0; leftIndex < len(objects); leftIndex++ {
		for rightIndex := leftIndex + 1; rightIndex < len(objects); rightIndex++ {
			left := objects[leftIndex]
			right := objects[rightIndex]
			normalizedName := commonNormalizedName(left.identityCandidate(), right.identityCandidate())
			if normalizedName == "" {
				continue
			}
			comparison := knowledge.CompareIdentity(left.identityCandidate(), right.identityCandidate())
			audits = append(audits, IdentityAudit{
				VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration,
				SourceWikiPageID: left.WikiPageID, SourceObjectID: left.KnowledgeObjectID,
				CandidateWikiPageID: right.WikiPageID, CandidateObjectID: right.KnowledgeObjectID,
				NormalizedName: normalizedName,
				SourceType:     left.KnowledgeType, CandidateType: right.KnowledgeType,
				SourceEntitySubType: left.EntitySubType, CandidateEntitySubType: right.EntitySubType,
				TypeMatch: comparison.TypeMatch, TitleMatch: comparison.TitleMatch,
				ContextMatch: comparison.ContextMatch, EvidenceOverlap: comparison.EvidenceOverlap,
				Score: comparison.Score, Decision: string(comparison.Decision), Reason: comparison.Reason,
			})
		}
	}
	return audits
}

func (o wikiObject) identityCandidate() knowledge.IdentityCandidate {
	return knowledge.IdentityCandidate{
		KnowledgeObjectID:    o.KnowledgeObjectID,
		KnowledgeType:        o.KnowledgeType,
		EntitySubType:        o.EntitySubType,
		Title:                o.Title,
		Aliases:              append([]string(nil), o.Aliases...),
		SourceVideoID:        o.SourceVideoID,
		TranscriptGeneration: o.TranscriptGeneration,
		StructureFields:      cloneStringMap(o.StructureFields),
		EvidenceIDs:          append([]string(nil), o.EvidenceIDs...),
	}
}

func commonNormalizedName(left, right knowledge.IdentityCandidate) string {
	leftNames := normalizedNames(append([]string{left.Title}, left.Aliases...))
	rightNames := normalizedNames(append([]string{right.Title}, right.Aliases...))
	for name := range leftNames {
		if _, ok := rightNames[name]; ok {
			return name
		}
	}
	return ""
}

func normalizedNames(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if normalized := knowledge.NormalizeIdentity(value); normalized != "" {
			out[normalized] = struct{}{}
		}
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func persistIdentityAudits(db *gorm.DB, videoID, generation string, audits []IdentityAudit) error {
	if db == nil {
		return nil
	}
	if err := db.Where("video_id = ? AND transcript_generation = ?", videoID, generation).
		Delete(&model.WikiIdentityAudit{}).Error; err != nil {
		return fmt.Errorf("clear Wiki identity audits: %w", err)
	}
	if len(audits) == 0 {
		return nil
	}
	rows := make([]model.WikiIdentityAudit, 0, len(audits))
	for _, audit := range audits {
		rows = append(rows, model.WikiIdentityAudit{
			ID: uuid.NewString(), VideoID: audit.VideoID, TranscriptGeneration: audit.TranscriptGeneration,
			SourceWikiPageID: audit.SourceWikiPageID, SourceObjectID: audit.SourceObjectID,
			CandidateWikiPageID: audit.CandidateWikiPageID, CandidateObjectID: audit.CandidateObjectID,
			NormalizedName: audit.NormalizedName,
			SourceType:     string(audit.SourceType), CandidateType: string(audit.CandidateType),
			SourceEntitySubType: audit.SourceEntitySubType, CandidateEntitySubType: audit.CandidateEntitySubType,
			TypeMatch: audit.TypeMatch, TitleMatch: audit.TitleMatch, ContextMatch: audit.ContextMatch,
			EvidenceOverlap: audit.EvidenceOverlap, Score: audit.Score,
			Decision: audit.Decision, Reason: audit.Reason,
		})
	}
	if err := db.Create(&rows).Error; err != nil {
		return fmt.Errorf("persist Wiki identity audits: %w", err)
	}
	return nil
}

func encodeStringSlice(values []string) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	data, _ := json.Marshal(out)
	return string(data)
}

func parseObject(video *model.Video, page weknora.WikiPage) (wikiObject, bool, error) {
	if strings.TrimSpace(page.Content) == "" {
		return wikiObject{}, false, nil
	}
	fm := page.ParsedFrontmatter()
	rawType := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		frontmatterString(fm, "primary_type"),
		frontmatterString(fm, "type"),
	)))
	if rawType == "knowledge_base" || rawType == "outline" || rawType == "typed_summary" || rawType == "transcript_page" {
		return wikiObject{}, false, nil
	}
	if rawType == "" {
		return wikiObject{}, false, nil
	}
	auditStatus := strings.ToLower(strings.TrimSpace(frontmatterString(fm, "audit_status")))
	if auditStatus != "passed" {
		return wikiObject{}, false, nil
	}
	if frontmatterString(fm, "source_video_id") != video.ID ||
		frontmatterString(fm, "transcript_generation") != video.TranscriptGeneration {
		return wikiObject{}, false, nil
	}
	validation, validationErr := knowledge.ValidateWikiObjectPage(
		page.Content,
		page.PageType,
		video.ID,
		video.TranscriptGeneration,
	)
	if validationErr != nil {
		// Pages outside the active video or non-object index pages are filtered
		// by ownership before projection. A page that claims the active video
		// but violates the object contract must fail the projection explicitly.
		if frontmatterString(fm, "source_video_id") == video.ID {
			return wikiObject{}, false, fmt.Errorf("validate Wiki object %s: %w", page.ID, validationErr)
		}
		return wikiObject{}, false, nil
	}
	sourceVideoID := frontmatterString(fm, "source_video_id")
	generation := frontmatterString(fm, "transcript_generation")
	if sourceVideoID != video.ID || generation != video.TranscriptGeneration {
		return wikiObject{}, false, nil
	}
	status := strings.ToLower(frontmatterString(fm, "audit_status"))
	if status != "passed" {
		return wikiObject{}, false, nil
	}
	mapped := validation.KnowledgeType
	objectID := validation.KnowledgeObjectID
	confidence := validation.ClassificationConfidence
	relations, err := parseRelations(fm["relations"])
	if err != nil {
		return wikiObject{}, false, fmt.Errorf("parse relations for Wiki page %s: %w", page.ID, err)
	}
	evidence := validation.EvidenceIDs
	aliases := stringSliceValue(fm["aliases"])
	if alias := strings.TrimSpace(stringValue(fm["alias"])); alias != "" {
		aliases = append(aliases, alias)
	}
	title := firstNonEmpty(page.Title, frontmatterString(fm, "title"), frontmatterString(fm, "canonical_name"), page.Slug)
	summary := firstNonEmpty(frontmatterString(fm, "summary"), frontmatterString(fm, "core_content"), frontmatterString(fm, "description"))
	return wikiObject{
		Node: Node{
			ID: "wiki:" + page.ID, WikiPageID: page.ID, KnowledgeObjectID: objectID,
			KnowledgeType: mapped, Title: title, Summary: summary, SourceVideoID: sourceVideoID,
			TranscriptGeneration: generation, AuditStatus: status,
			ClassificationConfidence: confidence, EvidenceIDs: evidence,
		},
		EntitySubType: validation.EntitySubType, Aliases: aliases,
		StructureFields: validation.StructureFields, Relations: relations,
	}, true, nil
}

func containsAll(values, required []string) bool {
	lookup := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			lookup[value] = struct{}{}
		}
	}
	for _, value := range required {
		if _, ok := lookup[strings.TrimSpace(value)]; !ok {
			return false
		}
	}
	return true
}

func parseRelations(raw any) ([]relation, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("relations must be a list")
	}
	out := make([]relation, 0, len(items))
	for _, item := range items {
		data, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("relation item must be an object")
		}
		out = append(out, relation{
			ID:               firstNonEmpty(stringValue(data["relation_id"]), stringValue(data["id"])),
			RelationType:     strings.TrimSpace(stringValue(data["relation_type"])),
			TargetObjectID:   strings.TrimSpace(stringValue(data["target_object_id"])),
			TargetWikiPageID: strings.TrimSpace(stringValue(data["target_wiki_page_id"])),
			EvidenceIDs:      stringSliceValue(data["evidence_ids"]),
			Confidence:       floatFrom(data["confidence"]),
		})
	}
	return out, nil
}

func projectionNodes(nodes []wikiObject) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, map[string]any{
			"wiki_page_id": node.WikiPageID, "knowledge_object_id": node.KnowledgeObjectID,
			"knowledge_type": string(node.KnowledgeType), "title": node.Title, "summary": node.Summary,
			"source_video_id": node.SourceVideoID, "transcript_generation": node.TranscriptGeneration,
			"audit_status": node.AuditStatus, "classification_confidence": node.ClassificationConfidence,
			"evidence_ids": node.EvidenceIDs,
		})
	}
	return out
}

func projectionEdges(edges []relation) []map[string]any {
	out := make([]map[string]any, 0, len(edges))
	for _, edge := range edges {
		out = append(out, map[string]any{
			"relation_id": edge.ID, "source_wiki_page_id": edge.SourceWikiPageID,
			"target_wiki_page_id": edge.TargetWikiPageID, "relation_type": edge.RelationType,
			"confidence": edge.Confidence, "evidence_ids": edge.EvidenceIDs,
		})
	}
	return out
}

func nodeFromProps(props map[string]any) Node {
	return Node{
		ID:                "wiki:" + stringProp(props, "wiki_page_id"),
		WikiPageID:        stringProp(props, "wiki_page_id"),
		KnowledgeObjectID: stringProp(props, "knowledge_object_id"),
		KnowledgeType:     knowledge.KnowledgeType(stringProp(props, "knowledge_type")),
		Title:             stringProp(props, "title"), Summary: stringProp(props, "summary"),
		SourceVideoID:            stringProp(props, "source_video_id"),
		TranscriptGeneration:     stringProp(props, "transcript_generation"),
		AuditStatus:              stringProp(props, "audit_status"),
		ClassificationConfidence: floatProp(props, "classification_confidence"),
		EvidenceIDs:              stringSliceProp(props, "evidence_ids"),
	}
}

func knowledgeTypeValues(types []knowledge.KnowledgeType) []string {
	out := make([]string, 0, len(types))
	for _, item := range types {
		if knowledge.IsKnowledgeType(item) {
			out = append(out, string(item))
		}
	}
	return out
}

func consume(ctx context.Context, result neo4j.ResultWithContext) error {
	_, err := result.Consume(ctx)
	return err
}

func runAndConsume(ctx context.Context, tx neo4j.ManagedTransaction, query string, params map[string]any) error {
	result, err := tx.Run(ctx, query, params)
	if err != nil {
		return err
	}
	return consume(ctx, result)
}

func keys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	return out
}

func frontmatterString(values map[string]any, key string) string {
	return strings.TrimSpace(stringValue(values[key]))
}

func stringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func stringSliceValue(value any) []string {
	switch values := value.(type) {
	case []any:
		out := make([]string, 0, len(values))
		for _, item := range values {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				out = append(out, strings.TrimSpace(value))
			}
		}
		return out
	case []string:
		return append([]string(nil), values...)
	default:
		return nil
	}
}

func stringProp(props map[string]any, key string) string {
	if value, ok := props[key].(string); ok {
		return value
	}
	return ""
}

func stringSliceProp(props map[string]any, key string) []string {
	switch value := props[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		return stringSliceValue(value)
	default:
		return nil
	}
}

func floatFrom(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case float32:
		return float64(number)
	case int:
		return float64(number)
	case int64:
		return float64(number)
	case uint64:
		return float64(number)
	default:
		return 0
	}
}

func floatProp(props map[string]any, key string) float64 {
	return floatFrom(props[key])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
