package activate

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/hostapi"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/webhook"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestEnvelopeCarriesHostAccessOnlyForGrantedPlugins(t *testing.T) {
	iss := hostapi.NewIssuer([]byte("k"))
	iv := NewInvoker(fakeClients{})
	iv.SetHostAPI(iss, "http://127.0.0.1:9")
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	granted := &manifest.Manifest{
		ID: "acme.x", Version: "1.0.0", Permissions: manifest.Permissions{HostAPI: []string{"kv"}},
	}
	env, err := iv.Envelope(ctx, granted, nil)
	if err != nil || env.Context.Host == nil || env.Context.Host.URL != "http://127.0.0.1:9" {
		t.Fatalf("envelope = %+v, %v", env.Context, err)
	}
	claims, err := iss.Verify(env.Context.Host.Token)
	if err != nil || claims.PluginID != "acme.x" || claims.TenantID != 7 || !claims.Has("kv") {
		t.Fatalf("token claims = %+v, %v", claims, err)
	}

	plain := &manifest.Manifest{ID: "acme.y", Version: "1.0.0"}
	if env, _ := iv.Envelope(ctx, plain, nil); env.Context.Host != nil {
		t.Fatal("a plugin without Host API scopes must get no token")
	}
	if env, _ := iv.Envelope(context.Background(), granted, nil); env.Context.Host != nil {
		t.Fatal("a call without a tenant must get no token")
	}

	remote := *granted
	remote.ID = "acme.remote"
	if env, _ := iv.Envelope(ctx, &remote, nil); env.Context.Host != nil {
		t.Fatal("a remote plugin must not be sent to this node's loopback address")
	}
	iv.SetPublicHostAPI("https://weknora.example.com")
	if env, _ := iv.Envelope(ctx, &remote, nil); env.Context.Host == nil ||
		env.Context.Host.URL != "https://weknora.example.com" {
		t.Fatalf("remote envelope = %+v", env.Context)
	}
}

func TestEnvelopeCarriesWebhookURLs(t *testing.T) {
	iv := NewInvoker(fakeClients{})
	m := &manifest.Manifest{ID: "acme.hooks", Contributes: manifest.Contributions{
		manifest.PointWebhooks: {{ID: "inbox"}},
	}}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	if env, _ := iv.Envelope(ctx, m, nil); env.Context.Webhooks != nil {
		t.Fatal("no URLs without a public address")
	}
	tokens := webhook.NewTokens([]byte("k"))
	iv.SetWebhooks(tokens, "https://weknora.example.com")
	env, err := iv.Envelope(ctx, m, nil)
	want := "https://weknora.example.com" + tokens.Path("acme.hooks", "inbox", 7)
	if err != nil || env.Context.Webhooks["inbox"] != want {
		t.Fatalf("webhooks = %v, %v", env.Context.Webhooks, err)
	}
}
