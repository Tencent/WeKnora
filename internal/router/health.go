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

const readinessCheckTimeout = 2 * time.Second

type readinessCheck struct {
	name     string
	disabled bool
	probe    func(context.Context) error
}

func registerHealthRoutes(r gin.IRouter, db *gorm.DB, redisClient *redis.Client) {
	livenessHandler := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}

	r.GET("/health", livenessHandler)
	r.GET("/livez", livenessHandler)
	r.GET("/readyz", newReadinessHandler([]readinessCheck{
		{
			name: "database",
			probe: func(ctx context.Context) error {
				if db == nil {
					return errors.New("database is not configured")
				}

				sqlDB, err := db.DB()
				if err != nil {
					return err
				}
				return sqlDB.PingContext(ctx)
			},
		},
		{
			name:     "redis",
			disabled: redisClient == nil,
			probe: func(ctx context.Context) error {
				return redisClient.Ping(ctx).Err()
			},
		},
	}, readinessCheckTimeout))
}

func newReadinessHandler(checks []readinessCheck, timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		statuses := gin.H{}
		failed := false

		for _, check := range checks {
			if check.disabled {
				statuses[check.name] = "disabled"
				continue
			}

			ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
			err := check.probe(ctx)
			cancel()
			if err != nil {
				statuses[check.name] = "failed"
				failed = true
				continue
			}

			statuses[check.name] = "ok"
		}

		if failed {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "checks": statuses})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": statuses})
	}
}
