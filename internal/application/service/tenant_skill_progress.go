package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// skillProgressTTL keeps a finished run's last value around long enough for a
// late SSE subscriber to render it, then lets it expire.
const skillProgressTTL = 30 * time.Minute

// SkillProgress is the transient install/remove progress. It is deliberately
// NOT persisted: the durable state is tenant_skills.status, and a percentage
// that outlives the job that produced it is worse than no percentage.
type SkillProgress struct {
	Percent int    `json:"percent"`
	Stage   string `json:"stage"`
	Log     string `json:"log,omitempty"`
	Status  string `json:"status,omitempty"`
}

func skillProgressKey(tenantID uint64, configID, skillID string) string {
	return fmt.Sprintf("weknora-skill-install:%d:%s:%s", tenantID, configID, skillID)
}

// publishProgress stores the latest value and broadcasts it. Without Redis both
// are no-ops and the UI falls back to the DB status alone; the install itself
// keeps working, which is the point.
func (s *TenantSkillService) publishProgress(
	ctx context.Context, tenantID uint64, configID, skillID string, p SkillProgress,
) {
	if s.redis == nil {
		return
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return
	}
	key := skillProgressKey(tenantID, configID, skillID)
	if err := s.redis.Set(ctx, key, payload, skillProgressTTL).Err(); err != nil {
		logger.Warnf(ctx, "[skill] store progress %s failed: %v", key, err)
	}
	if err := s.redis.Publish(ctx, key, payload).Err(); err != nil {
		logger.Warnf(ctx, "[skill] publish progress %s failed: %v", key, err)
	}
}

// LastProgress returns the last known value so a fresh SSE connection can paint
// immediately instead of waiting for the next tick. tenantID is part of the
// key so a caller that only has config/skill IDs cannot read another
// workspace's progress.
func (s *TenantSkillService) LastProgress(
	ctx context.Context, tenantID uint64, configID, skillID string,
) (SkillProgress, bool) {
	if s.redis == nil {
		return SkillProgress{}, false
	}
	raw, err := s.redis.Get(ctx, skillProgressKey(tenantID, configID, skillID)).Bytes()
	if err != nil {
		return SkillProgress{}, false
	}
	var p SkillProgress
	if err := json.Unmarshal(raw, &p); err != nil {
		return SkillProgress{}, false
	}
	return p, true
}
