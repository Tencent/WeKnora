package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
)

// ErrorHandler 애플리케이션 오류를 처리하는 미들웨어
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 요청 처리
		c.Next()

		// 오류가 있는지 확인
		if len(c.Errors) > 0 {
			// 마지막 오류 가져오기
			err := c.Errors.Last().Err

			// 애플리케이션 오류인지 확인
			if appErr, ok := errors.IsAppError(err); ok {
				// 애플리케이션 오류 반환
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

			// 기타 유형의 오류 처리
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
