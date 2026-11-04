package neo4j

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// GraphEngine selects which Cypher dialect the repository speaks. Neo4j ships
// APOC and Memgraph does not, so the import and deletion queries differ while
// everything else — labels, search, decoding — is shared.
type GraphEngine string

const (
	// EngineNeo4j keeps the APOC procedures existing deployments rely on.
	EngineNeo4j GraphEngine = "neo4j"
	// EngineMemgraph uses plain Cypher and batches deletions itself.
	EngineMemgraph GraphEngine = "memgraph"
)

// elementIDFunc names the Cypher function that yields a stable internal
// identity, used as the last tie breaker in the search ordering. Memgraph has
// no elementId(); its equivalent is id().
func (e GraphEngine) elementIDFunc() string {
	if e == EngineMemgraph {
		return "id"
	}
	return "elementId"
}

// Neo4jRepository is a repository for Neo4j
type Neo4jRepository struct {
	driver     neo4j.Driver
	nodePrefix string
	engine     GraphEngine
}

const (
	// graphSearchMaxSeedNodes bounds how many entities a single graph search
	// expands from. The match is a substring match, so this is the knob that
	// keeps one vague entity name from pulling in the whole graph.
	graphSearchMaxSeedNodes = 200
	// graphSearchMaxRows bounds returned (node, relation) rows as a backstop
	// for hub entities whose neighbourhood alone is huge.
	graphSearchMaxRows = 2000
)

// NewNeo4jRepository creates a new graph repository for engine. An empty engine
// means Neo4j, so an unset GRAPH_DATABASE_ENGINE behaves exactly as before.
func NewNeo4jRepository(driver neo4j.Driver, engine GraphEngine) interfaces.RetrieveGraphRepository {
	if engine == "" {
		engine = EngineNeo4j
	}
	return &Neo4jRepository{driver: driver, nodePrefix: "ENTITY", engine: engine}
}

// _remove_hyphen removes hyphens from a string
func _remove_hyphen(s string) string {
	return strings.ReplaceAll(s, "-", "_")
}

// Labels returns the labels for a namespace
func (n *Neo4jRepository) Labels(namespace types.NameSpace) []string {
	res := make([]string, 0)
	for _, label := range namespace.Labels() {
		res = append(res, n.nodePrefix+_remove_hyphen(label))
	}
	return res
}

// Label returns the label for a namespace
func (n *Neo4jRepository) Label(namespace types.NameSpace) string {
	labels := n.Labels(namespace)
	return strings.Join(labels, ":")
}

// AddGraph adds a graph to the Neo4j repository
func (n *Neo4jRepository) AddGraph(ctx context.Context, namespace types.NameSpace, graphs []*types.GraphData) error {
	if n.driver == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	for _, graph := range graphs {
		if err := n.addGraph(ctx, namespace, graph); err != nil {
			return err
		}
	}
	return nil
}

// addGraph adds a graph to the Neo4j repository
func (n *Neo4jRepository) addGraph(ctx context.Context, namespace types.NameSpace, graph *types.GraphData) error {
	if n.engine == EngineMemgraph {
		return n.addGraphMemgraph(ctx, namespace, graph)
	}
	return n.addGraphNeo4j(ctx, namespace, graph)
}

