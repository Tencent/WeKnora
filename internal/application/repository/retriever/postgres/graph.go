// Package postgres now also stores the entity-relation knowledge graph.
//
// The graph contract is small (three methods on RetrieveGraphRepository plus
// SearchGraph for the public API) and the Neo4j adapter already defines its
// semantics: document-scoped nodes, idempotent merges, bounded one-hop
// expansion and stored edge direction. This adapter reproduces those rules on
// the PostgreSQL database the deployment already runs, so a small host does
// not need a second JVM service for a graph whose hot path is a substring
// lookup plus one hop.
package postgres

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// These caps mirror the Neo4j adapter (graphSearchMaxSeedNodes and
	// graphSearchMaxRows) and the public API's constants (types.GraphSearch*),
	// so switching backends does not change how much a result can contain.
	graphSearchMaxSeedNodes = 200
	graphSearchMaxRows      = 2000

	// graphProjectionOvershoot keeps one extra element/character so the service
	// can report clipping instead of a false total, exactly like the Cypher
	// projection's [..513] / substring(.., 0, 513).
	graphProjectionOvershoot = 1
)

// GraphRepository stores the entity-relation graph in PostgreSQL. It implements
// both RetrieveGraphRepository and GraphSearchRepository.
type GraphRepository struct {
	db *gorm.DB
}

// NewGraphStore builds the PostgreSQL graph adapter. A nil database makes every
// method a no-op, matching the disabled Neo4j adapter's behaviour.
func NewGraphStore(db *gorm.DB) *GraphRepository {
	return &GraphRepository{db: db}
}

type graphNodeRow struct {
	ID              string          `gorm:"column:id"`
	KnowledgeBaseID string          `gorm:"column:knowledge_base_id"`
	KnowledgeID     string          `gorm:"column:knowledge_id"`
	Name            string          `gorm:"column:name"`
	Attributes      jsonStringArray `gorm:"column:attributes"`
}

func (graphNodeRow) TableName() string { return "graph_nodes" }

type graphNodeChunkRow struct {
	NodeID  string `gorm:"column:node_id"`
	ChunkID string `gorm:"column:chunk_id"`
}

func (graphNodeChunkRow) TableName() string { return "graph_node_chunks" }

type graphEdgeRow struct {
	ID              string `gorm:"column:id"`
	KnowledgeBaseID string `gorm:"column:knowledge_base_id"`
	KnowledgeID     string `gorm:"column:knowledge_id"`
	SourceID        string `gorm:"column:source_id"`
	TargetID        string `gorm:"column:target_id"`
	Type            string `gorm:"column:type"`
}

func (graphEdgeRow) TableName() string { return "graph_edges" }

// jsonStringArray scans JSON arrays from both PostgreSQL jsonb ([]byte) and the
// SQLite tests' TEXT columns (string), so the semantics harness can run on a
// plain in-memory database.
type jsonStringArray []string

func (a jsonStringArray) Value() (driver.Value, error) {
	if a == nil {
		return "[]", nil
	}
	return json.Marshal(a)
}

func (a *jsonStringArray) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*a = nil
		return nil
	case []byte:
		return json.Unmarshal(v, a)
	case string:
		return json.Unmarshal([]byte(v), a)
	default:
		return fmt.Errorf("graph attributes: unsupported type %T", value)
	}
}

type graphNodeInput struct {
	name       string
	attributes []string
	chunks     []string
}

type graphRelationInput struct {
	source string
	target string
	typ    string
}

// AddGraph upserts nodes, their chunk membership and edges for one document.
// Re-extraction is idempotent: attributes are set on first insert (the Neo4j
// merge ignores on-match props) and chunks accumulate.
func (g *GraphRepository) AddGraph(
	ctx context.Context, namespace types.NameSpace, graphs []*types.GraphData,
) error {
	if g == nil || g.db == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	if namespace.KnowledgeBase == "" || namespace.Knowledge == "" {
		return fmt.Errorf("graph namespace requires both a knowledge base and a document")
	}
	if len(graphs) == 0 {
		return nil
	}

	return g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, graph := range graphs {
			if graph == nil {
				continue
			}
			nodes, relations := normalizeGraph(graph)
			if len(nodes) == 0 {
				continue
			}
			if err := upsertGraphNodes(tx, namespace, nodes); err != nil {
				return err
			}
			ids, err := lookupGraphNodeIDs(tx, namespace, nodes)
			if err != nil {
				return err
			}
			if err := insertGraphNodeChunks(tx, ids, nodes); err != nil {
				return err
			}
			if err := insertGraphEdges(tx, namespace, relations, ids); err != nil {
				return err
			}
		}
		return nil
	})
}

