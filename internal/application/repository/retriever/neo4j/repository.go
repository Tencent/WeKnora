package neo4j

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	driver                      neo4j.Driver
	nodePrefix                  string
	contributionConstraintMu    sync.Mutex
	contributionConstraintReady bool
}

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

// ReplaceGraphContribution atomically replaces the graph facts owned by one
// stable chunk. A marker node fences attempts inside Neo4j so an older worker
// cannot publish after a newer attempt has claimed the same contribution.
func (n *Neo4jRepository) ReplaceGraphContribution(
	ctx context.Context,
	namespace types.NameSpace,
	chunkID string,
	attempt int,
	graph *types.GraphData,
) (bool, error) {
	if n.driver == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return false, nil
	}
	if chunkID == "" || graph == nil {
		return false, fmt.Errorf("graph contribution chunk and graph must not be empty")
	}
	if err := n.ensureContributionConstraint(ctx); err != nil {
		return false, err
	}
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	result, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		applied, err := claimGraphContributionAttempt(ctx, tx, namespace, chunkID, attempt)
		if err != nil || !applied {
			return applied, err
		}
		if err := n.deleteGraphContributionsTx(ctx, tx, namespace, []string{chunkID}); err != nil {
			return false, err
		}
		if err := n.importGraphContributionTx(ctx, tx, namespace, chunkID, graph); err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		return false, err
	}
	applied, _ := result.(bool)
	return applied, nil
}