// addGraphNeo4j imports a graph through the APOC procedures Neo4j ships with.
// Existing deployments run this path, so the queries stay as they were before
// Memgraph was an option.
func (n *Neo4jRepository) addGraphNeo4j(
	ctx context.Context, namespace types.NameSpace, graph *types.GraphData,
) error {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		nodeData := []map[string]interface{}{}
		for _, node := range graph.Node {
			nodeData = append(nodeData, map[string]interface{}{
				"name":         node.Name,
				"knowledge_id": namespace.Knowledge,
				"props":        map[string][]string{"attributes": node.Attributes},
				"chunks":       node.Chunks,
				"labels":       n.Labels(namespace),
			})
		}
		if _, err := tx.Run(ctx, neo4jNodeImportQuery, map[string]interface{}{"data": nodeData}); err != nil {
			return nil, fmt.Errorf("failed to create nodes: %v", err)
		}

		relData := []map[string]interface{}{}
		for _, rel := range graph.Relation {
			relData = append(relData, map[string]interface{}{
				"source":        rel.Node1,
				"target":        rel.Node2,
				"knowledge_id":  namespace.Knowledge,
				"type":          rel.Type,
				"source_labels": n.Labels(namespace),
				"target_labels": n.Labels(namespace),
			})
		}
		if _, err := tx.Run(ctx, neo4jRelationshipImportQuery, map[string]interface{}{"data": relData}); err != nil {
			return nil, fmt.Errorf("failed to create relationships: %v", err)
		}
		return nil, nil
	})
	if err != nil {
		logger.Errorf(ctx, "failed to add graph: %v", err)
		return err
	}
	return nil
}

// addGraphMemgraph imports a graph with plain Cypher. Memgraph has no APOC, so
// nodes are merged with MERGE and relationships are grouped by type: the
// relationship type is part of the pattern and cannot be parameterised, so it
// is escaped as a Cypher identifier instead.
func (n *Neo4jRepository) addGraphMemgraph(
	ctx context.Context, namespace types.NameSpace, graph *types.GraphData,
) error {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer func() { _ = session.Close(ctx) }()

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		nodeData := []map[string]interface{}{}
		for _, node := range graph.Node {
			nodeData = append(nodeData, map[string]interface{}{
				"name":         node.Name,
				"knowledge_id": namespace.Knowledge,
				"attributes":   node.Attributes,
				"chunks":       node.Chunks,
			})
		}
		if len(nodeData) > 0 {
			query := memgraphNodeImportQuery(n.Label(namespace))
			if _, err := tx.Run(ctx, query, map[string]interface{}{"data": nodeData}); err != nil {
				return nil, fmt.Errorf("failed to create nodes: %v", err)
			}
		}

		relationshipsByType := make(map[string][]map[string]interface{})
		for _, rel := range graph.Relation {
			relationshipsByType[rel.Type] = append(relationshipsByType[rel.Type], map[string]interface{}{
				"source":       rel.Node1,
				"target":       rel.Node2,
				"knowledge_id": namespace.Knowledge,
			})
		}
		for relType, relData := range relationshipsByType {
			query, err := memgraphRelationshipImportQuery(n.Label(namespace), relType)
			if err != nil {
				return nil, err
			}
			if _, err := tx.Run(ctx, query, map[string]interface{}{"data": relData}); err != nil {
				return nil, fmt.Errorf("failed to create relationships: %v", err)
			}
		}
		return nil, nil
	})
	if err != nil {
		logger.Errorf(ctx, "failed to add graph: %v", err)
		return err
	}
	return nil
}

// DelGraph deletes a graph from the Neo4j repository
func (n *Neo4jRepository) DelGraph(ctx context.Context, namespaces []types.NameSpace) error {
	if n.driver == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	if n.engine == EngineMemgraph {
		return n.delGraphMemgraph(ctx, namespaces)
	}
	return n.delGraphNeo4j(ctx, namespaces)
}

