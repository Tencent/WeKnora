package middleware

import (
	"fmt"
	"runtime/debug"

	"github.com/sirupsen/logrus"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
)

// Recovery is a middleware that recovers from panics
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// Get request ID from context
				ctx := c.Request.Context()
				requestID, _ := c.Get("RequestID")

				// Print stacktrace
				stacktrace := debug.Stack()
				// Log error with structured logger
				logger.ErrorWithFields(ctx, fmt.Errorf("panic: %v", err), logrus.Fields{
					"request_id": requestID,
					"stacktrace": string(stacktrace),
				})

				// 返回500错误
				c.AbortWithStatusJSON(500, gin.H{
					"error":   "Internal Server Error",
					"message": fmt.Sprintf("%v", err),
				})
			}
		}()

		c.Next()
	}
}
