package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type voiceInputASRStub struct {
	gotBytes []byte
	gotName  string
	err      error
}

func (s *voiceInputASRStub) Transcribe(
	_ context.Context, audio []byte, fileName string,
) (*asr.TranscriptionResult, error) {
	s.gotBytes = audio
	s.gotName = fileName
	if s.err != nil {
		return nil, s.err
	}
	return &asr.TranscriptionResult{Text: "  Hello. This is a test.  ", Duration: 1.5}, nil
}
func (s *voiceInputASRStub) GetModelName() string { return "qwen3-asr" }
func (s *voiceInputASRStub) GetModelID() string   { return "asr-1" }

type voiceInputModelServiceStub struct {
	interfaces.ModelService
	asr         *voiceInputASRStub
	err         error
	gotModelID  string
	gotTenantID uint64
}

func (s *voiceInputModelServiceStub) GetASRModel(ctx context.Context, modelID string) (asr.ASR, error) {
	s.gotModelID = modelID
	s.gotTenantID, _ = types.TenantIDFromContext(ctx)
	if s.err != nil {
		return nil, s.err
	}
	return s.asr, nil
}

// voiceInputDictation is a clip dictated to agent-1, which recognizes with
// asr-1 through the given model service.
func voiceInputDictation(t *testing.T, models *voiceInputModelServiceStub) (*gin.Context, *Handler) {
	t.Helper()
	c, _ := newVoiceInputRequest(t, map[string]string{"agent_id": "agent-1"},
		"voice.wav", []byte("RIFF....WAVEfmt "))
	return c, &Handler{
		customAgentService: &resolveOwnAgentStub{agent: &types.CustomAgent{
			ID: "agent-1", TenantID: 7,
			Config: types.CustomAgentConfig{AudioUploadEnabled: true, ASRModelID: "asr-1"},
		}},
		modelService: models,
	}
}

func requireVoiceError(t *testing.T, c *gin.Context, status int, message string) {
	t.Helper()
	require.Len(t, c.Errors, 1)
	var appErr *apperrors.AppError
	require.ErrorAs(t, c.Errors[0].Err, &appErr)
	require.Equal(t, status, appErr.HTTPCode)
	require.Contains(t, appErr.Message, message)
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

// The selection can predate its validation at save time. A model that is gone
// is the agent's configuration to fix, not a server fault.
func TestTranscribeVoiceInput_ReportsMissingASRModelAsBadRequest(t *testing.T) {
	c, h := voiceInputDictation(t, &voiceInputModelServiceStub{err: service.ErrModelNotFound})

	h.TranscribeVoiceInput(c)

	requireVoiceError(t, c, http.StatusBadRequest, "no longer exists")
}

func TestTranscribeVoiceInput_KeepsLoadFailureAsServerError(t *testing.T) {
	c, h := voiceInputDictation(t, &voiceInputModelServiceStub{err: errors.New("database is on fire")})

	h.TranscribeVoiceInput(c)

	requireVoiceError(t, c, http.StatusInternalServerError, "failed to load ASR model")
}

// Audio the vendor has documented it will not take is refused before the
// upload. It is the client's to correct; it used to be reported as an outage.
func TestTranscribeVoiceInput_ReportsRefusedAudioAsBadRequest(t *testing.T) {
	c, h := voiceInputDictation(t, &voiceInputModelServiceStub{asr: &voiceInputASRStub{
		err: &api.RejectedError{Reason: "the audio is 30.0 MB; the vendor accepts at most 25 MB per file"},
	}})

	h.TranscribeVoiceInput(c)

	requireVoiceError(t, c, http.StatusBadRequest, "at most 25 MB")
}

// The audio is the client's to correct when the vendor refuses it before the
// upload or blames it in a 400 reply. Everything else is the recognition
// service failing or misconfigured, which the client cannot fix and may retry.
func TestVoiceTranscriptionErrorSortsByWhoCanFixIt(t *testing.T) {
	vendorReply := func(status int) error {
		return fmt.Errorf("ASR transcription request failed: %w", &api.HTTPError{StatusCode: status})
	}
	networkFailure := fmt.Errorf("ASR transcription request failed: %w",
		&api.TransportError{Op: "send request", Err: io.ErrUnexpectedEOF})
	for name, err := range map[string]error{
		"refused before upload":    &api.RejectedError{Reason: "over the limit"},
		"vendor rejects the audio": vendorReply(http.StatusBadRequest),
		"vendor says too large":    vendorReply(http.StatusRequestEntityTooLarge),
		"vendor says wrong format": vendorReply(http.StatusUnsupportedMediaType),
		"vendor cannot process it": vendorReply(http.StatusUnprocessableEntity),
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, http.StatusBadRequest, voiceTranscriptionError(err).HTTPCode)
		})
	}

	for name, err := range map[string]error{
		"bad key":                     vendorReply(http.StatusUnauthorized),
		"unknown model at the vendor": vendorReply(http.StatusNotFound),
		"rate limited":                vendorReply(http.StatusTooManyRequests),
		"vendor outage":               vendorReply(http.StatusBadGateway),
		"network failure":             networkFailure,
		"vendor error in a 2xx body":  errors.New("transcription API error: busy"),
	} {
		t.Run(name, func(t *testing.T) {
			appErr := voiceTranscriptionError(err)
			require.Equal(t, http.StatusServiceUnavailable, appErr.HTTPCode)
			require.Equal(t, "transcription failed", appErr.Message)
		})
	}
}

// A refusal carries its reason as the message, and a vendor reply that blames
// the audio passes on the vendor's own words; an outage keeps the whole chain,
// which is what the operator needs.
func TestVoiceTranscriptionErrorCarriesTheReason(t *testing.T) {
	refused := voiceTranscriptionError(&api.RejectedError{Reason: "accepts at most 25 MB per file"})
	require.Equal(t, "accepts at most 25 MB per file", refused.Message)

	blamed := voiceTranscriptionError(fmt.Errorf("ASR transcription request failed: %w",
		&api.HTTPError{StatusCode: http.StatusRequestEntityTooLarge, Body: "file is 30 MB, limit is 25 MB"}))
	require.Equal(t, "file is 30 MB, limit is 25 MB", blamed.Details)

	outage := voiceTranscriptionError(fmt.Errorf("ASR transcription request failed: %w",
		&api.HTTPError{StatusCode: http.StatusServiceUnavailable, Body: "overloaded"}))
	require.Contains(t, outage.Details, "overloaded")
}
