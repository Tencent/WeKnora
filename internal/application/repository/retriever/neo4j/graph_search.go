// Package neo4j implements the knowledge graph storage and query adapters.
package neo4j

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// NewGraphSearchRepository constructs the bounded public-query adapter. An
// empty engine keeps the default Neo4j dialect, as in NewNeo4jRepository.
func NewGraphSearchRepository(driver neo4j.Driver, engine GraphEngine) interfaces.GraphSearchRepository {
	if engine == "" {
		engine = EngineNeo4j
	}
	return &Neo4jRepository{driver: driver, nodePrefix: "ENTITY", engine: engine}
}

// graphSearchID projects string identities on both engines. Memgraph's id()
// returns an integer; keep native IDs for ordering and point lookups below.
func graphSearchID(engine GraphEngine, expression string) string {
	if engine == EngineMemgraph {
		return "toString(id(" + expression + "))"
	}
	return "elementId(" + expression + ")"
}

// graphSearchTextProjection bounds fields before they cross Bolt. Neo4j counts
// characters, but Memgraph 3.7 counts bytes and may split the final UTF-8 rune.
// Four bytes per rune preserve the full probe; decoding applies its rune cap.
func graphSearchTextProjection(engine GraphEngine, expression string, runeLimit int) string {
	limit := runeLimit
	if engine == EngineMemgraph {
		limit *= utf8.UTFMax
	}
	return fmt.Sprintf("substring(%s, 0, %d)", expression, limit)
}

// graphSearchProjection keeps one extra item (or character) so the service can
// detect clipping instead of returning partial names or trusting source-ID prefixes.
func graphSearchProjection(engine GraphEngine, alias string) string {
	name := graphSearchTextProjection(engine, alias+".name", types.GraphSearchMaxNameRunes+1)
	attribute := graphSearchTextProjection(engine, "a", types.GraphSearchMaxAttributeRunes+1)
	return alias + ` {
		id: ` + graphSearchID(engine, alias) + `,
		name: ` + name + `,
		knowledge_id: ` + graphSearchTextProjection(engine, alias+".kg", 129) + `,
		chunks: [c IN ` + alias + `.chunks[..513] | ` + graphSearchTextProjection(engine, "c", 129) + `],
		attributes: [a IN ` + alias + `.attributes[..21] | ` + attribute + `]
	}`
}

// trimGraphSearchProjection restores the character probe after Memgraph's byte
// projection. At four bytes per rune, any split tail lies beyond this probe.
// Source IDs retain 129 runes, so an overlong ID still fails the service's
// 128-byte validation rather than becoming a valid, truncated source identity.
func trimGraphSearchProjection(value string, runeLimit int) string {
	for index := range value {
		if runeLimit == 0 {
			return value[:index]
		}
		runeLimit--
	}
	return value
}

func graphSearchSeedQuery(engine GraphEngine, label string) string {
	return `
		MATCH (n:` + label + `)
		WHERE n.name CONTAINS $query
		WITH n ORDER BY CASE WHEN n.name = $query THEN 0 ELSE 1 END,
			size(n.name), n.name, n.kg, ` + engine.elementIDFunc() + `(n)
		LIMIT $probeLimit
		RETURN ` + graphSearchProjection(engine, "n") + ` AS n`
}

func graphSearchNeighbourQuery(engine GraphEngine, label string) string {
	id := engine.elementIDFunc()
	relationType := graphSearchTextProjection(engine, "type(r)", 129)
	return `
		UNWIND range(0, size($seeds) - 1) AS seed_index
		MATCH (n:` + label + `) WHERE ` + id + `(n) = $seeds[seed_index]
		MATCH (n)-[r]-(m:` + label + `)
		WHERE NOT ` + id + `(m) IN $seeds[0..seed_index]
		WITH n, r, m, seed_index ORDER BY seed_index, ` + id + `(r)
		LIMIT $probeLimit
		RETURN ` + graphSearchProjection(engine, "n") + ` AS n, ` + graphSearchProjection(engine, "m") + ` AS m,
			` + graphSearchID(engine, "r") + ` AS id, ` + graphSearchID(engine, "startNode(r)") + ` AS source_id,
			` + graphSearchID(engine, "endNode(r)") + ` AS target_id, ` + relationType + ` AS type`
}

// graphSearchSeedIDs reverses only the Memgraph projection's string conversion.
// Supplying integers lets id(n) use a point lookup and makes the earlier-seed
// exclusion compare like types, without losing precision to floating point.
func graphSearchSeedIDs(engine GraphEngine, ids []string) (any, error) {
	if engine != EngineMemgraph {
		return ids, nil
	}
	nativeIDs := make([]int64, len(ids))
	for i, id := range ids {
		value, err := strconv.ParseInt(id, 10, 64)
		if err != nil || value < 0 {
			return nil, fmt.Errorf("invalid Memgraph graph seed identity %q", id)
		}
		nativeIDs[i] = value
	}
	return nativeIDs, nil
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
		seedResult, err := tx.Run(ctx, graphSearchSeedQuery(n.engine, label),
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
		nativeSeedIDs, err := graphSearchSeedIDs(n.engine, seedIDs)
		if err != nil {
			return nil, err
		}
		// Keep relation-less seeds, and expand each stored edge only from its
		// earliest seed. Target matches cannot reverse the stored direction or
		// fill the budget twice. Both endpoints must carry the KB label.
		rows, err := tx.Run(ctx, graphSearchNeighbourQuery(n.engine, label),
			map[string]any{"seeds": nativeSeedIDs, "probeLimit": types.GraphSearchMaxRows + 1})
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
				if field == "type" {
					v = trimGraphSearchProjection(v, 129)
					if !utf8.ValidString(v) {
						return nil, fmt.Errorf("invalid graph relationship field %s", field)
					}
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
		switch key {
		case "name":
			value = trimGraphSearchProjection(value, types.GraphSearchMaxNameRunes+1)
		case "knowledge_id":
			value = trimGraphSearchProjection(value, 129)
		}
		if !utf8.ValidString(value) {
			return nil, fmt.Errorf("invalid graph node %s", key)
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
			limit := 129
			if key == "attributes" {
				limit = types.GraphSearchMaxAttributeRunes + 1
			}
			text = trimGraphSearchProjection(text, limit)
			if !utf8.ValidString(text) {
				return nil, fmt.Errorf("invalid graph node %s item", key)
			}
			*dest = append(*dest, text)
		}
	}
	return node, nil
}