// delGraphNeo4j deletes through apoc.periodic.iterate, which does the batching
// on the server. Unchanged from before Memgraph support.
func (n *Neo4jRepository) delGraphNeo4j(ctx context.Context, namespaces []types.NameSpace) error {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	result, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		for _, namespace := range namespaces {
			deleteRelsQuery, deleteNodesQuery := neo4jDeleteQueries(n.Label(namespace))
			if _, err := tx.Run(ctx, deleteRelsQuery,
				map[string]interface{}{"knowledge_id": namespace.Knowledge}); err != nil {
				return nil, fmt.Errorf("failed to delete relationships: %v", err)
			}

			if _, err := tx.Run(ctx, deleteNodesQuery,
				map[string]interface{}{"knowledge_id": namespace.Knowledge}); err != nil {
				return nil, fmt.Errorf("failed to delete nodes: %v", err)
			}
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	logger.Infof(ctx, "delete graph result: %v", result)
	return nil
}

// delGraphMemgraph deletes in bounded batches, each committed on its own.
// Memgraph has no apoc.periodic.iterate, and a single DETACH DELETE over a
// whole knowledge base holds every deleted node and relationship in the
// transaction until it commits.
func (n *Neo4jRepository) delGraphMemgraph(ctx context.Context, namespaces []types.NameSpace) error {
	for _, namespace := range namespaces {
		relsQuery, nodesQuery := memgraphDeleteQueries(n.Label(namespace), memgraphDeleteBatchSize)
		for _, step := range []struct {
			what  string
			query string
		}{
			{what: "relationships", query: relsQuery},
			{what: "nodes", query: nodesQuery},
		} {
			deleted, err := n.deleteInBatches(ctx, step.query, namespace.Knowledge)
			if err != nil {
				return fmt.Errorf("failed to delete %s: %w", step.what, err)
			}
			logger.Infof(ctx, "deleted %d %s for knowledge %s", deleted, step.what, namespace.Knowledge)
		}
	}
	return nil
}

// deleteInBatches runs one bounded batch per transaction until a batch deletes
// nothing, and reports how many rows were removed in total.
func (n *Neo4jRepository) deleteInBatches(ctx context.Context, query, knowledgeID string) (int64, error) {
	var total int64
	for {
		deleted, err := n.deleteOneBatch(ctx, query, knowledgeID)
		if err != nil {
			return total, err
		}
		if deleted == 0 {
			return total, nil
		}
		total += deleted
		// Per batch, so the batch boundaries are observable: a run that only
		// ever logs one batch is not batching.
		logger.Debugf(ctx, "deleted a batch of %d (%d so far, cap %d)", deleted, total, memgraphDeleteBatchSize)
	}
}

// deleteOneBatch commits a single batch and returns the number of rows it
// deleted. A fresh session per batch keeps each commit independent, so an
// interrupted deletion leaves the batches it already committed deleted.
func (n *Neo4jRepository) deleteOneBatch(ctx context.Context, query, knowledgeID string) (int64, error) {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer func() { _ = session.Close(ctx) }()

	deleted, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		result, err := tx.Run(ctx, query, map[string]interface{}{"knowledge_id": knowledgeID})
		if err != nil {
			return nil, err
		}
		record, err := result.Single(ctx)
		if err != nil {
			return nil, err
		}
		count, _, err := neo4j.GetRecordValue[int64](record, "deleted")
		if err != nil {
			return nil, fmt.Errorf("delete batch returned no count: %w", err)
		}
		return count, nil
	})
	if err != nil {
		return 0, err
	}
	return deleted.(int64), nil
}

// Neo4j import queries. APOC merges nodes by a label list, so one query covers
// every namespace; the text is unchanged from before Memgraph support.
const (
	neo4jNodeImportQuery = `
		UNWIND $data AS row
		CALL apoc.merge.node(row.labels, {name: row.name, kg: row.knowledge_id}, row.props, {}) YIELD node
		SET node.chunks = apoc.coll.union(node.chunks, row.chunks)
		RETURN distinct 'done' AS result
	`
	neo4jRelationshipImportQuery = `
		UNWIND $data AS row
		CALL apoc.merge.node(row.source_labels, {name: row.source, kg: row.knowledge_id}, {}, {}) YIELD node as source
		CALL apoc.merge.node(row.target_labels, {name: row.target, kg: row.knowledge_id}, {}, {}) YIELD node as target
		CALL apoc.merge.relationship(source, row.type, {}, row.attributes, target) YIELD rel
		RETURN distinct 'done'
	`
)

