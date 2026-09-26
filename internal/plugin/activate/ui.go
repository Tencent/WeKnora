package activate

import (
	"context"
	"errors"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/utils"
)

// ErrNoPage is a page file or mount that does not exist.
var ErrNoPage = errors.New("no such plugin page")

// UIPages keeps the pages of loaded plugins (pages, settingsSections,
// kbTabs, and tool result pages of mcpServers): where their files are, and
// which mounts exist.
type UIPages struct {
	mu     sync.RWMutex
	loaded map[string]uiPlugin
}

type uiPlugin struct {
	m   *manifest.Manifest
	dir string
}

// NewUIPages creates the page activator.
func NewUIPages() *UIPages { return &UIPages{loaded: map[string]uiPlugin{}} }

// Name implements reconcile.Activator.
func (a *UIPages) Name() string { return "ui" }

// Activate implements reconcile.Activator.
func (a *UIPages) Activate(_ context.Context, l *reconcile.Loaded) error {
	has := false
	for point, list := range l.Manifest.Contributes {
		has = has || manifest.IsUIPoint(point)
		for _, c := range list {
			has = has || (point == manifest.PointMCPServers && c.HasToolPages()) || manifest.HasEditor(point, c)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if has {
		a.loaded[l.Manifest.ID] = uiPlugin{m: l.Manifest, dir: l.Dir}
	} else {
		delete(a.loaded, l.Manifest.ID)
	}
	return nil
}

// Deactivate implements reconcile.Activator.
func (a *UIPages) Deactivate(_ context.Context, pluginID string) error {
	a.mu.Lock()
	delete(a.loaded, pluginID)
	a.mu.Unlock()
	return nil
}

// File returns the path on disk of a page file of the loaded version. Only
// files under manifest.UIRoot are served.
func (a *UIPages) File(pluginID, version, rel string) (string, error) {
	a.mu.RLock()
	p, ok := a.loaded[pluginID]
	a.mu.RUnlock()
	if !ok || p.m.Version != version {
		return "", ErrNoPage
	}
	rel = path.Clean("/" + rel)[1:]
	if !strings.HasPrefix(rel, manifest.UIRoot) {
		return "", ErrNoPage
	}
	full, err := utils.SafeJoinUnderBase(p.dir, rel)
	if err != nil {
		return "", ErrNoPage
	}
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrNoPage
	}
	return full, nil
}

// Mount is one page of a loaded plugin.
type Mount struct {
	Manifest     *manifest.Manifest
	Point        manifest.Point
	Contribution manifest.Contribution
}

// MinRole is the workspace role the page needs.
func (m Mount) MinRole() string { return manifest.UIMinRole(m.Point, m.Contribution) }

// Mount finds a page by "<point>/<id>".
func (a *UIPages) Mount(pluginID, mount string) (Mount, error) {
	a.mu.RLock()
	p, ok := a.loaded[pluginID]
	a.mu.RUnlock()
	point, id, found := strings.Cut(mount, "/")
	pt := manifest.Point(point)
	toolPages := pt == manifest.PointMCPServers
	editors := slices.Contains(manifest.EditorPoints, pt)
	if !ok || !found || (!manifest.IsUIPoint(pt) && !toolPages && !editors) {
		return Mount{}, ErrNoPage
	}
	for _, c := range p.m.Contributes[pt] {
		// A tool result page answers to its server ("mcpServers/<id>"), an
		// instance editor to its contribution ("connectors/<id>").
		if c.ID == id && (!toolPages || c.HasToolPages()) && (!editors || manifest.HasEditor(pt, c)) {
			return Mount{Manifest: p.m, Point: manifest.Point(point), Contribution: c}, nil
		}
	}
	return Mount{}, ErrNoPage
}
