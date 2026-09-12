package router

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const healthProbeTimeout = 2 * time.Second

const (
	healthStatusOK          = "ok"
	healthStatusUnavailable = "unavailable"
	healthStatusDisabled    = "disabled"
)

type dependencyProbe func(context.Context) error

// HealthChecker owns the operational dependencies required before this
// process can receive application traffic. Redis is optional in Lite mode, so
// a nil Redis probe is reported as disabled and does not make readiness fail.
type HealthChecker struct {
	database dependencyProbe
	redis    dependencyProbe
}

// NewHealthChecker builds readiness probes from the infrastructure clients
// already managed by the dependency-injection container.
func NewHealthChecker(db *gorm.DB, redisClient *redis.Client) *HealthChecker {
	var databaseProbe dependencyProbe
	if db == nil {
		databaseProbe = func(context.Context) error {
			return errors.New("database client is unavailable")
		}
	} else if sqlDB, err := db.DB(); err != nil {
		databaseProbe = func(context.Context) error { return err }
	} else {
		databaseProbe = sqlDB.PingContext
	}

	var redisProbe dependencyProbe
	if redisClient != nil {
		redisProbe = func(ctx context.Context) error {
			return redisClient.Ping(ctx).Err()
		}
	}

	return newHealthCheckerWithProbes(databaseProbe, redisProbe)
}

func newHealthCheckerWithProbes(databaseProbe, redisProbe dependencyProbe) *HealthChecker {
	return &HealthChecker{database: databaseProbe, redis: redisProbe}
}

func (h *HealthChecker) check(ctx context.Context) (map[string]string, bool) {
	checks := map[string]string{
		"database": healthStatusUnavailable,
		"redis":    healthStatusDisabled,
	}
	ready := true

	if h == nil || h.database == nil || h.database(ctx) != nil {
		ready = false
	} else {
		checks["database"] = healthStatusOK
	}

	if h != nil && h.redis != nil {
		if h.redis(ctx) != nil {
			checks["redis"] = healthStatusUnavailable
			ready = false
		} else {
			checks["redis"] = healthStatusOK
		}
	}

	return checks, ready
}

func registerHealthRoutes(r *gin.Engine, checker *HealthChecker) {
	live := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"status": healthStatusOK})
	}
	r.GET("/health", live)
	r.GET("/health/live", live)
	r.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), healthProbeTimeout)
		defer cancel()

		checks, ready := checker.check(ctx)
		status := healthStatusOK
		statusCode := http.StatusOK
		if !ready {
			status = healthStatusUnavailable
			statusCode = http.StatusServiceUnavailable
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(statusCode, gin.H{"status": status, "checks": checks})
	})
}
