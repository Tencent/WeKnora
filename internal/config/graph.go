package config

import (
	"fmt"
	"os"
	"strings"
)

// Supported graph backends.
const (
	GraphBackendNone     = "none"
	GraphBackendNeo4j    = "neo4j"
	GraphBackendHologres = "hologres"
)

// GraphBackend preserves NEO4J_ENABLE when GRAPH_BACKEND is unset. Explicit
// selection wins, so a Hologres deployment never also opens a Neo4j connection.
func GraphBackend() string {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("GRAPH_BACKEND")))
	if backend != "" {
		return backend
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("NEO4J_ENABLE")), "true") {
		return GraphBackendNeo4j
	}
	return GraphBackendNone
}

// ValidateGraphBackend checks that the selected graph backend is supported.
func ValidateGraphBackend() error {
	switch GraphBackend() {
	case GraphBackendNone, GraphBackendNeo4j, GraphBackendHologres:
		return nil
	default:
		return fmt.Errorf("GRAPH_BACKEND must be none, neo4j, or hologres")
	}
}

// GraphEnabled reports whether a supported graph backend is selected.
func GraphEnabled() bool {
	switch GraphBackend() {
	case GraphBackendNeo4j, GraphBackendHologres:
		return true
	default:
		return false
	}
}
