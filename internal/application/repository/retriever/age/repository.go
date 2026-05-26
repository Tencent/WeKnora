package age

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// cacheEntry represents a cached query result with expiration
type cacheEntry struct {
	data      *types.GraphData
	timestamp time.Time
}

// AGERepository implements RetrieveGraphRepository interface using Apache AGE
type AGERepository struct {
	db          *sql.DB
	graphName   string
	nodePrefix  string
	queryCache  sync.Map      // Query result cache: key=cache key, value=*cacheEntry
	cacheTTL    time.Duration // Cache time-to-live
	cacheMaxAge time.Duration // Maximum cache age before cleanup
}

// NewAGERepository creates a new AGE Repository instance
func NewAGERepository(db *sql.DB, graphName string) interfaces.RetrieveGraphRepository {
	repo := &AGERepository{
		db:          db,
		graphName:   graphName,
		nodePrefix:  "ENTITY",
		cacheTTL:    5 * time.Minute,  // Cache entries expire after 5 minutes
		cacheMaxAge: 10 * time.Minute, // Clean up entries older than 10 minutes
	}

	// Start background cache cleanup goroutine
	go repo.cleanupExpiredCache()

	return repo
}

// cleanupExpiredCache periodically removes expired cache entries
func (r *AGERepository) cleanupExpiredCache() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		r.queryCache.Range(func(key, value interface{}) bool {
			entry := value.(*cacheEntry)
			if now.Sub(entry.timestamp) > r.cacheMaxAge {
				r.queryCache.Delete(key)
			}
			return true
		})
	}
}

// removeHyphen removes hyphens from a string (AGE label constraint)
func removeHyphen(s string) string {
	return strings.ReplaceAll(s, "-", "_")
}

// sanitizeLabel ensures the label name is valid for AGE
// AGE labels must start with a letter and contain only letters, numbers, and underscores
func sanitizeLabel(s string) string {
	// Remove hyphens
	s = strings.ReplaceAll(s, "-", "_")
	// Remove any other invalid characters
	var result strings.Builder
	for i, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9' && i > 0) || r == '_' {
			result.WriteRune(r)
		}
	}
	// Ensure it starts with a letter
	res := result.String()
	if len(res) > 0 && !((res[0] >= 'a' && res[0] <= 'z') || (res[0] >= 'A' && res[0] <= 'Z')) {
		res = "L" + res
	}
	return res
}

// getLabel generates a label string for the given namespace
func (r *AGERepository) getLabel(namespace types.NameSpace) string {
	labels := namespace.Labels()
	result := make([]string, 0, len(labels))
	for _, label := range labels {
		sanitized := sanitizeLabel(label)
		result = append(result, r.nodePrefix+"_"+sanitized)
	}
	finalLabel := strings.Join(result, "_")
	// AGE has a label name length limit (63 characters for PostgreSQL identifiers)
	if len(finalLabel) > 63 {
		finalLabel = finalLabel[:63]
	}
	return finalLabel
}

// initAGE initializes AGE extension in the current session
func (r *AGERepository) initAGE(ctx context.Context, conn interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}) error {
	// Load AGE extension
	_, err := conn.ExecContext(ctx, "LOAD 'age'")
	if err != nil {
		// Extension might already be loaded, log and continue
		logger.Debugf(ctx, "Load AGE extension: %v", err)
	}

	// Set search path to include ag_catalog
	_, err = conn.ExecContext(ctx, `SET search_path = ag_catalog, "$user", public`)
	if err != nil {
		return fmt.Errorf("failed to set search_path: %w", err)
	}

	return nil
}

// ensureGraph ensures the graph exists, creating it if necessary
func (r *AGERepository) ensureGraph(ctx context.Context) error {
	// Check if graph exists
	var exists bool
	err := r.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM ag_catalog.ag_graph WHERE name = $1)",
		r.graphName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check graph existence: %w", err)
	}

	if !exists {
		// Create graph
		_, err = r.db.ExecContext(ctx, fmt.Sprintf("SELECT create_graph('%s')", r.graphName))
		if err != nil {
			return fmt.Errorf("failed to create graph: %w", err)
		}
		logger.Infof(ctx, "Created graph: %s", r.graphName)
	}

	// Create indices to accelerate queries
	if err := r.createIndices(ctx); err != nil {
		logger.Warnf(ctx, "Failed to create indices: %v", err)
		// Don't return error, index creation failure should not block system operation
	}

	return nil
}

