package middleware

import "github.com/gin-gonic/gin"

// MultipartFormCleanup 在请求结束后删除 multipart 解析写入系统临时目录的文件。
// 入参：Gin 请求上下文 c；出参：无。未解析 multipart 表单或仅使用内存的请求不会执行删除。
// 该中间件应在全部路由之前注册，以覆盖成功、失败和 panic 恢复后的上传请求。
func MultipartFormCleanup() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			// Go 在 ParseMultipartForm 落盘后会设置 MultipartForm。
			// RemoveAll 仅删除其自动创建的 multipart 临时文件，不影响已持久化的业务文件。
			if c.Request != nil && c.Request.MultipartForm != nil {
				_ = c.Request.MultipartForm.RemoveAll()
			}
		}()
		c.Next()
	}
}
