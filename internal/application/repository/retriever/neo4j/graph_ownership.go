package neo4j

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

const graphOwnershipSchemaVersion = 1

type graphOwnershipVersionValue struct {
	Nodes     []graphOwnershipVersionNode     `json:"nodes"`
	Relations []graphOwnershipVersionRelation `json:"relations"`
}

type graphOwnershipVersionNode struct {
	Name       string   `json:"name"`
	Attributes []string `json:"attributes"`
}

type graphOwnershipVersionRelation struct {
	Node1 string `json:"node1"`
	Node2 string `json:"node2"`
	Type  string `json:"type"`
}

func graphOwnershipVersion(graph *types.GraphData) (string, error) {
	if graph == nil {
		return "", fmt.Errorf("graph must not be nil")
	}
	value := graphOwnershipVersionValue{
		Nodes:     make([]graphOwnershipVersionNode, 0, len(graph.Node)),
		Relations: make([]graphOwnershipVersionRelation, 0, len(graph.Relation)),
	}
	nodeNames := make(map[string]struct{}, len(graph.Node))
	for _, node := range graph.Node {
		if node == nil {
			return "", fmt.Errorf("graph contains a nil node")
		}
		nodeNames[node.Name] = struct{}{}
		attributes := append([]string(nil), node.Attributes...)
		sort.Strings(attributes)
		value.Nodes = append(value.Nodes, graphOwnershipVersionNode{
			Name:       node.Name,
			Attributes: attributes,
		})
	}
	sort.Slice(value.Nodes, func(i, j int) bool {
		return value.Nodes[i].Name < value.Nodes[j].Name
	})
	for _, relation := range graph.Relation {
		if relation == nil {
			return "", fmt.Errorf("graph contains a nil relation")
		}
		if _, exists := nodeNames[relation.Node1]; !exists {
			return "", fmt.Errorf("graph relation references unknown node %q", relation.Node1)
		}
		if _, exists := nodeNames[relation.Node2]; !exists {
			return "", fmt.Errorf("graph relation references unknown node %q", relation.Node2)
		}
		value.Relations = append(value.Relations, graphOwnershipVersionRelation{
			Node1: relation.Node1,
			Node2: relation.Node2,
			Type:  relation.Type,
		})
	}
	sort.Slice(value.Relations, func(i, j int) bool {
		left, right := value.Relations[i], value.Relations[j]
		if left.Node1 != right.Node1 {
			return left.Node1 < right.Node1
		}
		if left.Type != right.Type {
			return left.Type < right.Type
		}
		return left.Node2 < right.Node2
	})
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode graph ownership version: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// FenceGraphAttempt prevents older extraction tasks from publishing after reconciliation starts.
func (n *Neo4jRepository) FenceGraphAttempt(
	ctx context.Context,
	namespace types.NameSpace,
	attempt int,
) error {
	if n.driver == nil && n.writeTransaction == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	if err := n.validateGraphChunkOperation(namespace, nil); err != nil {
		return err
	}
	if attempt < 0 {
		return fmt.Errorf("graph attempt must not be negative")
	}
	if err := n.ensureGraphOwnershipSchema(ctx); err != nil {
		return err
	}

	return n.runGraphWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		query := `
			MERGE (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
			ON CREATE SET state.version = 0,
				state.revision = 0,
				state.attempt = 0,
				state.dirty = false,
				state.deleting = false,
				state.deletion_token = null
			CALL apoc.lock.nodes([state])
			CALL apoc.util.validate(
				coalesce(state.attempt, 0) > $attempt,
				'graph attempt superseded',
				[]
			)
			SET state.attempt = CASE
					WHEN coalesce(state.attempt, 0) < $attempt THEN $attempt
					ELSE coalesce(state.attempt, 0)
				END,
				state.revision = coalesce(state.revision, 0) + 1
			RETURN state
		`
		if _, err := tx.Run(ctx, query, map[string]any{
			"knowledge_id": namespace.Knowledge,
			"attempt":      attempt,
		}); err != nil {
			return nil, fmt.Errorf("failed to fence graph attempt: %w", err)
		}
		return nil, nil
	})
}

