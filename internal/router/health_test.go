package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

type healthTestResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func TestHealthLiveDoesNotProbeDependencies(t *testing.T) {
	var calls atomic.Int32
	probe := func(context.Context) error {
		calls.Add(1)
		return errors.New("sensitive dependency failure")
	}
	engine := gin.New()
	registerHealthRoutes(engine, newHealthCheckerWithProbes(probe, probe))

	for _, path := range []string{"/health", "/health/live"} {
		recorder := performHealthRequest(engine, path)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, http.StatusOK)
		}
		var response healthTestResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode GET %s response: %v", path, err)
		}
		if response.Status != healthStatusOK {
			t.Fatalf("GET %s status body = %q, want %q", path, response.Status, healthStatusOK)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("liveness invoked dependency probes %d times, want 0", got)
	}
}

func TestHealthReadyReportsRequiredDependencies(t *testing.T) {
	tests := []struct {
		name          string
		databaseProbe dependencyProbe
		redisProbe    dependencyProbe
		wantCode      int
		wantStatus    string
		wantDatabase  string
		wantRedis     string
	}{
		{
			name:          "database and redis available",
			databaseProbe: successfulHealthProbe,
			redisProbe:    successfulHealthProbe,
			wantCode:      http.StatusOK,
			wantStatus:    healthStatusOK,
			wantDatabase:  healthStatusOK,
			wantRedis:     healthStatusOK,
		},
		{
			name:          "database unavailable",
			databaseProbe: failingHealthProbe,
			redisProbe:    successfulHealthProbe,
			wantCode:      http.StatusServiceUnavailable,
			wantStatus:    healthStatusUnavailable,
			wantDatabase:  healthStatusUnavailable,
			wantRedis:     healthStatusOK,
		},
		{
			name:          "configured redis unavailable",
			databaseProbe: successfulHealthProbe,
			redisProbe:    failingHealthProbe,
			wantCode:      http.StatusServiceUnavailable,
			wantStatus:    healthStatusUnavailable,
			wantDatabase:  healthStatusOK,
			wantRedis:     healthStatusUnavailable,
		},
		{
			name:          "redis disabled",
			databaseProbe: successfulHealthProbe,
			redisProbe:    nil,
			wantCode:      http.StatusOK,
			wantStatus:    healthStatusOK,
			wantDatabase:  healthStatusOK,
			wantRedis:     healthStatusDisabled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := gin.New()
			registerHealthRoutes(engine, newHealthCheckerWithProbes(test.databaseProbe, test.redisProbe))

			recorder := performHealthRequest(engine, "/health/ready")
			if recorder.Code != test.wantCode {
				t.Fatalf("status code = %d, want %d; body=%s", recorder.Code, test.wantCode, recorder.Body.String())
			}
			var response healthTestResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Status != test.wantStatus {
				t.Errorf("status = %q, want %q", response.Status, test.wantStatus)
			}
			if response.Checks["database"] != test.wantDatabase {
				t.Errorf("database = %q, want %q", response.Checks["database"], test.wantDatabase)
			}
			if response.Checks["redis"] != test.wantRedis {
				t.Errorf("redis = %q, want %q", response.Checks["redis"], test.wantRedis)
			}
			if strings.Contains(recorder.Body.String(), "sensitive dependency failure") {
				t.Fatal("readiness response exposed an internal dependency error")
			}
		})
	}
}

func TestNewHealthCheckerRequiresDatabase(t *testing.T) {
	engine := gin.New()
	registerHealthRoutes(engine, NewHealthChecker(nil, nil))

	recorder := performHealthRequest(engine, "/health/ready")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf(
			"status code = %d, want %d; body=%s",
			recorder.Code,
			http.StatusServiceUnavailable,
			recorder.Body.String(),
		)
	}

	var response healthTestResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != healthStatusUnavailable {
		t.Errorf("status = %q, want %q", response.Status, healthStatusUnavailable)
	}
	if response.Checks["database"] != healthStatusUnavailable {
		t.Errorf("database = %q, want %q", response.Checks["database"], healthStatusUnavailable)
	}
	if response.Checks["redis"] != healthStatusDisabled {
		t.Errorf("redis = %q, want %q", response.Checks["redis"], healthStatusDisabled)
	}
}

func successfulHealthProbe(context.Context) error { return nil }

func failingHealthProbe(context.Context) error {
	return errors.New("sensitive dependency failure")
}

func performHealthRequest(engine http.Handler, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}
