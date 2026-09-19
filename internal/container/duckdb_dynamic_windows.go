//go:build windows && duckdb_use_lib

package container

/*
#error "WeKnora does not support duckdb_use_lib on Windows: duckdb-go-bindings v0.10502.0 can free C.CString memory through duckdb_free across CRT heaps. Use the default static binding with a compatible UCRT toolchain."
*/
import "C"
