package driver

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
)

func TestBuiltinDriver(t *testing.T) {
	plugins := map[string]*manifest.Manifest{
		"weknora.bing": {ID: "weknora.bing", Version: "0.8.2", Builtin: true},
	}
	d := NewBuiltin(func(id string) (*manifest.Manifest, bool) { m, ok := plugins[id]; return m, ok })
	ctx := context.Background()

	if err := d.Ensure(ctx, plugins["weknora.bing"]); err != nil {
		t.Fatalf("Ensure builtin: %v", err)
	}
	if err := d.Ensure(ctx, &manifest.Manifest{ID: "acme.x"}); err == nil {
		t.Fatal("the builtin driver must refuse non-builtin manifests")
	}
	if err := d.Remove(ctx, "weknora.bing", "0.8.2"); err == nil {
		t.Fatal("builtins cannot be removed")
	}
	ep, err := d.Resolve(ctx, "weknora.bing", 1)
	if err != nil || ep.Kind != EndpointInProcess {
		t.Fatalf("Resolve = %+v, %v", ep, err)
	}
	if _, err := d.Resolve(ctx, "weknora.nope", 1); err == nil {
		t.Fatal("unknown plugin must not resolve")
	}
	st, err := d.Status(ctx, "weknora.bing")
	if err != nil || len(st) != 1 || st[0].State != StateReady || st[0].Version != "0.8.2" || st[0].Node == "" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
}

func TestSet(t *testing.T) {
	s := NewSet(NewBuiltin(func(string) (*manifest.Manifest, bool) { return nil, false }))
	if d, err := s.For(manifest.RuntimeBuiltin); err != nil || d.Type() != manifest.RuntimeBuiltin {
		t.Fatalf("For(builtin) = %v, %v", d, err)
	}
	if _, err := s.For(manifest.RuntimeHost); err == nil {
		t.Fatal("no host driver is installed yet")
	}
}