// createIndices creates indices for graph queries
func (r *AGERepository) createIndices(ctx context.Context) error {
	// Get graph schema
	var schemaName string
	err := r.db.QueryRowContext(ctx,
		"SELECT nspname FROM ag_catalog.ag_graph g JOIN pg_namespace n ON g.namespace = n.oid WHERE g.name = $1",
		r.graphName).Scan(&schemaName)
	if err != nil {
		return fmt.Errorf("failed to get graph schema: %w", err)
	}

	// Create indices for all vertex tables
	indexQuery := fmt.Sprintf(`
		DO $$
		DECLARE
			label_rec RECORD;
			index_name TEXT;
			table_exists BOOLEAN;
		BEGIN
			FOR label_rec IN
				SELECT name FROM ag_catalog.ag_label
				WHERE graph = (SELECT graphid FROM ag_catalog.ag_graph WHERE name = '%s')
				AND kind = 'v'
			LOOP
				-- Check if table exists
				SELECT EXISTS (
					SELECT FROM pg_tables
					WHERE schemaname = '%s' AND tablename = label_rec.name
				) INTO table_exists;

				IF table_exists THEN
					-- Create index on name property
					index_name := '%s_' || label_rec.name || '_name_idx';
					BEGIN
						EXECUTE format('CREATE INDEX IF NOT EXISTS %%I ON %%I.%%I USING btree ((properties->>''name''))',
							index_name, '%s', label_rec.name);
					EXCEPTION WHEN OTHERS THEN
						RAISE NOTICE 'Failed to create index %%: %%', index_name, SQLERRM;
					END;

					-- Create lowercase index for case-insensitive queries
					index_name := '%s_' || label_rec.name || '_name_lower_idx';
					BEGIN
						EXECUTE format('CREATE INDEX IF NOT EXISTS %%I ON %%I.%%I USING btree (LOWER(properties->>''name''))',
							index_name, '%s', label_rec.name);
					EXCEPTION WHEN OTHERS THEN
						RAISE NOTICE 'Failed to create lowercase index %%: %%', index_name, SQLERRM;
					END;

					-- Create index on kg property for faster filtering
					index_name := '%s_' || label_rec.name || '_kg_idx';
					BEGIN
						EXECUTE format('CREATE INDEX IF NOT EXISTS %%I ON %%I.%%I USING btree ((properties->>''kg''))',
							index_name, '%s', label_rec.name);
					EXCEPTION WHEN OTHERS THEN
						RAISE NOTICE 'Failed to create kg index %%: %%', index_name, SQLERRM;
					END;
				END IF;
			END LOOP;
		END $$;
	`, r.graphName, schemaName, r.graphName, schemaName, r.graphName, schemaName, r.graphName, schemaName)

	_, err = r.db.ExecContext(ctx, indexQuery)
	if err != nil {
		return fmt.Errorf("failed to create indices: %w", err)
	}

	logger.Infof(ctx, "Created indices for graph: %s", r.graphName)
	return nil
}

// ensureVertexLabel ensures a vertex label exists in the graph
func (r *AGERepository) ensureVertexLabel(ctx context.Context, tx *sql.Tx, label string) error {
	logger.Debugf(ctx, "Ensuring vertex label: %s (length: %d)", label, len(label))

	// Check if label exists
	var exists bool
	query := `SELECT EXISTS(
		SELECT 1 FROM ag_catalog.ag_label
		WHERE graph = (SELECT graphid FROM ag_catalog.ag_graph WHERE name = $1)
		AND name = $2
	)`
	err := tx.QueryRowContext(ctx, query, r.graphName, label).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check label existence: %w", err)
	}

	if !exists {
		// Create vertex label
		createQuery := fmt.Sprintf("SELECT create_vlabel('%s', '%s')", r.graphName, label)
		_, err = tx.ExecContext(ctx, createQuery)
		if err != nil {
			return fmt.Errorf("failed to create vertex label: %w", err)
		}
		logger.Debugf(ctx, "Created vertex label: %s", label)
	}

	return nil
}

// AddGraph adds graph data to AGE
func (r *AGERepository) AddGraph(ctx context.Context, namespace types.NameSpace, graphs []*types.GraphData) error {
	if r.db == nil {
		logger.Warnf(ctx, "AGE database connection is nil")
		return nil
	}

	// Initialize AGE extension
	if err := r.initAGE(ctx, r.db); err != nil {
		return err
	}

	// Ensure graph exists
	if err := r.ensureGraph(ctx); err != nil {
		return err
	}

	label := r.getLabel(namespace)

	for _, graph := range graphs {
		if err := r.addGraphData(ctx, namespace, label, graph); err != nil {
			return err
		}
	}

	return nil
}

// addGraphData adds a single graph data to AGE
func (r *AGERepository) addGraphData(ctx context.Context, namespace types.NameSpace, label string, graph *types.GraphData) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Initialize AGE in transaction
	if err := r.initAGE(ctx, tx); err != nil {
		return err
	}

	// Ensure vertex label exists
	if err := r.ensureVertexLabel(ctx, tx, label); err != nil {
		return err
	}

	// Batch add nodes
	if err := r.addNodesBatch(ctx, tx, label, namespace.Knowledge, graph.Node); err != nil {
		return err
	}

	// Batch add relationships
	if err := r.addRelationshipsBatch(ctx, tx, label, namespace.Knowledge, graph.Relation); err != nil {
		return err
	}

	return tx.Commit()
}

