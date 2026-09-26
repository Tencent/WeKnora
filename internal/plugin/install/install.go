// Package install is how a system administrator manages installed plugins:
// review a package, install or upgrade it, switch it on or off platform-wide,
// roll back to a stored version, uninstall. It only writes rows and package
// blobs; the reconciler on every node does the loading.
package install

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	goruntime "runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/host"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/trust"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// ErrNotInstalled is returned for a plugin ID with no installed row.
var ErrNotInstalled = errors.New("plugin is not installed")

// InvalidError is a request the administrator has to change: a bad package,
// an unsupported runtime, a digest that no longer matches what was reviewed.
type InvalidError struct{ Err error }

func (e *InvalidError) Error() string { return e.Err.Error() }
func (e *InvalidError) Unwrap() error { return e.Err }

func invalid(format string, args ...any) error {
	return &InvalidError{Err: fmt.Errorf(format, args...)}
}

// supportedRuntimes are the runtimes an installed package may use today.
var supportedRuntimes = map[manifest.RuntimeType]bool{
	manifest.RuntimeDeclarative: true,
	manifest.RuntimeHost:        true,
	manifest.RuntimeRemote:      true,
}

// checkHostRuntime makes sure this server can run a host plugin: a binary
// built for its OS and architecture, or a Python entry and an interpreter.
func checkHostRuntime(p *pkg.Package) error {
	rt := p.Manifest.Runtime
	if !host.Supported(rt.Kind) {
		return invalid("runtime.kind %q is not supported yet; host plugins must be binaries or python", rt.Kind)
	}
	entry := host.EntryName(rt)
	if _, ok := p.ReadFile(entry); !ok {
		if rt.Kind == host.KindBinary {
			return invalid("the package has no build for this server (%s/%s): %s is missing",
				goruntime.GOOS, goruntime.GOARCH, entry)
		}
		return invalid("the package has no %s (runtime.entry)", entry)
	}
	if rt.Kind == host.KindPython {
		if _, err := exec.LookPath(host.PythonCommand()); err != nil {
			return invalid("python plugins need %s on this server; install it or set WEKNORA_PLUGIN_PYTHON",
				host.PythonCommand())
		}
	}
	return nil
}

// Syncer is the node's reconciler as the installer uses it.
type Syncer interface {
	Reconcile(ctx context.Context) error
	Notify(ctx context.Context)
	Status(pluginID string) (reconcile.Status, bool)
}

// Service manages installed plugins.
type Service struct {
	repo        interfaces.PluginRepository
	store       reconcile.PackageStore
	sync        Syncer
	hostVersion string
	client      *http.Client
	checks      []PackageCheck
	trust       *trust.Store
}

// PackageCheck is a domain's install-time verdict on a package, such as
// whether its model vendor definitions would load.
type PackageCheck func(*pkg.Package) error

// WithChecks adds install-time package checks.
func (s *Service) WithChecks(checks ...PackageCheck) *Service {
	s.checks = append(s.checks, checks...)
	return s
}

// WithTrust sets the trust store packages are judged by; without one every
// package is community and accepted.
func (s *Service) WithTrust(store *trust.Store) *Service {
	s.trust = store
	return s
}

// NewService creates a Service. hostVersion is the running WeKnora version
// that engines ranges are checked against.
func NewService(
	repo interfaces.PluginRepository, store reconcile.PackageStore, sync Syncer, hostVersion string,
) *Service {
	cfg := utils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = 2 * time.Minute
	anyTrust, _ := trust.NewStore(nil, trust.Community)
	return &Service{
		repo: repo, store: store, sync: sync, hostVersion: hostVersion,
		client: utils.NewSSRFSafeHTTPClient(cfg), trust: anyTrust,
	}
}

// Change is what installing a package would do.
type Change string

// Changes a Preview reports.
const (
	ChangeInstall   Change = "install"
	ChangeUpgrade   Change = "upgrade"
	ChangeDowngrade Change = "downgrade"
	ChangeReinstall Change = "reinstall"
)

// Preview is a package as the administrator reviews it before installing.
type Preview struct {
	Manifest *manifest.Manifest `json:"manifest"`
	Digest   string             `json:"digest"`
	Size     int64              `json:"size"`
	Change   Change             `json:"change"`
	// Trust is how far the platform trusts the package, from its signature.
	Trust trust.Verdict `json:"trust"`
	// InstalledVersion is the active version when the plugin is installed.
	InstalledVersion string `json:"installedVersion,omitempty"`
}

