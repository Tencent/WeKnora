package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetUserByEmailCaseInsensitiveIncludesInactiveASCIICase(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:user_email_case?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&types.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := NewUserRepository(db)
	stored := &types.User{
		ID:           "inactive-mixed-case",
		Username:     "inactive-admin",
		Email:        "Admin@example.com",
		PasswordHash: "hashed",
		IsActive:     true,
	}
	if err := repo.CreateUser(context.Background(), stored); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// The bool zero value is dropped by the column default, so deactivate
	// with an explicit update. The occupancy query must still see the row.
	if err := db.Model(stored).Update("is_active", false).Error; err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	got, err := repo.GetUserByEmailCaseInsensitive(context.Background(), "admin@example.com")
	if err != nil {
		t.Fatalf("case-insensitive lookup: %v", err)
	}
	if got == nil || got.ID != stored.ID || got.IsActive {
		t.Fatalf("lookup = %+v, want inactive %s", got, stored.ID)
	}

	// Exact lookup stays case-sensitive. Login and startup promotion use it.
	if _, err := repo.GetUserByEmail(context.Background(), "admin@example.com"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("exact lookup err = %v, want ErrUserNotFound", err)
	}

	_, err = repo.GetUserByEmailCaseInsensitive(context.Background(), "other@example.com")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("other email err = %v, want ErrUserNotFound", err)
	}

	if err := repo.DeleteUser(context.Background(), stored.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	_, err = repo.GetUserByEmailCaseInsensitive(context.Background(), "ADMIN@example.com")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("soft-deleted lookup err = %v, want ErrUserNotFound", err)
	}
}
