package neo4j

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

type Neo4jRepository struct {
	driver     neo4j.Driver
	nodePrefix string
}

func NewNeo4jRepository(driver neo4j.Driver) interfaces.RetrieveGraphRepository {
	return &Neo4jRepository{driver: driver, nodePrefix: "ENTITY"}
}

func _remove_hyphen(s string) string {
	return strings.ReplaceAll(s, "-", "_")
}

func (n *Neo4jRepository) Labels(namespace types.NameSpace) []string {
	res := make([]string, 0)
	for _, label := range namespace.Labels() {
		res = append(res, n.nodePrefix+_remove_hyphen(label))
	}
	return res
}

func (n *Neo4jRepository) Label(namespace types.NameSpace) string {
	labels := n.Labels(namespace)
	return strings.Join(labels, ":")
}

// AddGraph implements interfaces.RetrieveGraphRepository.
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

func (n *Neo4jRepository) addGraph(ctx context.Context, namespace types.NameSpace, graph *types.GraphData) error {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		node_import_query := `
			UNWIND $data AS row
			CALL apoc.merge.node(
			row.labels,
			{id: row.id},
			apoc.map.merge(row.attributes, {chunkids: row.chunkids}),
			apoc.map.merge(
				node, 
				row.attributes,
				{
					chunkids: CASE 
						WHEN exists(node.chunkids) THEN node.chunkids + row.chunkids 
						ELSE row.chunkids
					END
				}
			)
			) YIELD node
			RETURN distinct 'done' AS result
		`
		nodeData := []map[string]interface{}{}
		for _, node := range graph.Node {
			nodeData = append(nodeData, map[string]interface{}{
				"id":         node.ID,
				"attributes": node.Attributes,
				"chunkids":   node.ChunkIDs,
				"labels":     n.Labels(namespace),
			})
		}
		if _, err := tx.Run(ctx, node_import_query, map[string]interface{}{"data": nodeData}); err != nil {
			return nil, fmt.Errorf("failed to create nodes: %v", err)
		}

		rel_import_query := `
			UNWIND $data AS row
			CALL apoc.merge.node(row.source_labels, {id: row.source}, {}, {}) YIELD node as source
			CALL apoc.merge.node(row.target_labels, {id: row.target}, {}, {}) YIELD node as target
			CALL apoc.merge.relationship(source, row.type, {}, row.attributes, target) YIELD rel
			RETURN distinct 'done'
		`
		relData := []map[string]interface{}{}
		for _, rel := range graph.Relation {
			relData = append(relData, map[string]interface{}{
				"source":        rel.Source.ID,
				"target":        rel.Target.ID,
				"type":          rel.Type,
				"attributes":    rel.Attributes,
				"source_labels": n.Labels(namespace),
				"target_labels": n.Labels(namespace),
			})
		}
		if _, err := tx.Run(ctx, rel_import_query, map[string]interface{}{"data": relData}); err != nil {
			return nil, fmt.Errorf("failed to create relationships: %v", err)
		}
		return nil, nil
	})
	return err
}

// DelGraph implements interfaces.RetrieveGraphRepository.
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
				MATCH (n:` + labelExpr + `)-[r]-(m:` + labelExpr + `)
				CALL apoc.periodic.iterate(
					"MATCH (n:` + labelExpr + `)-[r]-(m:` + labelExpr + `) RETURN r",
					"DELETE r",
					{batchSize: 1000, parallel: true}
				) YIELD batches, total
				RETURN total
        	`
			if _, err := tx.Run(ctx, deleteRelsQuery, nil); err != nil {
				return nil, fmt.Errorf("failed to delete relationships: %v", err)
			}

			deleteNodesQuery := `
				CALL apoc.periodic.iterate(
					"MATCH (n:` + labelExpr + `) RETURN n",
					"DELETE n",
					{batchSize: 1000, parallel: true}
				) YIELD batches, total
				RETURN total
        	`
			if _, err := tx.Run(ctx, deleteNodesQuery, nil); err != nil {
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

func (n *Neo4jRepository) SearchNode(ctx context.Context, namespace types.NameSpace, nodes []string) (*types.GraphData, error) {
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
			WHERE ANY(nodeText IN $nodes WHERE n.id CONTAINS nodeText)
			RETURN n, r, m
		`
		params := map[string]interface{}{"nodes": nodes}
		result, err := tx.Run(ctx, query, params)
		if err != nil {
			return nil, err
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
				idStr := n.Props["id"].(string)
				if _, ok := nodeSeen[idStr]; !ok {
					nodeSeen[idStr] = true
					graphData.Node = append(graphData.Node, &types.GraphNode{
						ID:         idStr,
						Attributes: prop2attribute(n.Props),
					})
				}
			}

			// Convert relationship to types.Relation
			relData := rel.(neo4j.Relationship)
			graphData.Relation = append(graphData.Relation, &types.GraphRelation{
				Source: &types.GraphNode{
					ID:         nodeData.Props["id"].(string),
					Attributes: prop2attribute(nodeData.Props),
				},
				Target: &types.GraphNode{
					ID:         targetNodeData.Props["id"].(string),
					Attributes: prop2attribute(targetNodeData.Props),
				},
				Type:       relData.Type,
				Attributes: prop2attribute(relData.Props),
			})
		}
		return graphData, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*types.GraphData), nil
}

func prop2attribute(prop map[string]any) map[string]string {
	attributes := make(map[string]string)
	for k, v := range prop {
		attributes[k] = fmt.Sprintf("%v", v)
	}
	return attributes
}
