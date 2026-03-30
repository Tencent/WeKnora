package handler

import (
	"encoding/json"
	"net/http"

	"github.com/Tencent/WeKnora/internal/datasource/connector/wecom"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// WecomCallbackHandler handles WeCom callback URL verification.
// This is needed so the WeCom admin console can verify the callback URL,
// which unlocks the "企业可信IP" configuration, which in turn unlocks
// the "可调用接口的应用" setting for document/drive APIs.
type WecomCallbackHandler struct {
	dsService interfaces.DataSourceService
}

// NewWecomCallbackHandler creates a new handler.
func NewWecomCallbackHandler(dsService interfaces.DataSourceService) *WecomCallbackHandler {
	return &WecomCallbackHandler{dsService: dsService}
}

// HandleCallback responds to WeCom's callback URL verification (GET) and
// message push (POST). Only GET verification is needed for our use case.
//
// Route: GET /api/v1/wecom/callback/:datasource_id
//
// WeCom sends: ?msg_signature=xxx&timestamp=xxx&nonce=xxx&echostr=xxx
// We decrypt echostr and return the plaintext.
func (h *WecomCallbackHandler) HandleCallback(c *gin.Context) {
	ctx := c.Request.Context()
	dsID := c.Param("datasource_id")

	ds, err := h.dsService.GetDataSource(ctx, dsID)
	if err != nil {
		logger.Warnf(ctx, "[WecomCallback] data source %s not found: %v", dsID, err)
		c.String(http.StatusNotFound, "data source not found")
		return
	}

	if ds.Type != "wecom_doc" {
		c.String(http.StatusBadRequest, "not a wecom_doc data source")
		return
	}

	var dsConfig struct {
		Credentials map[string]interface{} `json:"credentials"`
		Settings    map[string]interface{} `json:"settings"`
	}
	if err := json.Unmarshal(ds.Config, &dsConfig); err != nil {
		logger.Warnf(ctx, "[WecomCallback] failed to parse config for %s: %v", dsID, err)
		c.String(http.StatusInternalServerError, "config parse error")
		return
	}

	getString := func(m map[string]interface{}, key string) string {
		if m == nil {
			return ""
		}
		v, _ := m[key].(string)
		return v
	}

	cfg := wecom.CallbackConfig{
		Token:          getString(dsConfig.Settings, "callback_token"),
		EncodingAESKey: getString(dsConfig.Settings, "callback_aes_key"),
		CorpID:         getString(dsConfig.Credentials, "corp_id"),
	}

	if cfg.Token == "" || cfg.EncodingAESKey == "" {
		logger.Warnf(ctx, "[WecomCallback] ds %s missing callback_token or callback_aes_key in settings", dsID)
		c.String(http.StatusBadRequest, "callback_token and callback_aes_key must be configured in data source settings")
		return
	}

	msgSignature := c.Query("msg_signature")
	timestamp := c.Query("timestamp")
	nonce := c.Query("nonce")
	echostr := c.Query("echostr")

	plaintext, err := wecom.VerifyCallback(cfg, msgSignature, timestamp, nonce, echostr)
	if err != nil {
		logger.Warnf(ctx, "[WecomCallback] verification failed for ds %s: %v", dsID, err)
		c.String(http.StatusForbidden, "verification failed")
		return
	}

	logger.Infof(ctx, "[WecomCallback] callback URL verified successfully for ds %s", dsID)
	c.String(http.StatusOK, string(plaintext))
}
