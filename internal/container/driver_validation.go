package container

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// localOnlyRetrieveEngines are the retriever engines that rely on a
// local embeddings table created by the DB_DRIVER's own migration set.
// They are only valid when DB_DRIVER is the matching local driver
// (postgres or sqlite). Under DB_DRIVER=mysql the embeddings table is
// never created, so any of these engines would crash at the first
// embedding write.
//
// The keys must match the keys of retrieverEngineMapping in
// internal/types/tenant.go.
var localOnlyRetrieveEngines = map[string]struct{}{
	"postgres": {},
	"sqlite":   {},
}

// ParseRetrieveDrivers splits, trims, dedupes, and validates the RETRIEVE_DRIVER env value.
// Returns the normalized list of driver names. Used by both validation and registry registration.
func ParseRetrieveDrivers(retrieveDriver string) []string {
	raw := strings.Split(retrieveDriver, ",")
	seen := make(map[string]bool)
	var result []string
	for _, d := range raw {
		trimmed := strings.TrimSpace(d)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	return result
}

// ValidateDriverCombination checks that the DB_DRIVER and
// RETRIEVE_DRIVER environment variables are compatible.
//
// Rules:
//
//  1. DB_DRIVER=mysql cannot be paired with any local-only retriever
//     (postgres or sqlite). The embeddings table is never created under
//     MySQL mode (MySQL has no native vector type below 9.0, and the
//     pgvector / ParadeDB stack is PostgreSQL-only), so a postgres or
//     sqlite retriever would crash at the first embedding write.
//
//  2. DB_DRIVER=mysql requires at least one valid external retriever
//     engine. An empty / whitespace-only RETRIEVE_DRIVER would let the
//     app boot but every retrieval call would fail - fail fast instead.
//
//  3. Under DB_DRIVER=mysql every declared retriever must be a known
//     key of the retriever registry (types.GetRetrieverEngineMapping()).
//     This catches typos like "qdrnat" or stale names like "vikingdb"
//     that the system would otherwise silently ignore.
//
// Non-mysql DB drivers are not validated here - the historical contract
// is that they fall through to per-engine validation downstream. We
// preserve that to avoid widening the blast radius of this guard.
//
// dbDriver is the value of DB_DRIVER ("postgres", "sqlite", "mysql").
// retrieveDriver is the raw comma-separated RETRIEVE_DRIVER value
// (may be empty or contain surrounding whitespace).
//
// Returns nil for any combination that does not violate the rules.
func ValidateDriverCombination(dbDriver, retrieveDriver string) error {
	if dbDriver != "mysql" {
		return nil
	}

	drivers := ParseRetrieveDrivers(retrieveDriver)

	// 1. Reject any local-only retriever (postgres / sqlite).
	for _, d := range drivers {
		if _, isLocal := localOnlyRetrieveEngines[d]; isLocal {
			return fmt.Errorf(
				"DB_DRIVER=mysql is incompatible with RETRIEVE_DRIVER=%s: "+
					"the %s retriever needs the embeddings table, which MySQL mode does not create. "+
					"Set RETRIEVE_DRIVER to an external engine instead "+
					"(%s).",
				d, d, validExternalRetrieveEnginesHint(),
			)
		}
	}

	// 2. Reject empty / whitespace-only RETRIEVE_DRIVER. MySQL mode has
	// no self-hosted embeddings, so an empty retriever is never usable.
	if len(drivers) == 0 {
		return fmt.Errorf(
			"DB_DRIVER=mysql requires RETRIEVE_DRIVER to be set to at least one external engine "+
				"(%s); MySQL mode has no self-hosted embeddings table.",
			validExternalRetrieveEnginesHint(),
		)
	}

	// 3. Reject unknown engines. The names must match the keys of the
	// retriever registry so the operator does not configure an engine
	// the system will silently ignore.
	registry := types.GetRetrieverEngineMapping()
	for _, d := range drivers {
		if _, known := registry[d]; !known {
			return fmt.Errorf(
				"DB_DRIVER=mysql: RETRIEVE_DRIVER entry %q is not a registered retriever engine. "+
					"Valid external engines: %s.",
				d, validExternalRetrieveEnginesHint(),
			)
		}
	}

	// 4. Require at least one vector-capable engine. MySQL mode has no
	// local embeddings table, so vector retrieval is mandatory. An
	// engine like elasticsearch_v7 only supports keyword retrieval —
	// accepting it would let the app boot but fail every vector query.
	if !hasVectorCapableEngine(drivers, registry) {
		return fmt.Errorf(
			"DB_DRIVER=mysql requires at least one vector-capable retriever engine "+
				"(%s). All configured engines (%s) lack VectorRetrieverType capability.",
			validExternalRetrieveEnginesHint(), strings.Join(drivers, ", "),
		)
	}

	return nil
}

// hasVectorCapableEngine returns true if at least one of the given drivers
// has VectorRetrieverType in its capability set.
func hasVectorCapableEngine(drivers []string, registry map[string][]types.RetrieverEngineParams) bool {
	for _, d := range drivers {
		if caps, ok := registry[d]; ok {
			for _, c := range caps {
				if c.RetrieverType == types.VectorRetrieverType {
					return true
				}
			}
		}
	}
	return false
}

// validExternalRetrieveEnginesHint returns a human-readable list of
// retriever engines that are usable under DB_DRIVER=mysql. It is built
// from the actual registry (internal/types/tenant.go) minus the
// local-only engines, so the hint can never go stale or suggest a name
// the system does not recognise.
func validExternalRetrieveEnginesHint() string {
	registry := types.GetRetrieverEngineMapping()
	var names []string
	for name := range registry {
		if _, isLocal := localOnlyRetrieveEngines[name]; isLocal {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}