// Source says where a package came from.
type Source struct {
	Kind string `json:"kind"` // "upload" or "url"
	URL  string `json:"url,omitempty"`
}

// View is an installed plugin with its versions and how this node runs it.
type View struct {
	types.InstalledPlugin
	Manifest *manifest.Manifest    `json:"manifest"`
	Versions []types.PluginVersion `json:"versions"`
	// Node is the plugin's state on the node that served the request.
	Node *reconcile.Status `json:"node,omitempty"`
	// IssuedSecret is a remote plugin's signing secret, returned only by
	// the call that created it: the service needs it to check requests.
	IssuedSecret string `json:"issuedSecret,omitempty"`
}

// Inspect opens a package and says what installing it would do.
func (s *Service) Inspect(ctx context.Context, data []byte) (*Preview, error) {
	p, verdict, err := s.open(data)
	if err != nil {
		return nil, err
	}
	out := &Preview{Manifest: p.Manifest, Digest: p.Digest, Size: p.Size, Change: ChangeInstall, Trust: verdict}
	row, err := s.repo.GetPlugin(ctx, p.Manifest.ID)
	if err != nil {
		return nil, err
	}
	if row != nil {
		out.InstalledVersion = row.ActiveVersion
		switch c := semver.Compare("v"+p.Manifest.Version, "v"+row.ActiveVersion); {
		case c > 0:
			out.Change = ChangeUpgrade
		case c < 0:
			out.Change = ChangeDowngrade
		default:
			out.Change = ChangeReinstall
		}
	}
	return out, nil
}

// open validates a package for installation on this platform and says how
// far it is trusted.
func (s *Service) open(data []byte) (*pkg.Package, trust.Verdict, error) {
	p, err := pkg.Open(data)
	if err != nil {
		return nil, trust.Verdict{}, &InvalidError{Err: err}
	}
	verdict, err := s.trust.Check(p)
	if err != nil {
		return nil, verdict, &InvalidError{Err: err}
	}
	return p, verdict, s.check(p)
}

// check applies the platform's runtime, engine and domain checks.
func (s *Service) check(p *pkg.Package) error {
	if !supportedRuntimes[p.Manifest.Runtime.Type] {
		return invalid("runtime %q is not supported yet; declarative, host and remote plugins can be installed",
			p.Manifest.Runtime.Type)
	}
	if p.Manifest.Runtime.Type == manifest.RuntimeHost {
		if err := checkHostRuntime(p); err != nil {
			return err
		}
	}
	if err := p.Manifest.CheckEngines(s.hostVersion); err != nil {
		return &InvalidError{Err: err}
	}
	for _, check := range s.checks {
		if err := check(p); err != nil {
			return &InvalidError{Err: err}
		}
	}
	return nil
}

// Request installs or upgrades a plugin from a package.
type Request struct {
	Data   []byte
	Source Source
	// ExpectedDigest, when set, must match the package: it pins the install
	// to the package the administrator reviewed.
	ExpectedDigest string
	UserID         string
	// RemoteURL is where a remote plugin's service runs. An upgrade may
	// leave it empty to keep the registered one.
	RemoteURL string
}