// neo4jDeleteQueries returns the APOC batch deletions for one label
// expression: relationships first, then the nodes.
func neo4jDeleteQueries(labelExpr string) (string, string) {
	return `
		CALL apoc.periodic.iterate(
			"MATCH (n:` + labelExpr + ` {kg: $knowledge_id})-[r]-(m:` + labelExpr + ` {kg: $knowledge_id}) RETURN r",
			"DELETE r",
			{batchSize: 1000, parallel: true, params: {knowledge_id: $knowledge_id}}
		) YIELD batches, total
		RETURN total
	`, `
		CALL apoc.periodic.iterate(
			"MATCH (n:` + labelExpr + ` {kg: $knowledge_id}) RETURN n",
			"DELETE n",
			{batchSize: 1000, parallel: true, params: {knowledge_id: $knowledge_id}}
		) YIELD batches, total
		RETURN total
	`
}

// memgraphDeleteBatchSize bounds one delete transaction, mirroring the
// batchSize apoc.periodic.iterate uses on the Neo4j path.
const memgraphDeleteBatchSize = 1000

// memgraphNodeImportQuery merges nodes without APOC. The label expression is
// interpolated because labels are part of the pattern, and chunks are unioned
// in Cypher because apoc.coll.union is not available.
func memgraphNodeImportQuery(labelExpr string) string {
	return `
		UNWIND $data AS row
		MERGE (node:` + labelExpr + ` {name: row.name, kg: row.knowledge_id})
		ON CREATE SET node.attributes = row.attributes
		SET node.chunks = CASE
			WHEN node.chunks IS NULL THEN row.chunks
			ELSE node.chunks + [chunk IN row.chunks WHERE NOT chunk IN node.chunks]
		END
		RETURN distinct 'done' AS result
	`
}

// memgraphRelationshipImportQuery merges one relationship type without APOC.
// The type is escaped as an identifier: it reaches the pattern as text, so a
// backtick in it would otherwise end the identifier.
func memgraphRelationshipImportQuery(labelExpr, relType string) (string, error) {
	if strings.TrimSpace(relType) == "" || strings.IndexFunc(relType, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("invalid relationship type %q", relType)
	}
	return `
		UNWIND $data AS row
		MERGE (source:` + labelExpr + ` {name: row.source, kg: row.knowledge_id})
		ON CREATE SET source.attributes = [], source.chunks = []
		MERGE (target:` + labelExpr + ` {name: row.target, kg: row.knowledge_id})
		ON CREATE SET target.attributes = [], target.chunks = []
		MERGE (source)-[rel:` + quoteCypherIdentifier(relType) + `]->(target)
		RETURN distinct 'done'
	`, nil
}

// memgraphDeleteQueries returns one bounded relationship batch and one bounded
// node batch, each reporting how many rows it deleted so the caller can stop
// when a batch comes back empty. The batch size is interpolated rather than
// passed as a parameter because LIMIT does not accept one in every Cypher
// dialect; it is an internal constant, never caller input.
func memgraphDeleteQueries(labelExpr string, batchSize int) (string, string) {
	limit := strconv.Itoa(batchSize)
	return `
		MATCH (n:` + labelExpr + ` {kg: $knowledge_id})-[r]-(m:` + labelExpr + ` {kg: $knowledge_id})
		WITH DISTINCT r LIMIT ` + limit + `
		DELETE r
		RETURN count(*) AS deleted
	`, `
		MATCH (n:` + labelExpr + ` {kg: $knowledge_id})
		WITH n LIMIT ` + limit + `
		DETACH DELETE n
		RETURN count(*) AS deleted
	`
}

func quoteCypherIdentifier(identifier string) string {
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}

