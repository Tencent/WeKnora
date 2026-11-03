// internal/handler/secure_form_test.go
package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/application/service/credential"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeSubmitter struct {
	got    credential.SubmitRequest
	owner  credential.Owner
	result credential.SubmitResult
	err    error
}

func (f *fakeSubmitter) Submit(
	_ context.Context, owner credential.Owner, req credential.SubmitRequest,
) (credential.SubmitResult, error) {
	f.owner, f.got = owner, req
	if req.Values != nil {
		f.got.Values = make(map[string]string, len(req.Values))
		for k, v := range req.Values {
			f.got.Values[k] = v
		}
	}
	return f.result, f.err
}

func renderAppErrors(c *gin.Context) {
	c.Next()
	if len(c.Errors) == 0 {
		return
	}
	if appErr, ok := apperrors.IsAppError(c.Errors.Last().Err); ok {
		c.JSON(appErr.HTTPCode, gin.H{"success": false, "error": gin.H{"message": appErr.Message}})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"success": false})
}

func asWebUser(c *gin.Context) {
	ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "u1"})
	c.Request = c.Request.WithContext(ctx)
	c.Set(types.TenantIDContextKey.String(), uint64(1))
	c.Next()
}

func secureFormRouter(h *MCPServiceHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(renderAppErrors, asWebUser)
	r.POST("/agent/secure-forms/:prompt_id", h.SubmitSecureForm)
	return r
}

func postJSON(r http.Handler, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestSubmitSecureForm(t *testing.T) {
	sub := &fakeSubmitter{result: credential.SubmitResult{Resolution: hitl.Resolution{}}}
	h := &MCPServiceHandler{secureForms: sub}
	w := postJSON(secureFormRouter(h), "/agent/secure-forms/p1",
		`{"action":"submit","choice":"use_once","values":{"password":"sentinel-pw"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "p1", sub.got.PromptID)
	assert.Equal(t, "sentinel-pw", sub.got.Values["password"])
	assert.Equal(t, credential.Owner{TenantID: 1, UserID: "web_user:u1"}, sub.owner)
	assert.NotContains(t, w.Body.String(), "sentinel-pw")
}

func TestSubmitSecureFormErrors(t *testing.T) {
	sub := &fakeSubmitter{err: apperrors.NewBadRequestError(`field "pin" is not part of this form`)}
	h := &MCPServiceHandler{secureForms: sub}
	r := secureFormRouter(h)
	w := postJSON(r, "/agent/secure-forms/p1", `{"action":"submit","choice":"use_once","values":{"pin":"sentinel"}}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NotContains(t, w.Body.String(), "sentinel")

	w = postJSON(r, "/agent/secure-forms/p1", `{"action":"submit","unknown":"sentinel"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NotContains(t, w.Body.String(), "sentinel")

	sub.err = hitl.ErrAlreadyResolved
	sub.result = credential.SubmitResult{SavedButClosed: true}
	w = postJSON(r, "/agent/secure-forms/p1", `{"action":"submit","choice":"save","values":{"password":"x"}}`)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), `"saved":true`)

	h.secureForms = nil
	w = postJSON(r, "/agent/secure-forms/p1", `{"action":"decline"}`)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
