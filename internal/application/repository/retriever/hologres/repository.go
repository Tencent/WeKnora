package hologres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	entityLabel     = "entity"
	relationLabel   = "relation"
	deleteBatchSize = 1000
	writeTimeout    = 2 * time.Minute
)

// Repository uses a dedicated AGE graph with fixed labels. KB/document scope is
// stored as data, not labels, because Hologres does not support multiple labels.
// SQL accesses AGE's real vertex/edge tables; it does not create shadow graphs.
type Repository struct {
	db        *sql.DB
	graph     string
	entities  string
	relations string
}

var (
	_ interfaces.RetrieveGraphRepository = (*Repository)(nil)
	_ interfaces.GraphSearchRepository   = (*Repository)(nil)
)

// NewRepository binds fixed AGE labels to a validated graph identifier.
func NewRepository(db *sql.DB, graph string) (*Repository, error) {
	if !graphIdentifier.MatchString(graph) {
		return nil, fmt.Errorf("invalid Hologres graph identifier")
	}
	return &Repository{
		db: db, graph: graph,
		entities:  `"` + graph + `"."entity"`,
		relations: `"` + graph + `"."relation"`,
	}, nil
}

// EnsureSchema never modifies the primary WeKnora database. Auto-creation must
// be explicitly enabled; production can provision labels with a separate role.
func (r *Repository) EnsureSchema(ctx context.Context, create bool) error {
	if r.db == nil {
		return fmt.Errorf("hologres graph connection is not configured")
	}
	var exists bool
	if err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM hologres.hg_ag_label WHERE graph = $1)`, r.graph).Scan(&exists); err != nil {
		return fmt.Errorf("read AGE metadata (Hologres V5+ required): %w", err)
	}
	if !exists {
		if !create {
			return fmt.Errorf("AGE graph %q is missing; provision it or enable HOLOGRES_GRAPH_AUTO_CREATE", r.graph)
		}
		if _, err := r.db.ExecContext(ctx, `CALL ag_catalog.hg_age_create_graph($1)`, r.graph); err != nil {
			// A second application instance may have provisioned it concurrently.
			if checkErr := r.db.QueryRowContext(ctx,
				`SELECT EXISTS (SELECT 1 FROM hologres.hg_ag_label WHERE graph = $1)`, r.graph,
			).Scan(&exists); checkErr != nil || !exists {
				return fmt.Errorf("create AGE graph: %w", err)
			}
		}
	}
	for _, label := range []struct{ name, kind, procedure string }{
		{entityLabel, "v", "hg_age_create_vlabel"},
		{relationLabel, "e", "hg_age_create_elabel"},
	} {
		check := `SELECT EXISTS (SELECT 1 FROM hologres.hg_ag_label WHERE graph = $1 AND name = $2 AND kind = $3)`
		if err := r.db.QueryRowContext(ctx, check, r.graph, label.name, label.kind).Scan(&exists); err != nil {
			return fmt.Errorf("check AGE label: %w", err)
		}
		if exists {
			continue
		}
		if !create {
			return fmt.Errorf("AGE label %q is missing", label.name)
		}
		if _, err := r.db.ExecContext(ctx,
			`CALL ag_catalog.`+label.procedure+`($1, $2)`, r.graph, label.name,
		); err != nil {
			if checkErr := r.db.QueryRowContext(ctx,
				check, r.graph, label.name, label.kind,
			).Scan(&exists); checkErr != nil || !exists {
				return fmt.Errorf("create AGE label %q: %w", label.name, err)
			}
		}
	}
	return nil
}

// The bounded projection is maintained in the same atomic write as the full
// properties. Public queries fetch only this projection, never unlimited lists.
type nodeProperties struct {
	Name       string          `json:"name"`
	KB         string          `json:"kb"`
	Knowledge  string          `json:"kg"`
	Chunks     []string        `json:"chunks"`
	Attributes []string        `json:"attributes"`
	Projection *nodeProjection `json:"_search,omitempty"`
}

type nodeProjection struct {
	Name       string   `json:"name"`
	Knowledge  string   `json:"knowledge_id"`
	Chunks     []string `json:"chunks"`
	Attributes []string `json:"attributes"`
}

func clip(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

func boundedList(values []string, maxItems, maxRunes int) []string {
	out := make([]string, 0, min(len(values), maxItems))
	for _, value := range values {
		if len(out) == maxItems {
			break
		}
		out = append(out, clip(value, maxRunes))
	}
	return out
}

func (p *nodeProperties) encode() ([]byte, error) {
	p.Projection = &nodeProjection{
		Name:       clip(p.Name, types.GraphSearchMaxNameRunes+1),
		Knowledge:  clip(p.Knowledge, 129),
		Chunks:     boundedList(p.Chunks, types.GraphSearchMaxEvidence+1, 129),
		Attributes: boundedList(p.Attributes, types.GraphSearchMaxAttributes+1, types.GraphSearchMaxAttributeRunes+1),
	}
	return json.Marshal(p)
}

func validText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func validateNamespace(ns types.NameSpace, documentRequired bool) error {
	if ns.KnowledgeBase == "" || !validText(ns.KnowledgeBase) || !validText(ns.Knowledge) ||
		(documentRequired && ns.Knowledge == "") {
		return fmt.Errorf("graph operations require a valid knowledge base%s",
			map[bool]string{true: " and document", false: ""}[documentRequired])
	}
	return nil
}

// prepareGraph validates before any write, merges chunks without losing order,
// and creates endpoint placeholders like apoc.merge.node. Attributes are set on
// creation only, matching the original Neo4j adapter's onCreate properties.
func prepareGraph(ns types.NameSpace, graph *types.GraphData) ([]*nodeProperties, error) {
	if graph == nil {
		return nil, fmt.Errorf("nil graph")
	}
	byName := map[string]*nodeProperties{}
	add := func(name string) (*nodeProperties, error) {
		if name == "" || !validText(name) {
			return nil, fmt.Errorf("graph entity must have a valid nonempty name")
		}
		if p := byName[name]; p != nil {
			return p, nil
		}
		p := &nodeProperties{
			Name: name, KB: ns.KnowledgeBase, Knowledge: ns.Knowledge, Chunks: []string{}, Attributes: []string{},
		}
		byName[name] = p
		return p, nil
	}
	for _, node := range graph.Node {
		if node == nil {
			return nil, fmt.Errorf("nil graph entity")
		}
		_, existed := byName[node.Name]
		p, err := add(node.Name)
		if err != nil {
			return nil, err
		}
		for _, value := range append(append([]string{}, node.Chunks...), node.Attributes...) {
			if !validText(value) {
				return nil, fmt.Errorf("invalid graph property text")
			}
		}
		if !existed {
			p.Attributes = append([]string{}, node.Attributes...)
		}
		p.Chunks = unionStrings(p.Chunks, node.Chunks)
	}
	for _, rel := range graph.Relation {
		if rel == nil || rel.Type == "" || !validText(rel.Type) {
			return nil, fmt.Errorf("graph relationship must have a valid nonempty type")
		}
		if _, err := add(rel.Node1); err != nil {
			return nil, err
		}
		if _, err := add(rel.Node2); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*nodeProperties, 0, len(names))
	for _, name := range names {
		out = append(out, byName[name])
	}
	return out, nil
}

func unionStrings(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	seen := map[string]bool{}
	for _, values := range [][]string{a, b} {
		for _, value := range values {
			if !seen[value] {
				seen[value] = true
				out = append(out, value)
			}
		}
	}
	return out
}

// AddGraph atomically merges each graph while preserving AGE identities and provenance.
func (r *Repository) AddGraph(ctx context.Context, ns types.NameSpace, graphs []*types.GraphData) error {
	if r.db == nil {
		return nil // Match the disabled Neo4j adapter.
	}
	if err := validateNamespace(ns, true); err != nil {
		return err
	}
	prepared := make([][]*nodeProperties, len(graphs))
	for i, graph := range graphs {
		var err error
		if prepared[i], err = prepareGraph(ns, graph); err != nil {
			return err
		}
	}
	for i, graph := range graphs {
		if len(prepared[i]) == 0 {
			continue
		}
		if err := r.addGraph(ctx, ns, graph, prepared[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) addGraph(
	ctx context.Context, ns types.NameSpace, graph *types.GraphData, nodes []*nodeProperties,
) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	conn, tx, err := r.beginWrite(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	defer func() { _ = tx.Rollback() }() // Preserve the operation error; committed transactions are already closed.

	// The first table access takes Hologres's transaction table lock. Later
	// reads see this transaction's writes; concurrent graph writers serialize.
	existing, err := r.loadNodes(ctx, tx, ns, nodes)
	if err != nil {
		return err
	}
	ids := map[string]string{}
	for _, p := range nodes {
		stored := existing[p.Name]
		if stored != nil {
			p.Chunks = unionStrings(stored.props.Chunks, p.Chunks)
			p.Attributes = stored.props.Attributes
		}
		payload, err := p.encode()
		if err != nil {
			return err
		}
		if stored != nil {
			_, err = tx.ExecContext(ctx, `UPDATE `+r.entities+`
				SET properties = CAST($1 AS ag_catalog.agtype) WHERE id::text = $2`, string(payload), stored.id)
			ids[p.Name] = stored.id
		} else {
			// IDs are generated by AGE. Never supply a Neo4j or application ID.
			_, err = tx.ExecContext(ctx, `INSERT INTO `+r.entities+`
				(properties) VALUES (CAST($1 AS ag_catalog.agtype))`, string(payload))
			if err == nil {
				var id string // Keep AGE identities out of floating-point JSON decoding.
				err = tx.QueryRowContext(ctx, `SELECT id::text FROM `+r.entities+` WHERE `+scopePredicate("", true)+`
					AND ag_catalog.agtype_object_field_text(properties, 'name') = $3`,
					ns.KnowledgeBase, ns.Knowledge, p.Name).Scan(&id)
				if err == nil && id == "" {
					err = fmt.Errorf("empty AGE identity")
				}
				ids[p.Name] = id
			}
		}
		if err != nil {
			return fmt.Errorf("write graph entity: %w", err)
		}
	}
	seen := map[string]bool{}
	for _, rel := range graph.Relation {
		key, _ := json.Marshal([]string{rel.Node1, rel.Node2, rel.Type})
		if seen[string(key)] {
			continue
		}
		seen[string(key)] = true
		props, _ := json.Marshal(map[string]string{"kb": ns.KnowledgeBase, "kg": ns.Knowledge, "rel_type": rel.Type})
		query := `INSERT INTO ` + r.relations + ` (start_id, end_id, properties)
			SELECT s.id, t.id, CAST($3 AS ag_catalog.agtype) FROM ` + r.entities + ` s, ` + r.entities + ` t
			WHERE s.id::text = $1 AND t.id::text = $2
			AND NOT EXISTS (SELECT 1 FROM ` + r.relations + ` e WHERE e.start_id = s.id AND e.end_id = t.id
			AND ag_catalog.agtype_object_field_text(e.properties, 'rel_type') = $4)`
		if _, err := tx.ExecContext(ctx, query, ids[rel.Node1], ids[rel.Node2], string(props), rel.Type); err != nil {
			return fmt.Errorf("write graph relationship: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit graph: %w", err)
	}
	return nil
}

type storedNode struct {
	id    string
	props nodeProperties
}

func (r *Repository) loadNodes(
	ctx context.Context, tx *sql.Tx, ns types.NameSpace, nodes []*nodeProperties,
) (map[string]*storedNode, error) {
	args := []any{ns.KnowledgeBase, ns.Knowledge}
	marks := make([]string, len(nodes))
	for i, node := range nodes {
		marks[i] = fmt.Sprintf("$%d", len(args)+1)
		args = append(args, node.Name)
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id::text, ag_catalog.agtype_out(properties)::text FROM `+r.entities+`
		WHERE `+scopePredicate("", true)+`
		AND ag_catalog.agtype_object_field_text(properties, 'name') IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // Preserve any query or decoding error.
	out := map[string]*storedNode{}
	for rows.Next() {
		var node storedNode
		var payload string
		if err := rows.Scan(&node.id, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &node.props); err != nil {
			return nil, fmt.Errorf("decode stored graph entity: %w", err)
		}
		if node.id == "" || node.props.Name == "" || node.props.KB != ns.KnowledgeBase ||
			node.props.Knowledge != ns.Knowledge {
			return nil, fmt.Errorf("invalid stored graph entity scope")
		}
		if out[node.props.Name] != nil {
			return nil, fmt.Errorf("duplicate graph entity identity in document")
		}
		out[node.props.Name] = &node
	}
	return out, rows.Err()
}

func scopePredicate(alias string, document bool) string {
	if alias != "" {
		alias += "."
	}
	query := `ag_catalog.agtype_object_field_text(` + alias + `properties, 'kb') = $1`
	if document {
		query += ` AND ag_catalog.agtype_object_field_text(` + alias + `properties, 'kg') = $2`
	}
	return query
}

// DelGraph removes scoped entities and attached edges in bounded transactions.
func (r *Repository) DelGraph(ctx context.Context, namespaces []types.NameSpace) error {
	if r.db == nil {
		return nil
	}
	for _, ns := range namespaces {
		if err := validateNamespace(ns, false); err != nil {
			return err
		}
	}
	seen := map[types.NameSpace]bool{}
	for _, ns := range namespaces {
		if seen[ns] {
			continue
		}
		seen[ns] = true
		for {
			count, err := r.deleteBatch(ctx, ns)
			if err != nil {
				return err
			}
			if count == 0 {
				break
			}
		}
	}
	return nil
}

func (r *Repository) deleteBatch(ctx context.Context, ns types.NameSpace) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	conn, tx, err := r.beginWrite(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	defer func() { _ = tx.Rollback() }() // Preserve the operation error; committed transactions are already closed.
	args := []any{ns.KnowledgeBase}
	if ns.Knowledge != "" {
		args = append(args, ns.Knowledge)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM `+r.entities+`
		WHERE `+scopePredicate("", ns.Knowledge != "")+` ORDER BY id LIMIT 1000`, args...)
	if err != nil {
		return 0, err
	}
	ids := []any{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close() // Preserve the scan error if cleanup also fails.
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close() // Preserve the iteration error if cleanup also fails.
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	marks := make([]string, len(ids))
	for i := range ids {
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	list := strings.Join(marks, ",")
	// SQL DELETE does not cascade. Remove all attached edges, even malformed
	// cross-scope edges, while leaving their other endpoints untouched.
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+r.relations+`
		WHERE start_id::text IN (`+list+`) OR end_id::text IN (`+list+`)`, ids...); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+r.entities+` WHERE id::text IN (`+list+`)`, ids...); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}
