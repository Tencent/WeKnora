package router

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/gin-gonic/gin"
)

// assertOperationRoutesExist panics when an agent catalog operation names a
// route the engine does not serve, so route drift fails at startup instead of
// turning an operation into a permanent 404.
func assertOperationRoutesExist(r *gin.Engine, catalog *operations.Catalog) {
	if missing := missingOperationRoutes(r, catalog); len(missing) > 0 {
		panic(fmt.Sprintf("agent operations point at unregistered routes: %v", missing))
	}
}

func missingOperationRoutes(r *gin.Engine, catalog *operations.Catalog) []string {
	served := make(map[string]bool)
	for _, route := range r.Routes() {
		served[route.Method+" "+route.Path] = true
	}
	var missing []string
	for _, op := range catalog.All() {
		if key := op.Method + " " + op.Route; !served[key] {
			missing = append(missing, op.ID+" ("+key+")")
		}
	}
	return missing
}