// Install stores the package as a version of its plugin and makes it the
// active one. Installing makes the plugin available platform-wide; each
// tenant still opts in. The permissions the manifest asks for are recorded
// as granted.
func (s *Service) Install(ctx context.Context, req Request) (*View, error) {
	p, verdict, err := s.open(req.Data)
	if err != nil {
		return nil, err
	}
	if req.ExpectedDigest != "" && req.ExpectedDigest != p.Digest {
		return nil, invalid("the package changed since it was reviewed (digest %s, reviewed %s)",
			p.Digest, req.ExpectedDigest)
	}
	m := p.Manifest
	existing, err := s.repo.GetVersion(ctx, m.ID, m.Version)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Digest != p.Digest {
		return nil, invalid("version %s of %s is already installed with different contents; "+
			"publish the change under a new version", m.Version, m.ID)
	}
	if existing == nil {
		uri, err := s.store.Put(ctx, p.Digest, req.Data)
		if err != nil {
			return nil, err
		}
		manifestJSON, _ := json.Marshal(m)
		if err := s.repo.SaveVersion(ctx, &types.PluginVersion{
			PluginID: m.ID, Version: m.Version, Digest: p.Digest, Manifest: types.JSON(manifestJSON),
			PackageURI: uri, Size: p.Size, Trust: string(verdict.Level), SignerKeyID: verdict.KeyID,
			CreatedBy: req.UserID, CreatedAt: time.Now(),
		}); err != nil {
			return nil, err
		}
	}

	row, err := s.repo.GetPlugin(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		row = &types.InstalledPlugin{ID: m.ID, DesiredState: types.PluginStateEnabled, CreatedBy: req.UserID}
	}
	var issued string
	if m.Runtime.Type == manifest.RuntimeRemote {
		if req.RemoteURL != "" {
			if err := checkRemoteURL(req.RemoteURL); err != nil {
				return nil, err
			}
			row.RemoteURL = strings.TrimSuffix(req.RemoteURL, "/")
		}
		if row.RemoteURL == "" {
			return nil, invalid("a remote plugin needs the URL of its service")
		}
		if row.RemoteSecret == "" {
			if issued, err = s.issueSecret(row); err != nil {
				return nil, err
			}
		}
	}
	source, _ := json.Marshal(req.Source)
	perms, _ := json.Marshal(m.Permissions)
	row.Source = types.JSON(source)
	row.ActiveVersion = m.Version
	row.Runtime = string(m.Runtime.Type)
	row.GrantedPerms = types.JSON(perms)
	if err := s.repo.SavePlugin(ctx, row); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "[plugin] %s installed %s %s (%s, %s)", req.UserID, m.ID, m.Version, p.Digest, verdict.Level)
	v, err := s.apply(ctx, m.ID)
	if v != nil {
		v.IssuedSecret = issued
	}
	return v, err
}

// checkRemoteURL accepts an http(s) service URL that passes the SSRF rules;
// a service on a private network needs its host in SSRF_WHITELIST.
func checkRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return invalid("service URL must be an http(s) URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return invalid("service URL must not carry credentials, a query or a fragment")
	}
	if err := utils.ValidateURLForSSRF(raw); err != nil {
		return invalid("service URL is not allowed (private hosts must be in SSRF_WHITELIST): %v", err)
	}
	return nil
}

// issueSecret gives a remote plugin a new signing secret, stored sealed,
// and returns it in the clear for the administrator to hand to the service.
func (s *Service) issueSecret(row *types.InstalledPlugin) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(b)
	sealed, err := utils.EncryptAESGCM(secret, utils.GetAESKey())
	if err != nil {
		return "", err
	}
	row.RemoteSecret = sealed
	return secret, nil
}

// SetRemoteURL moves a remote plugin to another service URL.
func (s *Service) SetRemoteURL(ctx context.Context, id, rawURL string) (*View, error) {
	row, err := s.remoteRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkRemoteURL(rawURL); err != nil {
		return nil, err
	}
	row.RemoteURL = strings.TrimSuffix(rawURL, "/")
	if err := s.repo.SavePlugin(ctx, row); err != nil {
		return nil, err
	}
	return s.apply(ctx, id)
}

// RotateSecret replaces a remote plugin's signing secret. Calls fail until
// the service is given the new one, which only this response shows.
func (s *Service) RotateSecret(ctx context.Context, id string) (*View, error) {
	row, err := s.remoteRow(ctx, id)
	if err != nil {
		return nil, err
	}
	secret, err := s.issueSecret(row)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SavePlugin(ctx, row); err != nil {
		return nil, err
	}
	v, err := s.apply(ctx, id)
	if v != nil {
		v.IssuedSecret = secret
	}
	return v, err
}

func (s *Service) remoteRow(ctx context.Context, id string) (*types.InstalledPlugin, error) {
	row, err := s.repo.GetPlugin(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrNotInstalled
	}
	if row.Runtime != string(manifest.RuntimeRemote) {
		return nil, invalid("%s is not a remote plugin", id)
	}
	return row, nil
}

