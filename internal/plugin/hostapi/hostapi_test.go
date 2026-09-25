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
