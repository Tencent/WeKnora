package neo4j

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

const relationshipWriteBatchSize = 50

// Neo4jRepository is a repository for Neo4j
type Neo4jRepository struct {
	driver          neo4j.Driver
	nodePrefix      string
	newWriteSession func(context.Context) graphWriteSession
}

// graphWriteSession is the subset of neo4j.Session used by graph writes. The
// official Session satisfies it; keeping the dependency narrow also lets unit
// tests verify transaction boundaries without a running Neo4j instance.
type graphWriteSession interface {
	ExecuteWrite(
		ctx context.Context,
		work neo4j.ManagedTransactionWork,
		configurers ...func(*neo4j.TransactionConfig),
	) (any, error)
	Close(ctx context.Context) error
}

// NewNeo4jRepository creates a new Neo4j repository
func NewNeo4jRepository(driver neo4j.Driver) interfaces.RetrieveGraphRepository {
	repository := &Neo4jRepository{driver: driver, nodePrefix: "ENTITY"}
	if driver != nil {
		repository.newWriteSession = func(ctx context.Context) graphWriteSession {
			return driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
		}
	}
	return repository
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

// AddGraph adds a graph to the Neo4j repository.
func (n *Neo4jRepository) AddGraph(
	ctx context.Context,
	namespace types.NameSpace,
	graphs []*types.GraphData,
) (*types.GraphWriteResult, error) {
	result := &types.GraphWriteResult{}
	if n.driver == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return result, nil
	}
	for _, graph := range graphs {
		graphResult, err := n.addGraph(ctx, namespace, graph)
		mergeGraphWriteResult(result, graphResult)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// addGraph adds a graph to the Neo4j repository.
func (n *Neo4jRepository) addGraph(
	ctx context.Context,
	namespace types.NameSpace,
	graph *types.GraphData,
) (*types.GraphWriteResult, error) {
	result := &types.GraphWriteResult{}
	if graph == nil {
		return result, nil
	}
	result.NodesAttempted = len(graph.Node)
	result.RelationsAttempted = len(graph.Relation)
	session := n.newWriteSession(ctx)
	defer session.Close(ctx)

	// Nodes and relationships intentionally use separate managed transactions.
	// Once ExecuteWrite for nodes returns successfully, that transaction has
	// committed. A malformed relationship can then fail independently without
	// rolling back nodes that were already valid and persisted.
	if len(graph.Node) > 0 {
		_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
			node_import_query := `
			UNWIND $data AS row
			CALL apoc.merge.node(row.labels, {name: row.name, kg: row.knowledge_id}, row.props, {}) YIELD node
			SET node.chunks = apoc.coll.union(node.chunks, row.chunks)
			RETURN distinct 'done' AS result
		`
			nodeData := make([]map[string]interface{}, 0, len(graph.Node))
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
				return nil, fmt.Errorf("failed to create nodes: %w", err)
			}
			return nil, nil
		})
		if err != nil {
			logger.Errorf(ctx, "failed to add graph nodes: %v", err)
			result.NodesFailed = len(graph.Node)
			result.AddFailure(types.GraphFailureDetail{
				Stage: "neo4j_write", Kind: "node", ItemIndex: -1,
				Reason: err.Error(),
			})
			return result, err
		}
		result.NodesWritten = len(graph.Node)
	}

	if err := n.writeRelationships(ctx, session, namespace, graph.Relation, result); err != nil {
		logger.Errorf(ctx, "failed to add graph relationships: %v", err)
		return result, err
	}
	return result, nil
}

func mergeGraphWriteResult(target, source *types.GraphWriteResult) {
	if target == nil || source == nil {
		return
	}
	target.NodesAttempted += source.NodesAttempted
	target.NodesWritten += source.NodesWritten
	target.NodesFailed += source.NodesFailed
	target.RelationsAttempted += source.RelationsAttempted
	target.RelationsWritten += source.RelationsWritten
	target.RelationsFailed += source.RelationsFailed
	for _, failure := range source.Failures {
		target.AddFailure(failure)
	}
}

