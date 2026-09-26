package install

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/trust"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/pluginsdk/pluginsign"
)

func newService(t *testing.T) (*Service, *plugintest.MemRepo, *plugintest.MemStore, *registry.Registry) {
	t.Helper()
	repo, store, reg := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New()
	r := reconcile.New(reconcile.Options{Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir()})
	return NewService(repo, store, r, "0.5.0"), repo, store, reg
}

func TestInstallUpgradeRollbackUninstall(t *testing.T) {
	ctx := context.Background()
	s, repo, store, reg := newService(t)

	v1 := plugintest.KitPackage(t, "1.0.0")
	preview, err := s.Inspect(ctx, v1)
	if err != nil || preview.Change != ChangeInstall || preview.Manifest.ID != "acme.kit" {
		t.Fatalf("Inspect = %+v, %v", preview, err)
	}
	view, err := s.Install(ctx, Request{
		Data: v1, Source: Source{Kind: "upload"},
		ExpectedDigest: preview.Digest, UserID: "admin",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if view.ActiveVersion != "1.0.0" || view.DesiredState != types.PluginStateEnabled ||
		view.Node == nil || view.Node.State != reconcile.StateReady {
		t.Fatalf("view = %+v node=%+v", view.InstalledPlugin, view.Node)
	}
	if _, ok := reg.Plugin("acme.kit"); !ok {
		t.Fatal("install must load the plugin on this node")
	}

	v2 := plugintest.KitPackage(t, "1.1.0")
	if p, _ := s.Inspect(ctx, v2); p.Change != ChangeUpgrade || p.InstalledVersion != "1.0.0" {
		t.Fatalf("upgrade preview = %+v", p)
	}
	if _, err := s.Install(ctx, Request{Data: v2, ExpectedDigest: preview.Digest}); !isInvalid(err) {
		t.Fatalf("a digest other than the reviewed one must be refused, got %v", err)
	}
	if _, err := s.Install(ctx, Request{Data: v2}); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	view, err = s.Activate(ctx, "acme.kit", "1.0.0")
	if err != nil || view.ActiveVersion != "1.0.0" || len(view.Versions) != 2 {
		t.Fatalf("rollback = %+v, %v", view, err)
	}
	if m, _ := reg.Plugin("acme.kit"); m.Version != "1.0.0" {
		t.Fatalf("registry runs %s after rollback", m.Version)
	}

	if view, err = s.SetEnabled(ctx, "acme.kit", false); err != nil || view.Node != nil {
		t.Fatalf("disable = %+v, %v", view, err)
	}
	if _, ok := reg.Plugin("acme.kit"); ok {
		t.Fatal("a disabled plugin must be unloaded")
	}

	if err := s.Uninstall(ctx, "acme.kit"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if row, _ := repo.GetPlugin(ctx, "acme.kit"); row != nil {
		t.Fatal("row survived uninstall")
	}
	if len(store.Blobs) != 0 {
		t.Fatalf("packages survived uninstall: %d", len(store.Blobs))
	}
	if _, err := s.Get(ctx, "acme.kit"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Get after uninstall = %v", err)
	}
}

func TestInstallRejects(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newService(t)
	if _, err := s.Install(ctx, Request{Data: []byte("nope")}); !isInvalid(err) {
		t.Fatalf("garbage must be invalid, got %v", err)
	}

	pkg := plugintest.KitPackage(t, "1.0.0")
	if _, err := s.Install(ctx, Request{Data: pkg}); err != nil {
		t.Fatal(err)
	}
	// Same version, different bytes.
	other := plugintest.KitPackageWith(t, "1.0.0", "changed")
	if _, err := s.Install(ctx, Request{Data: other}); !isInvalid(err) ||
		!strings.Contains(err.Error(), "new version") {
		t.Fatalf("want same-version conflict, got %v", err)
	}
	// Reinstalling the identical package is fine.
	if p, _ := s.Inspect(ctx, pkg); p.Change != ChangeReinstall {
		t.Fatalf("change = %s", p.Change)
	}
	if _, err := s.Install(ctx, Request{Data: pkg}); err != nil {
		t.Fatalf("reinstall: %v", err)
	}

	old := NewService(plugintest.NewMemRepo(), &plugintest.MemStore{}, nil, "0.1.0")
	engines := plugintest.KitPackageWith(t, "2.0.0", "", "engines: { weknora: \">=0.2.0\" }")
	if _, err := old.Inspect(ctx, engines); !isInvalid(err) || !strings.Contains(err.Error(), "needs WeKnora") {
		t.Fatalf("want engines error, got %v", err)
	}
}

func TestFetchURLRefusesPrivateAddresses(t *testing.T) {
	s, _, _, _ := newService(t)
	for _, u := range []string{"http://127.0.0.1:8080/p.wkp", "file:///etc/passwd", "http://169.254.169.254/x"} {
		if _, err := s.FetchURL(context.Background(), u); !isInvalid(err) {
			t.Errorf("FetchURL(%s) = %v, want refusal", u, err)
		}
	}
}

func isInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

func hostPackage(t *testing.T, kind, binPath string) []byte {
	return plugintest.Zip(t, map[string]string{
		"plugin.yaml": "schemaVersion: 1\nid: acme.search\nversion: 1.0.0\napiVersion: weknora.plugin/v1\n" +
			"name: { en-US: ACME Search }\npublisher: { id: acme }\n" +
			"runtime: { type: host, kind: " + kind + ", entry: \"bin/{os}-{arch}/search\" }\n" +
			"permissions: { egress: [api.acme.example] }\n" +
			"contributes:\n  webSearch:\n    - { id: search, name: ACME Search }\n",
		binPath: "binary",
	})
}

func TestInstallHostPlugins(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newService(t)
	name := "search"
	if goruntime.GOOS == "windows" {
		name += ".exe"
	}
	here := "bin/" + goruntime.GOOS + "-" + goruntime.GOARCH + "/" + name
	p, err := s.Inspect(ctx, hostPackage(t, "binary", here))
	if err != nil || p.Manifest.Permissions.Egress[0] != "api.acme.example" {
		t.Fatalf("Inspect = %+v, %v", p, err)
	}
	if _, err := s.Inspect(ctx, hostPackage(t, "binary", "bin/plan9-mips/search")); !isInvalid(err) ||
		!strings.Contains(err.Error(), "no build for this server") {
		t.Fatalf("want a missing build error, got %v", err)
	}
	if _, err := s.Inspect(ctx, hostPackage(t, "node", here)); !isInvalid(err) ||
		!strings.Contains(err.Error(), "binaries or python") {
		t.Fatalf("want an unsupported kind error, got %v", err)
	}
}

func TestInstallPythonPlugins(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newService(t)
	pkg := func(files map[string]string) []byte {
		files["plugin.yaml"] = "schemaVersion: 1\nid: acme.py\nversion: 1.0.0\napiVersion: weknora.plugin/v1\n" +
			"name: { en-US: ACME Py }\npublisher: { id: acme }\n" +
			"runtime: { type: host, kind: python, entry: main.py }\n" +
			"contributes:\n  webSearch:\n    - { id: search, name: ACME Search }\n"
		return plugintest.Zip(t, files)
	}
	if _, err := s.Inspect(ctx, pkg(map[string]string{})); !isInvalid(err) ||
		!strings.Contains(err.Error(), "no main.py") {
		t.Fatalf("want a missing entry error, got %v", err)
	}
	t.Setenv("WEKNORA_PLUGIN_PYTHON", "no-such-python-here")
	if _, err := s.Inspect(ctx, pkg(map[string]string{"main.py": "print()"})); !isInvalid(err) ||
		!strings.Contains(err.Error(), "no-such-python-here") {
		t.Fatalf("want a missing interpreter error, got %v", err)
	}
	t.Setenv("WEKNORA_PLUGIN_PYTHON", os.Args[0]) // any executable will do
	if _, err := s.Inspect(ctx, pkg(map[string]string{"main.py": "print()"})); err != nil {
		t.Fatal(err)
	}
}

func remotePackage(t *testing.T, version string) []byte {
	return plugintest.Zip(t, map[string]string{
		"plugin.yaml": "schemaVersion: 1\nid: acme.remote\nversion: " + version +
			"\napiVersion: weknora.plugin/v1\n" +
			"name: { en-US: ACME Remote }\npublisher: { id: acme }\nruntime: { type: remote }\n" +
			"contributes:\n  webSearch:\n    - { id: search, name: ACME Search }\n",
	})
}

func TestInstallRemotePlugins(t *testing.T) {
	ctx := context.Background()
	utils.SetSSRFWhitelistFromRaw("plugins.example.com")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	s, repo, _, _ := newService(t)

	if _, err := s.Install(ctx, Request{Data: remotePackage(t, "1.0.0")}); !isInvalid(err) ||
		!strings.Contains(err.Error(), "URL of its service") {
		t.Fatalf("want a missing URL error, got %v", err)
	}
	refused := []string{"http://127.0.0.1:9000", "ftp://plugins.example.com", "https://u:p@plugins.example.com"}
	for _, u := range refused {
		if _, err := s.Install(ctx, Request{Data: remotePackage(t, "1.0.0"), RemoteURL: u}); !isInvalid(err) {
			t.Errorf("RemoteURL %s = %v, want refusal", u, err)
		}
	}

	view, err := s.Install(ctx, Request{
		Data: remotePackage(t, "1.0.0"), RemoteURL: "https://plugins.example.com/acme/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.IssuedSecret) != 64 || view.RemoteURL != "https://plugins.example.com/acme" {
		t.Fatalf("view = %+v", view)
	}
	row, _ := repo.GetPlugin(ctx, "acme.remote")
	if plain, _ := utils.DecryptStoredSecret(row.RemoteSecret); !strings.HasPrefix(row.RemoteSecret, utils.EncPrefix) ||
		plain != view.IssuedSecret {
		t.Fatalf("stored secret %q does not seal the issued one", row.RemoteSecret)
	}
	first := view.IssuedSecret

	view, err = s.Install(ctx, Request{Data: remotePackage(t, "1.1.0")})
	if err != nil || view.IssuedSecret != "" || view.RemoteURL != "https://plugins.example.com/acme" {
		t.Fatalf("upgrade must keep the URL and secret: %+v, %v", view, err)
	}

	view, err = s.RotateSecret(ctx, "acme.remote")
	if err != nil || len(view.IssuedSecret) != 64 || view.IssuedSecret == first {
		t.Fatalf("rotate = %+v, %v", view, err)
	}
	if view, err = s.SetRemoteURL(ctx, "acme.remote", "https://plugins.example.com/v2"); err != nil ||
		view.RemoteURL != "https://plugins.example.com/v2" || view.IssuedSecret != "" {
		t.Fatalf("move = %+v, %v", view, err)
	}
	if _, err := s.SetRemoteURL(ctx, "acme.remote", "http://10.0.0.1"); !isInvalid(err) {
		t.Fatalf("private URL = %v", err)
	}

	if _, err := s.Install(ctx, Request{Data: plugintest.KitPackage(t, "1.0.0")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RotateSecret(ctx, "acme.kit"); !isInvalid(err) {
		t.Fatalf("rotating a declarative plugin = %v", err)
	}
	if _, err := s.RotateSecret(ctx, "acme.none"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("rotating a missing plugin = %v", err)
	}
}

func TestInstallRecordsTrustAndEnforcesTheMinimum(t *testing.T) {
	ctx := context.Background()
	s, repo, _, _ := newService(t)
	pub, priv, _ := ed25519.GenerateKey(nil)
	store, err := trust.NewStore([]trust.Key{{ID: "market", PublicKey: pub, Level: trust.Verified}}, trust.Community)
	if err != nil {
		t.Fatal(err)
	}
	s.WithTrust(store)

	signed, err := pluginsign.SignArchive(plugintest.KitPackage(t, "1.1.0"), "market", priv)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.Inspect(ctx, signed)
	if err != nil || preview.Trust.Level != trust.Verified || preview.Trust.KeyID != "market" {
		t.Fatalf("signed preview = %+v, %v", preview, err)
	}
	if _, err := s.Install(ctx, Request{Data: plugintest.KitPackage(t, "1.0.0")}); err != nil {
		t.Fatalf("community install under a community minimum: %v", err)
	}
	if _, err := s.Install(ctx, Request{Data: signed}); err != nil {
		t.Fatalf("signed install: %v", err)
	}
	if v, _ := repo.GetVersion(ctx, "acme.kit", "1.1.0"); v.Trust != "verified" || v.SignerKeyID != "market" {
		t.Fatalf("stored version = %+v", v)
	}

	strict, _ := trust.NewStore([]trust.Key{{ID: "market", PublicKey: pub, Level: trust.Verified}}, trust.Verified)
	s.WithTrust(strict)
	if _, err := s.Inspect(ctx, plugintest.KitPackage(t, "1.2.0")); !isInvalid(err) {
		t.Fatalf("an unsigned package under a verified minimum: %v", err)
	}
	if _, err := s.Activate(ctx, "acme.kit", "1.0.0"); !isInvalid(err) {
		t.Fatalf("rolling back to a community version under a verified minimum: %v", err)
	}
}

func TestSetAudience(t *testing.T) {
	ctx := context.Background()
	s, repo, _, reg := newService(t)
	if _, err := s.Install(ctx, Request{Data: plugintest.KitPackage(t, "1.0.0")}); err != nil {
		t.Fatal(err)
	}
	v, err := s.SetAudience(ctx, "acme.kit", []uint64{9, 7, 9})
	if err != nil || string(v.Audience) != "[7,9]" {
		t.Fatalf("audience = %s, %v", v.Audience, err)
	}
	if reg.VisibleTo("acme.kit", 8) || !reg.VisibleTo("acme.kit", 7) {
		t.Fatal("the node did not apply the audience")
	}
	if _, err := s.SetAudience(ctx, "acme.kit", nil); err != nil {
		t.Fatal(err)
	}
	if row, _ := repo.GetPlugin(ctx, "acme.kit"); row.AudienceTenants() != nil || !reg.VisibleTo("acme.kit", 8) {
		t.Fatalf("audience not cleared: %s", row.Audience)
	}
	if _, err := s.SetAudience(ctx, "acme.missing", nil); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("unknown plugin: %v", err)
	}
}

func ownedPackage(t *testing.T, id, version string, extra ...string) []byte {
	t.Helper()
	manifest := "schemaVersion: 1\nid: " + id + "\nversion: " + version + "\napiVersion: weknora.plugin/v1\n" +
		"name: { en-US: Own }\npublisher: { id: " + strings.Split(id, ".")[0] + " }\nruntime: { type: remote }\n" +
		"contributes:\n  webSearch:\n    - { id: s, name: S }\n"
	for _, line := range extra {
		manifest += line + "\n"
	}
	return plugintest.Zip(t, map[string]string{"plugin.yaml": manifest})
}

func TestWorkspaceOwnedPlugins(t *testing.T) {
	ctx := context.Background()
	utils.SetSSRFWhitelistFromRaw("plugins.example.com")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	s, _, _, reg := newService(t)
	allowed := false
	s.WithTenantPlugins(func(context.Context) bool { return allowed })
	own := ownedPackage(t, "team.search", "1.0.0")
	req := Request{Data: own, RemoteURL: "https://plugins.example.com/search"}

	if _, err := s.InstallOwned(ctx, 7, req); !errors.Is(err, ErrTenantPluginsOff) {
		t.Fatalf("switched off: %v", err)
	}
	allowed = true
	if _, err := s.InstallOwned(ctx, 7, Request{Data: plugintest.KitPackage(t, "1.0.0")}); !isInvalid(err) {
		t.Fatalf("a declarative package: %v", err)
	}
	pages := plugintest.Zip(t, map[string]string{
		"plugin.yaml": "schemaVersion: 1\nid: team.pages\nversion: 1.0.0\napiVersion: weknora.plugin/v1\n" +
			"name: P\npublisher: { id: team }\nruntime: { type: remote }\n" +
			"contributes:\n  pages:\n    - { id: p, name: P, entry: ui/p.html }\n",
		"ui/p.html": "<html></html>",
	})
	if _, err := s.InspectOwned(ctx, 7, pages); err == nil || !strings.Contains(err.Error(), "cannot add pages") {
		t.Fatalf("a page in a workspace's plugin: %v", err)
	}

	v, err := s.InstallOwned(ctx, 7, req)
	if err != nil || v.OwnerTenantID == nil || *v.OwnerTenantID != 7 || v.IssuedSecret == "" {
		t.Fatalf("install owned = %+v, %v", v, err)
	}
	if !reg.VisibleTo("team.search", 7) || reg.VisibleTo("team.search", 8) {
		t.Fatal("a workspace's plugin must be its own")
	}
	if _, err := s.SetAudience(ctx, "team.search", nil); !isInvalid(err) {
		t.Fatalf("widening a workspace's plugin: %v", err)
	}
	if _, err := s.InstallOwned(ctx, 8, req); !isInvalid(err) {
		t.Fatalf("another workspace took the ID: %v", err)
	}
	if _, err := s.Install(ctx, req); !isInvalid(err) {
		t.Fatalf("the platform installed over a workspace's plugin: %v", err)
	}
	if list, _ := s.ListOwned(ctx, 8); len(list) != 0 {
		t.Fatalf("workspace 8 lists %v", list)
	}
	if err := s.UninstallOwned(ctx, 8, "team.search"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("workspace 8 removed workspace 7's plugin: %v", err)
	}
	if _, err := s.RotateSecretOwned(ctx, 7, "team.search"); err != nil {
		t.Fatal(err)
	}
	if err := s.UninstallOwned(ctx, 7, "team.search"); err != nil {
		t.Fatal(err)
	}

	// A workspace cannot take a platform plugin's ID either.
	if _, err := s.Install(ctx, Request{Data: plugintest.KitPackage(t, "1.0.0")}); err != nil {
		t.Fatal(err)
	}
	taken := Request{Data: ownedPackage(t, "acme.kit", "2.0.0"), RemoteURL: "https://plugins.example.com/kit"}
	if _, err := s.InstallOwned(ctx, 7, taken); !isInvalid(err) {
		t.Fatalf("a workspace took a platform plugin's ID: %v", err)
	}
}

func TestKubernetesPackagesNeedACluster(t *testing.T) {
	ctx := context.Background()
	s, repo, _, _ := newService(t)
	data := plugintest.Zip(t, map[string]string{"plugin.yaml": "schemaVersion: 1\nid: acme.kube\nversion: 1.0.0\n" +
		"apiVersion: weknora.plugin/v1\nname: K\npublisher: { id: acme }\n" +
		"runtime: { type: kubernetes, image: ghcr.io/acme/kube:1.0.0 }\n" +
		"contributes:\n  webSearch:\n    - { id: s, name: S }\n"})
	_, err := s.Inspect(ctx, data)
	if !isInvalid(err) || !strings.Contains(err.Error(), "WEKNORA_PLUGIN_K8S_NAMESPACE") {
		t.Fatalf("without a cluster: %v", err)
	}
	s.WithRuntimes(manifest.RuntimeKubernetes)
	v, err := s.Install(ctx, Request{Data: data})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := repo.GetPlugin(ctx, "acme.kube")
	if row.RemoteSecret == "" || v.IssuedSecret != "" {
		t.Fatalf("secret stored %v, shown %q", row.RemoteSecret != "", v.IssuedSecret)
	}
	before := row.RemoteSecret
	if v, err := s.RotateSecret(ctx, "acme.kube"); err != nil || v.IssuedSecret != "" {
		t.Fatalf("rotate = %v (shown %q)", err, v.IssuedSecret)
	}
	if row, _ := repo.GetPlugin(ctx, "acme.kube"); row.RemoteSecret == before {
		t.Fatal("rotation kept the secret")
	}
}

// A plugin that moves from kubernetes to remote gets a secret the
// administrator is shown: nobody ever saw the one its pods had.
func TestKubernetesToRemoteIssuesASecret(t *testing.T) {
	ctx := context.Background()
	utils.SetSSRFWhitelistFromRaw("plugins.example.com")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	s, repo, _, _ := newService(t)
	s.WithRuntimes(manifest.RuntimeKubernetes)
	kubePkg := func(version string) []byte {
		return plugintest.Zip(t, map[string]string{"plugin.yaml": "schemaVersion: 1\nid: acme.remote\n" +
			"version: " + version + "\napiVersion: weknora.plugin/v1\nname: K\npublisher: { id: acme }\n" +
			"runtime: { type: kubernetes, image: ghcr.io/acme/remote:1 }\n" +
			"contributes:\n  webSearch:\n    - { id: search, name: S }\n"})
	}
	if _, err := s.Install(ctx, Request{Data: kubePkg("1.0.0")}); err != nil {
		t.Fatal(err)
	}
	kubeSecret := func() string { row, _ := repo.GetPlugin(ctx, "acme.remote"); return row.RemoteSecret }
	deployed := kubeSecret()

	v, err := s.Install(ctx, Request{Data: remotePackage(t, "2.0.0"), RemoteURL: "https://plugins.example.com/r"})
	if err != nil || len(v.IssuedSecret) != 64 || kubeSecret() == deployed {
		t.Fatalf("switch to remote = %+v, %v", v, err)
	}

	// Back to kubernetes keeps it; a rollback to remote shows a new one.
	if v, err = s.Activate(ctx, "acme.remote", "1.0.0"); err != nil || v.IssuedSecret != "" {
		t.Fatalf("back to kubernetes = %+v, %v", v, err)
	}
	if v, err = s.Activate(ctx, "acme.remote", "2.0.0"); err != nil || len(v.IssuedSecret) != 64 {
		t.Fatalf("rollback to remote = %+v, %v", v, err)
	}
}

// Activating a stored version runs the checks installing it did: the
// platform may no longer accept it.
func TestActivateChecksTheStoredPackage(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newService(t)
	refuse := ""
	s.WithChecks(func(p *pkg.Package) error {
		if p.Manifest.Version == refuse {
			return errors.New("vendor definitions no longer load")
		}
		return nil
	})
	for _, v := range []string{"1.0.0", "1.1.0"} {
		if _, err := s.Install(ctx, Request{Data: plugintest.KitPackage(t, v)}); err != nil {
			t.Fatal(err)
		}
	}
	refuse = "1.0.0"
	if _, err := s.Activate(ctx, "acme.kit", "1.0.0"); !isInvalid(err) || !strings.Contains(err.Error(), "no longer") {
		t.Fatalf("activate a version the checks refuse = %v", err)
	}
	if v, _ := s.Get(ctx, "acme.kit"); v.ActiveVersion != "1.1.0" {
		t.Fatalf("active = %s", v.ActiveVersion)
	}
	refuse = ""
	if _, err := s.Activate(ctx, "acme.kit", "1.0.0"); err != nil {
		t.Fatal(err)
	}
}

// A kind this node leaves to standalone plugin hosts is not checked against
// this machine: the plugin host checks it against its own.
func TestHostKindsLeftToPluginHosts(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newService(t)
	elsewhere := hostPackage(t, "binary", "bin/plan9-mips/search")
	py := plugintest.Zip(t, map[string]string{
		"plugin.yaml": "schemaVersion: 1\nid: acme.py\nversion: 1.0.0\napiVersion: weknora.plugin/v1\n" +
			"name: { en-US: ACME Py }\npublisher: { id: acme }\n" +
			"runtime: { type: host, kind: python, entry: main.py }\n" +
			"contributes:\n  webSearch:\n    - { id: search, name: ACME Search }\n",
		"main.py": "print()",
	})
	t.Setenv("WEKNORA_PLUGIN_PYTHON", "no-such-python-here")
	t.Setenv("WEKNORA_PLUGIN_EMBEDDED_KINDS", "none")
	if _, err := s.Inspect(ctx, elsewhere); err != nil {
		t.Fatalf("a binary for another machine, run elsewhere: %v", err)
	}
	if _, err := s.Inspect(ctx, py); err != nil {
		t.Fatalf("python run elsewhere: %v", err)
	}
	t.Setenv("WEKNORA_PLUGIN_EMBEDDED_KINDS", "binary,python")
	if _, err := s.Inspect(ctx, elsewhere); !isInvalid(err) {
		t.Fatalf("a binary for another machine, run here: %v", err)
	}
	if _, err := s.Inspect(ctx, py); !isInvalid(err) {
		t.Fatalf("python run here without an interpreter: %v", err)
	}
}

// versionRefused is a repository whose database refuses to store versions.
type versionRefused struct{ *plugintest.MemRepo }

func (versionRefused) SaveVersion(context.Context, *types.PluginVersion) error {
	return errors.New("value too long for type character varying(64)")
}

// A package whose version row could not be stored does not stay behind.
func TestFailedInstallLeavesNoPackage(t *testing.T) {
	repo, store := versionRefused{plugintest.NewMemRepo()}, &plugintest.MemStore{}
	r := reconcile.New(reconcile.Options{Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir()})
	s := NewService(repo, store, r, "0.5.0")
	if _, err := s.Install(context.Background(), Request{Data: plugintest.KitPackage(t, "1.0.0")}); err == nil {
		t.Fatal("want the database error")
	}
	if len(store.Blobs) != 0 {
		t.Fatalf("the package stayed behind: %d blobs", len(store.Blobs))
	}
}

// Removing a workspace uninstalls only its own plugins and drops its
// plugin data.
func TestRemoveTenant(t *testing.T) {
	ctx := context.Background()
	utils.SetSSRFWhitelistFromRaw("plugins.example.com")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	s, repo, _, reg := newService(t)
	s.WithTenantPlugins(func(context.Context) bool { return true })
	for tenant, id := range map[uint64]string{7: "team.seven", 8: "team.eight"} {
		req := Request{Data: ownedPackage(t, id, "1.0.0"), RemoteURL: "https://plugins.example.com/" + id}
		if _, err := s.InstallOwned(ctx, tenant, req); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Install(ctx, Request{Data: plugintest.KitPackage(t, "1.0.0")}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTenant(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if row, _ := repo.GetPlugin(ctx, "team.seven"); row != nil {
		t.Fatal("the workspace's plugin is still installed")
	}
	if _, ok := reg.Plugin("team.seven"); ok {
		t.Fatal("the workspace's plugin is still loaded")
	}
	for _, id := range []string{"team.eight", "acme.kit"} {
		if row, _ := repo.GetPlugin(ctx, id); row == nil {
			t.Fatalf("%s was removed", id)
		}
	}
	if len(repo.DeletedTenants) != 1 || repo.DeletedTenants[0] != 7 {
		t.Fatalf("plugin data removed for %v", repo.DeletedTenants)
	}
}
