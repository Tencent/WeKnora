// Package install is how a system administrator manages installed plugins:
// review a package, install or upgrade it, switch it on or off platform-wide,
// roll back to a stored version, uninstall. It only writes rows and package
// blobs; the reconciler on every node does the loading.
package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"golang.org/x/mod/semver"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
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
// Code plugins arrive with the plugin host.
var supportedRuntimes = map[manifest.RuntimeType]bool{manifest.RuntimeDeclarative: true}

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
}

// PackageCheck is a domain's install-time verdict on a package, such as
// whether its model vendor definitions would load.
type PackageCheck func(*pkg.Package) error

// WithChecks adds install-time package checks.
func (s *Service) WithChecks(checks ...PackageCheck) *Service {
	s.checks = append(s.checks, checks...)
	return s
}

// NewService creates a Service. hostVersion is the running WeKnora version
// that engines ranges are checked against.
func NewService(
	repo interfaces.PluginRepository, store reconcile.PackageStore, sync Syncer, hostVersion string,
) *Service {
	cfg := utils.DefaultSSRFSafeHTTPClientConfig()
	cfg.Timeout = 2 * time.Minute
	return &Service{
		repo: repo, store: store, sync: sync, hostVersion: hostVersion,
		client: utils.NewSSRFSafeHTTPClient(cfg),
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
}

// Inspect opens a package and says what installing it would do.
func (s *Service) Inspect(ctx context.Context, data []byte) (*Preview, error) {
	p, err := s.open(data)
	if err != nil {
		return nil, err
	}
	out := &Preview{Manifest: p.Manifest, Digest: p.Digest, Size: p.Size, Change: ChangeInstall}
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

// open validates a package for installation on this platform.
func (s *Service) open(data []byte) (*pkg.Package, error) {
	p, err := pkg.Open(data)
	if err != nil {
		return nil, &InvalidError{Err: err}
	}
	if !supportedRuntimes[p.Manifest.Runtime.Type] {
		return nil, invalid("runtime %q is not supported yet; only declarative plugins can be installed",
			p.Manifest.Runtime.Type)
	}
	if err := p.Manifest.CheckEngines(s.hostVersion); err != nil {
		return nil, &InvalidError{Err: err}
	}
	for _, check := range s.checks {
		if err := check(p); err != nil {
			return nil, &InvalidError{Err: err}
		}
	}
	return p, nil
}

// Request installs or upgrades a plugin from a package.
type Request struct {
	Data   []byte
	Source Source
	// ExpectedDigest, when set, must match the package: it pins the install
	// to the package the administrator reviewed.
	ExpectedDigest string
	UserID         string
}

// Install stores the package as a version of its plugin and makes it the
// active one. Installing makes the plugin available platform-wide; each
// tenant still opts in. The permissions the manifest asks for are recorded
// as granted.
func (s *Service) Install(ctx context.Context, req Request) (*View, error) {
	p, err := s.open(req.Data)
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
			PackageURI: uri, Size: p.Size, CreatedBy: req.UserID, CreatedAt: time.Now(),
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
	source, _ := json.Marshal(req.Source)
	perms, _ := json.Marshal(m.Permissions)
	row.Source = types.JSON(source)
	row.ActiveVersion = m.Version
	row.Runtime = string(m.Runtime.Type)
	row.GrantedPerms = types.JSON(perms)
	if err := s.repo.SavePlugin(ctx, row); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "[plugin] %s installed %s %s (%s)", req.UserID, m.ID, m.Version, p.Digest)
	return s.apply(ctx, m.ID)
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