// addNodesBatch adds multiple nodes in batch
func (r *AGERepository) addNodesBatch(ctx context.Context, tx *sql.Tx, label, knowledgeID string, nodes []*types.GraphNode) error {
	if len(nodes) == 0 {
		return nil
	}

	logger.Debugf(ctx, "Batch adding %d nodes to label %s", len(nodes), label)

	// Process in batches to avoid transaction size limits
	batchSize := 50
	for i := 0; i < len(nodes); i += batchSize {
		end := i + batchSize
		if end > len(nodes) {
			end = len(nodes)
		}
		batch := nodes[i:end]

		// Build batch MERGE statements with unique variable names
		var mergeStatements []string
		for idx, node := range batch {
			attributesJSON, _ := json.Marshal(node.Attributes)
			chunksJSON, _ := json.Marshal(node.Chunks)

			// Use unique variable name for each node
			varName := fmt.Sprintf("n%d", idx)
			stmt := fmt.Sprintf(
				"MERGE (%s:%s {name: '%s', kg: '%s'}) SET %s.attributes = '%s', %s.chunks = '%s'",
				varName, label,
				escapeCypherString(node.Name),
				escapeCypherString(knowledgeID),
				varName, escapeCypherString(string(attributesJSON)),
				varName, escapeCypherString(string(chunksJSON)),
			)
			mergeStatements = append(mergeStatements, stmt)
		}

		// Execute batch
		cypherQuery := fmt.Sprintf(`
			SELECT * FROM cypher('%s', $$
				%s
				RETURN count(*)
			$$) AS (count agtype)
		`, r.graphName, strings.Join(mergeStatements, "\n"))

		_, err := tx.ExecContext(ctx, cypherQuery)
		if err != nil {
			logger.Errorf(ctx, "Failed to batch create nodes (batch %d-%d): %v", i, end, err)
			return fmt.Errorf("failed to batch create nodes: %w", err)
		}

		logger.Debugf(ctx, "Successfully added nodes batch %d-%d", i, end)
	}

	logger.Infof(ctx, "Successfully batch added %d nodes", len(nodes))
	return nil
}

// addRelationshipsBatch adds multiple relationships in batch
// Note: Apache AGE does not support multiple independent MATCH-MERGE sequences in a single query,
// so we execute each relationship creation individually within the same transaction.
func (r *AGERepository) addRelationshipsBatch(ctx context.Context, tx *sql.Tx, label, knowledgeID string, relations []*types.GraphRelation) error {
	if len(relations) == 0 {
		return nil
	}

	logger.Debugf(ctx, "Batch adding %d relationships to label %s", len(relations), label)

	// Collect unique edge labels
	edgeLabels := make(map[string]bool)
	for _, rel := range relations {
		relType := sanitizeRelationType(rel.Type)
		edgeLabels[relType] = true
	}

	// Ensure all edge labels exist
	for edgeLabel := range edgeLabels {
		if err := r.ensureEdgeLabel(ctx, tx, edgeLabel); err != nil {
			return err
		}
	}

	successCount := 0
	failCount := 0

	// Execute each relationship creation individually
	// AGE doesn't support multiple independent MATCH-MERGE sequences in one query
	for _, rel := range relations {
		relType := sanitizeRelationType(rel.Type)

		cypherQuery := fmt.Sprintf(`
			SELECT * FROM cypher('%s', $$
				MATCH (a:%s {name: '%s', kg: '%s'})
				MATCH (b:%s {name: '%s', kg: '%s'})
				MERGE (a)-[r:%s]->(b)
				RETURN r
			$$) AS (r agtype)
		`, r.graphName, label,
			escapeCypherString(rel.Node1),
			escapeCypherString(knowledgeID),
			label,
			escapeCypherString(rel.Node2),
			escapeCypherString(knowledgeID),
			relType)

		_, err := tx.ExecContext(ctx, cypherQuery)
		if err != nil {
			logger.Warnf(ctx, "Failed to create relationship %s->%s: %v", rel.Node1, rel.Node2, err)
			failCount++
			// Continue with other relationships
		} else {
			successCount++
		}
	}

	logger.Infof(ctx, "Relationship creation completed: %d succeeded, %d failed", successCount, failCount)
	return nil
}

// addNode adds a single node to the graph
func (r *AGERepository) addNode(ctx context.Context, tx *sql.Tx, label, knowledgeID string, node *types.GraphNode) error {
	// Serialize attributes and chunks to JSON arrays
	attributesJSON, err := json.Marshal(node.Attributes)
	if err != nil {
		attributesJSON = []byte("[]")
	}
	chunksJSON, err := json.Marshal(node.Chunks)
	if err != nil {
		chunksJSON = []byte("[]")
	}

	// AGE doesn't support ON CREATE SET / ON MATCH SET syntax
	// Use simple MERGE with SET for all properties
	cypherQuery := fmt.Sprintf(`
		SELECT * FROM cypher('%s', $$
			MERGE (n:%s {name: '%s', kg: '%s'})
			SET n.attributes = '%s', n.chunks = '%s'
			RETURN n
		$$) AS (n agtype)
	`, r.graphName, label,
		escapeCypherString(node.Name),
		escapeCypherString(knowledgeID),
		escapeCypherString(string(attributesJSON)),
		escapeCypherString(string(chunksJSON)))

	_, err = tx.ExecContext(ctx, cypherQuery)
	if err != nil {
		logger.Errorf(ctx, "Failed to create node %s: %v", node.Name, err)
		return fmt.Errorf("failed to create node: %w", err)
	}

	return nil
}

