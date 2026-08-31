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

// Neo4jRepository is a repository for Neo4j
type Neo4jRepository struct {
	driver     neo4j.Driver
	nodePrefix string
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
				"attributes":    map[string]interface{}{"chunk_ids": rel.ChunkIDs},
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

func (n *Neo4jRepository) GetGraph(
	ctx context.Context,
	namespace types.NameSpace,
	query types.GraphQuery,
) (*types.GraphQueryResult, error) {
	if n.driver == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return &types.GraphQueryResult{}, nil
	}
	if namespace.KnowledgeBase == "" {
		return nil, fmt.Errorf("knowledge base is required")
	}
	limit := query.Limit
	if limit <= 0 || limit > 2000 {
		limit = 500
	}

	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (interface{}, error) {
		labelExpr := n.Label(types.NameSpace{KnowledgeBase: namespace.KnowledgeBase})
		params := map[string]interface{}{"knowledge_base_id": namespace.KnowledgeBase, "limit": limit}
		attributeClause := ""
		if len(query.Attributes) > 0 {
			attributeClause = " AND ANY(attribute IN coalesce(n.attributes, []) WHERE attribute IN $attributes)"
			params["attributes"] = query.Attributes
		}

		countResult, err := tx.Run(ctx, "MATCH (n:"+labelExpr+") WHERE n.kg IS NOT NULL"+attributeClause+" RETURN count(n) AS total", params)
		if err != nil {
			return nil, fmt.Errorf("count graph nodes: %w", err)
		}
		var total int
		if countResult.Next(ctx) {
			if value, ok := countResult.Record().Get("total"); ok {
				switch number := value.(type) {
				case int64:
					total = int(number)
				case int:
					total = number
				}
			}
		}
		if err := countResult.Err(); err != nil {
			return nil, fmt.Errorf("read graph node count: %w", err)
		}

		nodeResult, err := tx.Run(ctx, "MATCH (n:"+labelExpr+") WHERE n.kg IS NOT NULL"+attributeClause+" RETURN n ORDER BY size(coalesce(n.chunks, [])) DESC, n.name LIMIT $limit", params)
		if err != nil {
			return nil, fmt.Errorf("load graph nodes: %w", err)
		}
		data := &types.GraphQueryResult{TotalNodes: total}
		keys := make([]string, 0, limit)
		seen := make(map[string]struct{}, limit)
		for nodeResult.Next(ctx) {
			value, ok := nodeResult.Record().Get("n")
			if !ok {
				continue
			}
			node, ok := value.(neo4j.Node)
			if !ok {
				continue
			}
			name := propertyString(node.Props, "name")
			knowledgeID := propertyString(node.Props, "kg")
			if name == "" || knowledgeID == "" {
				continue
			}
			key := knowledgeID + "|" + name
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			keys = append(keys, key)
			data.Node = append(data.Node, &types.GraphNode{
				Name:        name,
				KnowledgeID: knowledgeID,
				Chunks:      propertyStrings(node.Props, "chunks"),
				Attributes:  propertyStrings(node.Props, "attributes"),
			})
		}
		if err := nodeResult.Err(); err != nil {
			return nil, fmt.Errorf("read graph nodes: %w", err)
		}
		if len(keys) == 0 {
			return data, nil
		}

		edgeResult, err := tx.Run(ctx, "MATCH (source:"+labelExpr+")-[r]->(target:"+labelExpr+") WHERE (source.kg + '|' + source.name) IN $keys AND (target.kg + '|' + target.name) IN $keys RETURN source, r, target", map[string]interface{}{"keys": keys})
		if err != nil {
			return nil, fmt.Errorf("load graph relations: %w", err)
		}
		seenRelations := make(map[string]struct{})
		for edgeResult.Next(ctx) {
			record := edgeResult.Record()
			sourceValue, sourceOK := record.Get("source")
			targetValue, targetOK := record.Get("target")
			relationValue, relationOK := record.Get("r")
			if !sourceOK || !targetOK || !relationOK {
				continue
			}
			source, sourceOK := sourceValue.(neo4j.Node)
			target, targetOK := targetValue.(neo4j.Node)
			relation, relationOK := relationValue.(neo4j.Relationship)
			if !sourceOK || !targetOK || !relationOK {
				continue
			}
			sourceName := propertyString(source.Props, "name")
			targetName := propertyString(target.Props, "name")
			sourceKnowledgeID := propertyString(source.Props, "kg")
			targetKnowledgeID := propertyString(target.Props, "kg")
			key := sourceKnowledgeID + "|" + sourceName + "|" + relation.Type + "|" + targetKnowledgeID + "|" + targetName
			if _, exists := seenRelations[key]; exists {
				continue
			}
			seenRelations[key] = struct{}{}
			data.Relation = append(data.Relation, &types.GraphRelation{
				Node1:             sourceName,
				Node2:             targetName,
				SourceKnowledgeID: sourceKnowledgeID,
				TargetKnowledgeID: targetKnowledgeID,
				Type:              relation.Type,
				ChunkIDs:          propertyStrings(relation.Props, "chunk_ids"),
			})
		}
		if err := edgeResult.Err(); err != nil {
			return nil, fmt.Errorf("read graph relations: %w", err)
		}
		return data, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*types.GraphQueryResult), nil
}

func propertyString(properties map[string]interface{}, key string) string {
	value, ok := properties[key]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%v", value)
}

func propertyStrings(properties map[string]interface{}, key string) []string {
	value, ok := properties[key]
	if !ok {
		return nil
	}
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []interface{}:
		return listI2listS(values)
	default:
		return []string{fmt.Sprintf("%v", value)}
	}
}

func listI2listS(list []any) []string {
	result := make([]string, len(list))
	for i, v := range list {
		result[i] = fmt.Sprintf("%v", v)
	}
	return result
}