// FetchURL downloads a package over HTTP(S), refusing private addresses.
func (s *Service) FetchURL(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, invalid("package URL must be an http(s) URL")
	}
	if err := utils.ValidateURLForSSRF(rawURL); err != nil {
		return nil, invalid("package URL is not allowed: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, invalid("package URL: %v", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, invalid("download package: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, invalid("download package: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, pkg.MaxArchiveBytes+1))
	if err != nil {
		return nil, invalid("download package: %v", err)
	}
	if len(data) > pkg.MaxArchiveBytes {
		return nil, invalid("package is over the %d byte limit", pkg.MaxArchiveBytes)
	}
	return data, nil
}

// List returns every installed plugin.
func (s *Service) List(ctx context.Context) ([]View, error) {
	rows, err := s.repo.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	out := make([]View, 0, len(rows))
	for _, row := range rows {
		v, err := s.view(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

// Get returns one installed plugin.
func (s *Service) Get(ctx context.Context, id string) (*View, error) {
	row, err := s.repo.GetPlugin(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrNotInstalled
	}
	return s.view(ctx, *row)
}

func (s *Service) view(ctx context.Context, row types.InstalledPlugin) (*View, error) {
	versions, err := s.repo.ListVersions(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	v := &View{InstalledPlugin: row, Versions: versions}
	for _, pv := range versions {
		if pv.Version == row.ActiveVersion {
			var m manifest.Manifest
			if json.Unmarshal(pv.Manifest, &m) == nil {
				v.Manifest = &m
			}
		}
	}
	if st, ok := s.sync.Status(row.ID); ok {
		v.Node = &st
	}
	return v, nil
}

// SetEnabled switches an installed plugin on or off for the whole platform.
// Off unloads it everywhere; tenant switches are kept for when it returns.
func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool) (*View, error) {
	row, err := s.repo.GetPlugin(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrNotInstalled
	}
	row.DesiredState = types.PluginStateDisabled
	if enabled {
		row.DesiredState = types.PluginStateEnabled
	}
	if err := s.repo.SavePlugin(ctx, row); err != nil {
		return nil, err
	}
	return s.apply(ctx, id)
}

// SetAudience limits an installed plugin to some tenants; nil gives it back
// to every tenant. Tenants outside the audience no longer see it: their
// switch and configuration are kept for when they are let back in.
func (s *Service) SetAudience(ctx context.Context, id string, tenants []uint64) (*View, error) {
	row, err := s.repo.GetPlugin(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrNotInstalled
	}
	row.Audience = nil
	if tenants != nil {
		sort.Slice(tenants, func(i, j int) bool { return tenants[i] < tenants[j] })
		tenants = slices.Compact(tenants)
		b, _ := json.Marshal(tenants)
		row.Audience = types.JSON(b)
	}
	if err := s.repo.SavePlugin(ctx, row); err != nil {
		return nil, err
	}
	return s.apply(ctx, id)
}

// Activate makes a stored version the active one: a rollback, or a return to
// a newer version after one.
func (s *Service) Activate(ctx context.Context, id, version string) (*View, error) {
	row, err := s.repo.GetPlugin(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrNotInstalled
	}
	v, err := s.repo.GetVersion(ctx, id, version)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, invalid("version %s of %s is not stored", version, id)
	}
	if err := s.trust.Admit(trust.Level(v.Trust)); err != nil {
		return nil, &InvalidError{Err: err}
	}
	var m manifest.Manifest
	if err := json.Unmarshal(v.Manifest, &m); err == nil {
		if err := m.CheckEngines(s.hostVersion); err != nil {
			return nil, &InvalidError{Err: err}
		}
		perms, _ := json.Marshal(m.Permissions)
		row.GrantedPerms = types.JSON(perms)
	}
	row.ActiveVersion = version
	if err := s.repo.SavePlugin(ctx, row); err != nil {
		return nil, err
	}
	return s.apply(ctx, id)
}

// Uninstall removes a plugin, its versions and their packages. Tenant
// switches and configuration are kept, so reinstalling restores them.
func (s *Service) Uninstall(ctx context.Context, id string) error {
	row, err := s.repo.GetPlugin(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return ErrNotInstalled
	}
	versions, err := s.repo.ListVersions(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.DeletePlugin(ctx, id); err != nil {
		return err
	}
	if err := s.sync.Reconcile(ctx); err != nil {
		logger.Warnf(ctx, "[plugin] reconcile after uninstalling %s: %v", id, err)
	}
	s.sync.Notify(ctx)
	for _, v := range versions {
		if err := s.store.Delete(ctx, v.PackageURI); err != nil {
			logger.Warnf(ctx, "[plugin] delete package %s: %v", v.PackageURI, err)
		}
	}
	return nil
}

// apply reconciles this node now, tells the others, and returns the result.
// A plugin that fails to load is still installed; its View says why.
func (s *Service) apply(ctx context.Context, id string) (*View, error) {
	if err := s.sync.Reconcile(ctx); err != nil {
		logger.Warnf(ctx, "[plugin] reconcile after changing %s: %v", id, err)
	}
	s.sync.Notify(ctx)
	return s.Get(ctx, id)
}