// addRelationship adds a relationship between two nodes
func (r *AGERepository) addRelationship(ctx context.Context, tx *sql.Tx, label, knowledgeID string, rel *types.GraphRelation) error {
	// Ensure edge label exists
	relType := sanitizeRelationType(rel.Type)
	if err := r.ensureEdgeLabel(ctx, tx, relType); err != nil {
		return err
	}

	cypherQuery := fmt.Sprintf(`
		SELECT * FROM cypher('%s', $$
			MATCH (a:%s {name: '%s', kg: '%s'})
			MATCH (b:%s {name: '%s', kg: '%s'})
			MERGE (a)-[r:%s]->(b)
			RETURN r
		$$) AS (r agtype)
	`, r.graphName, label,
		escapeCypherString(rel.Node1),
		escapeCypherString(knowledgeID),
		label,
		escapeCypherString(rel.Node2),
		escapeCypherString(knowledgeID),
		relType)

	_, err := tx.ExecContext(ctx, cypherQuery)
	if err != nil {
		return fmt.Errorf("failed to create relationship: %w", err)
	}

	return nil
}

// ensureEdgeLabel ensures an edge label exists in the graph
func (r *AGERepository) ensureEdgeLabel(ctx context.Context, tx *sql.Tx, label string) error {
	// Check if edge label exists
	var exists bool
	query := `SELECT EXISTS(
		SELECT 1 FROM ag_catalog.ag_label
		WHERE graph = (SELECT graphid FROM ag_catalog.ag_graph WHERE name = $1)
		AND name = $2 AND kind = 'e'
	)`
	err := tx.QueryRowContext(ctx, query, r.graphName, label).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check edge label existence: %w", err)
	}

	if !exists {
		// Create edge label
		createQuery := fmt.Sprintf("SELECT create_elabel('%s', '%s')", r.graphName, label)
		_, err = tx.ExecContext(ctx, createQuery)
		if err != nil {
			return fmt.Errorf("failed to create edge label: %w", err)
		}
		logger.Debugf(ctx, "Created edge label: %s", label)
	}

	return nil
}

// DelGraph deletes graph data from AGE
func (r *AGERepository) DelGraph(ctx context.Context, namespaces []types.NameSpace) error {
	if r.db == nil {
		logger.Warnf(ctx, "AGE database connection is nil")
		return nil
	}

	// Initialize AGE extension
	if err := r.initAGE(ctx, r.db); err != nil {
		return err
	}

	// 检查context是否已经取消
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// 批量构建删除条件
	for _, namespace := range namespaces {
		// 检查每次循环时context状态
		if ctx.Err() != nil {
			logger.Warnf(ctx, "Context canceled during deletion, processed some namespaces")
			return ctx.Err()
		}

		label := r.getLabel(namespace)
		escapedKg := escapeCypherString(namespace.Knowledge)

		// 方案1: 合并为单个查询，同时删除节点和关系
		// DETACH DELETE会自动删除节点及其所有关系
		deleteQuery := fmt.Sprintf(`
			SELECT * FROM cypher('%s', $$
				MATCH (n:%s {kg: '%s'})
				DETACH DELETE n
				RETURN count(n)
			$$) AS (count agtype)
		`, r.graphName, label, escapedKg)

		_, err := r.db.ExecContext(ctx, deleteQuery)
		if err != nil {
			// 记录具体的namespace信息便于排查
			logger.Errorf(ctx, "Failed to delete graph for namespace %s (label: %s): %v",
				namespace.Knowledge, label, err)
			// 根据业务需求决定是否继续还是返回错误
			// return fmt.Errorf("failed to delete graph for %s: %w", namespace.Knowledge, err)
		} else {
			logger.Debugf(ctx, "Successfully deleted graph for namespace: %s", namespace.Knowledge)
		}
	}

	logger.Infof(ctx, "Completed graph deletion for %d namespaces", len(namespaces))
	return nil
}

// SearchNode searches for nodes and their relationships (Optimized for nodes >> labels)
func (r *AGERepository) SearchNode(
	ctx context.Context,
	namespace types.NameSpace,
	nodes []string,
) (*types.GraphData, error) {
	if r.db == nil {
		logger.Warnf(ctx, "AGE database connection is nil")
		return nil, nil
	}

	// Generate cache key
	cacheKey := r.generateCacheKey(namespace, nodes)

	// Check cache first
	if cached, ok := r.queryCache.Load(cacheKey); ok {
		entry := cached.(*cacheEntry)
		// Check if cache is still valid
		if time.Since(entry.timestamp) < r.cacheTTL {
			logger.Debugf(ctx, "Using cached query result for %d nodes (cache age: %v)",
				len(nodes), time.Since(entry.timestamp))
			return entry.data, nil
		}
		// Cache expired, remove it
		r.queryCache.Delete(cacheKey)
	}

	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer conn.Close()

	if err := r.initAGEConn(ctx, conn); err != nil {
		return nil, err
	}

	kbPrefix := r.nodePrefix + "_" + sanitizeLabel(namespace.KnowledgeBase)
	labels, err := r.getLabelsWithPrefix(ctx, conn, kbPrefix)
	if err != nil {
		logger.Warnf(ctx, "Failed to get labels with prefix %s: %v", kbPrefix, err)
	}

	if len(labels) == 0 {
		logger.Debugf(ctx, "No labels found with prefix %s", kbPrefix)
		return &types.GraphData{}, nil
	}

	logger.Debugf(ctx, "Searching %d nodes across %d labels", len(nodes), len(labels))

	// 动态决策
	strategy := r.chooseStrategy(len(nodes), len(labels))

	var result *types.GraphData
	switch strategy {
	case "labels":
		result, err = r.searchByLabels(ctx, conn, labels, nodes)
	case "nodes":
		result, err = r.searchByNodes(ctx, conn, nodes, labels)
	default:
		result, err = r.searchByLabels(ctx, conn, labels, nodes)
	}

	if err != nil {
		return nil, err
	}

	// Store result in cache
	r.queryCache.Store(cacheKey, &cacheEntry{
		data:      result,
		timestamp: time.Now(),
	})
	logger.Debugf(ctx, "Cached query result for %d nodes", len(nodes))

	return result, nil
}

