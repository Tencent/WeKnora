package middleware

import (
	"fmt"
	"log"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// Recovery 패닉에서 복구하는 미들웨어
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// 요청 ID 가져오기
				requestID, _ := c.Get("RequestID")

				// 스택 트레이스 출력
				stacktrace := debug.Stack()
				// 오류 로그 기록
				log.Printf("[PANIC] %s | %v | %s", requestID, err, stacktrace)

				// 500 오류 반환
				c.AbortWithStatusJSON(500, gin.H{
					"error":   "Internal Server Error",
					"message": fmt.Sprintf("%v", err),
				})
			}
		}()

		c.Next()
	}
}
