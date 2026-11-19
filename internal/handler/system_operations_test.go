package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type summarySettings struct{ runtimeTestSettings }

func (summarySettings) List(context.Context) ([]*types.SystemSetting, error) {
	return []*types.SystemSetting{
		{Key: "sandbox.docker_enabled", Value: types.JSON(`false`)},
		{Key: "unknown", Value: types.JSON(`"secret"`)},
		{Key: "auth.registration_mode", Value: types.JSON(`"secret"`), IsSecret: true},
	}, nil
}

type failingRuntimeInspector struct{ runtimeTestInspector }

func (failingRuntimeInspector) QueueStats(context.Context) ([]types.QueueStat, bool, error) {
	return nil, true, errors.New("private connection details")
}

func (failingRuntimeInspector) WorkerServerStats(context.Context) ([]types.WorkerServerStat, bool, error) {
	return nil, true, errors.New("private connection details")
}

func TestSystemAdminSettingsProjectionUsesActualResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &SystemHandler{systemSettingSvc: summarySettings{}}
	r := gin.New()
	r.GET("/api/v1/system/admin/settings-summary", h.ListSystemSettingSummaries)
	op, _ := operations.Builtin().Get("system_admin.list_settings")
	for _, path := range []string{"", "?key=sandbox.docker_enabled", "?key=unknown"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/system/admin/settings-summary"+path, nil))
		if path == "?key=unknown" {
			assert.Equal(t, http.StatusNotFound, w.Code)
			continue
		}
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		out, err := op.Project(w.Body.Bytes())
		require.NoError(t, err)
		blob, _ := json.Marshal(out)
		assert.Contains(t, string(blob), `"value_json":"false"`)
		assert.NotContains(t, string(blob), "secret")
		assert.NotContains(t, string(blob), "unknown")
	}
}

func TestSystemAdminRuntimeDiagnosticsExcludeRawErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inspector := &runtimeTaskTestInspector{
		tasks: []types.RuntimeTaskInfo{
			{
				ID:          "t1",
				State:       types.RuntimeTaskArchived,
				LastError:   "deadline exceeded: https://secret:password@internal/doc?token=private",
				Worker:      "private-host",
				Retried:     5,
				KnowledgeID: "k1",
			},
		},
		hasMore:    true,
		nextCursor: "next",
	}
	h := &SystemHandler{taskInspector: inspector}
	r := gin.New()
	r.GET("/api/v1/system/admin/runtime/queues/:queue/tasks", h.ListRuntimeTasks)
	w := httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(
			"GET",
			"/api/v1/system/admin/runtime/queues/"+types.QueueDefault+"/tasks?state=archived",
			nil,
		),
	)
	require.Equal(t, 200, w.Code, w.Body.String())
	op, _ := operations.Builtin().Get("system_admin.list_tasks")
	out, err := op.Project(w.Body.Bytes())
	require.NoError(t, err)
	blob, _ := json.Marshal(out)
	assert.Contains(t, string(blob), `"error_category":"timeout"`)
	assert.Contains(t, string(blob), `"next_cursor":"next"`)
	assert.Contains(t, string(blob), `"knowledge_id":"k1"`)
	assert.NotContains(t, string(blob), "private")
	assert.NotContains(t, string(blob), "password")
	assert.NotContains(t, string(blob), "last_error")
	for _, inspector := range []struct {
		h      *SystemHandler
		failed bool
	}{{&SystemHandler{}, false}, {&SystemHandler{taskInspector: failingRuntimeInspector{}}, true}} {
		w = httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Request = httptest.NewRequest("GET", "/api/v1/system/admin/runtime/queues", nil)
		inspector.h.GetRuntimeQueues(ctx)
		op, _ = operations.Builtin().Get("system_admin.get_queues")
		out, err = op.Project(w.Body.Bytes())
		require.NoError(t, err)
		blob, _ = json.Marshal(out)
		assert.NotContains(t, string(blob), "private")
		var response RuntimeQueuesResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		assert.Equal(t, inspector.failed, response.QueuesError)
		assert.Equal(t, inspector.failed, response.WorkersError)
		if !inspector.failed {
			assert.False(t, response.Available)
		}
	}
}
