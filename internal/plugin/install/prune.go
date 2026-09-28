package install

import (
	"context"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/logger"
)

// keptVersions is how many versions of a plugin are kept besides the
// active one, to roll back to; older ones and their packages go.
const keptVersions = 5

// pruneVersions removes a plugin's versions beyond the active one and the
// keptVersions most recent others, with their stored packages. A failure
// only leaves them for the next install.
func (s *Service) pruneVersions(ctx context.Context, pluginID, active string) {
	versions, err := s.repo.ListVersions(ctx, pluginID)
	if err != nil {
		logger.Warnf(ctx, "[plugin] list versions of %s to prune: %v", pluginID, err)
		return
	}
	sort.SliceStable(versions, func(i, j int) bool { return versions[i].CreatedAt.After(versions[j].CreatedAt) })
	kept := 0
	for _, v := range versions {
		if v.Version == active {
			continue
		}
		if kept < keptVersions {
			kept++
			continue
		}
		if err := s.repo.DeleteVersion(ctx, pluginID, v.Version); err != nil {
			logger.Warnf(ctx, "[plugin] prune %s %s: %v", pluginID, v.Version, err)
			continue
		}
		if err := s.store.Delete(ctx, v.PackageURI); err != nil {
			logger.Warnf(ctx, "[plugin] delete the package of %s %s: %v", pluginID, v.Version, err)
		}
		logger.Infof(ctx, "[plugin] pruned %s %s", pluginID, v.Version)
	}
}

// WithOwnedLimit caps how many plugins one workspace may register, read on
// every registration so the setting applies at once; <= 0 means no cap.
func (s *Service) WithOwnedLimit(limit func(context.Context) int) *Service {
	s.ownedLimit = limit
	return s
}

// ErrOwnedLimit refuses a workspace at its cap of own plugins.
type ErrOwnedLimit struct{ Limit int }

func (e ErrOwnedLimit) Error() string {
	return fmt.Sprintf("a workspace can register at most %d plugins of its own; remove one first", e.Limit)
}

// checkOwnedLimit refuses a new plugin of a workspace at its cap.
func (s *Service) checkOwnedLimit(ctx context.Context, tenantID uint64) error {
	if s.ownedLimit == nil {
		return nil
	}
	limit := s.ownedLimit(ctx)
	if limit <= 0 {
		return nil
	}
	rows, err := s.repo.ListPlugins(ctx)
	if err != nil {
		return err
	}
	n := 0
	for _, r := range rows {
		if r.OwnerTenantID != nil && *r.OwnerTenantID == tenantID {
			n++
		}
	}
	if n >= limit {
		return &InvalidError{Err: ErrOwnedLimit{Limit: limit}}
	}
	return nil
}
