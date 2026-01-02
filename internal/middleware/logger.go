package middleware

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	maxBodySize = 1024 * 10 // 최대 10KB의 body 내용 기록
)

// loggerResponseBodyWriter 응답 내용을 캡처하기 위한 사용자 정의 ResponseWriter (로거 미들웨어용)
type loggerResponseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

// Write Write 메서드 재정의, 버퍼와 원본 writer 모두에 쓰기
func (r loggerResponseBodyWriter) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// sanitizeBody 민감한 정보 정리
func sanitizeBody(body string) string {
	result := body
	// 일반적인 민감한 필드 대체 (JSON 형식)
	sensitivePatterns := []struct {
		pattern     string
		replacement string
	}{
		{`"password"\s*:\s*"[^"]*"`, `"password":"***"`},
		{`"token"\s*:\s*"[^"]*"`, `"token":"***"`},
		{`"access_token"\s*:\s*"[^"]*"`, `"access_token":"***"`},
		{`"refresh_token"\s*:\s*"[^"]*"`, `"refresh_token":"***"`},
		{`"authorization"\s*:\s*"[^"]*"`, `"authorization":"***"`},
		{`"api_key"\s*:\s*"[^"]*"`, `"api_key":"***"`},
		{`"secret"\s*:\s*"[^"]*"`, `"secret":"***"`},
		{`"apikey"\s*:\s*"[^"]*"`, `"apikey":"***"`},
		{`"apisecret"\s*:\s*"[^"]*"`, `"apisecret":"***"`},
	}

	for _, p := range sensitivePatterns {
		re := regexp.MustCompile(p.pattern)
		result = re.ReplaceAllString(result, p.replacement)
	}

	return result
}

// readRequestBody 요청 본문 읽기 (로그용 크기 제한, 리셋용 전체 읽기)
func readRequestBody(c *gin.Context) string {
	if c.Request.Body == nil {
		return ""
	}

	// Content-Type 확인, JSON 유형만 기록
	contentType := c.GetHeader("Content-Type")
	if !strings.Contains(contentType, "application/json") &&
		!strings.Contains(contentType, "application/x-www-form-urlencoded") &&
		!strings.Contains(contentType, "text/") {
		return "[텍스트 유형이 아님, 건너뜀]"
	}

	// body 내용 전체 읽기 (크기 제한 없음), 후속 핸들러 사용을 위해 전체 리셋 필요
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return "[요청 본문 읽기 실패]"
	}

	// request body 리셋, 전체 내용 사용, 후속 핸들러가 전체 데이터를 읽을 수 있도록 보장
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 로그용 body (크기 제한)
	var logBodyBytes []byte
	if len(bodyBytes) > maxBodySize {
		logBodyBytes = bodyBytes[:maxBodySize]
	} else {
		logBodyBytes = bodyBytes
	}

	bodyStr := string(logBodyBytes)
	if len(bodyBytes) > maxBodySize {
		bodyStr += "... [내용이 너무 김, 잘림]"
	}

	return sanitizeBody(bodyStr)
}

// RequestID 컨텍스트에 고유 요청 ID를 추가하는 미들웨어
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 헤더에서 요청 ID를 가져오거나 새로 생성
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}
		safeRequestID := secutils.SanitizeForLog(requestID)
		// 헤더에 요청 ID 설정
		c.Header("X-Request-ID", requestID)

		// 컨텍스트에 요청 ID 설정
		c.Set(types.RequestIDContextKey.String(), requestID)

		// 컨텍스트에 로거 설정
		requestLogger := logger.GetLogger(c)
		requestLogger = requestLogger.WithField("request_id", safeRequestID)
		c.Set(types.LoggerContextKey.String(), requestLogger)

		// 로깅을 위해 전역 컨텍스트에 요청 ID 설정
		c.Request = c.Request.WithContext(
			context.WithValue(
				context.WithValue(c.Request.Context(), types.RequestIDContextKey, requestID),
				types.LoggerContextKey, requestLogger,
			),
		)

		c.Next()
	}
}

// Logger 요청 ID, 입력 및 출력과 함께 요청 세부 정보를 기록하는 미들웨어
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// 요청 본문 읽기 (Next 이전에 읽어야 함, Next가 본문을 소비하기 때문)
		var requestBody string
		if c.Request.Method == "POST" || c.Request.Method == "PUT" || c.Request.Method == "PATCH" {
			requestBody = readRequestBody(c)
		}

		// 응답 본문 캡처기 생성
		responseBody := &bytes.Buffer{}
		responseWriter := &loggerResponseBodyWriter{
			ResponseWriter: c.Writer,
			body:           responseBody,
		}
		c.Writer = responseWriter

		// 요청 처리
		c.Next()

		// 컨텍스트에서 요청 ID 가져오기
		requestID, exists := c.Get(types.RequestIDContextKey.String())
		requestIDStr := "unknown"
		if exists {
			if idStr, ok := requestID.(string); ok && idStr != "" {
				requestIDStr = idStr
			}
		}
		safeRequestID := secutils.SanitizeForLog(requestIDStr)

		// 지연 시간 계산
		latency := time.Since(start)

		// 클라이언트 IP 및 상태 코드 가져오기
		clientIP := c.ClientIP()
		statusCode := c.Writer.Status()
		method := c.Request.Method

		if raw != "" {
			path = path + "?" + raw
		}

		// 응답 본문 읽기
		responseBodyStr := ""
		if responseBody.Len() > 0 {
			// Content-Type 확인, JSON 유형만 기록
			contentType := c.Writer.Header().Get("Content-Type")
			if strings.Contains(contentType, "application/json") ||
				strings.Contains(contentType, "text/") {
				bodyBytes := responseBody.Bytes()
				if len(bodyBytes) > maxBodySize {
					responseBodyStr = string(bodyBytes[:maxBodySize]) + "... [내용이 너무 김, 잘림]"
				} else {
					responseBodyStr = string(bodyBytes)
				}
				responseBodyStr = sanitizeBody(responseBodyStr)
			} else {
				responseBodyStr = "[텍스트 유형이 아님, 건너뜀]"
			}
		}

		// 로그 메시지 구성
		logMsg := logger.GetLogger(c)
		logMsg = logMsg.WithFields(map[string]interface{}{
			"request_id":  safeRequestID,
			"method":      method,
			"path":        secutils.SanitizeForLog(path),
			"status_code": statusCode,
			"size":        c.Writer.Size(),
			"latency":     latency.String(),
			"client_ip":   secutils.SanitizeForLog(clientIP),
		})

		// 요청 본문 추가 (있는 경우)
		if requestBody != "" {
			logMsg = logMsg.WithField("request_body", secutils.SanitizeForLog(requestBody))
		}

		// 응답 본문 추가 (있는 경우)
		if responseBodyStr != "" {
			logMsg = logMsg.WithField("response_body", secutils.SanitizeForLog(responseBodyStr))
		}
		logMsg.Info()
	}
}
