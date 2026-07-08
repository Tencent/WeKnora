//go:build !cgo

package utils

import (
	"errors"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

var errPgQueryRequiresCGO = errors.New("pg_query SQL parser requires CGO; rebuild with CGO_ENABLED=1")

func parsePgQuery(sql string) (*pg_query.ParseResult, error) {
	return nil, errPgQueryRequiresCGO
}

func deparsePgQuery(result *pg_query.ParseResult) (string, error) {
	return "", errPgQueryRequiresCGO
}