// DeleteGraphContributions removes only facts owned by the supplied chunks.
// Per-chunk attempt markers make stale cleanup a no-op after a newer attempt
// has already replaced that contribution.
func (n *Neo4jRepository) DeleteGraphContributions(
	ctx context.Context,
	namespace types.NameSpace,
	chunkIDs []string,
	attempt int,
) error {
	if n.driver == nil || len(chunkIDs) == 0 {
		return nil
	}
	if err := n.ensureContributionConstraint(ctx); err != nil {
		return err
	}
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		for _, chunkID := range chunkIDs {
			applied, err := claimGraphContributionAttempt(ctx, tx, namespace, chunkID, attempt)
			if err != nil {
				return nil, err
			}
			if !applied {
				continue
			}
			if err := n.deleteGraphContributionsTx(
				ctx,
				tx,
				namespace,
				[]string{chunkID},
			); err != nil {
				return nil, err
			}
			if _, err := tx.Run(ctx, `
				MATCH (c:WEKNORA_GRAPH_CONTRIBUTION {
					key: $contribution_key
				})
				WHERE c.attempt <= $attempt
				DELETE c
			`, map[string]interface{}{
				"contribution_key": graphContributionKey(namespace, chunkID),
				"attempt":          attempt,
			}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

func (n *Neo4jRepository) ensureContributionConstraint(ctx context.Context) error {
	n.contributionConstraintMu.Lock()
	defer n.contributionConstraintMu.Unlock()
	if n.contributionConstraintReady {
		return nil
	}
	session := n.driver.NewSession(
		ctx,
		neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite},
	)
	defer session.Close(ctx)
	result, err := session.Run(ctx, `
		CREATE CONSTRAINT weknora_graph_contribution_key IF NOT EXISTS
		FOR (c:WEKNORA_GRAPH_CONTRIBUTION)
		REQUIRE c.key IS UNIQUE
	`, nil)
	if err == nil {
		_, err = result.Consume(ctx)
	}
	if err != nil {
		return fmt.Errorf("ensure graph contribution uniqueness: %w", err)
	}
	n.contributionConstraintReady = true
	return nil
}

func graphContributionKey(namespace types.NameSpace, chunkID string) string {
	sum := sha256.Sum256([]byte(
		namespace.KnowledgeBase + "\x00" + namespace.Knowledge + "\x00" + chunkID,
	))
	return hex.EncodeToString(sum[:])
}

func claimGraphContributionAttempt(
	ctx context.Context,
	tx neo4j.ManagedTransaction,
	namespace types.NameSpace,
	chunkID string,
	attempt int,
) (bool, error) {
	result, err := tx.Run(ctx, `
		MERGE (c:WEKNORA_GRAPH_CONTRIBUTION {
			key: $contribution_key
		})
		ON CREATE SET
			c.knowledge_base_id = $knowledge_base_id,
			c.knowledge_id = $knowledge_id,
			c.chunk_id = $chunk_id,
			c.attempt = $attempt
		WITH c
		WHERE c.attempt <= $attempt
		SET c.attempt = $attempt
		RETURN count(c) AS applied
	`, map[string]interface{}{
		"contribution_key":  graphContributionKey(namespace, chunkID),
		"knowledge_base_id": namespace.KnowledgeBase,
		"knowledge_id":      namespace.Knowledge,
		"chunk_id":          chunkID,
		"attempt":           attempt,
	})
	if err != nil {
		return false, err
	}
	if !result.Next(ctx) {
		return false, result.Err()
	}
	raw, _ := result.Record().Get("applied")
	applied, _ := raw.(int64)
	return applied > 0, nil
}

func (n *Neo4jRepository) deleteGraphContributionsTx(
	ctx context.Context,
	tx neo4j.ManagedTransaction,
	namespace types.NameSpace,
	chunkIDs []string,
) error {
	labelExpr := n.Label(namespace)
	const attributeSeparator = "\u001f"
	chunkPrefixes := make([]string, len(chunkIDs))
	for index, chunkID := range chunkIDs {
		chunkPrefixes[index] = chunkID + attributeSeparator
	}
	deleteRelationships := `
		MATCH (n:` + labelExpr + ` {kg: $knowledge_id})-[r]-(m:` + labelExpr + ` {kg: $knowledge_id})
		WHERE ANY(chunk_id IN $chunk_ids WHERE chunk_id IN coalesce(r.chunks, []))
		SET r.chunks = [chunk_id IN coalesce(r.chunks, []) WHERE NOT chunk_id IN $chunk_ids]
		WITH DISTINCT r
		WHERE size(r.chunks) = 0
		DELETE r
	`
	if _, err := tx.Run(ctx, deleteRelationships, map[string]interface{}{
		"knowledge_id": namespace.Knowledge,
		"chunk_ids":    chunkIDs,
	}); err != nil {
		return fmt.Errorf("delete graph contribution relationships: %w", err)
	}
	deleteNodes := `
		MATCH (n:` + labelExpr + ` {kg: $knowledge_id})
		WHERE ANY(chunk_id IN $chunk_ids WHERE chunk_id IN coalesce(n.chunks, []))
		SET n.attribute_contributions =
			CASE
				WHEN n.attribute_contributions IS NULL
				THEN reduce(
					entries = [],
					owner IN coalesce(n.chunks, []) |
					entries + [
						attribute IN coalesce(n.attributes, []) |
						owner + $attribute_separator + attribute
					]
				)
				ELSE n.attribute_contributions
			END
		SET n.chunks = [chunk_id IN coalesce(n.chunks, []) WHERE NOT chunk_id IN $chunk_ids]
		SET n.attribute_contributions = [
			entry IN coalesce(n.attribute_contributions, [])
			WHERE NOT ANY(prefix IN $chunk_prefixes WHERE entry STARTS WITH prefix)
		]
		SET n.attributes = apoc.coll.toSet([
			entry IN coalesce(n.attribute_contributions, []) |
			substring(
				entry,
				size(split(entry, $attribute_separator)[0]) + 1
			)
		])
		WITH n
		WHERE size(n.chunks) = 0
		DETACH DELETE n
	`
	if _, err := tx.Run(ctx, deleteNodes, map[string]interface{}{
		"knowledge_id":        namespace.Knowledge,
		"chunk_ids":           chunkIDs,
		"chunk_prefixes":      chunkPrefixes,
		"attribute_separator": attributeSeparator,
	}); err != nil {
		return fmt.Errorf("delete graph contribution nodes: %w", err)
	}
	return nil
}

func (n *Neo4jRepository) importGraphContributionTx(
	ctx context.Context,
	tx neo4j.ManagedTransaction,
	namespace types.NameSpace,
	chunkID string,
	graph *types.GraphData,
) error {
	const attributeSeparator = "\u001f"
	nodeData := make([]map[string]interface{}, 0, len(graph.Node))
	for _, node := range graph.Node {
		attributeContributions := make([]string, len(node.Attributes))
		for index, attribute := range node.Attributes {
			attributeContributions[index] = chunkID + attributeSeparator + attribute
		}
		nodeData = append(nodeData, map[string]interface{}{
			"name":                    node.Name,
			"knowledge_id":            namespace.Knowledge,
			"attribute_contributions": attributeContributions,
			"chunks":                  []string{chunkID},
			"labels":                  n.Labels(namespace),
		})
	}
	if _, err := tx.Run(ctx, `
		UNWIND $data AS row
		CALL apoc.merge.node(row.labels, {name: row.name, kg: row.knowledge_id}, {}, {}) YIELD node
		SET node.chunks = apoc.coll.union(coalesce(node.chunks, []), row.chunks)
		SET node.attribute_contributions = apoc.coll.union(
			coalesce(node.attribute_contributions, []),
			row.attribute_contributions
		)
		SET node.attributes = apoc.coll.toSet([
			entry IN node.attribute_contributions |
			substring(
				entry,
				size(split(entry, $attribute_separator)[0]) + 1
			)
		])
		RETURN distinct 'done' AS result
	`, map[string]interface{}{
		"data":                nodeData,
		"attribute_separator": attributeSeparator,
	}); err != nil {
		return fmt.Errorf("import graph contribution nodes: %w", err)
	}

	relData := make([]map[string]interface{}, 0, len(graph.Relation))
	for _, relation := range graph.Relation {
		relData = append(relData, map[string]interface{}{
			"source":        relation.Node1,
			"target":        relation.Node2,
			"knowledge_id":  namespace.Knowledge,
			"type":          relation.Type,
			"source_labels": n.Labels(namespace),
			"target_labels": n.Labels(namespace),
			"chunks":        []string{chunkID},
		})
	}
	if _, err := tx.Run(ctx, `
		UNWIND $data AS row
		CALL apoc.merge.node(row.source_labels, {name: row.source, kg: row.knowledge_id}, {}, {}) YIELD node as source
		CALL apoc.merge.node(row.target_labels, {name: row.target, kg: row.knowledge_id}, {}, {}) YIELD node as target
		CALL apoc.merge.relationship(source, row.type, {}, {chunks: row.chunks}, target) YIELD rel
		SET rel.chunks = apoc.coll.union(coalesce(rel.chunks, []), row.chunks)
		RETURN distinct 'done'
	`, map[string]interface{}{"data": relData}); err != nil {
		return fmt.Errorf("import graph contribution relationships: %w", err)
	}
	return nil
}

// addGraph adds a graph to the Neo4j repository
func (n *Neo4jRepository) addGraph(ctx context.Context, namespace types.NameSpace, graph *types.GraphData) error {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		// Node import query
		node_import_query := `
			UNWIND $data AS row
			CALL apoc.merge.node(row.labels, {name: row.name, kg: row.knowledge_id}, row.props, {}) YIELD node
			SET node.chunks = apoc.coll.union(node.chunks, row.chunks)
			RETURN distinct 'done' AS result
		`
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
		if _, err := tx.Run(ctx, node_import_query, map[string]interface{}{"data": nodeData}); err != nil {
			return nil, fmt.Errorf("failed to create nodes: %v", err)
		}

		// Relationship import query
		rel_import_query := `
			UNWIND $data AS row
			CALL apoc.merge.node(row.source_labels, {name: row.source, kg: row.knowledge_id}, {}, {}) YIELD node as source
			CALL apoc.merge.node(row.target_labels, {name: row.target, kg: row.knowledge_id}, {}, {}) YIELD node as target
			CALL apoc.merge.relationship(source, row.type, {}, row.attributes, target) YIELD rel
			RETURN distinct 'done'
		`
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
		if _, err := tx.Run(ctx, rel_import_query, map[string]interface{}{"data": relData}); err != nil {
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
				) YIELD batches, total
				RETURN total
        	`
			if _, err := tx.Run(ctx, deleteRelsQuery, map[string]interface{}{"knowledge_id": namespace.Knowledge}); err != nil {
				return nil, fmt.Errorf("failed to delete relationships: %v", err)
			}

			deleteNodesQuery := `
				CALL apoc.periodic.iterate(
					"MATCH (n:` + labelExpr + ` {kg: $knowledge_id}) RETURN n",
					"DELETE n",
					{batchSize: 1000, parallel: true, params: {knowledge_id: $knowledge_id}}
				) YIELD batches, total
				RETURN total
        	`
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

// SearchNode searches for nodes in the Neo4j repository
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
		labelExpr := n.Label(namespace)
		query := `
			MATCH (n:` + labelExpr + `)-[r]-(m:` + labelExpr + `)
			WHERE ANY(nodeText IN $nodes WHERE n.name CONTAINS nodeText)
			RETURN n, r, m
		`
		params := map[string]interface{}{"nodes": nodes}
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
		return graphData, nil
	})
	if err != nil {
		logger.Errorf(ctx, "search node failed: %v", err)
		return nil, err
	}
	return result.(*types.GraphData), nil
}

func listI2listS(list []any) []string {
	result := make([]string, len(list))
	for i, v := range list {
		result[i] = fmt.Sprintf("%v", v)
	}
	return result
}
