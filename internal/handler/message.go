package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// MessageHandler 채팅 세션 내의 메시지 관련 HTTP 요청 처리
// 메시지 기록 로드 및 관리 엔드포인트를 제공합니다.
type MessageHandler struct {
	MessageService interfaces.MessageService // 메시지 비즈니스 로직을 구현하는 서비스
}

// NewMessageHandler 필요한 서비스를 사용하여 새로운 메시지 핸들러 인스턴스 생성
// 매개변수:
//   - messageService: 메시지 비즈니스 로직을 구현하는 서비스
//
// 반환값: 새로운 MessageHandler에 대한 포인터
func NewMessageHandler(messageService interfaces.MessageService) *MessageHandler {
	return &MessageHandler{
		MessageService: messageService,
	}
}

// LoadMessages godoc
// @Summary      메시지 기록 로드
// @Description  페이징 및 시간 필터링을 지원하여 세션의 메시지 기록 로드
// @Tags         메시지
// @Accept       json
// @Produce      json
// @Param        session_id   path      string  true   "세션 ID"
// @Param        limit        query     int     false  "반환 수량"  default(20)
// @Param        before_time  query     string  false  "이 시간 이전의 메시지 (RFC3339Nano 형식)"
// @Success      200          {object}  map[string]interface{}  "메시지 목록"
// @Failure      400          {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/{session_id}/load [get]
func (h *MessageHandler) LoadMessages(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start loading messages")

	// 경로 매개변수 및 쿼리 매개변수 가져오기
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	limit := secutils.SanitizeForLog(c.DefaultQuery("limit", "20"))
	beforeTimeStr := secutils.SanitizeForLog(c.DefaultQuery("before_time", ""))

	logger.Infof(ctx, "Loading messages params, session ID: %s, limit: %s, before time: %s",
		sessionID, limit, beforeTimeStr)

	// limit 매개변수 파싱, 실패 시 기본값 사용
	limitInt, err := strconv.Atoi(limit)
	if err != nil {
		logger.Warnf(ctx, "Invalid limit value, using default value 20, input: %s", limit)
		limitInt = 20
	}

	// beforeTime이 제공되지 않은 경우 가장 최근 메시지 검색
	if beforeTimeStr == "" {
		logger.Infof(ctx, "Getting recent messages for session, session ID: %s, limit: %d", sessionID, limitInt)
		messages, err := h.MessageService.GetRecentMessagesBySession(ctx, sessionID, limitInt)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError(err.Error()))
			return
		}

		logger.Infof(
			ctx,
			"Successfully retrieved recent messages, session ID: %s, message count: %d",
			sessionID, len(messages),
		)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    messages,
		})
		return
	}

	// beforeTime이 제공된 경우 타임스탬프 파싱
	beforeTime, err := time.Parse(time.RFC3339Nano, beforeTimeStr)
	if err != nil {
		logger.Errorf(
			ctx,
			"Invalid time format, please use RFC3339Nano format, err: %v, beforeTimeStr: %s",
			err, beforeTimeStr,
		)
		c.Error(errors.NewBadRequestError("Invalid time format, please use RFC3339Nano format"))
		return
	}

	// 지정된 시간 이전의 메시지 검색
	logger.Infof(ctx, "Getting messages before specific time, session ID: %s, before time: %s, limit: %d",
		sessionID, beforeTime.Format(time.RFC3339Nano), limitInt)
	messages, err := h.MessageService.GetMessagesBySessionBeforeTime(ctx, sessionID, beforeTime, limitInt)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Successfully retrieved messages before time, session ID: %s, message count: %d",
		sessionID, len(messages),
	)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    messages,
	})
}

// DeleteMessage godoc
// @Summary      메시지 삭제
// @Description  세션에서 지정된 메시지 삭제
// @Tags         메시지
// @Accept       json
// @Produce      json
// @Param        session_id  path      string  true  "세션 ID"
// @Param        id          path      string  true  "메시지 ID"
// @Success      200         {object}  map[string]interface{}  "삭제 성공"
// @Failure      500         {object}  errors.AppError         "서버 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /messages/{session_id}/{id} [delete]
func (h *MessageHandler) DeleteMessage(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting message")

	// 세션 및 메시지 식별을 위한 경로 매개변수 가져오기
	sessionID := secutils.SanitizeForLog(c.Param("session_id"))
	messageID := secutils.SanitizeForLog(c.Param("id"))

	logger.Infof(ctx, "Deleting message, session ID: %s, message ID: %s", sessionID, messageID)

	// 메시지 서비스를 사용하여 메시지 삭제
	if err := h.MessageService.DeleteMessage(ctx, sessionID, messageID); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Message deleted successfully, session ID: %s, message ID: %s", sessionID, messageID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Message deleted successfully",
	})
}