// writeRelationships commits relationships in small batches. When a
// data-specific batch failure occurs, the batch is bisected until the bad
// single relationship is isolated and skipped. Successful sibling batches
// remain committed. Infrastructure and retryable errors are returned
// immediately instead of causing an expensive split storm.
func (n *Neo4jRepository) writeRelationships(
	ctx context.Context,
	session graphWriteSession,
	namespace types.NameSpace,
	relations []*types.GraphRelation,
	result *types.GraphWriteResult,
) error {
	for start := 0; start < len(relations); start += relationshipWriteBatchSize {
		end := start + relationshipWriteBatchSize
		if end > len(relations) {
			end = len(relations)
		}
		if err := n.writeRelationshipBatchIsolated(
			ctx, session, namespace, relations[start:end], start, result,
		); err != nil {
			return err
		}
	}
	return nil
}

func (n *Neo4jRepository) writeRelationshipBatchIsolated(
	ctx context.Context,
	session graphWriteSession,
	namespace types.NameSpace,
	relations []*types.GraphRelation,
	startIndex int,
	result *types.GraphWriteResult,
) error {
	if len(relations) == 0 {
		return nil
	}
	err := n.writeRelationshipBatch(ctx, session, namespace, relations)
	if err == nil {
		result.RelationsWritten += len(relations)
		return nil
	}
	if !isRelationshipDataError(err) {
		return err
	}
	if len(relations) == 1 {
		relation := relations[0]
		logger.Warnf(ctx, "Skipping invalid graph relationship %q --[%q]--> %q: %v",
			relation.Node1, relation.Type, relation.Node2, err)
		result.RelationsFailed++
		result.AddFailure(types.GraphFailureDetail{
			Stage: "neo4j_write", Kind: "relation", ItemIndex: startIndex,
			Node1: relation.Node1, Node2: relation.Node2, Type: relation.Type,
			Reason: err.Error(),
		})
		return nil
	}

	middle := len(relations) / 2
	if err := n.writeRelationshipBatchIsolated(
		ctx, session, namespace, relations[:middle], startIndex, result,
	); err != nil {
		return err
	}
	return n.writeRelationshipBatchIsolated(
		ctx, session, namespace, relations[middle:], startIndex+middle, result,
	)
}

func (n *Neo4jRepository) writeRelationshipBatch(
	ctx context.Context,
	session graphWriteSession,
	namespace types.NameSpace,
	relations []*types.GraphRelation,
) error {
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		rel_import_query := `
			UNWIND $data AS row
			CALL apoc.merge.node(row.source_labels, {name: row.source, kg: row.knowledge_id}, {}, {}) YIELD node as source
			CALL apoc.merge.node(row.target_labels, {name: row.target, kg: row.knowledge_id}, {}, {}) YIELD node as target
			CALL apoc.merge.relationship(source, row.type, {}, row.attributes, target) YIELD rel
			RETURN distinct 'done'
		`
		relData := make([]map[string]interface{}, 0, len(relations))
		for _, rel := range relations {
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
			return nil, fmt.Errorf("failed to create relationships: %w", err)
		}
		return nil, nil
	})
	return err
}

func isRelationshipDataError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if neo4j.IsRetryable(err) {
		return false
	}
	var connectivityErr *neo4j.ConnectivityError
	var authenticationErr *neo4j.InvalidAuthenticationError
	var usageErr *neo4j.UsageError
	var transactionLimitErr *neo4j.TransactionExecutionLimit
	if errors.As(err, &connectivityErr) || errors.As(err, &authenticationErr) ||
		errors.As(err, &usageErr) || errors.As(err, &transactionLimitErr) {
		return false
	}
	var databaseErr *neo4j.Neo4jError
	if errors.As(err, &databaseErr) {
		return databaseErr.Classification() == "ClientError"
	}
	// Repository tests and non-driver adapters can return ordinary errors for
	// row-specific validation failures. Treat those as isolatable by default.
	return true
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
