// Package types defines the core data structures and interfaces used throughout the WeKnora system.
package types

import "context"

// Entity represents a node in the knowledge graph extracted from document chunks.
// Each entity corresponds to a meaningful concept, person, place or thing identified in the text.
type Entity struct {
	ID          string   // Unique identifier for the entity
	ChunkIDs    []string // References to document chunks where this entity appears
	Frequency   int      `json:"-"`                                                                      // Number of occurrences in the corpus
	Degree      int      `json:"-"`                                                                      // Number of connections to other entities
	Title       string   `json:"title" jsonschema:"display name of the entity"`                          // Display name of the entity
	Type        string   `json:"type" jsonschema:"type of the entity"`                                   // Classification of the entity (e.g., person, concept, organization)
	Description string   `json:"description" jsonschema:"brief explanation or context about the entity"` // Brief explanation or context about the entity
}

// Relationship represents a connection between two entities in the knowledge graph.
// It captures the semantic connection between entities identified in the document chunks.
type Relationship struct {
	ID             string   `json:"-"`                                                                          // Unique identifier for the relationship
	ChunkIDs       []string `json:"-"`                                                                          // References to document chunks where this relationship is established
	CombinedDegree int      `json:"-"`                                                                          // Sum of degrees of the connected entities, used for ranking
	Weight         float64  `json:"-"`                                                                          // Strength of the relationship based on textual evidence
	Source         string   `json:"source" jsonschema:"ID of the entity where the relationship starts"`         // ID of the entity where the relationship starts
	Target         string   `json:"target" jsonschema:"ID of the entity where the relationship ends"`           // ID of the entity where the relationship ends
	Description    string   `json:"description" jsonschema:"description of how these entities are related"`     // Description of how these entities are related
	Strength       int      `json:"strength" jsonschema:"normalized measure of relationship importance (1-10)"` // Normalized measure of relationship importance (1-10)
}

// GraphBuilder defines the interface for building and querying the knowledge graph.
// It provides methods to construct the graph from document chunks and retrieve related information.
type GraphBuilder interface {
	// BuildGraph constructs a knowledge graph from the provided document chunks.
	// It extracts entities and relationships, then builds the graph structure.
	BuildGraph(ctx context.Context, chunks []*Chunk) error

	// GetRelationChunks retrieves the IDs of chunks directly related to the specified chunk.
	// The topK parameter limits the number of results returned, based on relationship strength.
	GetRelationChunks(chunkID string, topK int) []string

	// GetIndirectRelationChunks finds chunk IDs that are indirectly connected to the specified chunk.
	// These are "second-degree" connections, useful for expanding the context during retrieval.
	GetIndirectRelationChunks(chunkID string, topK int) []string

	// GetAllEntities returns all entities currently in the knowledge graph.
	// This is primarily used for visualization and diagnostics.
	GetAllEntities() []*Entity

	// GetAllRelationships returns all relationships currently in the knowledge graph.
	// This is primarily used for visualization and diagnostics.
	GetAllRelationships() []*Relationship
}
