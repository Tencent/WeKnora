package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/gin-gonic/gin"
)

func TestRuntimeQueuesReportsSharedAdmissionWithoutRedis(t *testing.T) {
	if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{Enabled: true, Concurrency: 1, MaxWaiting: 2, InteractiveBurst: 3, MaxWait: time.Second}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{}); err != nil {
			t.Error(err)
		}
	})
	if err := limiter.RegisterModelEndpoint("runtime-probe", "http://127.0.0.1:8083/v1"); err != nil {
		t.Fatal(err)
	}
	release, err := limiter.AcquireCall(context.Background(), "runtime-probe", "probe", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest("GET", "/system/admin/runtime/queues", nil)
	(&SystemHandler{}).GetRuntimeQueues(ctx)
	if rec.Code != 200 {
		t.Fatalf("runtime status %d", rec.Code)
	}
	var got RuntimeQueuesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	s := got.SharedModelAdmission
	if !s.Enabled || s.Active != 1 || s.Limit != 1 || s.Waiting != 0 || s.QueueLimit != 2 {
		t.Fatalf("wrong actual occupancy %+v", s)
	}
	if len(s.Groups) != 1 {
		t.Fatalf("missing actual endpoint group %+v", s.Groups)
	}
	if strings.Contains(rec.Body.String(), "127.0.0.1") || strings.Contains(rec.Body.String(), "runtime-probe") {
		t.Fatal("runtime leaked endpoint/model identity")
	}
}
