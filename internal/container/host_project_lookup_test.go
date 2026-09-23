//go:build desktop

package container

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

func hostProjectLookupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Migrator().DropTable(&types.Session{}))
	require.NoError(t, db.AutoMigrate(&types.Session{}))
	return db
}

func insertHostSession(t *testing.T, db *gorm.DB, id, dir string) {
	t.Helper()
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{
		ID:               id,
		TenantID:         1,
		HostWorkspaceDir: dir,
	}).Error)
}

// A stored directory that is no longer approved must not be honoured: the user
// removed it from the list precisely to revoke access.
func TestProjectLookupFailsClosedWhenDirectoryRevoked(t *testing.T) {
	db := hostProjectLookupTestDB(t)
	insertHostSession(t, db, "sess-1", "/Users/dev/Old Project")

	lookup := newHostProjectLookup(db, func() []string {
		return []string{"/Users/dev/My Project"}
	})
	dir, ok, err := lookup.ProjectDirForSession(context.Background(), "sess-1")
	require.ErrorIs(t, err, localsandbox.ErrProjectDirRevoked)
	require.False(t, ok)
	require.Empty(t, dir)
}

func TestProjectLookupReturnsStoredDirectoryWhenStillApproved(t *testing.T) {
	db := hostProjectLookupTestDB(t)
	insertHostSession(t, db, "sess-1", "/Users/dev/My Project")

	lookup := newHostProjectLookup(db, func() []string {
		return []string{"/Users/dev/My Project"}
	})
	dir, ok, err := lookup.ProjectDirForSession(context.Background(), "sess-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "/Users/dev/My Project", dir)
}

func TestProjectLookupEmptyHostWorkspaceDirIsUnbound(t *testing.T) {
	db := hostProjectLookupTestDB(t)
	insertHostSession(t, db, "sess-1", "")

	lookup := newHostProjectLookup(db, func() []string {
		return []string{"/Users/dev/My Project"}
	})
	dir, ok, err := lookup.ProjectDirForSession(context.Background(), "sess-1")
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, dir)
}

func TestProjectLookupReturnsFalseForUnknownSession(t *testing.T) {
	db := hostProjectLookupTestDB(t)
	lookup := newHostProjectLookup(db, func() []string {
		return []string{"/Users/dev/My Project"}
	})
	dir, ok, err := lookup.ProjectDirForSession(context.Background(), "missing")
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, dir)
}
