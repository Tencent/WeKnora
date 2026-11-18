package neo4j

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// Neo4jRepository is a repository for Neo4j
type Neo4jRepository struct {
	driver                neo4j.Driver
	nodePrefix            string
	schemaConstraintMu    sync.Mutex
	schemaConstraintReady bool
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

// NewNeo4jRepository creates a new Neo4j repository
func NewNeo4jRepository(driver neo4j.Driver) interfaces.RetrieveGraphRepository {
	return &Neo4jRepository{driver: driver, nodePrefix: "ENTITY"}
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
	if graphHasTypedEntities(graph) {
		if namespace.KnowledgeBase == "" || namespace.Knowledge == "" {
			return fmt.Errorf("typed graph writes require a knowledge base and document")
		}
		if err := n.ensureSchemaConstraint(ctx); err != nil {
			return err
		}
	}
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		// Backfill only the legacy identity discriminator in this document.
		// No extraction or historical graph reinterpretation is performed.
		legacy, err := tx.Run(ctx, `MATCH (n:`+n.quotedGraphLabel(namespace)+` {kg: $kg})
			WHERE n.entity_type IS NULL SET n.entity_type = ''`,
			map[string]interface{}{"kg": namespace.Knowledge})
		if err != nil {
			return nil, err
		}
		if _, err := legacy.Consume(ctx); err != nil {
			return nil, err
		}
		if graphHasTypedEntities(graph) {
			// Upgrade pre-constraint records in this document, without changing their identity.
			// Existing duplicates fail explicitly; they must be rebuilt, not silently discarded.
			upgrade, err := tx.Run(ctx, `MATCH (n:`+n.quotedGraphLabel(namespace)+` {kg: $kg})
				WHERE n.entity_type IS NOT NULL AND n.entity_type <> ''
				  AND NOT n:WEKNORA_SCHEMA_ENTITY
				SET n:WEKNORA_SCHEMA_ENTITY, n.schema_scope = $scope`,
				map[string]interface{}{"kg": namespace.Knowledge, "scope": namespace.KnowledgeBase})
			if err != nil {
				return nil, fmt.Errorf(
					"failed to upgrade schema entities; rebuild graphs with duplicate identities: %w", err)
			}
			if _, err := upgrade.Consume(ctx); err != nil {
				return nil, fmt.Errorf(
					"failed to upgrade schema entities; rebuild graphs with duplicate identities: %w", err)
			}
		}
		// Merge with namespace labels first so pre-label typed nodes upgrade in place.
		node_import_query := `
			UNWIND $data AS row
			CALL apoc.merge.node(row.labels, row.identity, row.props, {}) YIELD node
			CALL apoc.create.addLabels(node, row.type_labels) YIELD node AS typed_node
			SET typed_node.chunks = apoc.coll.union(typed_node.chunks, row.chunks)
			RETURN distinct 'done' AS result
		`
		nodeData := []map[string]interface{}{}
		for _, node := range graph.Node {
			props := map[string]interface{}{}
			if node.EntityType == "" {
				props["attributes"] = node.Attributes
			}
			nodeData = append(nodeData, map[string]interface{}{
				"identity":    graphWriteIdentity(node.Name, node.EntityType, namespace),
				"props":       props,
				"chunks":      node.Chunks,
				"labels":      n.graphWriteLabels(namespace, node.EntityType),
				"type_labels": graphTypeLabels(node.EntityType),
			})
		}
		// Keep driver errors recognizable by ExecuteWrite's bounded retry.
		// Consume also catches failures reported while pulling the result.
		result, err := tx.Run(ctx, node_import_query, map[string]interface{}{"data": nodeData})
		if err != nil {
			return nil, fmt.Errorf("failed to create nodes: %w", err)
		}
		if _, err = result.Consume(ctx); err != nil {
			return nil, fmt.Errorf("failed to create nodes: %w", err)
		}

		// Relationship import query
		rel_import_query := `
			UNWIND $data AS row
			CALL apoc.merge.node(row.source_labels, row.source_identity, {}, {}) YIELD node as source
			CALL apoc.merge.node(row.target_labels, row.target_identity, {}, {}) YIELD node as target
			CALL apoc.create.addLabels(source, row.source_type_labels) YIELD node AS typed_source
			CALL apoc.create.addLabels(target, row.target_type_labels) YIELD node AS typed_target
			CALL apoc.merge.relationship(typed_source, row.type, {}, {}, typed_target) YIELD rel
			RETURN distinct 'done'
		`
		relData := []map[string]interface{}{}
		for _, rel := range graph.Relation {
			relData = append(relData, map[string]interface{}{
				"source_identity":    graphWriteIdentity(rel.Node1, rel.SourceType, namespace),
				"target_identity":    graphWriteIdentity(rel.Node2, rel.TargetType, namespace),
				"type":               rel.Type,
				"source_labels":      n.graphWriteLabels(namespace, rel.SourceType),
				"target_labels":      n.graphWriteLabels(namespace, rel.TargetType),
				"source_type_labels": graphTypeLabels(rel.SourceType),
				"target_type_labels": graphTypeLabels(rel.TargetType),
			})
		}
		result, err = tx.Run(ctx, rel_import_query, map[string]interface{}{"data": relData})
		if err != nil {
			return nil, fmt.Errorf("failed to create relationships: %w", err)
		}
		if _, err = result.Consume(ctx); err != nil {
			return nil, fmt.Errorf("failed to create relationships: %w", err)
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
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	result, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		for _, namespace := range namespaces {
			labelExpr := n.Label(namespace)

			deleteRelsQuery := `
				CALL apoc.periodic.iterate(
					"MATCH (n:` + labelExpr + ` {kg: $knowledge_id})-[r]-(m:` + labelExpr + ` {kg: $knowledge_id}) RETURN r",
					"DELETE r",
					{batchSize: 1000, parallel: true, params: {knowledge_id: $knowledge_id}}
				) YIELD failedBatches, failedOperations, wasTerminated
				RETURN failedBatches, failedOperations, wasTerminated
        	`
			params := map[string]any{"knowledge_id": namespace.Knowledge}
			if err := runGraphDeleteBatches(ctx, tx, deleteRelsQuery, params); err != nil {
				return nil, fmt.Errorf("failed to delete relationships: %w", err)
			}

			deleteNodesQuery := `
				CALL apoc.periodic.iterate(
					"MATCH (n:` + labelExpr + ` {kg: $knowledge_id}) RETURN n",
					"DELETE n",
					{batchSize: 1000, parallel: true, params: {knowledge_id: $knowledge_id}}
				) YIELD failedBatches, failedOperations, wasTerminated
				RETURN failedBatches, failedOperations, wasTerminated
        	`
			if err := runGraphDeleteBatches(ctx, tx, deleteNodesQuery, params); err != nil {
				return nil, fmt.Errorf("failed to delete nodes: %w", err)
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

func runGraphDeleteBatches(
	ctx context.Context, tx neo4j.ManagedTransaction, query string, params map[string]any,
) error {
	result, err := tx.Run(ctx, query, params)
	if err != nil {
		return err
	}
	// APOC can return a result row even when one of its inner batches failed.
	record, err := result.Single(ctx)
	if err != nil {
		return err
	}
	failedBatches, _ := record.Get("failedBatches")
	failedOperations, _ := record.Get("failedOperations")
	terminated, _ := record.Get("wasTerminated")
	if failedBatches != int64(0) || failedOperations != int64(0) || terminated != false {
		return fmt.Errorf("incomplete APOC deletion (failed batches: %v, failed operations: %v, terminated: %v)",
			failedBatches, failedOperations, terminated)
	}
	return nil
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
	query, params := graphSearchCypher(n.Label(namespace), nodes)
	return n.search(ctx, query, params)
}

func graphNodeIdentity(name, entityType, knowledgeID string) map[string]interface{} {
	return map[string]interface{}{"name": name, "kg": knowledgeID, "entity_type": entityType}
}

const schemaEntityLabel = "WEKNORA_SCHEMA_ENTITY"

const schemaEntityConstraintQuery = `CREATE CONSTRAINT weknora_schema_entity_identity IF NOT EXISTS
	FOR (n:WEKNORA_SCHEMA_ENTITY)
	REQUIRE (n.schema_scope, n.kg, n.name, n.entity_type) IS UNIQUE`

func (n *Neo4jRepository) ensureSchemaConstraint(ctx context.Context) error {
	n.schemaConstraintMu.Lock()
	defer n.schemaConstraintMu.Unlock()
	if n.schemaConstraintReady {
		return nil
	}
	// Schema DDL must run in a separate transaction from entity writes.
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer func() { _ = session.Close(ctx) }()
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		result, err := tx.Run(ctx, schemaEntityConstraintQuery, nil)
		if err != nil {
			return nil, err
		}
		_, err = result.Consume(ctx)
		return nil, err
	})
	if err != nil {
		return fmt.Errorf("failed to ensure schema entity uniqueness: %w", err)
	}
	n.schemaConstraintReady = true
	return nil
}

func graphHasTypedEntities(graph *types.GraphData) bool {
	for _, node := range graph.Node {
		if node.EntityType != "" {
			return true
		}
	}
	for _, rel := range graph.Relation {
		if rel.SourceType != "" || rel.TargetType != "" {
			return true
		}
	}
	return false
}

func graphWriteIdentity(name, entityType string, namespace types.NameSpace) map[string]interface{} {
	identity := graphNodeIdentity(name, entityType, namespace.Knowledge)
	if entityType != "" {
		identity["schema_scope"] = namespace.KnowledgeBase
	}
	return identity
}

func (n *Neo4jRepository) graphWriteLabels(namespace types.NameSpace, entityType string) []string {
	labels := n.Labels(namespace)
	if entityType != "" {
		labels = append(labels, schemaEntityLabel)
	}
	return labels
}

const graphTypeLabelPrefix = "TYPE_"

func graphTypeLabel(entityType string) string {
	if entityType == "" {
		return ""
	}
	return graphTypeLabelPrefix + entityType
}

func graphTypeLabels(entityType string) []string {
	if entityType == "" {
		return []string{}
	}
	return []string{graphTypeLabel(entityType)}
}

// The property remains authoritative; pre-label schema records are readable
// until they are touched by a write. A mismatched type label alone cannot qualify.
func graphTypePredicate(node, entityType, label string) string {
	return fmt.Sprintf(`(%s = '' OR (%s.entity_type = %s AND
		(%s IN labels(%s) OR NONE(typeLabel IN labels(%s) WHERE typeLabel STARTS WITH '%s'))))`,
		entityType, node, entityType, label, node, node, graphTypeLabelPrefix)
}

// SearchNodeWithSchema searches bounded one-hop relations using optional schema hints.
func (n *Neo4jRepository) SearchNodeWithSchema(
	ctx context.Context, namespace types.NameSpace, queries []types.GraphQuery,
) (*types.GraphData, error) {
	if namespace.KnowledgeBase == "" {
		return nil, fmt.Errorf("schema graph search requires a knowledge base")
	}
	query, params := schemaGraphSearchCypher(n.quotedGraphLabel(namespace), queries)
	return n.search(ctx, query, params)
}

func (n *Neo4jRepository) search(
	ctx context.Context, query string, params map[string]interface{},
) (*types.GraphData, error) {
	if n.driver == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil, nil
	}
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
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
				SourceType: nodeSeen[source.ElementId].EntityType, TargetType: nodeSeen[target.ElementId].EntityType,
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
	entityType, _ := node.Props["entity_type"].(string)
	return &types.GraphNode{
		Name: name, Chunks: listI2listS(chunks), Attributes: listI2listS(attributes),
		ID: node.ElementId, KnowledgeID: knowledgeID, EntityType: entityType,
	}, nil
}

// Schema hints are parameters, never interpolated labels or executable Cypher.
// Deduplication happens after directional filtering so another seed cannot hide
// an edge that is valid only from this seed's perspective.
func schemaGraphSearchCypher(labelExpr string, queries []types.GraphQuery) (string, map[string]interface{}) {
	seeds := make([]map[string]interface{}, 0, len(queries))
	seenQueries := make(map[types.GraphQuery]bool, len(queries))
	for _, q := range queries {
		if seenQueries[q] {
			continue
		}
		seenQueries[q] = true
		seeds = append(seeds, map[string]interface{}{
			"name": q.Name, "entity_type": q.EntityType, "relation_type": q.RelationType,
			"direction": q.Direction, "target_type": q.TargetType,
			"entity_label": graphTypeLabel(q.EntityType), "target_label": graphTypeLabel(q.TargetType),
		})
	}
	query := `
		UNWIND $queries AS q
		MATCH (n:` + labelExpr + `)
		WHERE n.name CONTAINS q.name
		  AND ` + graphTypePredicate("n", "q.entity_type", "q.entity_label") + `
		  AND EXISTS {
		    MATCH (n)-[candidate]-(neighbor:` + labelExpr + `)
		    WHERE (q.relation_type = '' OR type(candidate) = q.relation_type)
		      AND ` + graphTypePredicate("neighbor", "q.target_type", "q.target_label") + `
		      AND (q.direction = '' OR
		        (q.direction = 'out' AND startNode(candidate) = n) OR
		        (q.direction = 'in' AND endNode(candidate) = n))
		  }
		WITH n, q, CASE WHEN n.name = q.name THEN 0 ELSE 1 END AS seed_rank
		ORDER BY seed_rank, size(n.name), n.name, n.kg, elementId(n),
		  q.entity_type, q.relation_type, q.direction, q.target_type
		LIMIT $maxSeedNodes
		WITH collect({node: n, query: q}) AS seeds
		UNWIND range(0, size(seeds) - 1) AS seed_index
		WITH seeds[seed_index].node AS n, seeds[seed_index].query AS q,
		  seeds[0..seed_index] AS earlier_seeds, seed_index
		MATCH (n)-[r]-(m:` + labelExpr + `)
		WHERE (q.relation_type = '' OR type(r) = q.relation_type)
		  AND ` + graphTypePredicate("m", "q.target_type", "q.target_label") + `
		  AND (q.direction = '' OR
		    (q.direction = 'out' AND startNode(r) = n) OR
		    (q.direction = 'in' AND endNode(r) = n))
		  AND NOT ANY(previous IN earlier_seeds WHERE previous.node IN [n, m]
		    AND (previous.query.relation_type = '' OR type(r) = previous.query.relation_type)
		    AND ((previous.node = n AND ` +
		graphTypePredicate("m", "previous.query.target_type", "previous.query.target_label") + `) OR
		      (previous.node = m AND ` +
		graphTypePredicate("n", "previous.query.target_type", "previous.query.target_label") + `))
		    AND (previous.query.direction = '' OR
		      (previous.query.direction = 'out' AND startNode(r) = previous.node) OR
		      (previous.query.direction = 'in' AND endNode(r) = previous.node)))
		WITH n, r, m, seed_index
		ORDER BY seed_index, elementId(r)
		LIMIT $maxRows
		RETURN n, r, m`
	return query, map[string]interface{}{
		"queries": seeds, "maxSeedNodes": graphSearchMaxSeedNodes, "maxRows": graphSearchMaxRows,
	}
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
func graphSearchCypher(labelExpr string, nodes []string) (string, map[string]interface{}) {
	query := `
		MATCH (n:` + labelExpr + `)
		WHERE ANY(nodeText IN $nodes WHERE n.name CONTAINS nodeText)
		  AND EXISTS { (n)--() }
		WITH n,
		     CASE WHEN n.name IN $nodes THEN 0 ELSE 1 END AS seed_rank,
		     size(n.name) AS name_len,
		     n.name AS name
		ORDER BY seed_rank, name_len, name, n.kg, elementId(n)
		LIMIT $maxSeedNodes
		WITH collect(n) AS seeds
		UNWIND range(0, size(seeds) - 1) AS seed_index
		WITH seeds[seed_index] AS n, seeds[0..seed_index] AS earlier_seeds, seed_index
		MATCH (n)-[r]-(m:` + labelExpr + `)
		WHERE NOT m IN earlier_seeds
		WITH n, r, m, seed_index
		ORDER BY seed_index, elementId(r)
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
