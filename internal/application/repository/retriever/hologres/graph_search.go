package hologres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	chatMaxSeeds = 200
	chatMaxRows  = 2000
)

func (r *Repository) SearchNode(ctx context.Context, ns types.NameSpace, names []string) (*types.GraphData, error) {
	if r.db == nil {
		return nil, nil
	}
	if err := validateNamespace(ns, false); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return &types.GraphData{}, nil
	}
	candidates, err := r.search(ctx, ns, names, false)
	if err != nil {
		return nil, err
	}
	return candidates.Graph, nil
}

func (r *Repository) SearchGraph(ctx context.Context, ns types.NameSpace, query string) (*types.GraphSearchCandidates, error) {
	if r.db == nil {
		return nil, nil
	}
	if err := validateNamespace(ns, false); err != nil {
		return nil, err
	}
	if ns.Knowledge != "" {
		return nil, fmt.Errorf("public graph search requires exactly one knowledge base")
	}
	if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > types.GraphSearchMaxNameRunes {
		return nil, fmt.Errorf("invalid graph search query")
	}
	return r.search(ctx, ns, []string{query}, true)
}

// searchSQL performs seed ranking and one-hop expansion in ONE SQL statement,
// providing a consistent statement snapshot without a read/write transaction.
// Both endpoint scopes are checked. Joining each unique seed to start/end once
// makes every stored edge consume the row budget only once, including self-loops.
func (r *Repository) searchSQL(ns types.NameSpace, terms []string, public bool) (string, []any, error) {
	args := []any{ns.KnowledgeBase}
	if ns.Knowledge != "" {
		args = append(args, ns.Knowledge)
	}
	marks := []string{}
	contains := []string{}
	seen := map[string]bool{}
	for _, term := range terms {
		if term == "" || !validText(term) {
			return "", nil, fmt.Errorf("invalid graph entity query")
		}
		if seen[term] {
			continue
		}
		seen[term] = true
		mark := fmt.Sprintf("$%d", len(args)+1)
		args = append(args, term)
		marks = append(marks, mark)
		// Unlike LIKE, strpos treats %, _, quotes and backslashes as data.
		contains = append(contains, `strpos(ag_catalog.agtype_object_field_text(n.properties, 'name'), `+mark+`) > 0`)
	}
	if len(marks) == 0 {
		return "", nil, fmt.Errorf("empty graph entity queries")
	}
	maxSeeds, maxRows, seedProbe, rowProbe := chatMaxSeeds, chatMaxRows, chatMaxSeeds, chatMaxRows
	degree := ` AND EXISTS (SELECT 1 FROM ` + r.relations + ` e
		JOIN scoped_nodes a ON a.id = e.start_id JOIN scoped_nodes b ON b.id = e.end_id
		WHERE (e.start_id = n.id OR e.end_id = n.id)
		AND ` + edgeScope("e", "a", "b") + `)`
	seedRows := ""
	relationValue := `ag_catalog.agtype_object_field_text(edge_properties, 'rel_type')`
	// agtype::text casts scalar values only on Hologres. Use the type's
	// output function to serialize objects and lists as JSON text.
	projection := func(alias string) string { return `ag_catalog.agtype_out(` + alias + `.properties)::text` }
	if public {
		maxSeeds, maxRows = types.GraphSearchMaxSeeds, types.GraphSearchMaxRows
		seedProbe, rowProbe = maxSeeds+1, maxRows+1
		degree = "" // Public queries must preserve isolated entity seeds.
		relationValue = `left(ag_catalog.agtype_object_field_text(edge_properties, 'rel_type'), 129)`
		projection = func(alias string) string {
			return `ag_catalog.agtype_out(ag_catalog.agtype_object_field(` + alias + `.properties, '_search'))::text`
		}
		seedRows = `SELECT 0 AS kind, seed_index, id::text AS node_id, ` + projection("seeds") + ` AS node_payload,
			''::text AS neighbor_id, ''::text AS neighbor_payload, ''::text AS edge_id,
			''::text AS source_id, ''::text AS target_id, ''::text AS relation_type FROM seeds
			UNION ALL `
	}
	query := `WITH scoped_nodes AS (
		SELECT id, properties FROM ` + r.entities + ` n WHERE ` + scopePredicate("n", ns.Knowledge != "") + `
	), matched AS (
		SELECT n.*, CASE WHEN ag_catalog.agtype_object_field_text(n.properties, 'name') IN (` + strings.Join(marks, ",") + `) THEN 0 ELSE 1 END AS match_rank
		FROM scoped_nodes n WHERE (` + strings.Join(contains, " OR ") + `)` + degree + `
	), seeds AS (
		SELECT id, properties, row_number() OVER (ORDER BY match_rank,
			char_length(ag_catalog.agtype_object_field_text(properties, 'name')),
			ag_catalog.agtype_object_field_text(properties, 'name'), ag_catalog.agtype_object_field_text(properties, 'kg'), id) AS seed_index
		FROM matched ORDER BY match_rank, char_length(ag_catalog.agtype_object_field_text(properties, 'name')),
			ag_catalog.agtype_object_field_text(properties, 'name'), ag_catalog.agtype_object_field_text(properties, 'kg'), id
		LIMIT ` + fmt.Sprint(seedProbe) + `
	), selected_seeds AS (
		SELECT * FROM seeds WHERE seed_index <= ` + fmt.Sprint(maxSeeds) + `
	), edges AS (
		SELECT e.id AS edge_id, e.start_id, e.end_id, e.properties AS edge_properties,
			CASE WHEN t.seed_index IS NULL OR s.seed_index <= t.seed_index THEN a.id ELSE b.id END AS node_id,
			CASE WHEN t.seed_index IS NULL OR s.seed_index <= t.seed_index THEN ` + projection("a") + ` ELSE ` + projection("b") + ` END AS node_payload,
			CASE WHEN t.seed_index IS NULL OR s.seed_index <= t.seed_index THEN b.id ELSE a.id END AS neighbor_id,
			CASE WHEN t.seed_index IS NULL OR s.seed_index <= t.seed_index THEN ` + projection("b") + ` ELSE ` + projection("a") + ` END AS neighbor_payload,
			LEAST(COALESCE(s.seed_index, ` + fmt.Sprint(maxSeeds+1) + `), COALESCE(t.seed_index, ` + fmt.Sprint(maxSeeds+1) + `)) AS seed_index
		FROM ` + r.relations + ` e
		JOIN scoped_nodes a ON a.id = e.start_id JOIN scoped_nodes b ON b.id = e.end_id
		LEFT JOIN selected_seeds s ON s.id = e.start_id LEFT JOIN selected_seeds t ON t.id = e.end_id
		WHERE (s.id IS NOT NULL OR t.id IS NOT NULL) AND ` + edgeScope("e", "a", "b") + `
		ORDER BY seed_index, e.id LIMIT ` + fmt.Sprint(rowProbe) + `
	)
	` + seedRows + `SELECT 1 AS kind, seed_index, node_id::text, node_payload, neighbor_id::text, neighbor_payload,
		edge_id::text, start_id::text AS source_id, end_id::text AS target_id,
		` + relationValue + ` AS relation_type FROM edges
	ORDER BY kind, seed_index, edge_id`
	return query, args, nil
}

