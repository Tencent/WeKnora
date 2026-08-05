package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

func newPinTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Migrator().DropTable(&types.Session{}))
	require.NoError(t, db.AutoMigrate(&types.Session{}))
	require.NoError(t, db.Model(&types.Session{}).Create(map[string]any{
		"id":        "s-1",
		"tenant_id": uint64(7),
	}).Error)
	return db
}

func TestPinWritesWhenUnset(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	got, err := pinner.Pin(ctx, "s-1", "cfg-a")
	require.NoError(t, err)
	require.Equal(t, "cfg-a", got)

	read, err := pinner.Read(ctx, "s-1")
	require.NoError(t, err)
	require.Equal(t, "cfg-a", read)
}

// Two concurrent first-sandbox creations must converge on one config, or the
// session would end up with two sandboxes on two backends.
func TestPinIsIdempotentAndReturnsExistingWinner(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	first, err := pinner.Pin(ctx, "s-1", "cfg-a")
	require.NoError(t, err)
	require.Equal(t, "cfg-a", first)

	second, err := pinner.Pin(ctx, "s-1", "cfg-b")
	require.NoError(t, err)
	require.Equal(t, "cfg-a", second, "the first writer wins; later callers adopt it")
}

func TestPinNormalizesEmptyConfigToSentinel(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	got, err := pinner.Pin(ctx, "s-1", "")
	require.NoError(t, err)
	require.Equal(t, types.SandboxConfigIDGlobalDefault, got)
}

// A padded ID must land in the column exactly as Read will compare it, or the
// conditional claim would never recognise its own write.
func TestPinTrimsConfigIDBeforeStoring(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	got, err := pinner.Pin(ctx, "s-1", "  cfg-a  ")
	require.NoError(t, err)
	require.Equal(t, "cfg-a", got)

	read, err := pinner.Read(ctx, "s-1")
	require.NoError(t, err)
	require.Equal(t, "cfg-a", read)
}

// Pin runs right after a sandbox was created, so a vanished session must be an
// error: "" would read as "no live sandbox" and abandon a real one.
func TestPinFailsWhenSessionIsGone(t *testing.T) {
	db := newPinTestDB(t)
	pinner := NewSessionSandboxPinner(db)
	ctx := context.Background()

	_, err := pinner.Pin(ctx, "missing", "cfg-a")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	require.NoError(t, db.Delete(&types.Session{}, "id = ?", "s-1").Error)
	_, err = pinner.Pin(ctx, "s-1", "cfg-a")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound,
		"a soft-deleted session already had its sandbox destroyed")
}

// Read keeps the lenient contract: absent session and unpinned session are
// both simply "no live sandbox".
func TestReadReportsNoSandboxForMissingSession(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))

	read, err := pinner.Read(context.Background(), "missing")
	require.NoError(t, err)
	require.Empty(t, read)
}

// The pin dies with the sandbox: after teardown the session must be free to
// follow its agent's CURRENT backend choice.
func TestClearReleasesPin(t *testing.T) {
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	ctx := context.Background()

	_, err := pinner.Pin(ctx, "s-1", "cfg-a")
	require.NoError(t, err)
	require.NoError(t, pinner.Clear(ctx, "s-1"))

	read, err := pinner.Read(ctx, "s-1")
	require.NoError(t, err)
	require.Empty(t, read)
}
