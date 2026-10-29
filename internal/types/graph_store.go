package types

import (
	"os"
	"strings"
)

// Entity-relation graph backends. Neo4j is the historical default; postgres
// stores the same four-method contract in the database the deployment already
// runs, so no second service is needed.
const (
	GraphStoreEngineNeo4j    = "neo4j"
	GraphStoreEnginePostgres = "postgres"
)

// GraphStoreEngine returns the selected graph backend. Unset or unknown values
// fall back to Neo4j, preserving existing deployments.
func GraphStoreEngine() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("GRAPH_DATABASE_ENGINE")), GraphStoreEnginePostgres) {
		return GraphStoreEnginePostgres
	}
	return GraphStoreEngineNeo4j
}

// GraphStoreUsesPostgres reports whether the PostgreSQL graph store is both
// selected and usable. The application database must itself be PostgreSQL.
func GraphStoreUsesPostgres() bool {
	return GraphStoreEngine() == GraphStoreEnginePostgres &&
		strings.EqualFold(strings.TrimSpace(os.Getenv("DB_DRIVER")), "postgres")
}

// GraphStoreEnabled reports whether a graph backend is available. Extraction
// and query-time entity extraction are skipped when this is false: the work
// would be written nowhere.
func GraphStoreEnabled() bool {
	if GraphStoreUsesPostgres() {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("NEO4J_ENABLE")), "true")
}