// generateCacheKey generates a cache key for the query
func (r *AGERepository) generateCacheKey(namespace types.NameSpace, nodes []string) string {
	// Sort nodes to ensure consistent cache keys
	sortedNodes := make([]string, len(nodes))
	copy(sortedNodes, nodes)
	sort.Strings(sortedNodes)

	// Create a unique key based on namespace and nodes
	key := fmt.Sprintf("%s:%s:%s", namespace.KnowledgeBase, namespace.Knowledge, strings.Join(sortedNodes, ","))

	// Hash the key to keep it short
	h := sha256.New()
	h.Write([]byte(key))
	return hex.EncodeToString(h.Sum(nil))
}

func (r *AGERepository) chooseStrategy(nodeCount, labelCount int) string {
	// 规则1: 如果labels少很多，优先遍历labels
	if labelCount < nodeCount/3 {
		return "labels"
	}

	// 规则2: 如果nodes少很多，遍历nodes
	if nodeCount < labelCount/3 {
		return "nodes"
	}

	// 规则3: 数量接近，默认labels（索引友好）
	return "labels"
}

// searchByNodes 按node分组查询，每个node一次批量查询所有标签
func (r *AGERepository) searchByNodes(
	ctx context.Context,
	conn *sql.Conn,
	labels []string,
	searchTexts []string,
) (*types.GraphData, error) {
	// 优化2: 使用 struct{}{} 减少内存占用
	nodeSeen := make(map[string]struct{})
	relSeen := make(map[string]struct{})

	graphData := &types.GraphData{
		Node:     make([]*types.GraphNode, 0, len(searchTexts)*2),
		Relation: make([]*types.GraphRelation, 0, len(searchTexts)*2),
	}

	// 优化3: 构建批量查询 - 使用 UNION ALL 合并多个查询
	cypherQuery := r.buildBatchQuery(labels, searchTexts)

	logger.Debugf(ctx, "Executing batch query: %s", cypherQuery)

	rows, err := conn.QueryContext(ctx, cypherQuery)
	if err != nil {
		return nil, fmt.Errorf("batch query failed: %w", err)
	}
	defer rows.Close()

	// 优化4: 单次遍历处理所有结果
	for rows.Next() {
		var nJSON, rJSON, mJSON string
		if err := rows.Scan(&nJSON, &rJSON, &mJSON); err != nil {
			logger.Errorf(ctx, "Failed to scan row: %v", err)
			continue
		}

		nNode := parseAgtypeNode(nJSON)
		mNode := parseAgtypeNode(mJSON)

		// 添加节点（去重）
		if nNode != nil {
			if _, exists := nodeSeen[nNode.Name]; !exists {
				nodeSeen[nNode.Name] = struct{}{}
				graphData.Node = append(graphData.Node, nNode)
			}
		}
		if mNode != nil {
			if _, exists := nodeSeen[mNode.Name]; !exists {
				nodeSeen[mNode.Name] = struct{}{}
				graphData.Node = append(graphData.Node, mNode)
			}
		}

		// 添加关系（去重）
		if nNode != nil && mNode != nil {
			rel := parseAgtypeRelation(rJSON, nNode, mNode)
			if rel != nil {
				relKey := fmt.Sprintf("%s-%s-%s", rel.Node1, rel.Type, rel.Node2)
				if _, exists := relSeen[relKey]; !exists {
					relSeen[relKey] = struct{}{}
					graphData.Relation = append(graphData.Relation, rel)
				}
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return graphData, nil
}

func (r *AGERepository) buildBatchQuery(labels []string, searchTexts []string) string {
	var queries []string

	for _, label := range labels {
		var conditions []string
		for _, text := range searchTexts {
			escapedText := escapeCypherString(text)
			conditions = append(conditions,
				fmt.Sprintf("toLower(n.name) = toLower('%s')", escapedText))
		}

		whereClause := strings.Join(conditions, " OR ")

		// 关键修改：label 用反引号包裹
		escapedLabel := escapeCypherLabel(label)

		query := fmt.Sprintf(`
            SELECT * FROM cypher('%s', $$
                MATCH (n:%s)-[r]-(m:%s)
                WHERE %s
                RETURN n, r, m
            $$) AS (n agtype, r agtype, m agtype)
        `, r.graphName, escapedLabel, escapedLabel, whereClause)

		queries = append(queries, query)
	}

	return strings.Join(queries, " UNION ALL ")
}

// escapeCypherLabel 转义 Cypher label（处理空格和特殊字符）
func escapeCypherLabel(label string) string {
	// 如果 label 包含空格、特殊字符，用反引号包裹
	if strings.ContainsAny(label, " -./\\:") {
		// 反引号内的反引号需要转义为 ``
		escaped := strings.ReplaceAll(label, "`", "``")
		return "`" + escaped + "`"
	}
	return label
}

// searchByLabels 按标签分组查询，每个标签一次批量查询所有节点
func (r *AGERepository) searchByLabels(
	ctx context.Context,
	conn *sql.Conn,
	labels []string,
	searchTexts []string,
) (*types.GraphData, error) {
	graphData := &types.GraphData{
		Node:     make([]*types.GraphNode, 0, len(searchTexts)*2),
		Relation: make([]*types.GraphRelation, 0, len(searchTexts)*2),
	}

	// 使用 struct{}{} 节省内存
	nodeSeen := make(map[string]struct{}, len(searchTexts)*2)
	relSeen := make(map[string]struct{}, len(searchTexts)*4)

	// 优化：外层循环labels（数量少，如3-5个）
	for _, label := range labels {
		// 内层一次性处理所有nodes（数量多，如100个）
		if err := r.searchNodesInLabel(ctx, conn, label, searchTexts, graphData, nodeSeen, relSeen); err != nil {
			logger.Errorf(ctx, "Failed to search in label %s: %v", label, err)
			// 继续处理其他标签
			continue
		}
	}

	logger.Debugf(ctx, "Found %d nodes and %d relations", len(graphData.Node), len(graphData.Relation))
	return graphData, nil
}

// searchNodesInLabel 在单个标签中批量搜索所有节点
func (r *AGERepository) searchNodesInLabel(
	ctx context.Context,
	conn *sql.Conn,
	label string,
	searchTexts []string,
	graphData *types.GraphData,
	nodeSeen, relSeen map[string]struct{},
) error {
	// 关键优化：将所有搜索条件合并到一个查询中
	cypherQuery := r.buildLabelQuery(label, searchTexts)

	logger.Debugf(ctx, "Executing query for label %s with %d search terms", label, len(searchTexts))

	rows, err := conn.QueryContext(ctx, cypherQuery)
	if err != nil {
		return fmt.Errorf("query failed for label %s: %w", label, err)
	}
	defer rows.Close()

	// 处理查询结果
	return r.processQueryResults(rows, graphData, nodeSeen, relSeen)
}

// buildLabelQuery 构建优化的查询语句
func (r *AGERepository) buildLabelQuery(label string, searchTexts []string) string {
	// 策略选择：根据搜索词数量选择最优方案
	if len(searchTexts) <= 10 {
		// 少量搜索词：使用 OR 条件（简单高效）
		return r.buildORQuery(label, searchTexts)
	} else if len(searchTexts) <= 100 {
		// 中等数量：使用 IN 操作符（更简洁）
		return r.buildINQuery(label, searchTexts)
	} else {
		// 大量搜索词：使用临时列表匹配（最优性能）
		return r.buildListQuery(label, searchTexts)
	}
}

// buildORQuery 使用 OR 条件（适合少量搜索词）
func (r *AGERepository) buildORQuery(label string, searchTexts []string) string {
	var conditions []string
	for _, text := range searchTexts {
		// 使用 toLower 替代正则表达式，性能提升10-100倍
		conditions = append(conditions,
			fmt.Sprintf("toLower(n.name) = toLower('%s')", escapeCypherString(text)))
	}

	return fmt.Sprintf(`
		SELECT * FROM cypher('%s', $$
			MATCH (n:%s)-[r]-(m:%s)
			WHERE %s
			RETURN n, r, m
		$$) AS (n agtype, r agtype, m agtype)
	`, r.graphName, label, label, strings.Join(conditions, " OR "))
}

// buildINQuery 使用 IN 操作符（适合中等数量）
func (r *AGERepository) buildINQuery(label string, searchTexts []string) string {
	// 转义并构建列表
	var escapedTexts []string
	for _, text := range searchTexts {
		// 转换为小写并转义
		escapedTexts = append(escapedTexts,
			fmt.Sprintf("'%s'", strings.ToLower(escapeCypherString(text))))
	}

	return fmt.Sprintf(`
		SELECT * FROM cypher('%s', $$
			MATCH (n:%s)-[r]-(m:%s)
			WHERE toLower(n.name) IN [%s]
			RETURN n, r, m
		$$) AS (n agtype, r agtype, m agtype)
	`, r.graphName, label, label, strings.Join(escapedTexts, ", "))
}

// buildListQuery 使用列表匹配（适合大量搜索词）
func (r *AGERepository) buildListQuery(label string, searchTexts []string) string {
	// 构建搜索词列表
	var escapedTexts []string
	for _, text := range searchTexts {
		escapedTexts = append(escapedTexts,
			fmt.Sprintf("'%s'", strings.ToLower(escapeCypherString(text))))
	}

	return fmt.Sprintf(`
		SELECT * FROM cypher('%s', $$
			WITH [%s] AS search_list
			MATCH (n:%s)-[r]-(m:%s)
			WHERE toLower(n.name) IN search_list
			RETURN n, r, m
		$$) AS (n agtype, r agtype, m agtype)
	`, r.graphName, strings.Join(escapedTexts, ", "), label, label)
}

// processQueryResults 处理查询结果并去重
func (r *AGERepository) processQueryResults(
	rows *sql.Rows,
	graphData *types.GraphData,
	nodeSeen, relSeen map[string]struct{},
) error {
	for rows.Next() {
		var nJSON, rJSON, mJSON string
		if err := rows.Scan(&nJSON, &rJSON, &mJSON); err != nil {
			logger.Errorf(nil, "Failed to scan row: %v", err)
			continue
		}

		// 解析节点
		nNode := parseAgtypeNode(nJSON)
		mNode := parseAgtypeNode(mJSON)

		// 添加节点并去重
		if nNode != nil {
			if _, exists := nodeSeen[nNode.Name]; !exists {
				nodeSeen[nNode.Name] = struct{}{}
				graphData.Node = append(graphData.Node, nNode)
			}
		}
		if mNode != nil {
			if _, exists := nodeSeen[mNode.Name]; !exists {
				nodeSeen[mNode.Name] = struct{}{}
				graphData.Node = append(graphData.Node, mNode)
			}
		}

		// 添加关系并去重
		if nNode != nil && mNode != nil {
			rel := parseAgtypeRelation(rJSON, nNode, mNode)
			if rel != nil {
				relKey := fmt.Sprintf("%s-%s-%s", rel.Node1, rel.Type, rel.Node2)
				if _, exists := relSeen[relKey]; !exists {
					relSeen[relKey] = struct{}{}
					graphData.Relation = append(graphData.Relation, rel)
				}
			}
		}
	}

	return rows.Err()
}

// 如果需要模糊匹配，提供专门的方法
func (r *AGERepository) buildFuzzyQuery(label string, searchTexts []string) string {
	var conditions []string
	for _, text := range searchTexts {
		// 使用 CONTAINS 替代正则表达式，性能更好
		conditions = append(conditions,
			fmt.Sprintf("toLower(n.name) CONTAINS toLower('%s')", escapeCypherString(text)))
	}

	return fmt.Sprintf(`
		SELECT * FROM cypher('%s', $$
			MATCH (n:%s)-[r]-(m:%s)
			WHERE %s
			RETURN n, r, m
		$$) AS (n agtype, r agtype, m agtype)
	`, r.graphName, label, label, strings.Join(conditions, " OR "))
}

// 并发版本：适用于标签数量适中（5-20个）且查询较重的场景
func (r *AGERepository) searchByLabelsConcurrent(
	ctx context.Context,
	conn *sql.Conn,
	labels []string,
	searchTexts []string,
) (*types.GraphData, error) {
	type labelResult struct {
		nodes []*types.GraphNode
		rels  []*types.GraphRelation
		err   error
	}

	// 为每个label创建一个goroutine
	resultChan := make(chan labelResult, len(labels))

	// 控制并发数（避免过多连接）
	maxConcurrent := 3 // 可根据实际情况调整
	sem := make(chan struct{}, maxConcurrent)

	for _, label := range labels {
		label := label // 捕获循环变量
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			// 为每个label创建独立连接
			labelConn, err := r.db.Conn(ctx)
			if err != nil {
				resultChan <- labelResult{err: err}
				return
			}
			defer labelConn.Close()

			if err := r.initAGEConn(ctx, labelConn); err != nil {
				resultChan <- labelResult{err: err}
				return
			}

			// 执行查询
			cypherQuery := r.buildLabelQuery(label, searchTexts)
			rows, err := labelConn.QueryContext(ctx, cypherQuery)
			if err != nil {
				resultChan <- labelResult{err: err}
				return
			}
			defer rows.Close()

			// 收集结果
			var nodes []*types.GraphNode
			var rels []*types.GraphRelation
			localNodeSeen := make(map[string]struct{})

			for rows.Next() {
				var nJSON, rJSON, mJSON string
				if err := rows.Scan(&nJSON, &rJSON, &mJSON); err != nil {
					continue
				}

				nNode := parseAgtypeNode(nJSON)
				mNode := parseAgtypeNode(mJSON)

				if nNode != nil {
					if _, exists := localNodeSeen[nNode.Name]; !exists {
						localNodeSeen[nNode.Name] = struct{}{}
						nodes = append(nodes, nNode)
					}
				}
				if mNode != nil {
					if _, exists := localNodeSeen[mNode.Name]; !exists {
						localNodeSeen[mNode.Name] = struct{}{}
						nodes = append(nodes, mNode)
					}
				}

				if nNode != nil && mNode != nil {
					rel := parseAgtypeRelation(rJSON, nNode, mNode)
					if rel != nil {
						rels = append(rels, rel)
					}
				}
			}

			resultChan <- labelResult{nodes: nodes, rels: rels, err: rows.Err()}
		}()
	}

	// 合并结果
	graphData := &types.GraphData{
		Node:     make([]*types.GraphNode, 0),
		Relation: make([]*types.GraphRelation, 0),
	}
	nodeSeen := make(map[string]struct{})
	relSeen := make(map[string]struct{})

	for i := 0; i < len(labels); i++ {
		result := <-resultChan
		if result.err != nil {
			logger.Errorf(ctx, "Label query error: %v", result.err)
			continue
		}

		// 全局去重
		for _, node := range result.nodes {
			if _, exists := nodeSeen[node.Name]; !exists {
				nodeSeen[node.Name] = struct{}{}
				graphData.Node = append(graphData.Node, node)
			}
		}

		for _, rel := range result.rels {
			relKey := fmt.Sprintf("%s-%s-%s", rel.Node1, rel.Type, rel.Node2)
			if _, exists := relSeen[relKey]; !exists {
				relSeen[relKey] = struct{}{}
				graphData.Relation = append(graphData.Relation, rel)
			}
		}
	}

	return graphData, nil
}

// getLabelsWithPrefix returns all vertex labels that start with the given prefix
func (r *AGERepository) getLabelsWithPrefix(ctx context.Context, conn *sql.Conn, prefix string) ([]string, error) {
	query := `
		SELECT name FROM ag_catalog.ag_label
		WHERE graph = (SELECT graphid FROM ag_catalog.ag_graph WHERE name = $1)
		AND name LIKE $2
		AND kind = 'v'
	`
	rows, err := conn.QueryContext(ctx, query, r.graphName, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("failed to query labels: %w", err)
	}
	defer rows.Close()

	var labels []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			continue
		}
		labels = append(labels, label)
	}

	logger.Debugf(ctx, "Found %d labels with prefix %s", len(labels), prefix)
	return labels, nil
}

