package repository

import (
	"context"
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUserRepositoryTenantlessCreateAndUpdateKeepNullTenantID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:user_tenantless?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&types.Tenant{}, &types.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := NewUserRepository(db)
	user := &types.User{
		ID:           "tenantless-user",
		Username:     "before-update",
		Email:        "tenantless@example.com",
		PasswordHash: "hashed",
		TenantID:     0,
		IsActive:     true,
	}
	if err := repo.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	assertNullTenantID(t, db, user.ID)

	user.Username = "after-update"
	if err := repo.UpdateUser(context.Background(), user); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	assertNullTenantID(t, db, user.ID)

	var stored types.User
	if err := db.First(&stored, "id = ?", user.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.Username != "after-update" || stored.TenantID != 0 {
		t.Fatalf("stored user = %#v", stored)
	}
}

func assertNullTenantID(t *testing.T, db *gorm.DB, userID string) {
	t.Helper()
	var count int64
	if err := db.Table("users").Where("id = ? AND tenant_id IS NULL", userID).Count(&count).Error; err != nil {
		t.Fatalf("check tenant_id: %v", err)
	}
	if count != 1 {
		t.Fatalf("tenant_id for user %s is not NULL", userID)
	}
}

// TestUserRepositorySearchUsersEscapesLikeWildcards covers the two rules every
// other keyword filter in this package follows: the predicate has to parse on
// SQLite (the Lite driver), which has no ILIKE, and the keyword must not be
// handed to LIKE as a pattern. Without escaping, searching "a_b" also returns
// "axb" and searching "%" returns every user.
func TestUserRepositorySearchUsersEscapesLikeWildcards(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:user_searchwildcard?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&types.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := NewUserRepository(db)
	for _, u := range []*types.User{
		{ID: "u-underscore", Username: "a_b", Email: "a_b@example.com", PasswordHash: "h", IsActive: true},
		{ID: "u-anychar", Username: "axb", Email: "axb@example.com", PasswordHash: "h", IsActive: true},
		{ID: "u-alice", Username: "alice", Email: "alice@example.com", PasswordHash: "h", IsActive: true},
		{ID: "u-percent", Username: "pct%user", Email: "pct%user@example.com", PasswordHash: "h", IsActive: true},
	} {
		if err := repo.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser %s: %v", u.Username, err)
		}
	}

	usernames := func(users []*types.User) []string {
		got := make([]string, 0, len(users))
		for _, u := range users {
			got = append(got, u.Username)
		}
		return got
	}

	tests := []struct {
		name string
		want []string
	}{
		// "_" is a single-character wildcard in LIKE, so the unescaped form
		// would also match "axb".
		{"a_b", []string{"a_b"}},
		// "%" alone would otherwise match every user in the table; escaping it
		// still finds the user whose name really contains a percent.
		{"%", []string{"pct%user"}},
		// A keyword with no metacharacters must still match, and SQLite's LIKE
		// folds ASCII case on its own.
		{"ALICE", []string{"alice"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.SearchUsers(context.Background(), tt.name, 100)
			if err != nil {
				t.Fatalf("SearchUsers(%q): %v", tt.name, err)
			}
			if g := usernames(got); !slices.Equal(g, tt.want) {
				t.Fatalf("SearchUsers(%q) = %v, want %v", tt.name, g, tt.want)
			}
		})
	}
}
