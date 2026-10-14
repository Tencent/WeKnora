package operations

import (
	"sync"

	docs "github.com/Tencent/WeKnora/docs"
)

var (
	builtinOnce    sync.Once
	builtinCatalog *Catalog
	builtinErr     error
)

// Builtin returns the catalog compiled from the Swagger document embedded in
// the binary: every operation annotated with x-agent. It panics on an invalid
// declaration, which tests catch before release.
func Builtin() *Catalog {
	builtinOnce.Do(func() {
		var ops []Operation
		ops, builtinErr = FromSpec([]byte(docs.SwaggerInfo.ReadDoc()))
		if builtinErr == nil {
			builtinCatalog, builtinErr = NewCatalog(ops...)
		}
	})
	if builtinErr != nil {
		panic(builtinErr)
	}
	return builtinCatalog
}
