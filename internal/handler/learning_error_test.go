package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/learning"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLearningMissingSchemaReturnsInternalError(t *testing.T) {
	var logs bytes.Buffer
	log := logrus.New()
	log.SetOutput(&logs)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	svc := learning.NewService(repository.NewLearningRepository(db), nil, nil, nil)
	router := gin.New()
	router.GET("/settings", NewLearningHandler(svc).GetSettings)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	ctx = types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "review"})
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "review"})
	ctx = context.WithValue(ctx, types.LoggerContextKey, logrus.NewEntry(log))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/settings", nil).WithContext(ctx)
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var result struct {
		Success bool `json:"success"`
		Error   struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.False(t, result.Success)
	require.Equal(t, "learning_unavailable", result.Error.Code)
	require.NotContains(t, response.Body.String(), "learning_profiles")
	require.Contains(t, logs.String(), "sqlite")
	require.NotContains(t, logs.String(), "learning_profiles")

	status, code, _ := learningHTTPError(types.ErrLearningBusy)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "learning_busy", code)
}
