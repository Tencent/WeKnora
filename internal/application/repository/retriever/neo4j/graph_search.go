// Package neo4j implements the knowledge graph storage and query adapters.
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

// NewGraphSearchRepository constructs the bounded public-query adapter.
func NewGraphSearchRepository(driver neo4j.Driver) interfaces.GraphSearchRepository {
	return &Neo4jRepository{driver: driver, nodePrefix: "ENTITY"}
}

// graphSearchProjection bounds fields before they cross Bolt. One extra item
// (or character) lets the service report clipping instead of a false total.
func graphSearchProjection(alias string) string {
	return alias + ` {
		id: elementId(` + alias + `),
		name: substring(` + alias + `.name, 0, 513),
		knowledge_id: substring(` + alias + `.kg, 0, 129),
		chunks: [c IN ` + alias + `.chunks[..513] | substring(c, 0, 129)],
		attributes: [a IN ` + alias + `.attributes[..21] | substring(a, 0, 1001)]
	}`
}

// graphSearchLabel quotes every identifier. KB IDs are data, never Cypher,
// including imported/legacy IDs containing punctuation or backticks.
func (n *Neo4jRepository) graphSearchLabel(namespace types.NameSpace) (string, error) {
	if namespace.KnowledgeBase == "" || namespace.Knowledge != "" {
		return "", fmt.Errorf("graph search requires exactly one knowledge base")
	}
	labels := n.Labels(namespace)
	for i, label := range labels {
		labels[i] = "`" + strings.ReplaceAll(label, "`", "``") + "`"
	}
	return strings.Join(labels, ":"), nil
}

// SearchGraph returns ranked seed entities and a bounded one-hop neighbourhood.
func (n *Neo4jRepository) SearchGraph(
	ctx context.Context, namespace types.NameSpace, query string,
) (*types.GraphSearchCandidates, error) {
	if n.driver == nil {
		return nil, nil // Distinguished from an available backend with no matches.
	}
	label, err := n.graphSearchLabel(namespace)
	if err != nil {
		return nil, err
	}
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer func() {
		if err := session.Close(ctx); err != nil {
			logger.Warnf(ctx, "Failed to close graph query session: %v", err)
		}
	}()
	value, err := session.ExecuteRead(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		out := &types.GraphSearchCandidates{Graph: &types.GraphData{}}
		seedResult, err := tx.Run(ctx, `
			MATCH (n:`+label+`)
			WHERE n.name CONTAINS $query
			WITH n ORDER BY CASE WHEN n.name = $query THEN 0 ELSE 1 END,
				size(n.name), n.name, n.kg, elementId(n)
			LIMIT $probeLimit
			RETURN `+graphSearchProjection("n")+` AS n`,
			map[string]any{"query": query, "probeLimit": types.GraphSearchMaxSeeds + 1})
		if err != nil {
			return nil, err
		}
		seedIDs := []string{}
		seen := map[string]bool{}
		for seedResult.Next(ctx) {
			if len(seedIDs) == types.GraphSearchMaxSeeds {
				out.TruncationReasons = append(out.TruncationReasons, "candidate_seeds")
				continue
			}
			node, err := decodeGraphSearchProjection(seedResult.Record(), "n")
			if err != nil {
				return nil, err
			}
			seedIDs = append(seedIDs, node.ID)
			seen[node.ID] = true
			out.Graph.Node = append(out.Graph.Node, node)
		}
		if err := seedResult.Err(); err != nil {
			return nil, err
		}
		if len(seedIDs) == 0 {
			return out, nil
		}
		// Keep relation-less seeds, and expand each stored edge only from its
		// earliest seed. Target matches cannot reverse the stored direction or
		// fill the budget twice. Both endpoints must carry the KB label.
		rows, err := tx.Run(ctx, `
			UNWIND range(0, size($seeds) - 1) AS seed_index
			MATCH (n:`+label+`) WHERE elementId(n) = $seeds[seed_index]
			MATCH (n)-[r]-(m:`+label+`)
			WHERE NOT elementId(m) IN $seeds[0..seed_index]
			WITH n, r, m, seed_index ORDER BY seed_index, elementId(r)
			LIMIT $probeLimit
			RETURN `+graphSearchProjection("n")+` AS n, `+graphSearchProjection("m")+` AS m,
				elementId(r) AS id, elementId(startNode(r)) AS source_id,
				elementId(endNode(r)) AS target_id, substring(type(r), 0, 129) AS type`,
			map[string]any{"seeds": seedIDs, "probeLimit": types.GraphSearchMaxRows + 1})
		if err != nil {
			return nil, err
		}
		count := 0
		for rows.Next(ctx) {
			if count == types.GraphSearchMaxRows {
				out.TruncationReasons = append(out.TruncationReasons, "candidate_relations")
				continue
			}
			count++
			for _, alias := range []string{"n", "m"} {
				node, err := decodeGraphSearchProjection(rows.Record(), alias)
				if err != nil {
					return nil, err
				}
				if !seen[node.ID] {
					out.Graph.Node = append(out.Graph.Node, node)
					seen[node.ID] = true
				}
			}
			relation := &types.GraphRelation{}
			for field, dest := range map[string]*string{
				"id": &relation.ID, "source_id": &relation.SourceID,
				"target_id": &relation.TargetID, "type": &relation.Type,
			} {
				v, _, err := neo4j.GetRecordValue[string](rows.Record(), field)
				if err != nil || v == "" {
					return nil, fmt.Errorf("invalid graph relationship field %s", field)
				}
				*dest = v
			}
			if !seen[relation.SourceID] || !seen[relation.TargetID] {
				return nil, fmt.Errorf("graph relationship has an unreturned endpoint")
			}
			out.Graph.Relation = append(out.Graph.Relation, relation)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return out, nil
	}, neo4j.WithTxTimeout(types.GraphSearchTimeout))
	if err != nil {
		return nil, err
	}
	return value.(*types.GraphSearchCandidates), nil
}

func decodeGraphSearchProjection(record *neo4j.Record, alias string) (*types.GraphNode, error) {
	if record == nil {
		return nil, fmt.Errorf("nil graph search record")
	}
	value, ok := record.Get(alias)
	props, isMap := value.(map[string]any)
	if !ok || !isMap {
		return nil, fmt.Errorf("invalid graph node projection")
	}
	node := &types.GraphNode{}
	for key, dest := range map[string]*string{"id": &node.ID, "name": &node.Name, "knowledge_id": &node.KnowledgeID} {
		value, ok := props[key].(string)
		if !ok || value == "" {
			return nil, fmt.Errorf("graph node has no %s", key)
		}
		*dest = value
	}
	for key, dest := range map[string]*[]string{"chunks": &node.Chunks, "attributes": &node.Attributes} {
		*dest = []string{}
		if props[key] == nil {
			continue
		}
		values, ok := props[key].([]any)
		if !ok {
			return nil, fmt.Errorf("invalid graph node %s", key)
		}
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("invalid graph node %s item", key)
			}
			*dest = append(*dest, text)
		}
	}
	return node, nil
}
