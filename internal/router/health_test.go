package router

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestHealthAndLiveness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerHealthRoutes(r, nil, nil)

	for _, path := range []string{"/health", "/livez"} {
		recorder := performRequest(r, path)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.JSONEq(t, `{"status":"ok"}`, recorder.Body.String())
	}
}

func TestReadinessWithoutRedis(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)

	r := gin.New()
	registerHealthRoutes(r, db, nil)

	recorder := performRequest(r, "/readyz")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"status":"ready","checks":{"database":"ok","redis":"disabled"}}`, recorder.Body.String())
}

func TestReadinessDoesNotExposeDependencyErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/readyz", newReadinessHandler([]readinessCheck{
		{name: "database", probe: func(context.Context) error { return errors.New("mysql password is secret") }},
	}, time.Second))

	recorder := performRequest(r, "/readyz")
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.JSONEq(t, `{"status":"not_ready","checks":{"database":"failed"}}`, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "password")
	require.NotContains(t, recorder.Body.String(), "secret")
}

func TestReadinessWithUnavailableRedis(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)

	redisClient := redis.NewClient(&redis.Options{
		Addr: "redis:6379",
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("redis password is secret")
		},
	})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })

	r := gin.New()
	registerHealthRoutes(r, db, redisClient)

	recorder := performRequest(r, "/readyz")
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.JSONEq(t, `{"status":"not_ready","checks":{"database":"ok","redis":"failed"}}`, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "password")
	require.NotContains(t, recorder.Body.String(), "secret")
}

func TestReadinessTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/readyz", newReadinessHandler([]readinessCheck{
		{name: "database", probe: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}},
	}, 20*time.Millisecond))

	start := time.Now()
	recorder := performRequest(r, "/readyz")
	require.Less(t, time.Since(start), 500*time.Millisecond)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.JSONEq(t, `{"status":"not_ready","checks":{"database":"failed"}}`, recorder.Body.String())
}

func TestReadinessWithoutDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerHealthRoutes(r, nil, nil)

	recorder := performRequest(r, "/readyz")
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.JSONEq(t, `{"status":"not_ready","checks":{"database":"failed","redis":"disabled"}}`, recorder.Body.String())
}

func performRequest(r http.Handler, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	r.ServeHTTP(recorder, request)
	return recorder
}