// initAGEConn initializes AGE extension on a specific connection
func (r *AGERepository) initAGEConn(ctx context.Context, conn *sql.Conn) error {
	// Load AGE extension
	_, err := conn.ExecContext(ctx, "LOAD 'age'")
	if err != nil {
		// Extension might already be loaded, log and continue
		logger.Debugf(ctx, "Load AGE extension: %v", err)
	}

	// Set search path to include ag_catalog
	_, err = conn.ExecContext(ctx, `SET search_path = ag_catalog, "$user", public`)
	if err != nil {
		return fmt.Errorf("failed to set search_path: %w", err)
	}

	return nil
}

// escapeCypherString escapes special characters in Cypher strings
func escapeCypherString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

// sanitizeRelationType converts relationship type to valid AGE label
func sanitizeRelationType(relType string) string {
	// Replace spaces and special characters with underscores
	result := strings.ToUpper(relType)
	// Remove all non-alphanumeric characters except underscore
	var sb strings.Builder
	for i, r := range result {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9' && i > 0) || r == '_' {
			sb.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '.' {
			sb.WriteRune('_')
		}
	}
	result = sb.String()
	// Ensure it starts with a letter
	if len(result) > 0 && !((result[0] >= 'A' && result[0] <= 'Z') || (result[0] >= 'a' && result[0] <= 'z')) {
		result = "R_" + result
	}
	if result == "" {
		result = "RELATED_TO"
	}
	// AGE label length limit
	if len(result) > 63 {
		result = result[:63]
	}
	return result
}

