package session

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// maxVoiceInputBytes bounds one dictated clip. 16kHz mono WAV is ~32KB/s, so
// this leaves room for several minutes even when the global upload cap is lower.
const maxVoiceInputBytes int64 = 25 * 1024 * 1024

// VoiceTranscriptionResponse is the text dictated into the chat box.
type VoiceTranscriptionResponse struct {
	Text     string  `json:"text"`
	Duration float64 `json:"duration,omitempty"`
}

// TranscribeVoiceInput godoc
// @Summary      语音输入转写
// @Description  使用当前智能体配置的 ASR 模型将一段录音转写为文本，用于填充对话输入框。智能体需开启音频上传并选择 ASR 模型。
// @Tags         会话
// @Accept       multipart/form-data
// @Produce      json
// @Param        file                    formData  file    true   "录音文件（推荐 16kHz 单声道 WAV）"
// @Param        agent_id                formData  string  true   "智能体ID"
// @Param        agent_source_tenant_id  formData  string  false  "共享智能体所属空间ID"
// @Param        locale                  formData  string  false  "界面语言（如 zh-CN、ja-JP）；取主语言（zh、ja）作为识别语言提示"
// @Success      200  {object}  map[string]interface{}  "data: VoiceTranscriptionResponse"
// @Failure      400  {object}  map[string]interface{}  "智能体未启用语音识别或文件无效"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/transcriptions [post]
func (h *Handler) TranscribeVoiceInput(c *gin.Context) {
	ctx := c.Request.Context()

	maxBytes := min(secutils.GetMaxFileSize(), maxVoiceInputBytes)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+1024*1024)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			_ = c.Error(apperrors.NewBadRequestError(fmt.Sprintf("audio cannot exceed %d MB", maxBytes/1024/1024)))
			return
		}
		_ = c.Error(apperrors.NewBadRequestError("audio file is required"))
		return
	}
	if fileHeader.Size > maxBytes {
		_ = c.Error(apperrors.NewBadRequestError(fmt.Sprintf("audio cannot exceed %d MB", maxBytes/1024/1024)))
		return
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileHeader.Filename)), ".")
	if !isAudioExtension(ext) && ext != "webm" {
		_ = c.Error(apperrors.NewBadRequestError("unsupported audio format"))
		return
	}

	sourceTenantID, parseErr := types.ParseAgentSourceTenantID(c.PostForm(types.AgentSourceTenantIDParam))
	if parseErr != nil {
		_ = c.Error(apperrors.NewBadRequestError(parseErr.Error()))
		return
	}
	agentID := strings.TrimSpace(c.PostForm("agent_id"))
	if agentID == "" {
		_ = c.Error(apperrors.NewBadRequestError("agent_id is required"))
		return
	}
	agent, resourceTenantID, _ := h.resolveAgent(ctx, c, agentID, sourceTenantID)
	if agent == nil {
		_ = c.Error(apperrors.NewNotFoundError("Agent not found"))
		return
	}
	if !agent.Config.AudioUploadEnabled || agent.Config.ASRModelID == "" {
		_ = c.Error(apperrors.NewBadRequestError(
			"voice input is not enabled or no ASR model is configured for this agent"))
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		_ = c.Error(apperrors.NewBadRequestError("failed to open audio file"))
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		_ = c.Error(apperrors.NewBadRequestError("failed to read audio file"))
		return
	}
	if int64(len(data)) > maxBytes {
		_ = c.Error(apperrors.NewBadRequestError(fmt.Sprintf("audio cannot exceed %d MB", maxBytes/1024/1024)))
		return
	}
	if len(data) == 0 {
		_ = c.Error(apperrors.NewBadRequestError("audio file is empty"))
		return
	}

	// A shared agent's ASR model lives in the owner's workspace.
	runCtx := ctx
	if resourceTenantID != 0 {
		runCtx = types.WithExecutionTenant(ctx, resourceTenantID)
	}
	asrModel, err := h.modelService.GetASRModel(runCtx, agent.Config.ASRModelID)
	if err != nil {
		logger.Errorf(ctx, "voice input: failed to load ASR model %s: %v",
			secutils.SanitizeForLog(agent.Config.ASRModelID), err)
		_ = c.Error(apperrors.NewInternalServerError("failed to load ASR model").WithDetails(err.Error()))
		return
	}
	if language := voiceLanguageHint(c.PostForm("locale")); language != "" {
		runCtx = asr.WithLanguage(runCtx, language)
	}
	result, err := asrModel.Transcribe(runCtx, data, fileHeader.Filename)
	if err != nil {
		logger.Errorf(ctx, "voice input: transcription failed: %v", err)
		_ = c.Error(apperrors.NewServiceUnavailableError("transcription failed").WithDetails(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": VoiceTranscriptionResponse{
		Text:     strings.TrimSpace(result.Text),
		Duration: result.Duration,
	}})
}

// voiceLanguageHint maps a UI locale (zh-CN, en_US, ja) to the ISO 639-1
// language code ASR models expect. Anything that is not a language tag yields "".
func voiceLanguageHint(locale string) string {
	primary, _, _ := strings.Cut(strings.TrimSpace(locale), "-")
	primary, _, _ = strings.Cut(primary, "_")
	primary = strings.ToLower(primary)
	if len(primary) < 2 || len(primary) > 3 {
		return ""
	}
	for _, r := range primary {
		if r < 'a' || r > 'z' {
			return ""
		}
	}
	return primary
}
