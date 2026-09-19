package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// RevokeTokenByValue drives refresh-token rotation: the refresh flow revokes
// the presented token by value before issuing a new pair. Querying a column
// that does not exist makes every refresh fail, so pin the real column name
// and the atomic single-revoke semantics against a real SQLite schema.
func TestRevokeTokenByValueUsesTokenColumnAndIsAtomic(t *testing.T) {
	// Unique per invocation: a shared in-memory database outlives the test, so
	// a repeated run (-count>1) would collide on the same primary keys.
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf(
		"file:revoke_token_by_value_%d?mode=memory&cache=shared", time.Now().UnixNano(),
	)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&types.AuthToken{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repo := NewAuthTokenRepository(db)
	ctx := context.Background()

	live := &types.AuthToken{
		ID:        "token-live",
		UserID:    "user-1",
		Token:     "refresh-live",
		TokenType: "refresh_token",
	}
	alreadyRevoked := &types.AuthToken{
		ID:        "token-revoked",
		UserID:    "user-1",
		Token:     "refresh-revoked",
		TokenType: "refresh_token",
		IsRevoked: true,
	}
	for _, token := range []*types.AuthToken{live, alreadyRevoked} {
		if err := repo.CreateToken(ctx, token); err != nil {
			t.Fatalf("CreateToken(%s): %v", token.ID, err)
		}
	}

	revoked, err := repo.RevokeTokenByValue(ctx, "refresh-live")
	if err != nil {
		t.Fatalf("RevokeTokenByValue(live): %v", err)
	}
	if !revoked {
		t.Fatal("RevokeTokenByValue(live) = false, want true")
	}

	// Second attempt must not update an already revoked row.
	revoked, err = repo.RevokeTokenByValue(ctx, "refresh-live")
	if err != nil {
		t.Fatalf("RevokeTokenByValue(live) second call: %v", err)
	}
	if revoked {
		t.Fatal("RevokeTokenByValue(live) second call = true, want false")
	}

	revoked, err = repo.RevokeTokenByValue(ctx, "refresh-revoked")
	if err != nil {
		t.Fatalf("RevokeTokenByValue(already revoked): %v", err)
	}
	if revoked {
		t.Fatal("RevokeTokenByValue(already revoked) = true, want false")
	}

	revoked, err = repo.RevokeTokenByValue(ctx, "refresh-missing")
	if err != nil {
		t.Fatalf("RevokeTokenByValue(missing): %v", err)
	}
	if revoked {
		t.Fatal("RevokeTokenByValue(missing) = true, want false")
	}

	stored, err := repo.GetTokenByValue(ctx, "refresh-live")
	if err != nil {
		t.Fatalf("GetTokenByValue(live): %v", err)
	}
	if !stored.IsRevoked {
		t.Fatal("live token not revoked after RevokeTokenByValue")
	}
}
