package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPickHostProjectDirUnavailableWithoutDesktop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	SetHostProjectPicker(nil)
	t.Cleanup(func() { SetHostProjectPicker(nil) })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/system/host-project-dir", nil)
	(&SystemHandler{}).PickHostProjectDir(c)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestPickHostProjectDirReturnsPickedPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	SetHostProjectPicker(func() (string, error) {
		return "/Users/dev/My Project", nil
	})
	t.Cleanup(func() { SetHostProjectPicker(nil) })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/system/host-project-dir", nil)
	(&SystemHandler{}).PickHostProjectDir(c)
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Data struct {
			Dir string `json:"dir"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "/Users/dev/My Project", body.Data.Dir)
}

func TestPickHostProjectDirCancelReturnsEmptyDir(t *testing.T) {
	gin.SetMode(gin.TestMode)
	SetHostProjectPicker(func() (string, error) { return "", nil })
	t.Cleanup(func() { SetHostProjectPicker(nil) })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/system/host-project-dir", nil)
	(&SystemHandler{}).PickHostProjectDir(c)
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Data struct {
			Dir string `json:"dir"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Empty(t, body.Data.Dir)
}