func edgeScope(edge, source, target string) string {
	return `ag_catalog.agtype_object_field_text(` + edge + `.properties, 'kb') = $1
		AND ag_catalog.agtype_object_field_text(` + edge + `.properties, 'kg') = ag_catalog.agtype_object_field_text(` + source + `.properties, 'kg')
		AND ag_catalog.agtype_object_field_text(` + source + `.properties, 'kg') = ag_catalog.agtype_object_field_text(` + target + `.properties, 'kg')`
}

func (r *Repository) search(ctx context.Context, ns types.NameSpace, terms []string, public bool) (*types.GraphSearchCandidates, error) {
	query, args, err := r.searchSQL(ns, terms, public)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, types.GraphSearchTimeout)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query Hologres graph: %w", err)
	}
	defer rows.Close()
	out := &types.GraphSearchCandidates{Graph: &types.GraphData{}, TruncationReasons: []string{}}
	seenNodes, seenEdges := map[string]bool{}, map[string]bool{}
	nodeNames := map[string]string{}
	seedCount, edgeCount := 0, 0
	for rows.Next() {
		var kind int
		var rank int64
		var nodeID, payload, neighborID, neighborPayload, edgeID, sourceID, targetID, relationType string
		if err := rows.Scan(&kind, &rank, &nodeID, &payload, &neighborID, &neighborPayload, &edgeID, &sourceID, &targetID, &relationType); err != nil {
			return nil, fmt.Errorf("decode graph query row: %w", err)
		}
		if kind == 0 {
			seedCount++
			if seedCount > types.GraphSearchMaxSeeds {
				markTruncation(out, "candidate_seeds")
				continue
			}
		} else if kind == 1 {
			edgeCount++
			if public && edgeCount > types.GraphSearchMaxRows {
				markTruncation(out, "candidate_relations")
				continue
			}
		} else {
			return nil, fmt.Errorf("invalid graph row kind")
		}
		if !seenNodes[nodeID] {
			node, err := decodeNode(nodeID, payload, ns, public)
			if err != nil {
				return nil, err
			}
			out.Graph.Node = append(out.Graph.Node, node)
			seenNodes[nodeID] = true
			nodeNames[nodeID] = node.Name
		}
		if kind == 0 {
			continue
		}
		if !seenNodes[neighborID] {
			node, err := decodeNode(neighborID, neighborPayload, ns, public)
			if err != nil {
				return nil, err
			}
			out.Graph.Node = append(out.Graph.Node, node)
			seenNodes[neighborID] = true
			nodeNames[neighborID] = node.Name
		}
		if edgeID == "" || sourceID == "" || targetID == "" || relationType == "" || !seenNodes[sourceID] || !seenNodes[targetID] {
			return nil, fmt.Errorf("invalid graph relationship endpoints")
		}
		if (sourceID != nodeID || targetID != neighborID) && (sourceID != neighborID || targetID != nodeID) {
			return nil, fmt.Errorf("graph relationship does not connect returned endpoints")
		}
		if !seenEdges[edgeID] {
			seenEdges[edgeID] = true
			out.Graph.Relation = append(out.Graph.Relation, &types.GraphRelation{
				ID: edgeID, SourceID: sourceID, TargetID: targetID,
				Node1: nodeNames[sourceID], Node2: nodeNames[targetID], Type: relationType,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read graph query: %w", err)
	}
	return out, nil
}

func markTruncation(out *types.GraphSearchCandidates, reason string) {
	for _, existing := range out.TruncationReasons {
		if existing == reason {
			return
		}
	}
	out.TruncationReasons = append(out.TruncationReasons, reason)
}

func decodeNode(id, payload string, ns types.NameSpace, public bool) (*types.GraphNode, error) {
	if id == "" {
		return nil, fmt.Errorf("graph entity has no identity")
	}
	var p nodeProjection
	if public {
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			return nil, fmt.Errorf("decode bounded graph projection: %w", err)
		}
		// Projections are written bounded. Reject external data violating that
		// contract, rather than returning unlimited data or guessing a total.
		if utf8.RuneCountInString(p.Name) > types.GraphSearchMaxNameRunes+1 || utf8.RuneCountInString(p.Knowledge) > 129 ||
			len(p.Chunks) > types.GraphSearchMaxEvidence+1 || len(p.Attributes) > types.GraphSearchMaxAttributes+1 {
			return nil, fmt.Errorf("unbounded AGE graph projection")
		}
		for _, c := range p.Chunks {
			if utf8.RuneCountInString(c) > 129 {
				return nil, fmt.Errorf("unbounded graph chunk identity")
			}
		}
		for _, a := range p.Attributes {
			if utf8.RuneCountInString(a) > types.GraphSearchMaxAttributeRunes+1 {
				return nil, fmt.Errorf("unbounded graph attribute")
			}
		}
	} else {
		var full nodeProperties
		if err := json.Unmarshal([]byte(payload), &full); err != nil {
			return nil, fmt.Errorf("decode AGE graph entity: %w", err)
		}
		if full.KB != ns.KnowledgeBase || (ns.Knowledge != "" && full.Knowledge != ns.Knowledge) {
			return nil, fmt.Errorf("invalid graph entity scope")
		}
		p = nodeProjection{Name: full.Name, Knowledge: full.Knowledge, Chunks: full.Chunks, Attributes: full.Attributes}
	}
	if p.Name == "" || p.Knowledge == "" {
		return nil, fmt.Errorf("graph entity has no name or document")
	}
	if p.Chunks == nil {
		p.Chunks = []string{}
	}
	if p.Attributes == nil {
		p.Attributes = []string{}
	}
	return &types.GraphNode{ID: id, Name: p.Name, KnowledgeID: p.Knowledge, Chunks: p.Chunks, Attributes: p.Attributes}, nil
}
