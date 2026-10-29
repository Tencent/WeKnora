package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/gin-gonic/gin"
)

func TestOperationsStatusUnknownAndStale(t *testing.T) {
	now := time.Unix(2000, 0)
	p := filepath.Join(t.TempDir(), "status.json")
	if readOperationsStatus(p, now).Available {
		t.Fatal("missing sample must be unknown")
	}
	for _, bad := range []string{
		`{}`,
		`{"collected_at":3000}`,
		`broken`,
		`{"collected_at":1900,"host":{"cpu_count":"bad"}}`,
		`{"collected_at":1900,"host":{"memory_total_bytes":-1}}`,
		strings.Repeat("x", 128*1024+1),
	} {
		if err := os.WriteFile(p, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if readOperationsStatus(p, now).Available {
			t.Fatal("invalid sample must be unknown")
		}
	}
	if err := os.WriteFile(p, []byte(`{"collected_at":1300,"disks":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := readOperationsStatus(p, now)
	if !s.Available || !s.Stale {
		t.Fatal("old collector sample must be visibly stale")
	}
	if err := os.WriteFile(p, []byte(`{"collected_at":1900,"disks":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s = readOperationsStatus(p, now)
	if !s.Available || s.Stale {
		t.Fatal("recent sample should be available")
	}
}

func TestOperationsStatusRejectsDirectoryAndEmptyPath(t *testing.T) {
	now := time.Now()
	for _, path := range []string{"", t.TempDir()} {
		if readOperationsStatus(path, now).Available {
			t.Fatalf("unexpected availability for %q", path)
		}
	}
}

func TestGetRuntimeQueuesIncludesOperationsWithoutAsynq(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "status.json")
	sample := fmt.Sprintf(`{"collected_at":%d,"host":{"cpu_count":2},"secret":"not exported"}`, time.Now().Unix())
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEKNORA_OPERATIONS_STATUS_FILE", path)
	h := &SystemHandler{
		imRuntime: func() im.RuntimeStatus {
			return im.RuntimeStatus{Available: true, Workers: 5, Pending: 2, Active: 1}
		},
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/admin/runtime/queues", nil)
	h.GetRuntimeQueues(ctx)
	var result RuntimeQueuesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || result.Available || !result.Operations.Available ||
		result.IM == nil || result.IM.Pending != 2 || result.IM.Workers != 5 {
		t.Fatalf("unexpected runtime response: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "secret") {
		t.Fatal("unknown collector fields must not be exposed")
	}
}