// RecoverGraphNamespace finishes an interrupted full deletion before graph publication resumes.
func (n *Neo4jRepository) RecoverGraphNamespace(
	ctx context.Context,
	namespace types.NameSpace,
) error {
	if n.driver == nil && n.graphRecoveryRequired == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	if err := n.validateGraphChunkOperation(namespace, nil); err != nil {
		return err
	}
	required, err := n.needsGraphNamespaceRecovery(ctx, namespace)
	if err != nil {
		return err
	}
	if !required {
		return nil
	}
	return n.DelGraph(ctx, []types.NameSpace{namespace})
}

func (n *Neo4jRepository) needsGraphNamespaceRecovery(
	ctx context.Context,
	namespace types.NameSpace,
) (bool, error) {
	if n.graphRecoveryRequired != nil {
		return n.graphRecoveryRequired(ctx, namespace)
	}
	session := n.driver.NewSession(ctx, graphRecoverySessionConfig())
	defer session.Close(ctx)

	value, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
			OPTIONAL MATCH (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
			RETURN coalesce(state.dirty, false) OR coalesce(state.deleting, false) AS required
		`, map[string]any{"knowledge_id": namespace.Knowledge})
		if err != nil {
			return nil, err
		}
		record, err := result.Single(ctx)
		if err != nil {
			return nil, err
		}
		required, _, err := neo4j.GetRecordValue[bool](record, "required")
		return required, err
	})
	if err != nil {
		return false, fmt.Errorf("failed to inspect graph namespace recovery state: %w", err)
	}
	required, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("graph namespace recovery state has unexpected type %T", value)
	}
	return required, nil
}

func graphRecoverySessionConfig() neo4j.SessionConfig {
	return neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite}
}

// ReplaceGraphChunk atomically replaces one chunk's graph contribution.
func (n *Neo4jRepository) ReplaceGraphChunk(
	ctx context.Context,
	namespace types.NameSpace,
	chunkID string,
	attempt int,
	graph *types.GraphData,
) error {
	if n.driver == nil && n.writeTransaction == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	if err := n.validateGraphChunkOperation(namespace, []string{chunkID}); err != nil {
		return err
	}
	if graph == nil {
		return fmt.Errorf("graph must not be nil")
	}
	if attempt < 0 {
		return fmt.Errorf("graph attempt must not be negative")
	}
	if err := n.ensureGraphOwnershipSchema(ctx); err != nil {
		return err
	}
	graphVersion, err := graphOwnershipVersion(graph)
	if err != nil {
		return err
	}
	if err := n.prepareGraphOwnershipNamespace(ctx, namespace); err != nil {
		return err
	}

	return n.runGraphWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if err := n.lockGraphOwnershipNamespace(ctx, tx, namespace); err != nil {
			return nil, err
		}
		if err := n.ensureGraphChunkOwner(ctx, tx, namespace, chunkID, attempt); err != nil {
			return nil, err
		}
		if err := n.retractGraphChunks(
			ctx, tx, namespace, []string{chunkID}, graphVersion, attempt,
		); err != nil {
			return nil, err
		}
		if err := n.addOwnedGraph(ctx, tx, namespace, chunkID, attempt, graphVersion, graph); err != nil {
			return nil, err
		}
		if err := n.markGraphChunkVersion(ctx, tx, namespace, chunkID, attempt, graphVersion); err != nil {
			return nil, err
		}
		return nil, nil
	})
}

// DelGraphChunks retracts graph contributions for removed chunks.
func (n *Neo4jRepository) DelGraphChunks(
	ctx context.Context,
	namespace types.NameSpace,
	chunkIDs []string,
) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	if n.driver == nil && n.writeTransaction == nil {
		logger.Warnf(ctx, "NOT SUPPORT RETRIEVE GRAPH")
		return nil
	}
	if err := n.validateGraphChunkOperation(namespace, chunkIDs); err != nil {
		return err
	}
	if err := n.ensureGraphOwnershipSchema(ctx); err != nil {
		return err
	}
	if err := n.prepareGraphOwnershipNamespace(ctx, namespace); err != nil {
		return err
	}

	return n.runGraphWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if err := n.lockGraphOwnershipNamespace(ctx, tx, namespace); err != nil {
			return nil, err
		}
		if err := n.retractGraphChunks(ctx, tx, namespace, chunkIDs, "", 0); err != nil {
			return nil, err
		}
		if err := n.deleteGraphChunkOwners(ctx, tx, namespace, chunkIDs); err != nil {
			return nil, err
		}
		return nil, nil
	})
}

func (n *Neo4jRepository) ensureGraphOwnershipSchema(ctx context.Context) error {
	n.ownershipSchemaMu.Lock()
	defer n.ownershipSchemaMu.Unlock()
	if n.ownershipSchemaReady {
		return nil
	}

	err := n.runGraphWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		queries := []string{
			`CREATE CONSTRAINT weknora_graph_ownership_state_kg IF NOT EXISTS
			 FOR (state:GRAPH_OWNERSHIP_STATE) REQUIRE state.kg IS UNIQUE`,
			`CREATE CONSTRAINT weknora_graph_chunk_owner_identity IF NOT EXISTS
			 FOR (owner:GRAPH_CHUNK_OWNER) REQUIRE (owner.kg, owner.chunk_id) IS UNIQUE`,
			`CREATE CONSTRAINT weknora_graph_entity_identity IF NOT EXISTS
			 FOR (entity:GRAPH_ENTITY) REQUIRE (entity.kg, entity.name) IS UNIQUE`,
		}
		for _, query := range queries {
			if _, err := tx.Run(ctx, query, nil); err != nil {
				return nil, fmt.Errorf("failed to initialize graph ownership schema: %w", err)
			}
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	n.ownershipSchemaReady = true
	return nil
}

func (n *Neo4jRepository) runGraphWrite(
	ctx context.Context,
	work neo4j.ManagedTransactionWork,
) error {
	if n.writeTransaction != nil {
		return n.writeTransaction(ctx, work)
	}
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)
	_, err := session.ExecuteWrite(ctx, work)
	return err
}

func (n *Neo4jRepository) prepareGraphOwnershipNamespace(
	ctx context.Context,
	namespace types.NameSpace,
) error {
	return n.runGraphWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if err := n.purgeLegacyRelationships(ctx, tx, namespace); err != nil {
			return nil, err
		}
		return nil, nil
	})
}

func (n *Neo4jRepository) purgeLegacyRelationships(
	ctx context.Context,
	tx neo4j.ManagedTransaction,
	namespace types.NameSpace,
) error {
	query := `
		OPTIONAL MATCH (existing:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
		CALL {
			WITH existing
			WITH existing
			WHERE existing IS NOT NULL
				AND coalesce(existing.version, 0) >= $schema_version
			CALL apoc.util.validate(
				coalesce(existing.deleting, false),
				'graph namespace deletion in progress',
				[]
			)
			RETURN existing AS state, 0 AS migrated
			UNION
			WITH existing
			WITH existing
			WHERE existing IS NULL
				OR coalesce(existing.version, 0) < $schema_version
			MERGE (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
			ON CREATE SET state.version = 0,
				state.revision = 0,
				state.attempt = 0,
				state.dirty = false,
				state.deleting = false,
				state.deletion_token = null
			SET state.revision = coalesce(state.revision, 0) + 1
			WITH state
			CALL apoc.util.validate(
				coalesce(state.deleting, false),
				'graph namespace deletion in progress',
				[]
			)
			WITH state
			WHERE coalesce(state.version, 0) < $schema_version
			OPTIONAL MATCH (entity:` + n.Label(namespace) + ` {kg: $knowledge_id})
			WITH state, entity.name AS entity_name,
				[node IN collect(entity) WHERE node IS NOT NULL] AS duplicates
			WITH state, entity_name, duplicates,
				reduce(
					chunks = [],
					duplicate IN duplicates |
						apoc.coll.union(chunks, coalesce(duplicate.chunks, []))
				) AS chunks,
				reduce(
					attributes = [],
					duplicate IN duplicates |
						apoc.coll.union(attributes, coalesce(duplicate.attributes, []))
				) AS attributes
			CALL {
				WITH duplicates, chunks, attributes
				WITH duplicates, chunks, attributes WHERE size(duplicates) > 0
				CALL apoc.refactor.mergeNodes(
					duplicates,
					{properties: 'discard', mergeRels: true, produceSelfRel: false}
				) YIELD node
				SET node.chunks = chunks,
					node.attributes = attributes,
					node.legacy_attributes = attributes,
					node.legacy_attribute_chunks = chunks
				RETURN node
				UNION
				WITH duplicates, chunks, attributes
				WITH duplicates WHERE size(duplicates) = 0
				RETURN null AS node
			}
			WITH state, [entity IN collect(node) WHERE entity IS NOT NULL] AS entities
			FOREACH (entity IN entities | SET entity:GRAPH_ENTITY)
			CALL {
				WITH state, entities
				UNWIND entities AS entity
				UNWIND coalesce(entity.chunks, []) AS chunk_id
				WITH state, entity, chunk_id
				WHERE chunk_id IS NOT NULL AND trim(chunk_id) <> ''
				MERGE (owner:GRAPH_CHUNK_OWNER {
					kg: $knowledge_id,
					chunk_id: chunk_id
				})
				ON CREATE SET owner.revision = 0
				SET owner.initialized = true,
					owner.graph_version = 'legacy',
					owner.attempt = coalesce(state.attempt, 0)
				MERGE (owner)-[contribution:GRAPH_NODE_CONTRIBUTION]->(entity)
				SET contribution.attributes = []
				RETURN count(*) AS backfilled
			}
			WITH state, size(entities) AS labeled, backfilled
			OPTIONAL MATCH (n:` + n.Label(namespace) + ` {kg: $knowledge_id})-[r]-(m:` + n.Label(namespace) + ` {kg: $knowledge_id})
			WHERE r.chunks IS NULL
			WITH state, labeled,
				[relation IN collect(DISTINCT r) WHERE relation IS NOT NULL] AS relationships
			FOREACH (relation IN relationships | DELETE relation)
			SET state.version = $schema_version
			RETURN labeled + size(relationships) AS migrated
		}
		RETURN state, migrated
	`
	params := map[string]any{
		"knowledge_id":   namespace.Knowledge,
		"schema_version": graphOwnershipSchemaVersion,
	}
	if _, err := tx.Run(ctx, query, params); err != nil {
		return fmt.Errorf("failed to delete legacy relationships: %w", err)
	}
	return nil
}

func (n *Neo4jRepository) lockGraphOwnershipNamespace(
	ctx context.Context,
	tx neo4j.ManagedTransaction,
	namespace types.NameSpace,
) error {
	query := `
		MATCH (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
		CALL apoc.lock.read.nodes([state])
		CALL apoc.util.validate(
			coalesce(state.deleting, false),
			'graph namespace deletion in progress',
			[]
		)
		RETURN state
	`
	if _, err := tx.Run(ctx, query, map[string]any{
		"knowledge_id": namespace.Knowledge,
	}); err != nil {
		return fmt.Errorf("failed to lock graph ownership namespace: %w", err)
	}
	return nil
}

func (n *Neo4jRepository) ensureGraphChunkOwner(
	ctx context.Context,
	tx neo4j.ManagedTransaction,
	namespace types.NameSpace,
	chunkID string,
	attempt int,
) error {
	query := `
		MATCH (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
		WITH state WHERE coalesce(state.attempt, 0) = $attempt
		MERGE (owner:GRAPH_CHUNK_OWNER {kg: $knowledge_id, chunk_id: $chunk_id})
		ON CREATE SET owner.graph_version = '', owner.initialized = false, owner.revision = 0
		SET owner.revision = coalesce(owner.revision, 0) + 1,
			owner.attempt = $attempt
	`
	if _, err := tx.Run(ctx, query, map[string]any{
		"knowledge_id": namespace.Knowledge,
		"chunk_id":     chunkID,
		"attempt":      attempt,
	}); err != nil {
		return fmt.Errorf("failed to ensure graph chunk owner: %w", err)
	}
	return nil
}

func (n *Neo4jRepository) retractGraphChunks(
	ctx context.Context,
	tx neo4j.ManagedTransaction,
	namespace types.NameSpace,
	chunkIDs []string,
	graphVersion string,
	attempt int,
) error {
	ownerGuard := ""
	params := map[string]any{"knowledge_id": namespace.Knowledge, "chunk_ids": chunkIDs}
	if graphVersion != "" {
		ownerGuard = `
			MATCH (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
			WITH state WHERE coalesce(state.attempt, 0) = $attempt
			MATCH (owner:GRAPH_CHUNK_OWNER {kg: $knowledge_id, chunk_id: $chunk_id})
			WITH owner
			WHERE owner.attempt = $attempt
				AND owner.initialized = true
				AND coalesce(owner.graph_version, '') <> $graph_version
		`
		params["chunk_id"] = chunkIDs[0]
		params["graph_version"] = graphVersion
		params["attempt"] = attempt
	}
	relationQuery := ownerGuard + `
		MATCH (n:` + n.Label(namespace) + ` {kg: $knowledge_id})-[r]-(m:` + n.Label(namespace) + ` {kg: $knowledge_id})
		WHERE any(ownerID IN coalesce(r.chunks, []) WHERE ownerID IN $chunk_ids)
		WITH DISTINCT r
		SET r.chunks = [ownerID IN coalesce(r.chunks, []) WHERE NOT (ownerID IN $chunk_ids)]
		WITH r WHERE size(r.chunks) = 0
		DELETE r
	`
	if _, err := tx.Run(ctx, relationQuery, params); err != nil {
		return fmt.Errorf("failed to retract relationship ownership: %w", err)
	}

	contributionOwnerMatch := ownerGuard
	if graphVersion == "" {
		contributionOwnerMatch = `
			MATCH (owner:GRAPH_CHUNK_OWNER {kg: $knowledge_id})
			WHERE owner.chunk_id IN $chunk_ids
		`
	}
	attributeQuery := contributionOwnerMatch + `
		MATCH (owner)-[contribution:GRAPH_NODE_CONTRIBUTION]->(node:` + n.Label(namespace) + ` {kg: $knowledge_id})
		SET node.legacy_attribute_chunks = [
			legacyID IN coalesce(node.legacy_attribute_chunks, [])
			WHERE NOT (legacyID IN $chunk_ids)
		]
		DELETE contribution
		WITH DISTINCT node
		OPTIONAL MATCH (:GRAPH_CHUNK_OWNER)-[remaining:GRAPH_NODE_CONTRIBUTION]->(node)
		WITH node,
			[attributes IN collect(remaining.attributes) WHERE attributes IS NOT NULL] AS attribute_sets,
			CASE
				WHEN size(coalesce(node.legacy_attribute_chunks, [])) > 0
				THEN coalesce(node.legacy_attributes, [])
				ELSE []
			END AS legacy_attributes
		SET node.attributes = reduce(
			attributes = legacy_attributes,
			values IN attribute_sets | apoc.coll.union(attributes, values)
		),
			node.legacy_attributes = CASE
				WHEN size(coalesce(node.legacy_attribute_chunks, [])) = 0 THEN null
				ELSE node.legacy_attributes
			END,
			node.legacy_attribute_chunks = CASE
				WHEN size(coalesce(node.legacy_attribute_chunks, [])) = 0 THEN null
				ELSE node.legacy_attribute_chunks
			END
	`
	if _, err := tx.Run(ctx, attributeQuery, params); err != nil {
		return fmt.Errorf("failed to retract node attribute ownership: %w", err)
	}

	nodeQuery := ownerGuard + `
		MATCH (n:` + n.Label(namespace) + ` {kg: $knowledge_id})
		WHERE any(ownerID IN coalesce(n.chunks, []) WHERE ownerID IN $chunk_ids)
		SET n.chunks = [ownerID IN coalesce(n.chunks, []) WHERE NOT (ownerID IN $chunk_ids)]
		WITH n WHERE size(n.chunks) = 0
		DETACH DELETE n
	`
	if _, err := tx.Run(ctx, nodeQuery, params); err != nil {
		return fmt.Errorf("failed to retract node ownership: %w", err)
	}
	return nil
}

func (n *Neo4jRepository) addOwnedGraph(
	ctx context.Context,
	tx neo4j.ManagedTransaction,
	namespace types.NameSpace,
	chunkID string,
	attempt int,
	graphVersion string,
	graph *types.GraphData,
) error {
	ownerGuard := `
		MATCH (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
		WITH state WHERE coalesce(state.attempt, 0) = $attempt
		MATCH (owner:GRAPH_CHUNK_OWNER {kg: $knowledge_id, chunk_id: $chunk_id})
		WITH owner WHERE owner.attempt = $attempt
			AND coalesce(owner.graph_version, '') <> $graph_version
	`
	nodeQuery := ownerGuard + `
		UNWIND $data AS row
		CALL apoc.merge.node(row.labels, {name: row.name, kg: row.knowledge_id}, {}, {}) YIELD node
		SET node.chunks = apoc.coll.union(coalesce(node.chunks, []), row.chunks)
		MERGE (owner)-[contribution:GRAPH_NODE_CONTRIBUTION]->(node)
		SET contribution.attributes = row.attributes
		WITH node
		OPTIONAL MATCH (:GRAPH_CHUNK_OWNER)-[remaining:GRAPH_NODE_CONTRIBUTION]->(node)
		WITH node,
			[attributes IN collect(remaining.attributes) WHERE attributes IS NOT NULL] AS attribute_sets,
			CASE
				WHEN size(coalesce(node.legacy_attribute_chunks, [])) > 0
				THEN coalesce(node.legacy_attributes, [])
				ELSE []
			END AS legacy_attributes
		SET node.attributes = reduce(
			attributes = legacy_attributes,
			values IN attribute_sets | apoc.coll.union(attributes, values)
		)
		RETURN distinct 'done' AS result
	`
	nodeData := make([]map[string]any, 0, len(graph.Node))
	for _, node := range graph.Node {
		nodeData = append(nodeData, map[string]any{
			"name":         node.Name,
			"knowledge_id": namespace.Knowledge,
			"attributes":   node.Attributes,
			"chunks":       []string{chunkID},
			"labels":       n.ownershipLabels(namespace),
		})
	}
	nodeParams := map[string]any{
		"data":          nodeData,
		"knowledge_id":  namespace.Knowledge,
		"chunk_id":      chunkID,
		"attempt":       attempt,
		"graph_version": graphVersion,
	}
	if _, err := tx.Run(ctx, nodeQuery, nodeParams); err != nil {
		return fmt.Errorf("failed to create nodes: %w", err)
	}

	relationQuery := ownerGuard + `
		UNWIND $data AS row
		CALL apoc.merge.node(row.source_labels, {name: row.source, kg: row.knowledge_id}, {}, {}) YIELD node as source
		CALL apoc.merge.node(row.target_labels, {name: row.target, kg: row.knowledge_id}, {}, {}) YIELD node as target
		CALL apoc.lock.nodes(
			CASE
				WHEN elementId(source) < elementId(target) THEN [source, target]
				ELSE [target, source]
			END
		)
		CALL apoc.merge.relationship(source, row.type, {}, {}, target) YIELD rel
		SET rel.chunks = apoc.coll.union(coalesce(rel.chunks, []), row.chunks)
		RETURN distinct 'done'
	`
	relationData := make([]map[string]any, 0, len(graph.Relation))
	for _, relation := range graph.Relation {
		relationData = append(relationData, map[string]any{
			"source":        relation.Node1,
			"target":        relation.Node2,
			"knowledge_id":  namespace.Knowledge,
			"type":          relation.Type,
			"chunks":        []string{chunkID},
			"source_labels": n.ownershipLabels(namespace),
			"target_labels": n.ownershipLabels(namespace),
		})
	}
	relationParams := map[string]any{
		"data":          relationData,
		"knowledge_id":  namespace.Knowledge,
		"chunk_id":      chunkID,
		"attempt":       attempt,
		"graph_version": graphVersion,
	}
	if _, err := tx.Run(ctx, relationQuery, relationParams); err != nil {
		return fmt.Errorf("failed to create relationships: %w", err)
	}
	return nil
}

func (n *Neo4jRepository) markGraphChunkVersion(
	ctx context.Context,
	tx neo4j.ManagedTransaction,
	namespace types.NameSpace,
	chunkID string,
	attempt int,
	graphVersion string,
) error {
	query := `
		MATCH (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
		WITH state WHERE coalesce(state.attempt, 0) = $attempt
		MATCH (owner:GRAPH_CHUNK_OWNER {kg: $knowledge_id, chunk_id: $chunk_id})
		WITH owner WHERE owner.attempt = $attempt
		SET owner.graph_version = $graph_version,
			owner.initialized = true
	`
	if _, err := tx.Run(ctx, query, map[string]any{
		"knowledge_id":  namespace.Knowledge,
		"chunk_id":      chunkID,
		"attempt":       attempt,
		"graph_version": graphVersion,
	}); err != nil {
		return fmt.Errorf("failed to mark graph chunk version: %w", err)
	}
	return nil
}

func (n *Neo4jRepository) deleteGraphChunkOwners(
	ctx context.Context,
	tx neo4j.ManagedTransaction,
	namespace types.NameSpace,
	chunkIDs []string,
) error {
	query := `
		MATCH (owner:GRAPH_CHUNK_OWNER {kg: $knowledge_id})
		WHERE owner.chunk_id IN $chunk_ids
		DETACH DELETE owner
	`
	if _, err := tx.Run(ctx, query, map[string]any{
		"knowledge_id": namespace.Knowledge,
		"chunk_ids":    chunkIDs,
	}); err != nil {
		return fmt.Errorf("failed to delete graph chunk owners: %w", err)
	}
	return nil
}

func (n *Neo4jRepository) beginGraphNamespaceDeletion(
	ctx context.Context,
	namespace types.NameSpace,
	deletionToken string,
) error {
	return n.runGraphWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		query := `
			MERGE (state:GRAPH_OWNERSHIP_STATE {kg: $knowledge_id})
			ON CREATE SET state.version = 0,
				state.revision = 0,
				state.attempt = 0,
				state.dirty = false
			SET state.deleting = true,
				state.dirty = true,
				state.deletion_token = $deletion_token,
				state.revision = coalesce(state.revision, 0) + 1
			WITH state
			OPTIONAL MATCH (owner:GRAPH_CHUNK_OWNER {kg: $knowledge_id})
			OPTIONAL MATCH (owner)-[ownership:GRAPH_NODE_CONTRIBUTION]->()
			WITH state,
				collect(DISTINCT ownership) AS ownerships,
				collect(DISTINCT owner) AS owners
			FOREACH (ownership IN ownerships | DELETE ownership)
			FOREACH (owner IN owners | DELETE owner)
			RETURN state
		`
		if _, err := tx.Run(ctx, query, map[string]any{
			"knowledge_id":   namespace.Knowledge,
			"deletion_token": deletionToken,
		}); err != nil {
			return nil, fmt.Errorf("failed to begin graph namespace deletion: %w", err)
		}
		return nil, nil
	})
}

func (n *Neo4jRepository) finishGraphNamespaceDeletion(
	ctx context.Context,
	namespace types.NameSpace,
	deletionToken string,
	succeeded bool,
) error {
	return n.runGraphWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		query := `
			MATCH (state:GRAPH_OWNERSHIP_STATE {
				kg: $knowledge_id,
				deletion_token: $deletion_token
			})
			SET state.dirty = NOT $succeeded,
				state.deleting = NOT $succeeded,
				state.deletion_token = CASE
					WHEN $succeeded THEN null
					ELSE state.deletion_token
				END,
				state.version = CASE
					WHEN $succeeded THEN 0
					ELSE state.version
				END,
				state.revision = coalesce(state.revision, 0) + 1
			RETURN state
		`
		if _, err := tx.Run(ctx, query, map[string]any{
			"knowledge_id":   namespace.Knowledge,
			"deletion_token": deletionToken,
			"succeeded":      succeeded,
		}); err != nil {
			return nil, fmt.Errorf("failed to finish graph namespace deletion: %w", err)
		}
		return nil, nil
	})
}

func (n *Neo4jRepository) validateGraphChunkOperation(
	namespace types.NameSpace,
	chunkIDs []string,
) error {
	if strings.TrimSpace(namespace.Knowledge) == "" || n.Label(namespace) == "" {
		return fmt.Errorf("graph namespace must not be empty")
	}
	for _, chunkID := range chunkIDs {
		if strings.TrimSpace(chunkID) == "" {
			return fmt.Errorf("graph chunk ID must not be empty")
		}
	}
	return nil
}
