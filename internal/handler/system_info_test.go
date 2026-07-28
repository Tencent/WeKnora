package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	mysqlgorm "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestGetSystemInfoReportsInitializedDatabaseDriver(t *testing.T) {
	sqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock database: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	db, err := gorm.Open(mysqlgorm.New(mysqlgorm.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("open GORM MySQL dialector: %v", err)
	}

	t.Setenv("RETRIEVE_DRIVER", "qdrant")
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)

	(&SystemHandler{db: db}).GetSystemInfo(ctx)

	var body struct {
		Data GetSystemInfoResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode system info response: %v", err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body.Data.DatabaseDriver != "mysql" {
		t.Fatalf("database_driver = %q, want mysql", body.Data.DatabaseDriver)
	}
}

func TestGetDatabaseDriverWithoutInitializedDatabase(t *testing.T) {
	if got := (&SystemHandler{}).getDatabaseDriver(); got != "" {
		t.Fatalf("getDatabaseDriver() = %q, want empty", got)
	}
}
