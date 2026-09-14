package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type learningHandlerFake struct {
	interfaces.LearningService
	calls   int
	enabled bool
}

func (s *learningHandlerFake) SetEnabled(_ context.Context, enabled bool) (*types.LearningSettings, error) {
	s.calls++
	s.enabled = enabled
	return &types.LearningSettings{Enabled: enabled, AlgorithmVersion: "bkt-v1"}, nil
}

func (s *learningHandlerFake) SubmitAnswer(context.Context, types.LearningAnswer) (*types.LearningAnswerResult, error) {
	s.calls++
	return nil, types.ErrLearningConflict
}

func TestLearningHTTPStrictRequestsAndNoStore(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"enabled":null}`,
		`{"enabled":true,"subject_id":"other"}`,
		`{"enabled":true} {}`,
		`null`,
	} {
		t.Run(body, func(t *testing.T) {
			svc := &learningHandlerFake{}
			h := NewLearningHandler(svc)
			r := gin.New()
			r.PUT("/settings", h.SetEnabled)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("PUT", "/settings", strings.NewReader(body)))
			require.Equal(t, 400, w.Code)
			require.Zero(t, svc.calls)
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		})
	}
	svc := &learningHandlerFake{}
	h := NewLearningHandler(svc)
	r := gin.New()
	r.PUT("/settings", h.SetEnabled)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("PUT", "/settings", strings.NewReader(`{"enabled":true}`)))
	require.Equal(t, 200, w.Code)
	require.True(t, svc.enabled)
	var response struct {
		Success bool                   `json:"success"`
		Data    types.LearningSettings `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.True(t, response.Data.Enabled)
}

func TestLearningAnswerCannotSupplyGradeOrIdentity(t *testing.T) {
	svc := &learningHandlerFake{}
	r := gin.New()
	r.POST("/attempts", NewLearningHandler(svc).SubmitAnswer)
	for _, extra := range []string{
		`"correct":true`,
		`"p_mastery":1`,
		`"tenant_id":8`,
		`"subject_id":"bob"`,
		`"correct_option":"a"`,
	} {
		w := httptest.NewRecorder()
		body := `{"question_id":"q","option_id":"a","attempt_id":"attempt",` + extra + `}`
		r.ServeHTTP(w, httptest.NewRequest("POST", "/attempts", strings.NewReader(body)))
		require.Equal(t, 400, w.Code)
		require.Zero(t, svc.calls)
	}
}

func TestLearningHTTPErrorsHideWrappedProviderPayloads(t *testing.T) {
	status, code, msg := learningHTTPError(errors.New("provider replied with private-answer"))
	require.Equal(t, 500, status)
	require.Equal(t, "learning_unavailable", code)
	require.NotContains(t, msg, "private-answer")
	for _, test := range []struct {
		err    error
		status int
	}{
		{types.ErrLearningDisabled, 403},
		{types.ErrLearningNotFound, 404},
		{types.ErrLearningStale, 409},
		{types.ErrLearningConflict, 409},
		{types.ErrLearningEvidence, 422},
		{types.ErrLearningBusy, 429},
	} {
		status, _, _ := learningHTTPError(test.err)
		require.Equal(t, test.status, status)
	}
}
