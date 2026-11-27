package handler

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
)

// stubASRModelLookup answers the model check without a database.
type stubASRModelLookup struct {
	model *types.Model
	err   error

	lookups []string
}

func (s *stubASRModelLookup) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	s.lookups = append(s.lookups, id)
	return s.model, s.err
}

func activeASRModel(id string) *types.Model {
	return &types.Model{ID: id, Type: types.ModelTypeASR, Status: types.ModelStatusActive}
}

// asrAgentService records what the handler tries to store.
type asrAgentService struct {
	interfaces.CustomAgentService
	existing *types.CustomAgent
	created  *types.CustomAgent
	updated  *types.CustomAgent
}

func (s *asrAgentService) GetAgentByID(context.Context, string) (*types.CustomAgent, error) {
	if s.existing == nil {
		return nil, service.ErrAgentNotFound
	}
	return s.existing, nil
}

func (s *asrAgentService) CreateAgent(_ context.Context, agent *types.CustomAgent) (*types.CustomAgent, error) {
	s.created = agent
	return agent, nil
}

func (s *asrAgentService) UpdateAgent(
	_ context.Context, agent *types.CustomAgent, _ *string,
) (*types.CustomAgent, error) {
	s.updated = agent
	return agent, nil
}

func serveASRAgent(t *testing.T, method, path, body string, h *CustomAgentHandler) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.POST("/agents", h.CreateAgent)
	router.PUT("/agents/:id", h.UpdateAgent)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(7)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// A selection the workspace does not have must fail here, naming the setting.
// Saved unchecked, it failed only at the first dictation, as a server error
// that named neither the agent nor the setting.
func TestAgentASRModelValidationRejectsUnknownID(t *testing.T) {
	lookup := &stubASRModelLookup{err: service.ErrModelNotFound}
	h := &CustomAgentHandler{asrModels: lookup}

	err := h.validateAgentASRModel(agentTenantContext(),
		types.CustomAgentConfig{AudioUploadEnabled: true, ASRModelID: "asr-gone"}, "")

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	require.Equal(t, []string{"asr-gone"}, lookup.lookups)
}

func TestAgentASRModelValidationAcceptsASRModel(t *testing.T) {
	lookup := &stubASRModelLookup{model: activeASRModel("asr-1")}
	h := &CustomAgentHandler{asrModels: lookup}

	require.NoError(t, h.validateAgentASRModel(agentTenantContext(),
		types.CustomAgentConfig{AudioUploadEnabled: true, ASRModelID: "asr-1"}, ""))
}

// The editor offers ASR models only, so a chat model ID here comes from an
// API call; it used to be stored and reach the vendor's transcription
// endpoint, which answered 404 for the model.
func TestAgentASRModelValidationRejectsOtherModelType(t *testing.T) {
	lookup := &stubASRModelLookup{model: &types.Model{
		ID: "chat-1", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive,
	}}
	h := &CustomAgentHandler{asrModels: lookup}

	err := h.validateAgentASRModel(agentTenantContext(),
		types.CustomAgentConfig{AudioUploadEnabled: true, ASRModelID: "chat-1"}, "")

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	require.Equal(t, "asr_model_id must be an ASR model", appErr.Message)
}

func TestAgentASRModelValidationRejectsInactiveModel(t *testing.T) {
	lookup := &stubASRModelLookup{model: &types.Model{
		ID: "local-1", Type: types.ModelTypeASR, Status: types.ModelStatusDownloading,
	}}
	h := &CustomAgentHandler{asrModels: lookup}

	err := h.validateAgentASRModel(agentTenantContext(),
		types.CustomAgentConfig{AudioUploadEnabled: true, ASRModelID: "local-1"}, "")

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	require.Equal(t, "asr_model_id: model is not active", appErr.Message)
}

// A selection made earlier must not block an unrelated change such as a
// rename, so the stored ID is not looked up again.
func TestAgentASRModelValidationSkipsUnchangedID(t *testing.T) {
	lookup := &stubASRModelLookup{err: service.ErrModelNotFound}
	h := &CustomAgentHandler{asrModels: lookup}

	require.NoError(t, h.validateAgentASRModel(agentTenantContext(),
		types.CustomAgentConfig{AudioUploadEnabled: true, ASRModelID: "asr-gone"}, "asr-gone"))
	require.Empty(t, lookup.lookups)
}

// Most agents never dictate, so an empty selection must not pay for a lookup.
func TestAgentASRModelValidationSkipsEmptyID(t *testing.T) {
	lookup := &stubASRModelLookup{}
	h := &CustomAgentHandler{asrModels: lookup}

	require.NoError(t, h.validateAgentASRModel(agentTenantContext(),
		types.CustomAgentConfig{AudioUploadEnabled: true}, ""))
	require.Empty(t, lookup.lookups)
}

// A lookup failure is not the user's fault; it must not read as "you picked a
// model that does not exist".
func TestAgentASRModelValidationSurfacesLookupFailure(t *testing.T) {
	lookup := &stubASRModelLookup{err: stderrors.New("database is on fire")}
	h := &CustomAgentHandler{asrModels: lookup}

	err := h.validateAgentASRModel(agentTenantContext(),
		types.CustomAgentConfig{ASRModelID: "asr-1"}, "")

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusInternalServerError, appErr.HTTPCode)
}

func TestAgentASRModelValidationSkipsWhenServiceMissing(t *testing.T) {
	h := &CustomAgentHandler{}

	require.NoError(t, h.validateAgentASRModel(agentTenantContext(),
		types.CustomAgentConfig{ASRModelID: "asr-1"}, ""))
}

func TestCreateAgentRejectsUnknownASRModelBeforeStoring(t *testing.T) {
	svc := &asrAgentService{}
	h := &CustomAgentHandler{service: svc, asrModels: &stubASRModelLookup{err: service.ErrModelNotFound}}

	rec := serveASRAgent(t, http.MethodPost, "/agents",
		`{"name":"Dictation","config":{"audio_upload_enabled":true,"asr_model_id":"asr-gone"}}`, h)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "asr_model_id")
	require.Nil(t, svc.created, "the agent must not be stored with a selection that cannot work")
}

func TestUpdateAgentRechecksOnlyAChangedASRModel(t *testing.T) {
	existing := &types.CustomAgent{
		ID: "custom-1", Name: "Dictation", TenantID: 7,
		Config: types.CustomAgentConfig{AudioUploadEnabled: true, ASRModelID: "asr-gone"},
	}
	svc := &asrAgentService{existing: existing}
	lookup := &stubASRModelLookup{model: activeASRModel("asr-new")}
	h := &CustomAgentHandler{service: svc, asrModels: lookup}
	body := func(name, id string) string {
		return `{"name":"` + name + `","config":{"audio_upload_enabled":true,"asr_model_id":"` + id + `"}}`
	}

	rec := serveASRAgent(t, http.MethodPut, "/agents/custom-1", body("Renamed", "asr-gone"), h)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, lookup.lookups, "a stored selection is not re-checked on a rename")

	rec = serveASRAgent(t, http.MethodPut, "/agents/custom-1", body("Renamed", "asr-new"), h)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"asr-new"}, lookup.lookups)
	require.NotNil(t, svc.updated)
	assert.Equal(t, "asr-new", svc.updated.Config.ASRModelID)

	lookup.err = service.ErrModelNotFound
	rec = serveASRAgent(t, http.MethodPut, "/agents/custom-1", body("Renamed", "asr-other"), h)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}
