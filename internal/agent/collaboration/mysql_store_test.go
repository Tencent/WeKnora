//go:build mysqlintegration

package collaboration

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestMySQLMainCollaborationConcurrentChangesAndScope(t *testing.T) {
	db := testutil.MySQL(t)
	s := NewStore(db)
	ctx := context.Background()
	scope := Scope{TenantID: 1, UserID: "user", SessionID: "session", ID: "collaboration"}
	require.NoError(t, s.Ensure(ctx, scope))
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error {
		g.RootQuery = strings.Repeat("大", 30000)
		return nil
	}))
	require.NoError(t, s.Ensure(ctx, scope))
	other := scope
	other.TenantID = 2
	require.ErrorIs(t, s.Ensure(ctx, other), ErrScope)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Change(ctx, scope, func(g *Group) error { g.NextSequence++; return nil })
		}()
	}
	wg.Wait()
	for range 8 {
		require.NoError(t, <-errs)
	}
	group, err := s.Read(ctx, scope)
	require.NoError(t, err)
	require.EqualValues(t, 8, group.NextSequence)
	require.EqualValues(t, 10, group.Revision)
	require.Equal(t, strings.Repeat("大", 30000), group.RootQuery)
}
