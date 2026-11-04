package session

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type voiceInputASRStub struct {
	gotBytes []byte
	gotName  string
}

func (s *voiceInputASRStub) Transcribe(
	_ context.Context, audio []byte, fileName string,
) (*asr.TranscriptionResult, error) {
	s.gotBytes = audio
	s.gotName = fileName
	return &asr.TranscriptionResult{Text: "  Hello. This is a test.  ", Duration: 1.5}, nil
}
func (s *voiceInputASRStub) GetModelName() string { return "qwen3-asr" }
func (s *voiceInputASRStub) GetModelID() string   { return "asr-1" }

type voiceInputModelServiceStub struct {
	interfaces.ModelService
	asr         *voiceInputASRStub
	gotModelID  string
	gotTenantID uint64
}

func (s *voiceInputModelServiceStub) GetASRModel(ctx context.Context, modelID string) (asr.ASR, error) {
	s.gotModelID = modelID
	s.gotTenantID, _ = types.TenantIDFromContext(ctx)
	return s.asr, nil
}

func newVoiceInputRequest(
	t *testing.T, fields map[string]string, fileName string, audio []byte,
) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	for k, v := range fields {
		require.NoError(t, w.WriteField(k, v))
	}
	if fileName != "" {
		part, err := w.CreateFormFile("file", fileName)
		require.NoError(t, err)
		_, err = part.Write(audio)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/sessions/transcriptions", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	c.Set(types.UserIDContextKey.String(), "user-1")
	c.Set(types.TenantIDContextKey.String(), uint64(7))
	c.Request = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(7)))
	return c, rec
}

func TestTranscribeVoiceInput_RejectsAgentWithoutASR(t *testing.T) {
	c, _ := newVoiceInputRequest(t, map[string]string{"agent_id": "agent-1"}, "voice.wav", []byte("RIFF"))
	models := &voiceInputModelServiceStub{asr: &voiceInputASRStub{}}
	h := &Handler{
		customAgentService: &resolveOwnAgentStub{agent: &types.CustomAgent{ID: "agent-1", TenantID: 7}},
		modelService:       models,
	}

	h.TranscribeVoiceInput(c)

	require.Len(t, c.Errors, 1)
	require.Contains(t, c.Errors[0].Error(), "voice input is not enabled")
	require.Empty(t, models.gotModelID)
}

func TestTranscribeVoiceInput_RejectsMissingAgent(t *testing.T) {
	c, _ := newVoiceInputRequest(t, nil, "voice.wav", []byte("RIFF"))
	h := &Handler{modelService: &voiceInputModelServiceStub{asr: &voiceInputASRStub{}}}

	h.TranscribeVoiceInput(c)

	require.Len(t, c.Errors, 1)
	require.Contains(t, c.Errors[0].Error(), "agent_id is required")
}

func TestTranscribeVoiceInput_RejectsNonAudioFile(t *testing.T) {
	c, _ := newVoiceInputRequest(t, map[string]string{"agent_id": "agent-1"}, "notes.txt", []byte("hi"))
	h := &Handler{modelService: &voiceInputModelServiceStub{asr: &voiceInputASRStub{}}}

	h.TranscribeVoiceInput(c)

	require.Len(t, c.Errors, 1)
	require.Contains(t, c.Errors[0].Error(), "unsupported audio format")
}

func TestTranscribeVoiceInput_TranscribesWithAgentModel(t *testing.T) {
	audio := []byte("RIFF....WAVEfmt ")
	c, rec := newVoiceInputRequest(t, map[string]string{"agent_id": "agent-1"}, "voice.wav", audio)
	stubASR := &voiceInputASRStub{}
	models := &voiceInputModelServiceStub{asr: stubASR}
	h := &Handler{
		customAgentService: &resolveOwnAgentStub{agent: &types.CustomAgent{
			ID: "agent-1", TenantID: 7,
			Config: types.CustomAgentConfig{AudioUploadEnabled: true, ASRModelID: "asr-1"},
		}},
		modelService: models,
	}

	h.TranscribeVoiceInput(c)

	require.Empty(t, c.Errors)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "asr-1", models.gotModelID)
	require.Equal(t, uint64(7), models.gotTenantID)
	require.Equal(t, audio, stubASR.gotBytes)
	require.Equal(t, "voice.wav", stubASR.gotName)

	var resp struct {
		Success bool                       `json:"success"`
		Data    VoiceTranscriptionResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Equal(t, "Hello. This is a test.", resp.Data.Text)
	require.Equal(t, 1.5, resp.Data.Duration)
}

func TestVoiceLanguageHint(t *testing.T) {
	require.Equal(t, "zh", voiceLanguageHint("zh-CN"))
	require.Equal(t, "zh", voiceLanguageHint("zh-TW"))
	require.Equal(t, "en", voiceLanguageHint("en-US"))
	require.Equal(t, "ja", voiceLanguageHint("ja-JP"))
	require.Equal(t, "ko", voiceLanguageHint("ko_KR"))
	require.Equal(t, "ru", voiceLanguageHint(" RU "))
	require.Equal(t, "", voiceLanguageHint(""))
	require.Equal(t, "", voiceLanguageHint("x"))
	require.Equal(t, "", voiceLanguageHint("中文"))
	require.Equal(t, "", voiceLanguageHint("en1-US"))
}

func TestTranscribeVoiceInput_SharedAgentUsesOwnerWorkspace(t *testing.T) {
	c, rec := newVoiceInputRequest(t, map[string]string{
		"agent_id":                     "builtin-smart-reasoning",
		types.AgentSourceTenantIDParam: "84",
	}, "voice.wav", []byte("RIFF"))
	models := &voiceInputModelServiceStub{asr: &voiceInputASRStub{}}
	h := &Handler{
		agentShareService: &resolveAgentShareStub{agent: &types.CustomAgent{
			ID: "builtin-smart-reasoning", TenantID: 84,
			Config: types.CustomAgentConfig{AudioUploadEnabled: true, ASRModelID: "asr-shared"},
		}},
		customAgentService: &resolveOwnAgentStub{},
		modelService:       models,
	}

	h.TranscribeVoiceInput(c)

	require.Empty(t, c.Errors)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "asr-shared", models.gotModelID)
	require.Equal(t, uint64(84), models.gotTenantID)
}
