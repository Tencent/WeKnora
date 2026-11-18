package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupListUsersRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sqlite db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(&types.Tenant{}, &types.User{}); err != nil {
		t.Fatalf("migrate users: %v", err)
	}

	repo := repository.NewUserRepository(db)
	svc := service.NewUserService(nil, repo, nil, nil, nil, nil)
	h := &SystemHandler{userSvc: svc}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/system/admin/users", h.ListUsers)
	return r, db
}

type listUsersDecodedResponse struct {
	Users   []map[string]any `json:"users"`
	Offset  int              `json:"offset"`
	Limit   int              `json:"limit"`
	HasMore bool             `json:"has_more"`
}

func getListUsers(t *testing.T, r *gin.Engine, rawQuery string) listUsersDecodedResponse {
	t.Helper()
	target := "/system/admin/users"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: expected 200, got %d body=%s", target, w.Code, w.Body.String())
	}
	var resp listUsersDecodedResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", target, err)
	}
	for _, u := range resp.Users {
		if _, ok := u["password"]; ok {
			t.Fatalf("password leaked in %v", u)
		}
		if _, ok := u["Password"]; ok {
			t.Fatalf("Password leaked in %v", u)
		}
		if _, ok := u["password_hash"]; ok {
			t.Fatalf("password_hash leaked in %v", u)
		}
	}
	return resp
}

func TestListUsers_RedactedPaginatedAndLimitCapped(t *testing.T) {
	r, db := setupListUsersRouter(t)
	repo := repository.NewUserRepository(db)
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		u := &types.User{
			ID:            fmt.Sprintf("u-%02d", i),
			Username:      fmt.Sprintf("user-%02d", i),
			Email:         fmt.Sprintf("user-%02d@example.com", i),
			PasswordHash:  "bcrypt-secret-hash",
			IsActive:      true,
			IsSystemAdmin: i == 0,
			CreatedAt:     baseTime.Add(time.Duration(i) * time.Minute),
		}
		if err := repo.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser %d: %v", i, err)
		}
	}

	page1 := getListUsers(t, r, "offset=1&limit=2")
	if page1.Offset != 1 || page1.Limit != 2 || !page1.HasMore {
		t.Fatalf("unexpected pagination meta: %+v", page1)
	}
	if len(page1.Users) != 2 || page1.Users[0]["id"] != "u-03" || page1.Users[1]["id"] != "u-02" {
		t.Fatalf("unexpected page1 users: %+v", page1.Users)
	}

	capped := getListUsers(t, r, "limit=9999")
	if capped.Limit != 200 || capped.HasMore || len(capped.Users) != 5 {
		t.Fatalf("expected limit capped to 200 with all 5 users, got %+v", capped)
	}
}

