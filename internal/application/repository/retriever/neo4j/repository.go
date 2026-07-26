package neo4j

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

const graphEntityLabel = "GRAPH_ENTITY"

// Neo4jRepository is a repository for Neo4j
type Neo4jRepository struct {
	driver                neo4j.Driver
	nodePrefix            string
	writeTransaction      func(context.Context, neo4j.ManagedTransactionWork) error
	graphRecoveryRequired func(context.Context, types.NameSpace) (bool, error)

	ownershipSchemaMu    sync.Mutex
	ownershipSchemaReady bool
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

func (n *Neo4jRepository) ownershipLabels(namespace types.NameSpace) []string {
	labels := append([]string(nil), n.Labels(namespace)...)
	return append(labels, graphEntityLabel)
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
		node_import_query := `
			UNWIND $data AS row
			CALL apoc.merge.node(row.labels, {name: row.name, kg: row.knowledge_id}, row.props, {}) YIELD node
			SET node.chunks = apoc.coll.union(coalesce(node.chunks, []), row.chunks)
			RETURN distinct 'done' AS result
		`
		nodeData := []map[string]interface{}{}
		for _, node := range graph.Node {
			nodeData = append(nodeData, map[string]interface{}{
				"name":         node.Name,
				"knowledge_id": namespace.Knowledge,
				"props":        map[string][]string{"attributes": node.Attributes},
				"chunks":       node.Chunks,
				"labels":       n.ownershipLabels(namespace),
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
			CALL apoc.merge.relationship(source, row.type, {}, {}, target) YIELD rel
			SET rel.chunks = apoc.coll.union(coalesce(rel.chunks, []), row.chunks)
			RETURN distinct 'done'
		`
		relData := []map[string]interface{}{}
		for _, rel := range graph.Relation {
			relData = append(relData, map[string]interface{}{
				"source":        rel.Node1,
				"target":        rel.Node2,
				"knowledge_id":  namespace.Knowledge,
				"type":          rel.Type,
				"chunks":        rel.Chunks,
				"source_labels": n.ownershipLabels(namespace),
				"target_labels": n.ownershipLabels(namespace),
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
	if n.driver == nil && n.writeTransaction == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	if err := n.ensureGraphOwnershipSchema(ctx); err != nil {
		return err
	}
	for _, namespace := range namespaces {
		if err := n.validateGraphChunkOperation(namespace, nil); err != nil {
			return err
		}
		deletionToken := uuid.NewString()
		if err := n.beginGraphNamespaceDeletion(ctx, namespace, deletionToken); err != nil {
			return err
		}
		deleteErr := n.deleteGraphNamespaceData(ctx, namespace, deletionToken)
		finishErr := n.finishGraphNamespaceDeletion(ctx, namespace, deletionToken, deleteErr == nil)
		if deleteErr != nil || finishErr != nil {
			return errors.Join(deleteErr, finishErr)
		}
	}
	return nil
}

func (n *Neo4jRepository) deleteGraphNamespaceData(
	ctx context.Context,
	namespace types.NameSpace,
	deletionToken string,
) error {
	labelExpr := n.Label(namespace)
	return n.runGraphWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		deleteRelsQuery := `
			CALL apoc.periodic.iterate(
				"MATCH (n:` + labelExpr + ` {kg: $knowledge_id})-[r]-(m:` + labelExpr + ` {kg: $knowledge_id})
				 RETURN DISTINCT r",
				"MATCH (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
				 CALL apoc.util.validate(
					coalesce(state.deletion_token, '') <> $deletion_token,
					'graph namespace deletion superseded',
					[]
				 )
				 DELETE r",
				{
					batchSize: 1000,
					parallel: false,
					retries: 3,
					params: {
						knowledge_id: $knowledge_id,
						deletion_token: $deletion_token
					}
				}
			) YIELD total, failedOperations, failedBatches, errorMessages, wasTerminated
			MATCH (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
			CALL apoc.util.validate(
				coalesce(state.deletion_token, '') <> $deletion_token,
				'graph namespace deletion superseded',
				[]
			)
			WITH total, failedOperations, failedBatches, errorMessages, wasTerminated
			CALL apoc.util.validate(
				failedOperations > 0 OR failedBatches > 0 OR coalesce(wasTerminated, false),
				'graph relationship batch deletion failed',
				[]
			)
			RETURN total
		`
		if err := runConsumedGraphQuery(ctx, tx, deleteRelsQuery, map[string]any{
			"knowledge_id":   namespace.Knowledge,
			"deletion_token": deletionToken,
		}); err != nil {
			return nil, fmt.Errorf("failed to delete relationships: %w", err)
		}

		deleteNodesQuery := `
			CALL apoc.periodic.iterate(
				"MATCH (n:` + labelExpr + ` {kg: $knowledge_id}) RETURN n",
				"MATCH (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
				 CALL apoc.util.validate(
					coalesce(state.deletion_token, '') <> $deletion_token,
					'graph namespace deletion superseded',
					[]
				 )
				 DETACH DELETE n",
				{
					batchSize: 1000,
					parallel: false,
					retries: 3,
					params: {
						knowledge_id: $knowledge_id,
						deletion_token: $deletion_token
					}
				}
			) YIELD total, failedOperations, failedBatches, errorMessages, wasTerminated
			MATCH (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
			CALL apoc.util.validate(
				coalesce(state.deletion_token, '') <> $deletion_token,
				'graph namespace deletion superseded',
				[]
			)
			WITH total, failedOperations, failedBatches, errorMessages, wasTerminated
			CALL apoc.util.validate(
				failedOperations > 0 OR failedBatches > 0 OR coalesce(wasTerminated, false),
				'graph node batch deletion failed',
				[]
			)
			RETURN total
		`
		if err := runConsumedGraphQuery(ctx, tx, deleteNodesQuery, map[string]any{
			"knowledge_id":   namespace.Knowledge,
			"deletion_token": deletionToken,
		}); err != nil {
			return nil, fmt.Errorf("failed to delete nodes: %w", err)
		}
		return nil, nil
	})
}

func runConsumedGraphQuery(
	ctx context.Context,
	tx neo4j.ManagedTransaction,
	query string,
	params map[string]any,
) error {
	result, err := tx.Run(ctx, query, params)
	if err != nil || result == nil {
		return err
	}
	_, err = result.Consume(ctx)
	return err
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
