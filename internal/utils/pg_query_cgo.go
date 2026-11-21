//go:build cgo

package utils

import pg_query "github.com/pganalyze/pg_query_go/v6"

func parsePostgreSQL(sql string) (*pg_query.ParseResult, error) {
	return pg_query.Parse(sql)
}

func deparsePostgreSQL(tree *pg_query.ParseResult) (string, error) {
	return pg_query.Deparse(tree)
}