func TestListUsers_SearchAcrossPagesDisabledWildcardsAndDeleted(t *testing.T) {
	r, db := setupListUsersRouter(t)
	repo := repository.NewUserRepository(db)
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Oldest accounts sit beyond the 20-row first page (reproducing issue #5634).
	seed := []*types.User{
		{
			ID:           "u-oldest-active",
			Username:     "founder_alice",
			Email:        "founder.alice@corp.example",
			PasswordHash: "secret",
			IsActive:     true,
			CreatedAt:    baseTime,
		},
		{
			ID:           "u-oldest-disabled",
			Username:     "disabled_bob",
			Email:        "bob.legacy@corp.example",
			PasswordHash: "secret",
			IsActive:     false,
			CreatedAt:    baseTime.Add(time.Second),
		},
		{
			ID:           "u-wildcard-decoy",
			Username:     "founderXalice",
			Email:        "decoy@other.example",
			PasswordHash: "secret",
			IsActive:     true,
			CreatedAt:    baseTime.Add(2 * time.Second),
		},
		{
			ID:           "u-percent-literal",
			Username:     "ops%bot",
			Email:        "ops%bot@other.example",
			PasswordHash: "secret",
			IsActive:     true,
			CreatedAt:    baseTime.Add(3 * time.Second),
		},
		{
			ID:           "u-deleted",
			Username:     "deleted_founder",
			Email:        "deleted@corp.example",
			PasswordHash: "secret",
			IsActive:     true,
			CreatedAt:    baseTime.Add(4 * time.Second),
		},
	}
	for _, u := range seed {
		if err := repo.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("seed %s: %v", u.ID, err)
		}
		if u.ID == "u-oldest-disabled" {
			// Ensure GORM persists IsActive=false despite default:true tag.
			if err := db.Model(u).Update("is_active", false).Error; err != nil {
				t.Fatalf("disable %s: %v", u.ID, err)
			}
		}
	}
	if err := repo.DeleteUser(context.Background(), "u-deleted"); err != nil {
		t.Fatalf("soft-delete user: %v", err)
	}

	// Add 22 newer accounts so all seeded accounts above fall onto page 2+.
	sameTimestamp := baseTime.Add(time.Hour)
	for i := 0; i < 22; i++ {
		u := &types.User{
			ID:           fmt.Sprintf("u-recent-%02d", i),
			Username:     fmt.Sprintf("member-%02d", i),
			Email:        fmt.Sprintf("member-%02d@recent.example", i),
			PasswordHash: "secret",
			IsActive:     true,
			CreatedAt:    sameTimestamp,
		}
		if err := repo.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser recent %d: %v", i, err)
		}
	}

	// 1. Unfiltered page 1 (limit=20) only contains recent accounts, not the older ones.
	firstPage := getListUsers(t, r, "offset=0&limit=20")
	if len(firstPage.Users) != 20 || !firstPage.HasMore {
		t.Fatalf("expected 20 users with has_more=true, got len=%d has_more=%v", len(firstPage.Users), firstPage.HasMore)
	}
	for _, u := range firstPage.Users {
		if u["id"] == "u-oldest-active" || u["id"] == "u-oldest-disabled" {
			t.Fatalf("expected older user %v to be off page 1", u["id"])
		}
	}

	// 2. Searching by username/email finds page-2+ users (case-insensitive) AND escapes `_` wildcard.
	foundActive := getListUsers(t, r, "offset=0&limit=20&q="+url.QueryEscape("  FOUNDER_ALICE  "))
	if len(foundActive.Users) != 1 || foundActive.HasMore || foundActive.Users[0]["id"] != "u-oldest-active" {
		t.Fatalf("expected exact older user u-oldest-active (underscore escaped), got %+v", foundActive)
	}

	// 3. Disabled accounts remain searchable so system admins can locate and re-enable them.
	foundDisabled := getListUsers(t, r, "offset=0&limit=20&q="+url.QueryEscape("BOB.LEGACY@CORP"))
	if len(foundDisabled.Users) != 1 || foundDisabled.Users[0]["id"] != "u-oldest-disabled" || foundDisabled.Users[0]["is_active"] != false {
		t.Fatalf("expected disabled user u-oldest-disabled to be searchable, got %+v", foundDisabled)
	}

	// 4. `%` is escaped rather than matching the entire users table; soft-deleted users stay excluded.
	foundPercent := getListUsers(t, r, "offset=0&limit=20&q="+url.QueryEscape("%"))
	if len(foundPercent.Users) != 1 || foundPercent.Users[0]["id"] != "u-percent-literal" {
		t.Fatalf("expected literal %% match only, got %+v", foundPercent)
	}

	// 5. Filtered search supports multi-page pagination and excludes soft-deleted @corp.example row.
	corpPage1 := getListUsers(t, r, "offset=0&limit=1&q="+url.QueryEscape("@corp.example"))
	if len(corpPage1.Users) != 1 || !corpPage1.HasMore || corpPage1.Users[0]["id"] != "u-oldest-disabled" {
		t.Fatalf("unexpected corp page 1: %+v", corpPage1)
	}
	corpPage2 := getListUsers(t, r, "offset=1&limit=1&q="+url.QueryEscape("@corp.example"))
	if len(corpPage2.Users) != 1 || corpPage2.HasMore || corpPage2.Users[0]["id"] != "u-oldest-active" {
		t.Fatalf("unexpected corp page 2: %+v", corpPage2)
	}
}
