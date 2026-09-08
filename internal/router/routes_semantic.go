package router

// RegisterSemanticRoutes wires the semantic modeling module. This is the ONLY file the
// module adds inside the router package; router.go contributes two lines
// (a DB field on RouterParams and the call below). All module code lives in
// internal/semantic — see its README.md.
//
// Failure policy: module misconfiguration must not take the whole app down,
// so registration errors are logged and the app boots without the module.

import (
	"context"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/semantic"
)

// RegisterSemanticRoutes mounts /api/v1/semantic/* when CUBE_ENABLE is set.
func RegisterSemanticRoutes(v1 *gin.RouterGroup, db *gorm.DB, g *rbacGuards) {
	if err := semantic.Register(v1, db, g.Viewer(), g.Contributor(), g.Admin()); err != nil {
		// logger needs a ctx to extract tracing info, so never pass nil
		logger.Errorf(context.Background(), "[semantic] module disabled due to error: %v", err)
	}
}
