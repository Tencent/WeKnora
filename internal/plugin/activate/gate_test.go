package activate

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/hostapi"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// failingSettings cannot read switches.
type failingSettings struct{ plugintest.MemTenantSettings }

func (*failingSettings) Get(context.Context, uint64, string) (*types.PluginTenantSetting, error) {
	return nil, errors.New("database is down")
}

// Every call on behalf of a workspace needs the plugin on in it and visible
// to it; the workspace's configuration form needs it visible only, and the
// platform's calls none of that.
func TestEnvelopeGatesWorkspaceCalls(t *testing.T) {
	reg := registry.New()
	m := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: "acme.x", Version: "1.0.0", APIVersion: pluginapi.APIVersion,
		Name: manifest.Text("X", nil), Publisher: manifest.Publisher{ID: "acme"},
		Runtime:     manifest.Runtime{Type: manifest.RuntimeRemote},
		Permissions: manifest.Permissions{HostAPI: []string{"kv"}},
		Contributes: manifest.Contributions{manifest.PointWebSearch: {{ID: "web", Name: manifest.Text("Web", nil)}}},
	}
	if err := reg.Register(m); err != nil {
		t.Fatal(err)
	}
	settings := &plugintest.MemTenantSettings{}
	ten := tenancy.NewService(reg, settings)
	iv := NewInvoker(fakeClients{})
	iv.Bind(ten, nil)
	iv.SetHostAPI(hostapi.NewIssuer([]byte("k")), "http://127.0.0.1:9")
	as := func(tenant uint64) context.Context {
		return context.WithValue(context.Background(), types.TenantIDContextKey, tenant)
	}
	off := func(err error) bool {
		pe, ok := pluginapi.AsError(err)
		return errors.Is(err, tenancy.ErrPluginOff) && ok && !pe.Retryable
	}

	// Installed plugins start off in every workspace.
	if _, err := iv.Envelope(as(7), m, nil); !off(err) {
		t.Fatalf("a workspace that never switched it on: %v", err)
	}
	if err := ten.SetEnabled(context.Background(), 7, "acme.x", true, "u"); err != nil {
		t.Fatal(err)
	}
	env, err := iv.Envelope(as(7), m, nil)
	if err != nil || env.Context.Host == nil {
		t.Fatalf("switched on: %+v, %v", env.Context, err)
	}

	// Left out of the audience: cut off though its switch is on.
	reg.SetAudience("acme.x", []uint64{8})
	if _, err := iv.Envelope(as(7), m, nil); !off(err) {
		t.Fatalf("out of the audience: %v", err)
	}
	if _, err := iv.Envelope(Configuring(as(7)), m, nil); !off(err) {
		t.Fatalf("configuring out of the audience: %v", err)
	}

	// Configuring needs it visible only, and gets no Host API access.
	env, err = iv.Envelope(Configuring(as(8)), m, nil)
	if err != nil || env.Context.Host != nil {
		t.Fatalf("configuring before switching on: %+v, %v", env.Context, err)
	}
	// The platform's calls carry no workspace.
	if _, err := iv.Envelope(as(0), m, nil); err != nil {
		t.Fatalf("platform call: %v", err)
	}
	if _, err := iv.Envelope(context.Background(), m, nil); err != nil {
		t.Fatalf("call without a workspace: %v", err)
	}

	// A switch that cannot be read refuses, to be retried.
	iv.Bind(tenancy.NewService(reg, &failingSettings{}), nil)
	_, err = iv.Envelope(as(8), m, nil)
	if pe, ok := pluginapi.AsError(err); !ok || !pe.Retryable || errors.Is(err, tenancy.ErrPluginOff) {
		t.Fatalf("unreadable switch: %v", err)
	}
}
