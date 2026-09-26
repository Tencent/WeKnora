package registry

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
)

func builtin(id string, contributes manifest.Contributions) *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion,
		ID:            id,
		Version:       "1.0.0",
		Name:          manifest.Text(id, nil),
		Publisher:     manifest.Publisher{ID: manifest.BuiltinPublisher},
		Builtin:       true,
		Runtime:       manifest.Runtime{Type: manifest.RuntimeBuiltin},
		Contributes:   contributes,
	}
}

func contribution(id string, order int, aliases ...string) manifest.Contribution {
	return manifest.Contribution{ID: id, Name: manifest.Text(id, nil), Order: order, Aliases: aliases}
}

func TestRegisterAndResolve(t *testing.T) {
	r := New()
	feishu := builtin("weknora.feishu", manifest.Contributions{
		manifest.PointConnectors: {
			contribution("feishu", 2, "feishu"),
			contribution("feishu_drive", 3, "feishu_drive"),
		},
		manifest.PointIMChannels: {contribution("feishu", 1, "feishu")},
	})
	notion := builtin("weknora.notion", manifest.Contributions{
		manifest.PointConnectors: {contribution("notion", 1, "notion")},
	})
	for _, m := range []*manifest.Manifest{feishu, notion} {
		if err := r.Register(m); err != nil {
			t.Fatalf("Register(%s): %v", m.ID, err)
		}
	}

	// The same alias may live at two different points.
	for _, point := range []manifest.Point{manifest.PointConnectors, manifest.PointIMChannels} {
		e, ok := r.Resolve(point, "feishu")
		if !ok || e.QualifiedID != "weknora.feishu/feishu" || e.PluginID != "weknora.feishu" {
			t.Fatalf("Resolve(%s, feishu) = %+v, %v", point, e, ok)
		}
	}
	if _, ok := r.Resolve(manifest.PointConnectors, "weknora.notion/notion"); !ok {
		t.Fatal("qualified ID should resolve")
	}
	if _, ok := r.Resolve(manifest.PointWebSearch, "feishu"); ok {
		t.Fatal("alias must not leak across points")
	}

	var got []string
	for _, e := range r.Contributions(manifest.PointConnectors) {
		got = append(got, e.QualifiedID)
	}
	want := "weknora.notion/notion,weknora.feishu/feishu,weknora.feishu/feishu_drive"
	if strings.Join(got, ",") != want {
		t.Fatalf("connector order = %v, want %s", got, want)
	}

	var ids []string
	for _, m := range r.Plugins() {
		ids = append(ids, m.ID)
	}
	if strings.Join(ids, ",") != "weknora.feishu,weknora.notion" {
		t.Fatalf("plugins = %v", ids)
	}
}

func TestRegisterRejectsCollisionsAtomically(t *testing.T) {
	r := New()
	if err := r.Register(builtin("weknora.feishu", manifest.Contributions{
		manifest.PointConnectors: {contribution("feishu", 0, "feishu")},
	})); err != nil {
		t.Fatal(err)
	}

	clash := builtin("weknora.lark", manifest.Contributions{
		manifest.PointConnectors: {contribution("lark", 0, "lark"), contribution("other", 0, "feishu")},
	})
	err := r.Register(clash)
	if err == nil || !strings.Contains(err.Error(), "collides with weknora.feishu/feishu") {
		t.Fatalf("want collision error, got %v", err)
	}
	if _, ok := r.Plugin("weknora.lark"); ok {
		t.Fatal("failed registration must not leave the plugin behind")
	}
	if _, ok := r.Resolve(manifest.PointConnectors, "lark"); ok {
		t.Fatal("failed registration must not leave aliases behind")
	}

	if err := r.Register(builtin("weknora.feishu", manifest.Contributions{
		manifest.PointTools: {contribution("x", 0)},
	})); err == nil {
		t.Fatal("duplicate plugin ID must be rejected")
	}

	self := builtin("weknora.self", manifest.Contributions{
		manifest.PointTools: {contribution("a", 0, "same"), contribution("b", 0, "same")},
	})
	if err := r.Register(self); err == nil || !strings.Contains(err.Error(), "claimed twice") {
		t.Fatalf("want self-collision error, got %v", err)
	}
}

func TestRegisterValidates(t *testing.T) {
	m := builtin("weknora.bad", manifest.Contributions{manifest.PointTools: {{ID: "Bad ID"}}})
	if err := New().Register(m); err == nil {
		t.Fatal("invalid manifest must be rejected")
	}
}

func thirdParty(id, version string, contributes manifest.Contributions) *manifest.Manifest {
	publisher, _, _ := strings.Cut(id, ".")
	return &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: id, Version: version, Name: manifest.Text(id, nil),
		Publisher: manifest.Publisher{ID: publisher}, Runtime: manifest.Runtime{Type: manifest.RuntimeDeclarative},
		Contributes: contributes,
	}
}

func TestReplaceAndUnregister(t *testing.T) {
	r := New()
	skill := func(id string) manifest.Contribution {
		return manifest.Contribution{ID: id, Name: manifest.Text(id, nil), Path: "skills/" + id}
	}
	if err := r.Register(builtin("weknora.core", manifest.Contributions{
		manifest.PointTools: {contribution("thinking", 0, "thinking")},
	})); err != nil {
		t.Fatal(err)
	}
	v1 := thirdParty("acme.kit", "1.0.0", manifest.Contributions{manifest.PointSkills: {skill("triage"), skill("old")}})
	if err := r.Replace(v1); err != nil {
		t.Fatalf("Replace as first install: %v", err)
	}
	v2 := thirdParty("acme.kit", "2.0.0", manifest.Contributions{manifest.PointSkills: {skill("triage"), skill("new")}})
	if err := r.Replace(v2); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if m, _ := r.Plugin("acme.kit"); m.Version != "2.0.0" {
		t.Fatalf("version = %s", m.Version)
	}
	if _, ok := r.Resolve(manifest.PointSkills, "acme.kit/old"); ok {
		t.Fatal("contributions dropped by the upgrade must be gone")
	}
	if got := len(r.Contributions(manifest.PointSkills)); got != 2 {
		t.Fatalf("skills after upgrade = %d, want 2", got)
	}
	if err := r.Unregister("acme.kit"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Plugin("acme.kit"); ok || len(r.Contributions(manifest.PointSkills)) != 0 {
		t.Fatal("unregister must remove the plugin and its contributions")
	}
	if err := r.Unregister("weknora.core"); err == nil {
		t.Fatal("builtins cannot be removed")
	}
	if err := r.Replace(builtin("weknora.core", manifest.Contributions{
		manifest.PointTools: {contribution("thinking", 0)},
	})); err == nil {
		t.Fatal("builtins cannot be replaced")
	}
}
