package neo4j

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// Neo4jRepository is a repository for Neo4j
type Neo4jRepository struct {
	driver     neo4j.Driver
	nodePrefix string
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
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		// Node import query
		nodeImportQuery := `
			UNWIND $data AS row
			MERGE (node:` + n.Label(namespace) + ` {name: row.name, kg: row.knowledge_id})
			ON CREATE SET node.attributes = row.attributes
			SET node.chunks = CASE
				WHEN node.chunks IS NULL THEN row.chunks
				ELSE node.chunks + [chunk IN row.chunks WHERE NOT chunk IN node.chunks]
			END
			RETURN distinct 'done' AS result
		`
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
			if _, err := tx.Run(ctx, nodeImportQuery, map[string]interface{}{"data": nodeData}); err != nil {
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
			query, err := relationshipImportQuery(n.Label(namespace), relType)
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
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	result, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		for _, namespace := range namespaces {
			labelExpr := n.Label(namespace)

			deleteRelsQuery, deleteNodesQuery := graphDeleteQueries(labelExpr)
			if _, err := tx.Run(ctx, deleteRelsQuery, map[string]interface{}{"knowledge_id": namespace.Knowledge}); err != nil {
				return nil, fmt.Errorf("failed to delete relationships: %v", err)
			}

			if _, err := tx.Run(ctx, deleteNodesQuery, map[string]interface{}{"knowledge_id": namespace.Knowledge}); err != nil {
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

func relationshipImportQuery(labelExpr, relType string) (string, error) {
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

func graphDeleteQueries(labelExpr string) (string, string) {
	return `MATCH (n:` + labelExpr + ` {kg: $knowledge_id})-[r]-(m:` + labelExpr + ` {kg: $knowledge_id}) DELETE r`,
		`MATCH (n:` + labelExpr + ` {kg: $knowledge_id}) DETACH DELETE n`
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
		query, params := graphSearchCypher(n.Label(namespace), nodes)
		result, err := tx.Run(ctx, query, params)
		if err != nil {
			return nil, fmt.Errorf("failed to run query: %v", err)
		}

		graphData := &types.GraphData{}
		nodeSeen := make(map[string]bool)
		for result.Next(ctx) {
			record := result.Record()
			node, _ := record.Get("n")
			rel, _ := record.Get("r")
			targetNode, _ := record.Get("m")

			nodeData := node.(neo4j.Node)
			targetNodeData := targetNode.(neo4j.Node)

			// Convert node to types.Node
			for _, n := range []neo4j.Node{nodeData, targetNodeData} {
				nameStr := n.Props["name"].(string)
				if _, ok := nodeSeen[nameStr]; !ok {
					nodeSeen[nameStr] = true
					graphData.Node = append(graphData.Node, &types.GraphNode{
						Name:       nameStr,
						Chunks:     listI2listS(n.Props["chunks"].([]interface{})),
						Attributes: listI2listS(n.Props["attributes"].([]interface{})),
					})
				}
			}

			// Convert relationship to types.Relation
			relData := rel.(neo4j.Relationship)
			graphData.Relation = append(graphData.Relation, &types.GraphRelation{
				Node1: nodeData.Props["name"].(string),
				Node2: targetNodeData.Props["name"].(string),
				Type:  relData.Type,
			})
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
//   - The same key is carried into both LIMITs, so a truncated result keeps the
//     neighbourhoods of the highest-ranked seeds rather than an arbitrary
//     subset of the rows.
func graphSearchCypher(labelExpr string, nodes []string) (string, map[string]interface{}) {
	query := `
		MATCH (n:` + labelExpr + `)
		WHERE ANY(nodeText IN $nodes WHERE n.name CONTAINS nodeText)
		  AND EXISTS { (n)--() }
		WITH n,
		     CASE WHEN n.name IN $nodes THEN 0 ELSE 1 END AS seed_rank,
		     size(n.name) AS name_len,
		     n.name AS name
		ORDER BY seed_rank, name_len, name
		LIMIT $maxSeedNodes
		MATCH (n)-[r]-(m:` + labelExpr + `)
		WITH n, r, m, seed_rank, name_len, name
		ORDER BY seed_rank, name_len, name
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
