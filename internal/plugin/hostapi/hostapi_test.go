package hostapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

func TestTokens(t *testing.T) {
	iss := NewIssuer([]byte("k"))
	tok, exp, err := iss.Issue("acme.x", "1.0.0", 7, []string{"kv"})
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(TokenTTL), exp, time.Second)
	c, err := iss.Verify(tok)
	require.NoError(t, err)
	require.Equal(t, uint64(7), c.TenantID)
	require.True(t, c.Has("kv"))

	_, err = NewIssuer([]byte("other")).Verify(tok)
	require.ErrorIs(t, err, ErrInvalidToken, "another key must not verify")
	later := NewIssuer([]byte("k"))
	later.now = func() time.Time { return time.Now().Add(TokenTTL + time.Minute) }
	_, err = later.Verify(tok)
	require.ErrorIs(t, err, ErrInvalidToken, "expired tokens must not verify")
	_, err = iss.Verify(tok[:len(tok)-2] + "xx")
	require.ErrorIs(t, err, ErrInvalidToken)
}

// hostCall is what a plugin's call would carry, pointing at a test server.
func hostCall(t *testing.T, url string, iss *Issuer, tenant uint64, scopes ...string) *pluginsdk.Call {
	t.Helper()
	tok, _, err := iss.Issue("acme.x", "1.0.0", tenant, scopes)
	require.NoError(t, err)
	return &pluginsdk.Call{
		Context: pluginapi.Context{TenantID: tenant, Host: &pluginapi.HostAccess{URL: url, Token: tok}},
	}
}

func TestKVThroughTheSDK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.PluginKV{}))
	iss := NewIssuer([]byte("k"))
	h := NewHandler(iss, NewKV(repository.NewPluginKVRepository(db)))
	r := gin.New()
	h.Register(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	ctx := context.Background()

	host := hostCall(t, srv.URL, iss, 1, "kv").Host()
	require.NotNil(t, host)
	type cursor struct{ Page int }
	ok, err := host.KVGet(ctx, "cursor/a", &cursor{})
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, host.KVPut(ctx, "cursor/a", cursor{Page: 3}, 0))
	require.NoError(t, host.KVPut(ctx, "cursor/b", cursor{Page: 4}, time.Hour))
	var got cursor
	ok, err = host.KVGet(ctx, "cursor/a", &got)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 3, got.Page)

	page, err := host.KVList(ctx, "cursor/", "", 1)
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	require.Equal(t, "cursor/a", page.Next)
	page, err = host.KVList(ctx, "cursor/", page.Next, 1)
	require.NoError(t, err)
	require.Equal(t, "cursor/b", page.Entries[0].Key)
	require.NotNil(t, page.Entries[0].ExpiresAt)

	// Another tenant sees nothing of tenant 1.
	other := hostCall(t, srv.URL, iss, 2, "kv").Host()
	ok, err = other.KVGet(ctx, "cursor/a", &got)
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, host.KVDelete(ctx, "cursor/a"))
	ok, _ = host.KVGet(ctx, "cursor/a", &got)
	require.False(t, ok)

	// Without the scope, or with a forged token, the call is refused.
	noScope := hostCall(t, srv.URL, iss, 1).Host()
	_, err = noScope.KVGet(ctx, "cursor/b", &got)
	require.ErrorContains(t, err, `not granted the "kv" scope`)
	forged := hostCall(t, srv.URL, NewIssuer([]byte("wrong")), 1, "kv").Host()
	_, err = forged.KVGet(ctx, "cursor/b", &got)
	require.ErrorContains(t, err, "invalid or expired")

	// Bad input is a bad request, not a crash.
	err = host.KVPut(ctx, strings.Repeat("k", 300), 1, 0)
	e, ok := pluginapi.AsError(err)
	require.True(t, ok)
	require.Equal(t, pluginapi.CodeBadRequest, e.Code)
}

type fakeSyncer struct {
	started []string
	running map[string]time.Time
}

func (f *fakeSyncer) ManualSync(_ context.Context, id string) (*types.SyncLog, error) {
	f.started = append(f.started, id)
	return &types.SyncLog{ID: "log-" + id}, nil
}

func (f *fakeSyncer) GetSyncLogs(_ context.Context, id string, _, _ int) ([]*types.SyncLog, error) {
	if at, ok := f.running[id]; ok {
		return []*types.SyncLog{{ID: "busy", Status: types.SyncLogStatusRunning, StartedAt: at}}, nil
	}
	return nil, nil
}

func TestDataSourcesThroughTheSDK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.DataSource{}))
	ctx := context.Background()
	repo := repository.NewDataSourceRepository(db)
	for _, ds := range []*types.DataSource{
		{
			ID: "mine", TenantID: 1, Name: "Jira ENG", Type: "acme.x/jira", KnowledgeBaseID: "kb",
			Config: types.JSON(`{"resource_ids":["ENG"]}`), Status: types.DataSourceStatusActive,
		},
		{ID: "stuck", TenantID: 1, Name: "Jira OPS", Type: "acme.x/jira", Status: types.DataSourceStatusActive},
		{ID: "stale", TenantID: 1, Name: "Old run", Type: "acme.x/jira", Status: types.DataSourceStatusActive},
		{ID: "other-plugin", TenantID: 1, Type: "acme.xy/jira"},
		{ID: "like-escape", TenantID: 1, Type: "acmeaxbjira"},
		{ID: "other-tenant", TenantID: 2, Type: "acme.x/jira"},
	} {
		require.NoError(t, repo.Create(ctx, ds))
	}
	syncer := &fakeSyncer{running: map[string]time.Time{
		"stuck": time.Now().Add(-time.Minute), "stale": time.Now().Add(-2 * runningSyncWindow),
	}}
	iss := NewIssuer([]byte("k"))
	h := NewHandler(iss, nil)
	h.SetDataSources(NewDataSources(repo.(DataSourceStore), syncer))
	r := gin.New()
	h.Register(r)
	srv := httptest.NewServer(r)
	defer srv.Close()

	host := hostCall(t, srv.URL, iss, 1, ScopeDataSources).Host()
	list, err := host.DataSources(ctx)
	require.NoError(t, err)
	require.Len(t, list, 3, "only this plugin's data sources in this tenant")
	require.Equal(t, "jira", list[0].Connector)
	require.Equal(t, []string{"ENG"}, list[0].ResourceIDs)
	require.Equal(t, "kb", list[0].KnowledgeBaseID)

	started, err := host.SyncDataSource(ctx, "mine")
	require.NoError(t, err)
	require.Equal(t, pluginapi.SyncQueued, started.Status)
	require.Equal(t, "log-mine", started.SyncLogID)
	busy, err := host.SyncDataSource(ctx, "stuck")
	require.NoError(t, err)
	require.Equal(t, pluginapi.SyncRunning, busy.Status, "a running sync is not queued twice")
	_, err = host.SyncDataSource(ctx, "stale")
	require.NoError(t, err)
	require.Equal(t, []string{"mine", "stale"}, syncer.started, "a run stuck for long does not block")

	for _, id := range []string{"other-plugin", "other-tenant", "nope"} {
		_, err = host.SyncDataSource(ctx, id)
		pe, ok := pluginapi.AsError(err)
		require.True(t, ok && pe.Code == pluginapi.CodeNotFound, "%s: %v", id, err)
	}
	_, err = hostCall(t, srv.URL, iss, 1, "kv").Host().DataSources(ctx)
	pe, ok := pluginapi.AsError(err)
	require.True(t, ok && pe.Code == pluginapi.CodeUnauthorized, "the scope is required: %v", err)
}