// normalizeGraph deduplicates a graph payload into insertable nodes and
// relations. Relation endpoints missing from the node list are created but
// carry no attributes or chunks, mirroring apoc.merge.node in the relationship
// query.
func normalizeGraph(graph *types.GraphData) ([]graphNodeInput, []graphRelationInput) {
	nodes := make([]graphNodeInput, 0, len(graph.Node))
	index := make(map[string]int, len(graph.Node))
	seenChunks := make(map[string]map[string]bool, len(graph.Node))

	for _, node := range graph.Node {
		if node == nil || strings.TrimSpace(node.Name) == "" {
			continue
		}
		if at, ok := index[node.Name]; ok {
			existing := &nodes[at]
			for _, chunk := range node.Chunks {
				if chunk == "" || seenChunks[node.Name][chunk] {
					continue
				}
				seenChunks[node.Name][chunk] = true
				existing.chunks = append(existing.chunks, chunk)
			}
			continue
		}
		index[node.Name] = len(nodes)
		seenChunks[node.Name] = make(map[string]bool, len(node.Chunks))
		chunks := make([]string, 0, len(node.Chunks))
		for _, chunk := range node.Chunks {
			if chunk == "" || seenChunks[node.Name][chunk] {
				continue
			}
			seenChunks[node.Name][chunk] = true
			chunks = append(chunks, chunk)
		}
		nodes = append(nodes, graphNodeInput{
			name:       node.Name,
			attributes: append([]string(nil), node.Attributes...),
			chunks:     chunks,
		})
	}

	relations := make([]graphRelationInput, 0, len(graph.Relation))
	seenRelations := make(map[string]bool, len(graph.Relation))
	for _, relation := range graph.Relation {
		if relation == nil {
			continue
		}
		source := strings.TrimSpace(relation.Node1)
		target := strings.TrimSpace(relation.Node2)
		typ := strings.TrimSpace(relation.Type)
		if source == "" || target == "" || typ == "" {
			continue
		}
		key := source + "\x00" + target + "\x00" + typ
		if seenRelations[key] {
			continue
		}
		seenRelations[key] = true
		for _, endpoint := range []string{source, target} {
			if _, ok := index[endpoint]; ok {
				continue
			}
			index[endpoint] = len(nodes)
			seenChunks[endpoint] = map[string]bool{}
			nodes = append(nodes, graphNodeInput{name: endpoint})
		}
		relations = append(relations, graphRelationInput{source: source, target: target, typ: typ})
	}
	return nodes, relations
}

func upsertGraphNodes(tx *gorm.DB, namespace types.NameSpace, nodes []graphNodeInput) error {
	rows := make([]graphNodeRow, 0, len(nodes))
	for _, node := range nodes {
		rows = append(rows, graphNodeRow{
			ID:              uuid.NewString(),
			KnowledgeBaseID: namespace.KnowledgeBase,
			KnowledgeID:     namespace.Knowledge,
			Name:            node.name,
			Attributes:      jsonStringArray(node.attributes),
		})
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "knowledge_base_id"}, {Name: "knowledge_id"}, {Name: "name"},
		},
		DoNothing: true,
	}).CreateInBatches(rows, 200).Error
}

func lookupGraphNodeIDs(
	tx *gorm.DB, namespace types.NameSpace, nodes []graphNodeInput,
) (map[string]string, error) {
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		names = append(names, node.name)
	}
	rows := make([]graphNodeRow, 0, len(names))
	if err := tx.Table("graph_nodes").
		Select("id, name").
		Where("knowledge_base_id = ? AND knowledge_id = ? AND name IN ?",
			namespace.KnowledgeBase, namespace.Knowledge, names).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make(map[string]string, len(rows))
	for _, row := range rows {
		ids[row.Name] = row.ID
	}
	return ids, nil
}

