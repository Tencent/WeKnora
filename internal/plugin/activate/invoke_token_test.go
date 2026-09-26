package activate

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/hostapi"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/types"
)

// A data source sync streams on one call for up to two hours; the plugin's
// Host API token must outlive it.
func TestHostAPITokenLastsAsLongAsTheCall(t *testing.T) {
	iss := hostapi.NewIssuer([]byte("k"))
	iv := NewInvoker(fakeClients{})
	iv.SetHostAPI(iss, "http://127.0.0.1:9")
	m := &manifest.Manifest{
		ID: "acme.x", Version: "1.0.0", Permissions: manifest.Permissions{HostAPI: []string{"kv"}},
	}
	base := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx, cancel := context.WithTimeout(base, 2*time.Hour)
	defer cancel()
	env, err := iv.Envelope(ctx, m, nil)
	if err != nil || env.Context.Host == nil {
		t.Fatalf("envelope = %+v, %v", env.Context, err)
	}
	claims, err := iss.Verify(env.Context.Host.Token)
	if err != nil {
		t.Fatal(err)
	}
	if left := time.Until(claims.ExpiresAt.Time); left < 2*time.Hour {
		t.Fatalf("token of a two-hour call expires in %s", left)
	}
}
