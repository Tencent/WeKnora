package activate

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
)

// SkillSourcePrefix marks a skill install source that names a plugin skill:
// "plugin:<plugin id>/<skill id>".
const SkillSourcePrefix = "plugin:"

// ErrUnknownSkill is returned for a plugin skill no loaded, enabled plugin
// provides.
var ErrUnknownSkill = errors.New("no enabled plugin provides this skill")

// Skills offers the skills of installed plugins for installation into a
// workspace's sandbox, through the same pipeline as an uploaded skill: the
// sandbox is where skills run, so that is where a plugin skill has to go.
type Skills struct {
	mu      sync.RWMutex
	tenancy *tenancy.Service
	loaded  map[string]*reconcile.Loaded
}

// NewSkills creates the skills activator; Bind completes it.
func NewSkills() *Skills { return &Skills{loaded: map[string]*reconcile.Loaded{}} }

// Bind supplies the tenant switches.
func (a *Skills) Bind(t *tenancy.Service) {
	a.mu.Lock()
	a.tenancy = t
	a.mu.Unlock()
}

// Name implements reconcile.Activator.
func (a *Skills) Name() string { return "skills" }

// Activate implements reconcile.Activator.
func (a *Skills) Activate(_ context.Context, l *reconcile.Loaded) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(l.Manifest.Contributes[manifest.PointSkills]) == 0 {
		delete(a.loaded, l.Manifest.ID)
		return nil
	}
	a.loaded[l.Manifest.ID] = l
	return nil
}

// Deactivate implements reconcile.Activator.
func (a *Skills) Deactivate(_ context.Context, pluginID string) error {
	a.mu.Lock()
	delete(a.loaded, pluginID)
	a.mu.Unlock()
	return nil
}

// Archive returns a plugin skill as a skill bundle (a zip with SKILL.md at
// its root) for a workspace that has the plugin enabled. source is the
// qualified skill ID, with or without SkillSourcePrefix.
func (a *Skills) Archive(ctx context.Context, tenantID uint64, source string) ([]byte, error) {
	qualified := strings.TrimPrefix(source, SkillSourcePrefix)
	pluginID, localID, ok := strings.Cut(qualified, "/")
	if !ok {
		return nil, fmt.Errorf("%w: %q is not <plugin>/<skill>", ErrUnknownSkill, qualified)
	}
	a.mu.RLock()
	l, loaded := a.loaded[pluginID]
	t := a.tenancy
	a.mu.RUnlock()
	if !loaded || t == nil || !t.ContributionEnabled(ctx, tenantID, manifest.PointSkills, qualified) {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSkill, qualified)
	}
	for _, c := range l.Manifest.Contributes[manifest.PointSkills] {
		if c.ID == localID {
			return zipDir(l, c.Path)
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownSkill, qualified)
}

func zipDir(l *reconcile.Loaded, dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	prefix := strings.TrimSuffix(path.Clean(dir), "/") + "/"
	for _, name := range l.Package.Files(dir) {
		data, _ := l.Package.ReadFile(name)
		w, err := zw.Create(strings.TrimPrefix(name, prefix))
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
