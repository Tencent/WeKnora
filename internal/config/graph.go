package config

import (
	"fmt"
	"os"
	"strings"
)

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

func ValidateGraphBackend() error {
	switch GraphBackend() {
	case GraphBackendNone, GraphBackendNeo4j, GraphBackendHologres:
		return nil
	default:
		return fmt.Errorf("GRAPH_BACKEND must be none, neo4j, or hologres")
	}
}

func GraphEnabled() bool {
	switch GraphBackend() {
	case GraphBackendNeo4j, GraphBackendHologres:
		return true
	default:
		return false
	}
}