func insertGraphNodeChunks(
	tx *gorm.DB, ids map[string]string, nodes []graphNodeInput,
) error {
	rows := make([]graphNodeChunkRow, 0, len(nodes))
	for _, node := range nodes {
		nodeID := ids[node.name]
		if nodeID == "" {
			continue
		}
		for _, chunk := range node.chunks {
			rows = append(rows, graphNodeChunkRow{NodeID: nodeID, ChunkID: chunk})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 500).Error
}

func insertGraphEdges(
	tx *gorm.DB, namespace types.NameSpace, relations []graphRelationInput, ids map[string]string,
) error {
	rows := make([]graphEdgeRow, 0, len(relations))
	for _, relation := range relations {
		sourceID, targetID := ids[relation.source], ids[relation.target]
		if sourceID == "" || targetID == "" {
			continue
		}
		rows = append(rows, graphEdgeRow{
			ID:              uuid.NewString(),
			KnowledgeBaseID: namespace.KnowledgeBase,
			KnowledgeID:     namespace.Knowledge,
			SourceID:        sourceID,
			TargetID:        targetID,
			Type:            relation.typ,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 500).Error
}

// DelGraph removes every node, chunk link and edge in the given namespaces. A
// namespace without a document id clears the whole knowledge base.
func (g *GraphRepository) DelGraph(ctx context.Context, namespaces []types.NameSpace) error {
	if g == nil || g.db == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	if len(namespaces) == 0 {
		return nil
	}
	return g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, namespace := range namespaces {
			if namespace.KnowledgeBase == "" {
				continue
			}
			nodeScope := func() *gorm.DB {
				query := tx.Table("graph_nodes").Where("knowledge_base_id = ?", namespace.KnowledgeBase)
				if namespace.Knowledge != "" {
					query = query.Where("knowledge_id = ?", namespace.Knowledge)
				}
				return query.Select("id")
			}
			if err := tx.Where("node_id IN (?)", nodeScope()).Delete(&graphNodeChunkRow{}).Error; err != nil {
				return err
			}
			edges := tx.Where("knowledge_base_id = ?", namespace.KnowledgeBase)
			if namespace.Knowledge != "" {
				edges = edges.Where("knowledge_id = ?", namespace.Knowledge)
			}
			if err := edges.Delete(&graphEdgeRow{}).Error; err != nil {
				return err
			}
			nodes := tx.Where("knowledge_base_id = ?", namespace.KnowledgeBase)
			if namespace.Knowledge != "" {
				nodes = nodes.Where("knowledge_id = ?", namespace.Knowledge)
			}
			if err := nodes.Delete(&graphNodeRow{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// SearchNode is the agent retrieval path: bounded seeds, then a bounded one-hop
// expansion with stored edge direction.
func (g *GraphRepository) SearchNode(
	ctx context.Context, namespace types.NameSpace, nodes []string,
) (*types.GraphData, error) {
	if g == nil || g.db == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil, nil
	}
	graph, err := g.search(ctx, namespace, nodes, graphSearchMaxSeedNodes, graphSearchMaxRows, true, false)
	if err != nil {
		logger.Errorf(ctx, "search node failed: %v", err)
		return nil, err
	}
	if len(graph.Relation) >= graphSearchMaxRows {
		logger.Warnf(ctx,
			"graph search hit the row cap: %d nodes / %d relations returned "+
				"(seed cap %d, row cap %d) — results are truncated; "+
				"narrow the entity or raise the cap",
			len(graph.Node), len(graph.Relation), graphSearchMaxSeedNodes, graphSearchMaxRows)
	} else {
		logger.Debugf(ctx, "graph search: %d nodes / %d relations (seed cap %d, row cap %d)",
			len(graph.Node), len(graph.Relation), graphSearchMaxSeedNodes, graphSearchMaxRows)
	}
	return graph, nil
}

// SearchGraph is the public API path: it keeps relation-less seeds and reports
// both caps as truncation reasons.
func (g *GraphRepository) SearchGraph(
	ctx context.Context, namespace types.NameSpace, query string,
) (*types.GraphSearchCandidates, error) {
	if g == nil || g.db == nil {
		return nil, nil
	}
	if namespace.KnowledgeBase == "" || namespace.Knowledge != "" {
		return nil, fmt.Errorf("graph search requires exactly one knowledge base")
	}
	graph, reasons, err := g.searchGraph(ctx, namespace, query)
	if err != nil {
		return nil, err
	}
	return &types.GraphSearchCandidates{Graph: graph, TruncationReasons: reasons}, nil
}

func (g *GraphRepository) search(
	ctx context.Context,
	namespace types.NameSpace,
	terms []string,
	maxSeeds int,
	maxRows int,
	requireEdges bool,
	project bool,
) (*types.GraphData, error) {
	seeds, _, err := g.querySeeds(ctx, namespace, terms, maxSeeds, requireEdges)
	if err != nil {
		return nil, err
	}
	if len(seeds) == 0 {
		return &types.GraphData{}, nil
	}
	edges, _, err := g.queryExpansion(ctx, namespace, seeds, maxRows)
	if err != nil {
		return nil, err
	}
	return g.assembleGraph(ctx, namespace, seeds, edges, project)
}

func (g *GraphRepository) searchGraph(
	ctx context.Context, namespace types.NameSpace, query string,
) (*types.GraphData, []string, error) {
	seeds, truncatedSeeds, err := g.querySeeds(
		ctx, namespace, []string{query}, types.GraphSearchMaxSeeds, false)
	if err != nil {
		return nil, nil, err
	}
	reasons := []string{}
	if truncatedSeeds {
		reasons = append(reasons, "candidate_seeds")
	}
	if len(seeds) == 0 {
		return &types.GraphData{}, reasons, nil
	}
	edges, truncatedRows, err := g.queryExpansion(ctx, namespace, seeds, types.GraphSearchMaxRows)
	if err != nil {
		return nil, nil, err
	}
	if truncatedRows {
		reasons = append(reasons, "candidate_relations")
	}
	graph, err := g.assembleGraph(ctx, namespace, seeds, edges, true)
	if err != nil {
		return nil, nil, err
	}
	return graph, reasons, nil
}

// assembleGraph loads the seed and neighbour rows in the order the bounded
// queries produced them, so the result keeps the Neo4j adapter's seed-first
// ranking.
func (g *GraphRepository) assembleGraph(
	ctx context.Context, namespace types.NameSpace, seeds []graphNodeRow, edges []graphEdgeRow, project bool,
) (*types.GraphData, error) {
	order := make([]string, 0, len(seeds)+2*len(edges))
	for _, seed := range seeds {
		order = append(order, seed.ID)
	}
	for _, edge := range edges {
		order = append(order, edge.SourceID, edge.TargetID)
	}
	order = dedupeStrings(order)

	loaded, chunks, err := g.loadNodes(ctx, namespace, order)
	if err != nil {
		return nil, err
	}
	graph := &types.GraphData{}
	seen := make(map[string]bool, len(order))
	for _, id := range order {
		row, ok := loaded[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		node := &types.GraphNode{
			ID: row.ID, Name: row.Name, KnowledgeID: row.KnowledgeID,
			Attributes: append([]string(nil), row.Attributes...),
			Chunks:     append([]string(nil), chunks[id]...),
		}
		if project {
			projectGraphNode(node)
		}
		graph.Node = append(graph.Node, node)
	}
	for _, edge := range edges {
		if !seen[edge.SourceID] || !seen[edge.TargetID] {
			continue
		}
		graph.Relation = append(graph.Relation, &types.GraphRelation{
			Node1: loaded[edge.SourceID].Name, Node2: loaded[edge.TargetID].Name,
			Type: edge.Type, ID: edge.ID, SourceID: edge.SourceID, TargetID: edge.TargetID,
		})
	}
	return graph, nil
}

func (g *GraphRepository) querySeeds(
	ctx context.Context, namespace types.NameSpace, terms []string, maxSeeds int, requireEdges bool,
) ([]graphNodeRow, bool, error) {
	cleaned := cleanTerms(terms)
	if namespace.KnowledgeBase == "" || len(cleaned) == 0 {
		return []graphNodeRow{}, false, nil
	}
	likeClauses := make([]string, 0, len(cleaned))
	inClauses := make([]string, 0, len(cleaned))
	args := make([]any, 0, len(cleaned)*2+3)
	args = append(args, namespace.KnowledgeBase)
	if namespace.Knowledge != "" {
		args = append(args, namespace.Knowledge)
	}
	for _, term := range cleaned {
		likeClauses = append(likeClauses, "n.name LIKE ? ESCAPE '!'")
		args = append(args, "%"+escapeLike(term)+"%")
	}
	for _, term := range cleaned {
		inClauses = append(inClauses, "?")
		args = append(args, term)
	}
	documentFilter := ""
	if namespace.Knowledge != "" {
		documentFilter = " AND n.knowledge_id = ?"
	}
	exists := ""
	if requireEdges {
		exists = `
		  AND EXISTS (
		    SELECT 1 FROM graph_edges e
		    WHERE e.knowledge_base_id = n.knowledge_base_id
		      AND (e.source_id = n.id OR e.target_id = n.id)
		  )`
	}
	sql := `
		SELECT n.id, n.knowledge_base_id, n.knowledge_id, n.name, n.attributes
		FROM graph_nodes n
		WHERE n.knowledge_base_id = ?` + documentFilter + `
		  AND (` + strings.Join(likeClauses, " OR ") + `)` + exists + `
		ORDER BY CASE WHEN n.name IN (` + strings.Join(inClauses, ",") + `) THEN 0 ELSE 1 END,
		         LENGTH(n.name), n.name, n.knowledge_id, n.id
		LIMIT ?`
	args = append(args, maxSeeds+1)

	rows := make([]graphNodeRow, 0, maxSeeds+1)
	if err := g.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, false, err
	}
	truncated := false
	if len(rows) > maxSeeds {
		rows = rows[:maxSeeds]
		truncated = true
	}
	return rows, truncated, nil
}

func (g *GraphRepository) queryExpansion(
	ctx context.Context, namespace types.NameSpace, seeds []graphNodeRow, maxRows int,
) ([]graphEdgeRow, bool, error) {
	if len(seeds) == 0 {
		return []graphEdgeRow{}, false, nil
	}
	values := make([]string, 0, len(seeds))
	args := make([]any, 0, len(seeds)*2+2)
	for i, seed := range seeds {
		values = append(values, "(?, ?)")
		args = append(args, seed.ID, i)
	}
	documentFilter := ""
	if namespace.Knowledge != "" {
		documentFilter = " AND e.knowledge_id = ?"
	}
	args = append(args, namespace.KnowledgeBase)
	if namespace.Knowledge != "" {
		args = append(args, namespace.Knowledge)
	}
	args = append(args, maxRows+1)
	sql := `
		WITH seeds(id, idx) AS (VALUES ` + strings.Join(values, ",") + `)
		SELECT e.id, e.source_id, e.target_id, e.type, MIN(s.idx) AS seed_idx
		FROM graph_edges e
		JOIN seeds s ON s.id = e.source_id OR s.id = e.target_id
		WHERE e.knowledge_base_id = ?` + documentFilter + `
		GROUP BY e.id, e.source_id, e.target_id, e.type
		ORDER BY seed_idx, e.id
		LIMIT ?`
	rows := make([]graphEdgeRow, 0, maxRows+1)
	if err := g.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, false, err
	}
	truncated := false
	if len(rows) > maxRows {
		rows = rows[:maxRows]
		truncated = true
	}
	return rows, truncated, nil
}

func (g *GraphRepository) loadNodes(
	ctx context.Context, namespace types.NameSpace, ids []string,
) (map[string]graphNodeRow, map[string][]string, error) {
	if len(ids) == 0 {
		return map[string]graphNodeRow{}, map[string][]string{}, nil
	}
	rows := make([]graphNodeRow, 0, len(ids))
	if err := g.db.WithContext(ctx).
		Table("graph_nodes").
		Select("id, knowledge_base_id, knowledge_id, name, attributes").
		Where("knowledge_base_id = ? AND id IN ?", namespace.KnowledgeBase, ids).
		Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	byID := make(map[string]graphNodeRow, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	chunkRows := make([]graphNodeChunkRow, 0, len(ids))
	if err := g.db.WithContext(ctx).
		Table("graph_node_chunks").
		Select("node_id, chunk_id").
		Where("node_id IN ?", ids).
		Order("node_id, chunk_id").
		Find(&chunkRows).Error; err != nil {
		return nil, nil, err
	}
	chunks := make(map[string][]string, len(byID))
	for _, row := range chunkRows {
		chunks[row.NodeID] = append(chunks[row.NodeID], row.ChunkID)
	}
	return byID, chunks, nil
}

func projectGraphNode(node *types.GraphNode) {
	node.Name = headRunes(node.Name, types.GraphSearchMaxNameRunes+graphProjectionOvershoot)
	node.KnowledgeID = headRunes(node.KnowledgeID, 128+graphProjectionOvershoot)
	if len(node.Chunks) > types.GraphSearchMaxEvidence+graphProjectionOvershoot {
		node.Chunks = node.Chunks[:types.GraphSearchMaxEvidence+graphProjectionOvershoot]
	}
	for i, chunk := range node.Chunks {
		node.Chunks[i] = headRunes(chunk, 128+graphProjectionOvershoot)
	}
	if len(node.Attributes) > types.GraphSearchMaxAttributes+graphProjectionOvershoot {
		node.Attributes = node.Attributes[:types.GraphSearchMaxAttributes+graphProjectionOvershoot]
	}
	for i, attribute := range node.Attributes {
		node.Attributes[i] = headRunes(attribute, types.GraphSearchMaxAttributeRunes+graphProjectionOvershoot)
	}
}

func headRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func cleanTerms(terms []string) []string {
	out := make([]string, 0, len(terms))
	seen := make(map[string]bool, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" || seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, term)
	}
	return out
}

func dedupeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// escapeLike neutralises LIKE metacharacters so an entity name is matched
// literally. The ESCAPE '!' clause avoids backslash handling differences
// between PostgreSQL and the SQLite test harness.
var likeEscaper = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")

func escapeLike(value string) string {
	return likeEscaper.Replace(value)
}
