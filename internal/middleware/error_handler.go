package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
)

// ErrorHandler 是一个处理应用错误的中间件
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 处理请求
		c.Next()

		// 检查是否有错误
		if len(c.Errors) > 0 {
			// 获取最后一个错误
			err := c.Errors.Last().Err

			// 检查是否为应用错误
			if appErr, ok := errors.IsAppError(err); ok {
				// 返回应用错误
				c.JSON(appErr.HTTPCode, gin.H{
					"success": false,
					"error": gin.H{
						"code":    appErr.Code,
						"message": appErr.Message,
						"details": appErr.Details,
					},
				})
				return
			}

			// 处理其他类型的错误
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error": gin.H{
					"code":    errors.ErrInternalServer,
					"message": "Internal server error",
				},
			})
		}
	}
}
