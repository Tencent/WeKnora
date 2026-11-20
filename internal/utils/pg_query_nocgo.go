//go:build !cgo

package utils

import (
	"errors"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

var errPGQueryRequiresCGO = errors.New("pg_query SQL parser requires cgo")

func parsePostgreSQL(_ string) (*pg_query.ParseResult, error) {
	return nil, errPGQueryRequiresCGO
}

func deparsePostgreSQL(_ *pg_query.ParseResult) (string, error) {
	return "", errPGQueryRequiresCGO
}