// SearchNode searches for nodes in the Neo4j repository.
//
// The result set is bounded twice; graphSearchCypher owns the query, its
// ordering and the parameters, so both caps can be pinned by a unit test
// without a live database.
func (n *Neo4jRepository) SearchNode(
	ctx context.Context,
	namespace types.NameSpace,
	nodes []string,
) (*types.GraphData, error) {
	if n.driver == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil, nil
	}
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		query, params := graphSearchCypher(n.engine, n.Label(namespace), nodes)
		result, err := tx.Run(ctx, query, params)
		if err != nil {
			return nil, fmt.Errorf("failed to run query: %v", err)
		}

		graphData, err := decodeGraphSearchResult(ctx, result)
		if err != nil {
			return nil, err
		}
		// Make truncation visible. A silent cap reads as "the graph has no more
		// matches" when in fact candidates were dropped.
		if len(graphData.Relation) >= graphSearchMaxRows {
			logger.Warnf(ctx,
				"graph search hit the row cap: %d nodes / %d relations returned "+
					"(seed cap %d, row cap %d) — results are truncated; "+
					"narrow the entity or raise graphSearchMaxRows",
				len(graphData.Node), len(graphData.Relation),
				graphSearchMaxSeedNodes, graphSearchMaxRows)
		} else {
			// Debug, not Info: this runs on the retrieval path for every query.
			logger.Debugf(ctx, "graph search: %d nodes / %d relations (seed cap %d, row cap %d)",
				len(graphData.Node), len(graphData.Relation),
				graphSearchMaxSeedNodes, graphSearchMaxRows)
		}
		return graphData, nil
	})
	if err != nil {
		logger.Errorf(ctx, "search node failed: %v", err)
		return nil, err
	}
	return result.(*types.GraphData), nil
}

// decodeGraphSearchResult keeps document instances distinct and restores the
// stored direction even when the seed is the target of an undirected match.
func decodeGraphSearchResult(ctx context.Context, result neo4j.Result) (*types.GraphData, error) {
	graph := &types.GraphData{}
	nodeSeen := make(map[string]*types.GraphNode)
	relationSeen := make(map[string]bool)
	for result.Next(ctx) {
		record := result.Record()
		if record == nil {
			return nil, fmt.Errorf("graph query returned a nil record")
		}
		n, _, err := neo4j.GetRecordValue[neo4j.Node](record, "n")
		if err != nil {
			return nil, fmt.Errorf("failed to decode graph seed: %w", err)
		}
		m, _, err := neo4j.GetRecordValue[neo4j.Node](record, "m")
		if err != nil {
			return nil, fmt.Errorf("failed to decode graph neighbour: %w", err)
		}
		r, _, err := neo4j.GetRecordValue[neo4j.Relationship](record, "r")
		if err != nil {
			return nil, fmt.Errorf("failed to decode graph relationship: %w", err)
		}
		// Keep the matched seed first for evidence ranking, independently of direction.
		for _, node := range []neo4j.Node{n, m} {
			if nodeSeen[node.ElementId] != nil {
				continue
			}
			decoded, err := decodeGraphNode(node)
			if err != nil {
				return nil, err
			}
			nodeSeen[node.ElementId] = decoded
			graph.Node = append(graph.Node, decoded)
		}
		source, target := n, m
		if r.StartElementId == m.ElementId && r.EndElementId == n.ElementId {
			source, target = m, n
		} else if r.StartElementId != n.ElementId || r.EndElementId != m.ElementId {
			return nil, fmt.Errorf("graph relationship %q does not connect its returned endpoints", r.ElementId)
		}
		if r.ElementId == "" {
			return nil, fmt.Errorf("graph relationship has no element identity")
		}
		if !relationSeen[r.ElementId] {
			relationSeen[r.ElementId] = true
			graph.Relation = append(graph.Relation, &types.GraphRelation{
				Node1: nodeSeen[source.ElementId].Name, Node2: nodeSeen[target.ElementId].Name, Type: r.Type,
				ID: r.ElementId, SourceID: source.ElementId, TargetID: target.ElementId,
			})
		}
	}
	if err := result.Err(); err != nil {
		return nil, fmt.Errorf("failed to read graph query results: %w", err)
	}
	return graph, nil
}

