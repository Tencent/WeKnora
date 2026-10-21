package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/attachment"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAttachmentDescriptionPersistenceIsolationAndRetention(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.TemporaryDocument{}))
	doc := &types.TemporaryDocument{
		ID: "document", TenantID: 7, SessionID: "parent", Metadata: types.JSON(`{"parser":"pdf"}`),
	}
	require.NoError(t, db.Create(doc).Error)
	ctx := context.Background()
	cache := NewAttachmentDescriptionRepository(db, 7)
	for i := range maxDocumentDescriptions + 1 {
		require.NoError(t, cache.Put(ctx, doc.ID, fmt.Sprint(i), attachment.Description{
			Text: fmt.Sprint(i), CreatedAt: time.Unix(int64(i), 0),
		}))
	}
	// Reconstructing the repository simulates another turn or process.
	cache = NewAttachmentDescriptionRepository(db, 7)
	oldest, err := cache.Get(ctx, doc.ID, "0")
	require.NoError(t, err)
	require.Nil(t, oldest)
	newest, err := cache.Get(ctx, doc.ID, fmt.Sprint(maxDocumentDescriptions))
	require.NoError(t, err)
	require.NotNil(t, newest)
	stored, err := NewTemporaryDocumentRepository(db).GetByID(ctx, 7, doc.ID)
	require.NoError(t, err)
	var metadata map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(stored.Metadata, &metadata))
	require.JSONEq(t, `"pdf"`, string(metadata["parser"]))
	foreign := NewAttachmentDescriptionRepository(db, 8)
	value, err := foreign.Get(ctx, doc.ID, "1")
	require.NoError(t, err)
	require.Nil(t, value)
	require.Error(t, foreign.Put(ctx, doc.ID, "foreign", attachment.Description{Text: "no"}))
	require.NoError(t, NewTemporaryDocumentRepository(db).DeleteScoped(ctx, 7, "parent", doc.ID))
	value, err = cache.Get(ctx, doc.ID, "1")
	require.NoError(t, err)
	require.Nil(t, value)
}

func TestConcurrentAttachmentDescriptionsPreserveEachOther(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.TemporaryDocument{}))
	require.NoError(t, db.Create(&types.TemporaryDocument{ID: "concurrent", TenantID: 7}).Error)
	errors := make(chan error, 8)
	for i := range 8 {
		go func() {
			errors <- NewAttachmentDescriptionRepository(db, 7).Put(context.Background(), "concurrent", fmt.Sprint(i),
				attachment.Description{Text: fmt.Sprint(i), CreatedAt: time.Now()})
		}()
	}
	for range 8 {
		require.NoError(t, <-errors)
	}
	for i := range 8 {
		value, err := NewAttachmentDescriptionRepository(db, 7).Get(context.Background(), "concurrent", fmt.Sprint(i))
		require.NoError(t, err)
		require.NotNil(t, value)
		require.Equal(t, fmt.Sprint(i), value.Text)
	}
}