// agtypeNode represents a parsed AGE node
type agtypeNode struct {
	ID         int64             `json:"id"`
	Label      string            `json:"label"`
	Properties map[string]string `json:"properties"`
}

// agtypeEdge represents a parsed AGE edge
type agtypeEdge struct {
	ID         int64             `json:"id"`
	Label      string            `json:"label"`
	StartID    int64             `json:"start_id"`
	EndID      int64             `json:"end_id"`
	Properties map[string]string `json:"properties"`
}

// parseAgtypeNode parses an AGE node from agtype JSON
func parseAgtypeNode(agtypeStr string) *types.GraphNode {
	if agtypeStr == "" || agtypeStr == "null" {
		return nil
	}

	// AGE returns nodes in format: {"id": 123, "label": "Label", "properties": {...}}::vertex
	// Remove the ::vertex suffix if present
	jsonStr := strings.TrimSuffix(agtypeStr, "::vertex")

	var nodeData map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &nodeData); err != nil {
		return nil
	}

	props, ok := nodeData["properties"].(map[string]interface{})
	if !ok {
		return nil
	}

	name, _ := props["name"].(string)
	if name == "" {
		return nil
	}

	// Parse attributes and chunks from JSON strings
	var attributes, chunks []string
	if attrStr, ok := props["attributes"].(string); ok {
		json.Unmarshal([]byte(attrStr), &attributes)
	}
	if chunksStr, ok := props["chunks"].(string); ok {
		json.Unmarshal([]byte(chunksStr), &chunks)
	}

	return &types.GraphNode{
		Name:       name,
		Chunks:     chunks,
		Attributes: attributes,
	}
}

// parseAgtypeRelation parses an AGE relationship from agtype JSON
func parseAgtypeRelation(agtypeStr string, source, target *types.GraphNode) *types.GraphRelation {
	if agtypeStr == "" || agtypeStr == "null" || source == nil || target == nil {
		return nil
	}

	// AGE returns edges in format: {"id": 123, "label": "TYPE", ...}::edge
	jsonStr := strings.TrimSuffix(agtypeStr, "::edge")

	var edgeData map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &edgeData); err != nil {
		return nil
	}

	relType, _ := edgeData["label"].(string)
	if relType == "" {
		relType = "RELATED_TO"
	}

	return &types.GraphRelation{
		Node1: source.Name,
		Node2: target.Name,
		Type:  relType,
	}
}