func decodeGraphNode(node neo4j.Node) (*types.GraphNode, error) {
	name, ok := node.Props["name"].(string)
	if node.ElementId == "" || !ok || name == "" {
		return nil, fmt.Errorf("graph node %q has no identity or name", node.ElementId)
	}
	chunks, ok := node.Props["chunks"].([]any)
	if !ok && node.Props["chunks"] != nil {
		return nil, fmt.Errorf("graph node %q has invalid chunks", node.ElementId)
	}
	attributes, ok := node.Props["attributes"].([]any)
	if !ok && node.Props["attributes"] != nil {
		return nil, fmt.Errorf("graph node %q has invalid attributes", node.ElementId)
	}
	knowledgeID, _ := node.Props["kg"].(string)
	return &types.GraphNode{
		Name: name, Chunks: listI2listS(chunks), Attributes: listI2listS(attributes),
		ID: node.ElementId, KnowledgeID: knowledgeID,
	}, nil
}

// graphSearchCypher builds the bounded graph-search query and its parameters
// for one label expression and one set of entity names.
//
// The WHERE clause is a substring match, so a short entity name (or one that is
// a component of many others) matches a large slice of the graph: without a
// bound this returned 1309 nodes / 1917 relations for a single query, which
// then exceeded the reranker's per-request limit and made chunk_merge walk the
// entire set. Three rules keep the caps from deciding the result by accident:
//
//   - Only entities that have at least one relationship can seed an expansion —
//     a relation-less match occupies one of the seed slots and then contributes
//     nothing.
//   - Exact name matches rank first, then the shortest names, then alphabetical
//     order. Ordering by name alone lets an entity whose name *is* the query
//     fall outside the cap once the substring matches exceed it.
//   - The seed order is carried into the row LIMIT, so a truncated result keeps the
//     neighbourhoods of the highest-ranked seeds rather than an arbitrary
//     subset of the rows.
//
// Each edge is expanded only from its earliest seed, so duplicate endpoint
// matches cannot consume the row cap. Only the bounded seed list is collected;
// grouping every candidate relationship would require unbounded working memory.
func graphSearchCypher(
	engine GraphEngine, labelExpr string, nodes []string,
) (string, map[string]interface{}) {
	elementID := engine.elementIDFunc()
	query := `
		MATCH (n:` + labelExpr + `)
		WHERE ANY(nodeText IN $nodes WHERE n.name CONTAINS nodeText)
		  AND EXISTS { (n)--() }
		WITH n,
		     CASE WHEN n.name IN $nodes THEN 0 ELSE 1 END AS seed_rank,
		     size(n.name) AS name_len,
		     n.name AS name
		ORDER BY seed_rank, name_len, name, n.kg, ` + elementID + `(n)
		LIMIT $maxSeedNodes
		WITH collect(n) AS seeds
		UNWIND range(0, size(seeds) - 1) AS seed_index
		WITH seeds[seed_index] AS n, seeds[0..seed_index] AS earlier_seeds, seed_index
		MATCH (n)-[r]-(m:` + labelExpr + `)
		WHERE NOT m IN earlier_seeds
		WITH n, r, m, seed_index
		ORDER BY seed_index, ` + elementID + `(r)
		LIMIT $maxRows
		RETURN n, r, m
	`
	params := map[string]interface{}{
		"nodes":        nodes,
		"maxSeedNodes": graphSearchMaxSeedNodes,
		"maxRows":      graphSearchMaxRows,
	}
	return query, params
}

func listI2listS(list []any) []string {
	result := make([]string, len(list))
	for i, v := range list {
		result[i] = fmt.Sprintf("%v", v)
	}
	return result
}
